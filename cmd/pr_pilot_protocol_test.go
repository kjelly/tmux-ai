package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func fakePilotPR(t *testing.T, draft bool, head string, labels ...string) string {
	t.Helper()
	dir := fakePRGH(t)
	names := make([]map[string]string, 0, len(labels))
	for _, label := range labels {
		names = append(names, map[string]string{"name": label})
	}
	view, err := json.Marshal(map[string]any{"number": 333, "state": "OPEN", "isDraft": draft,
		"url": "https://github.com/owner/repo/pull/333", "headRefOid": head, "labels": names})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUXAI_TEST_PILOT_VIEW", string(view))
	const gh = `#!/bin/sh
case "$*" in
  'pr view 333 --json number,state,isDraft,labels,headRefOid,url --repo owner/repo')
    printf '%s' "$TMUXAI_TEST_PILOT_VIEW" ;;
  'pr view 333 --repo owner/repo --json number,url,headRefName')
    printf '%s' '{"number":333,"url":"https://github.com/owner/repo/pull/333","headRefName":"pr-333"}' ;;
  'api --hostname github.com user --jq .login') printf '%s' 'review-bot' ;;
  'pr view 333 --json comments --repo owner/repo') printf '%s' "$TMUXAI_TEST_PILOT_COMMENTS" ;;
  'repo clone https://github.com/owner/repo '*)
    exec git clone -q "$TMUXAI_TEST_PILOT_SOURCE" "$4" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPRLoopPilotNeedsPersonIsBoundToCurrentHeadAndActor(t *testing.T) {
	head := strings.Repeat("a", 40)
	fakePilotPR(t, false, head, "needs-person")
	for _, tt := range []struct {
		name, markedHead, author string
		blocked                  bool
	}{
		{"current", head, "review-bot", true},
		{"stale", strings.Repeat("b", 40), "review-bot", false},
		{"other author", head, "someone-else", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			marker := fmt.Sprintf("<!-- pr-review-merge:needs-person head=%s live-base=%s reasons=%s -->",
				tt.markedHead, strings.Repeat("b", 40), strings.Repeat("c", 16))
			data, err := json.Marshal(map[string]any{"comments": []map[string]any{
				{"author": map[string]string{"login": tt.author}, "body": marker, "url": "https://github.com/owner/repo/pull/333#issuecomment-1"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMUXAI_TEST_PILOT_COMMENTS", string(data))
			phase, err := queryPilotPRPhase(context.Background(), "owner/repo", 333)
			if tt.blocked {
				if err == nil || !strings.Contains(err.Error(), "needs a person at HEAD") {
					t.Fatalf("current hand-off did not block: %s, %v", phase, err)
				}
			} else if err != nil || phase != prReady {
				t.Fatalf("unexpected block: %s, %v", phase, err)
			}
		})
	}
}

func TestPRLoopPilotRejectsOtherDispatchAndIgnoresStaleHead(t *testing.T) {
	head := strings.Repeat("a", 40)
	fakePilotPR(t, true, head)
	task := &pilotPRTask{
		pilotTaskIdentity: pilotTaskIdentity{Version: 1, PR: 333, Repo: "owner/repo", Phase: prDraft,
			Nonce: strings.Repeat("b", 32), StartHead: head},
		path: filepath.Join(t.TempDir(), "task.json"),
	}
	var out bytes.Buffer
	agents := managedPRAgents{pilotTask: task, out: &out}
	result := pilotPRResult{pilotTaskIdentity: task.pilotTaskIdentity, Head: head, Outcome: "DRAFT-BLOCKED", Reason: "No fix"}
	result.Nonce = strings.Repeat("c", 32)
	writePilotTestResult(t, task, result)
	if err := agents.checkPilotResult(context.Background(), prDraft); err == nil || !strings.Contains(err.Error(), "mismatched") {
		t.Fatalf("wrong nonce was accepted: %v", err)
	}
	result.pilotTaskIdentity = task.pilotTaskIdentity
	result.Head = strings.Repeat("d", 40)
	writePilotTestResult(t, task, result)
	if err := agents.checkPilotResult(context.Background(), prDraft); err != nil || !strings.Contains(out.String(), "older HEAD") {
		t.Fatalf("stale HEAD affected current phase: %v, %s", err, out.String())
	}
}

func writePilotTestResult(t *testing.T, task *pilotPRTask, result pilotPRResult) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(task.path), "result.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLivePRLoopPilotStartsOnDefaultBranchAndStopsOnReceipt(t *testing.T) {
	if os.Getenv("TMUXAI_LIVE_TMUX") != "1" {
		t.Skip("set TMUXAI_LIVE_TMUX=1 to run live PR loop tests")
	}
	head := strings.Repeat("a", 40)
	dir := fakePilotPR(t, true, head)
	source := initPRCheckoutRepository(t, filepath.Join(dir, "source"))
	helper := ".agents/skills/_shared/scripts/pr_loop_result.py"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(source, helper)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, helper), []byte("# Pilot support on main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkoutGit(t, source, "add", "--", helper)
	checkoutGit(t, source, "-c", "user.name=PR loop test", "-c", "user.email=pr-loop@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "-qm", "add Pilot result support on main")
	t.Setenv("TMUXAI_TEST_PILOT_SOURCE", source)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	path := filepath.Join(dir, "agent")
	const agent = `#!/usr/bin/env python3
import json, os, pathlib, shlex, subprocess
assert subprocess.check_output(["git", "branch", "--show-current"], text=True).strip() == "main"
assert pathlib.Path(".agents/skills/_shared/scripts/pr_loop_result.py").is_file()
command, pr, task_arg = shlex.split(input("> "))
assert command == "/pr-ready" and pr == "333"
task = pathlib.Path(task_arg.removeprefix("--pr-loop-task="))
data = json.loads(task.read_text())
assert data["pr"] == 333 and data["phase"] == "draft"
subprocess.run(["git", "switch", "pr-333"], check=True)
assert not pathlib.Path(".agents/skills/_shared/scripts/pr_loop_result.py").exists()
subprocess.run(["git", "switch", "main"], check=True)
result = task.with_name("result.json")
tmp = result.with_suffix(".tmp")
tmp.write_text(json.dumps(dict(data, outcome="DRAFT-BLOCKED", head_sha=data["start_head"], reason="blocked by test fixture")))
os.replace(tmp, result)
input("> ")
`
	if err := os.WriteFile(path, []byte(agent), 0o700); err != nil {
		t.Fatal(err)
	}
	ex := tmux.NewExecutor()
	t.Cleanup(func() { _, _ = ex.Exec("kill-session", "-t", fmt.Sprintf("=tmux-ai-pr-333-%d", os.Getpid())) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := newPRLoopCommand()
	command.SetContext(ctx)
	command.SetArgs([]string{"333", "--repo", "owner/repo", "--pilot-skills", "--draft-agent", path,
		"--ready-agent", path, "--stable-polls", "1", "--poll-interval", "30ms", "--submit-delay", "30ms"})
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "draft reported DRAFT-BLOCKED") || !strings.Contains(err.Error(), "blocked by test fixture") {
		t.Fatalf("Pilot loop did not stop on the verified receipt: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "Checked out PR branch") {
		t.Fatalf("runner changed the branch before the skill:\n%s", out.String())
	}
}
