package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func TestPRLoopManagedDryRun(t *testing.T) {
	dir := fakePRGH(t)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("TMUXAI_TEST_PR_JSON", `{"number":333,"state":"OPEN","isDraft":false}`)
	args := []string{"333", "--repo", "owner/repo", "--draft-agent", "test-fixer",
		"--ready-agent", "test-reviewer", "--ready-command", "review {pr}", "--stable-polls", "1", "--dry-run"}
	command := newPRLoopCommand()
	command.SetArgs(args)
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ready -> test-reviewer: review 333") {
		t.Fatalf("unexpected dry-run output: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); !os.IsNotExist(err) {
		t.Fatalf("dry run accessed the managed workspace: %v", err)
	}

	command = newPRLoopCommand()
	command.SetArgs(append(args, "--draft-pane", "%1"))
	command.SetOut(&out)
	command.SetErr(&out)
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "choose either") {
		t.Fatalf("expected mixed modes to be rejected: %v", err)
	}
}

func TestPRLoopManagedStartupRejectsMenu(t *testing.T) {
	screen := "Quick safety check\n Do you trust this folder?\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n Enter to confirm"
	if managedPRStartupReady(screen) {
		t.Fatal("a numbered startup menu must not receive the PR prompt")
	}
	if !managedPRStartupReady("Claude Code\n❯ Try reviewing a PR\n──────────────────") {
		t.Fatal("the input prompt should accept the PR prompt")
	}
}

func TestPRLoopManagedCheckoutKeepsUnpublishedCommit(t *testing.T) {
	dir := initPRCheckoutRepository(t, filepath.Join(t.TempDir(), "repo"))
	checkoutGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	checkoutGit(t, dir, "switch", "-q", "pr-333")
	checkoutGit(t, dir, "-c", "user.name=PR loop test", "-c", "user.email=pr-loop@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "-qm", "unpublished", "--allow-empty")
	var out bytes.Buffer
	agents := managedPRAgents{checkout: dir, out: &out}
	agents.cleanupCheckout(context.Background())
	if _, err := os.Stat(dir); err != nil || !strings.Contains(out.String(), "Retained managed checkout") {
		t.Fatalf("unpublished work was not retained: %v, %s", err, out.String())
	}
}

func TestLivePRLoopLaunchesManagedAgents(t *testing.T) {
	if os.Getenv("TMUXAI_LIVE_TMUX") != "1" {
		t.Skip("set TMUXAI_LIVE_TMUX=1 to run live PR loop tests")
	}
	dir := fakePRGH(t)
	source := initPRCheckoutRepository(t, filepath.Join(dir, "source"))
	checkoutGit(t, source, "branch", "pr-334")
	state := filepath.Join(dir, "state")
	logPath := filepath.Join(dir, "commands")
	cache := filepath.Join(dir, "cache")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("TMUXAI_TEST_PR_SOURCE", source)
	t.Setenv("TMUXAI_TEST_PR_STATE", state)
	t.Setenv("TMUXAI_TEST_PR_STATE_NEXT", state+"-next")
	for path, json := range map[string]string{
		state:           `{"number":333,"state":"OPEN","isDraft":false}`,
		state + "-next": `{"number":334,"state":"OPEN","isDraft":false}`,
	} {
		if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ghPath := filepath.Join(dir, "gh")
	gh, err := os.ReadFile(ghPath)
	if err != nil {
		t.Fatal(err)
	}
	const clone = `if [ "$1 $2 $3" = 'repo clone https://github.com/owner/repo' ]; then
  exec git clone -q "$TMUXAI_TEST_PR_SOURCE" "$4"
fi
if [ "$*" = 'pr view 334 --repo owner/repo --json number,state,isDraft,labels' ]; then
  case "$(cat "$TMUXAI_TEST_PR_STATE_NEXT")" in
    *merge-queue*)
      cat "$TMUXAI_TEST_PR_STATE_NEXT"
      printf '%s' '{"number":334,"state":"MERGED"}' > "$TMUXAI_TEST_PR_STATE_NEXT.train"
      mv "$TMUXAI_TEST_PR_STATE_NEXT.train" "$TMUXAI_TEST_PR_STATE_NEXT"
      exit 0 ;;
  esac
fi
`
	script := strings.Replace(string(gh), "case \"$*\" in", clone+"case \"$*\" in", 1)
	t.Setenv("TMUXAI_TEST_CHECKOUT_LOG", filepath.Join(dir, "checkout-log"))
	if err := os.WriteFile(ghPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(dir, "agent.py")
	const agent = `import json, os, subprocess, sys
role, first, log = sys.argv[1:]
branch = subprocess.check_output(["git", "branch", "--show-current"], text=True).strip()
number = int(branch.removeprefix("pr-"))
path = first if number == 333 else first + "-next"
def log_line(message):
    with open(log, "a") as out:
        out.write(f"{role}:{message}\n")
log_line(f"start:{branch}")
count = 0
while True:
    prompt = input("> ")
    log_line(prompt)
    if prompt != (f"/repair {number}" if role == "draft" else f"$review {number}"):
        sys.exit(2)
    count += 1
    queued = role == "ready" and number == 334
    merged = role == "ready" and not queued and count == 2
    with open(path + ".tmp", "w") as out:
        json.dump({"number": number, "state": "MERGED" if merged else "OPEN",
                   "isDraft": role == "ready" and not (queued or merged),
                   "labels": [{"name": "merge-queue"}] if queued else []}, out)
    os.replace(path + ".tmp", path)
    if queued:
        sys.exit(0)
`
	if err := os.WriteFile(agentPath, []byte(agent), 0o600); err != nil {
		t.Fatal(err)
	}
	wrappers := map[prPhase]string{}
	for _, phase := range []prPhase{prDraft, prReady} {
		path := filepath.Join(dir, string(phase)+" agent")
		wrapper := "#!/bin/sh\nexec python3 " + shellQuote(agentPath) + " " + string(phase) + " " + shellQuote(state) + " " + shellQuote(logPath) + "\n"
		if err := os.WriteFile(path, []byte(wrapper), 0o700); err != nil {
			t.Fatal(err)
		}
		wrappers[phase] = path
	}
	ex := tmux.NewExecutor()
	for _, number := range []int{333, 334} {
		name := fmt.Sprintf("tmux-ai-pr-%d-%d", number, os.Getpid())
		t.Cleanup(func() { _, _ = ex.Exec("kill-session", "-t", "="+name) })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := newPRLoopCommand()
	command.SetContext(ctx)
	command.SetArgs([]string{"333", "334", "--repo", "owner/repo", "--draft-agent", wrappers[prDraft],
		"--ready-agent", wrappers[prReady], "--draft-command", "/repair {pr}", "--ready-command", "$review {pr}",
		"--stable-polls", "1", "--poll-interval", "40ms", "--submit-delay", "30ms"})
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.Execute(); err != nil {
		t.Fatalf("managed PR loop failed: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ready:start:pr-333", "ready:$review 333", "draft:start:pr-333", "draft:/repair 333",
		"ready:$review 333", "ready:start:pr-334", "ready:$review 334"}
	if got := strings.Split(strings.TrimSpace(string(data)), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("managed agent lifecycle = %v, want %v\n%s", got, want, out.String())
	}
	if !strings.Contains(out.String(), "PR #334 is in the merge train") {
		t.Fatalf("missing merge train wait after reviewer exited:\n%s", out.String())
	}
	entries, err := os.ReadDir(filepath.Join(cache, "tmux-ai"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("successful batch left a managed clone: %v, %v\n%s", entries, err, out.String())
	}
}
