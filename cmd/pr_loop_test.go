package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func testPRLoopOptions() prLoopOptions {
	return prLoopOptions{
		Number: 333, Repo: "owner/repo", DraftPane: "project:1", ReadyPane: "project:2",
		DraftCommand: "/pr-ready {pr}", ReadyCommand: "$pr-review-merge {pr}",
		PollInterval: time.Second, StablePolls: 2, MaxActions: 20,
	}
}

func TestParsePRPhase(t *testing.T) {
	for _, test := range []struct {
		name, json string
		phase      prPhase
	}{
		{"draft", `{"number":333,"state":"OPEN","isDraft":true}`, prDraft},
		{"ready", `{"number":333,"state":"OPEN","isDraft":false}`, prReady},
		{"auto-merge still ready", `{"number":333,"state":"OPEN","isDraft":false,"autoMergeRequest":{}}`, prReady},
		{"merged", `{"number":333,"state":"MERGED"}`, prMerged},
		{"closed", `{"number":333,"state":"CLOSED","isDraft":false}`, ""},
		{"missing draft flag", `{"number":333,"state":"OPEN"}`, ""},
		{"missing number", `{"state":"MERGED"}`, ""},
		{"different PR", `{"number":334,"state":"OPEN","isDraft":true}`, ""},
		{"unknown", `{"number":333,"state":"UNKNOWN"}`, ""},
		{"malformed", `{"number":`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			phase, err := parsePRPhase([]byte(test.json), 333)
			if phase != test.phase || (err != nil) != (test.phase == "") {
				t.Fatalf("parsePRPhase() = %q, %v; want %q", phase, err, test.phase)
			}
		})
	}
}

type prObservation struct {
	phase prPhase
	err   error
}

// A bounded fake clock/query sequence makes repeated polling and failure paths
// deterministic and prevents a regression from hanging the test suite.
func exercisePRLoop(t *testing.T, o prLoopOptions, observations []prObservation, sendErr error) ([]string, string, error) {
	t.Helper()
	index := 0
	now := time.Unix(100, 0)
	var sent []string
	var out bytes.Buffer
	err := runPRLoop(context.Background(), o,
		func(_ context.Context, repo string, number int) (prPhase, error) {
			if repo != o.Repo || number != o.Number {
				t.Fatalf("query targeted %s#%d", repo, number)
			}
			if index >= len(observations) {
				t.Fatal("queried beyond expected observation sequence")
			}
			observation := observations[index]
			index++
			return observation.phase, observation.err
		},
		func(_ context.Context, phase prPhase, text string) error {
			sent = append(sent, string(phase)+":"+text)
			return sendErr
		},
		func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil },
		func() time.Time { return now }, &out)
	return sent, out.String(), err
}

func phaseObservations(phases ...prPhase) []prObservation {
	result := make([]prObservation, len(phases))
	for i, phase := range phases {
		result[i].phase = phase
	}
	return result
}

func TestPRLoopRoutesNativeCommandsByPRState(t *testing.T) {
	for _, test := range []struct {
		name    string
		phases  []prPhase
		observe bool
		want    []string
	}{
		{
			name: "draft ready draft ready merged, no duplicates",
			phases: []prPhase{prDraft, prDraft, prDraft, prReady, prReady, prReady,
				prDraft, prDraft, prReady, prReady, prMerged},
			want: []string{"draft:/pr-ready 333", "ready:$pr-review-merge 333", "draft:/pr-ready 333", "ready:$pr-review-merge 333"},
		},
		{
			name:   "initially ready goes directly to review",
			phases: []prPhase{prReady, prReady, prReady, prMerged},
			want:   []string{"ready:$pr-review-merge 333"},
		},
		{
			name: "merged requires no dispatch", phases: []prPhase{prMerged},
		},
		{
			name:   "transient state does not dispatch",
			phases: []prPhase{prDraft, prDraft, prReady, prDraft, prDraft, prReady, prReady, prMerged},
			want:   []string{"draft:/pr-ready 333", "ready:$pr-review-merge 333"},
		},
		{
			name: "observe skips an already running draft task", observe: true,
			phases: []prPhase{prDraft, prDraft, prDraft, prReady, prReady, prMerged},
			want:   []string{"ready:$pr-review-merge 333"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := testPRLoopOptions()
			o.Observe = test.observe
			sent, out, err := exercisePRLoop(t, o, phaseObservations(test.phases...), nil)
			if err != nil || !reflect.DeepEqual(sent, test.want) || !strings.Contains(out, "merged; stopped") {
				t.Fatalf("sent=%v, err=%v, output=%s; want=%v", sent, err, out, test.want)
			}
		})
	}
}

func TestPRLoopQueryFailuresNeverDispatchStaleState(t *testing.T) {
	networkErr := &prQueryError{errors.New("network unavailable")}
	o := testPRLoopOptions()
	observations := phaseObservations(prDraft, prDraft, prReady)
	observations = append(observations, prObservation{err: networkErr})
	observations = append(observations, phaseObservations(prDraft, prDraft, prReady, prReady, prMerged)...)
	sent, _, err := exercisePRLoop(t, o, observations, nil)
	if err != nil || !reflect.DeepEqual(sent, []string{"draft:/pr-ready 333", "ready:$pr-review-merge 333"}) {
		t.Fatalf("sent=%v, err=%v", sent, err)
	}

	observations = phaseObservations(prDraft, prDraft)
	for i := 0; i < 3; i++ {
		observations = append(observations, prObservation{err: networkErr})
	}
	sent, _, err = exercisePRLoop(t, o, observations, nil)
	if len(sent) != 1 || err == nil || !strings.Contains(err.Error(), "3 consecutive") {
		t.Fatalf("persistent query failure: sent=%v, err=%v", sent, err)
	}

	sent, _, err = exercisePRLoop(t, o, []prObservation{{err: errors.New("closed without merge")}}, nil)
	if len(sent) != 0 || err == nil || err.Error() != "closed without merge" {
		t.Fatalf("terminal response should fail immediately: sent=%v, err=%v", sent, err)
	}
}

func TestPRLoopLimitsAndDeliveryFailures(t *testing.T) {
	o := testPRLoopOptions()
	o.PhaseTimeout = 2 * time.Second
	sent, _, err := exercisePRLoop(t, o, phaseObservations(prDraft, prDraft, prDraft, prDraft), nil)
	if len(sent) != 1 || err == nil || !strings.Contains(err.Error(), "no command resent") {
		t.Fatalf("phase timeout: sent=%v, err=%v", sent, err)
	}
	o = testPRLoopOptions()
	o.MaxActions = 1
	sent, _, err = exercisePRLoop(t, o, phaseObservations(prDraft, prDraft, prReady, prReady), nil)
	if len(sent) != 1 || err == nil || !strings.Contains(err.Error(), "--max-actions=1") {
		t.Fatalf("action limit: sent=%v, err=%v", sent, err)
	}
	o = testPRLoopOptions()
	sent, _, err = exercisePRLoop(t, o, phaseObservations(prDraft, prDraft), errors.New("Enter failed"))
	if len(sent) != 1 || err == nil || !strings.Contains(err.Error(), "delivery may be partial") {
		t.Fatalf("ambiguous delivery must not retry: sent=%v, err=%v", sent, err)
	}
}

func TestPRLoopCustomSkillsAndDryRun(t *testing.T) {
	o := testPRLoopOptions()
	o.DraftCommand, o.ReadyCommand = "/custom-fix {pr}", "$custom-merge {pr}"
	sent, _, err := exercisePRLoop(t, o, phaseObservations(prDraft, prDraft, prReady, prReady, prMerged), nil)
	if err != nil || !reflect.DeepEqual(sent, []string{"draft:/custom-fix 333", "ready:$custom-merge 333"}) {
		t.Fatalf("custom skills: sent=%v, err=%v", sent, err)
	}
	o.DryRun = true
	sent, out, err := exercisePRLoop(t, o, phaseObservations(prReady, prReady), nil)
	if len(sent) != 0 || err != nil || !strings.Contains(out, "$custom-merge 333") {
		t.Fatalf("dry run: sent=%v, err=%v, out=%s", sent, err, out)
	}
}

func TestPRLoopRejectsInvalidOptions(t *testing.T) {
	for _, change := range []func(*prLoopOptions){
		func(o *prLoopOptions) { o.Repo = "" },
		func(o *prLoopOptions) { o.Repo = "https://github.com/owner/repo" },
		func(o *prLoopOptions) { o.Repo = "owner/" },
		func(o *prLoopOptions) { o.DraftPane = "" },
		func(o *prLoopOptions) { o.ReadyPane = o.DraftPane },
		func(o *prLoopOptions) { o.DraftCommand = "" },
		func(o *prLoopOptions) { o.ReadyCommand = "$merge {pr}\nEnter" },
		func(o *prLoopOptions) { o.ReadyCommand = "$merge\x03{pr}" },
		func(o *prLoopOptions) { o.PollInterval = 0 },
		func(o *prLoopOptions) { o.StablePolls = 0 },
		func(o *prLoopOptions) { o.MaxActions = 0 },
		func(o *prLoopOptions) { o.PhaseTimeout = -time.Second },
		func(o *prLoopOptions) { o.SubmitDelay = -time.Second },
	} {
		o := testPRLoopOptions()
		change(&o)
		if err := o.validate(); err == nil {
			t.Fatalf("accepted invalid options: %+v", o)
		}
	}
}

func TestNativePRCommandDelivery(t *testing.T) {
	for _, text := range []string{"/pr-ready 333", "$pr-review-merge 333"} {
		ex := tmux.NewFakeExecutor()
		ex.ExecHook = func(args []string) (string, bool, error) {
			if args[0] == "display-message" {
				return "0|0||claude", true, nil
			}
			return "", false, nil
		}
		if err := sendNativePRCommand(context.Background(), ex, "%1", text, 0); err != nil {
			t.Fatal(err)
		}
		if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].Content != text || ex.SendLinesCalls[0].PaneID != "%1" {
			t.Fatalf("native command was not sent verbatim: %+v", ex.SendLinesCalls)
		}
		last := ex.ExecCalls[len(ex.ExecCalls)-1]
		if !reflect.DeepEqual(last, []string{"send-keys", "-t", "%1", "Enter"}) {
			t.Fatalf("missing final Enter: %v", last)
		}
	}
}

func TestNativePRCommandRefusesInvalidPaneOrChangedForeground(t *testing.T) {
	for _, state := range []string{"", "0|0||bash", "0|1||claude", "1|0|copy-mode|claude", "0|0||"} {
		ex := tmux.NewFakeExecutor()
		ex.ExecHook = func([]string) (string, bool, error) { return state, true, nil }
		err := sendNativePRCommand(context.Background(), ex, "%1", "/pr-ready 333", 0)
		if err == nil || len(ex.SendLinesCalls) != 0 {
			t.Fatalf("state %q: pasted into invalid pane; err=%v", state, err)
		}
	}
	ex := tmux.NewFakeExecutor()
	reads := 0
	ex.ExecHook = func(args []string) (string, bool, error) {
		if args[0] != "display-message" {
			t.Fatalf("unexpected command after agent exit: %v", args)
		}
		reads++
		if reads == 1 {
			return "0|0||claude", true, nil
		}
		return "0|0||bash", true, nil
	}
	if err := sendNativePRCommand(context.Background(), ex, "%1", "/pr-ready 333", 0); err == nil {
		t.Fatal("pressed Enter after agent exited")
	}
}

type cancelAfterPRPaste struct {
	tmux.Executor
	cancel context.CancelFunc
}

func (ex cancelAfterPRPaste) SendTextContext(ctx context.Context, pane, text string) error {
	err := ex.Executor.SendTextContext(ctx, pane, text)
	ex.cancel()
	return err
}

func TestPRLoopCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ex := tmux.NewFakeExecutor()
	ex.ExecHook = func([]string) (string, bool, error) { return "0|0||claude", true, nil }
	cancel()
	if err := sendNativePRCommand(ctx, ex, "%1", "/pr-ready 333", 0); !errors.Is(err, context.Canceled) || len(ex.SendLinesCalls) != 0 {
		t.Fatalf("cancel before paste: err=%v, sends=%v", err, ex.SendLinesCalls)
	}
	if err := waitPRPoll(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll did not cancel: %v", err)
	}
	err := runPRLoop(ctx, testPRLoopOptions(),
		func(context.Context, string, int) (prPhase, error) { t.Fatal("queried after cancel"); return "", nil },
		nil, nil, time.Now, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loop did not cancel: %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	err = sendNativePRCommand(ctx, cancelAfterPRPaste{ex, cancel}, "%1", "/pr-ready 333", time.Hour)
	if !errors.Is(err, context.Canceled) || len(ex.SendLinesCalls) != 1 {
		t.Fatalf("cancel after paste: err=%v, sends=%v", err, ex.SendLinesCalls)
	}
	for _, args := range ex.ExecCalls {
		if args[0] == "send-keys" {
			t.Fatalf("sent Enter after cancellation: %v", args)
		}
	}
}

func fakePRGH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$*" != 'pr view 333 --repo owner/repo --json number,state,isDraft' ]; then
  echo 'unexpected gh arguments' >&2
  exit 2
fi
if [ -n "$TMUXAI_TEST_PR_STATE" ]; then
  cat "$TMUXAI_TEST_PR_STATE"
else
  printf '%s' "$TMUXAI_TEST_PR_JSON"
fi
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestPRLoopCommandWithFakeGH(t *testing.T) {
	fakePRGH(t)
	for _, state := range []string{"OPEN", "MERGED"} {
		t.Setenv("TMUXAI_TEST_PR_JSON", fmt.Sprintf(`{"number":333,"state":%q,"isDraft":false}`, state))
		command := newPRLoopCommand()
		command.SetArgs([]string{"333", "--repo", "owner/repo", "--draft-pane", "project:1", "--ready-pane", "project:2", "--dry-run", "--stable-polls", "1"})
		var out bytes.Buffer
		command.SetOut(&out)
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		want := "$pr-review-merge 333"
		if state == "MERGED" {
			want = "merged; stopped"
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("command output = %s; want %s", out.String(), want)
		}
	}
}

func TestLivePRLoopNativeCommands(t *testing.T) {
	ex, target := livePane(t, "submit")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, text := range []string{"/pr-ready 333", "$pr-review-merge 333"} {
		if err := sendNativePRCommand(ctx, ex, target, text, 300*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		for {
			screen, err := ex.CaptureScreenClean(target)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(screen, "user: "+text) {
				break
			}
			if err := waitPRPoll(ctx, 50*time.Millisecond); err != nil {
				t.Fatalf("command not submitted verbatim: %q; screen=%s", text, screen)
			}
		}
	}
}

// Run the command itself against two real panes. Only gh is faked: the agent
// stand-ins publish draft -> ready -> draft -> ready -> merged after receiving
// exactly the expected native command. No GitHub repository is contacted.
func TestLivePRLoopCycleWithFakeGH(t *testing.T) {
	if os.Getenv("TMUXAI_LIVE_TMUX") != "1" {
		t.Skip("set TMUXAI_LIVE_TMUX=1 to run live PR loop tests")
	}
	dir := fakePRGH(t)
	statePath := filepath.Join(dir, "pr.json")
	logPath := filepath.Join(dir, "commands.log")
	t.Setenv("TMUXAI_TEST_PR_STATE", statePath)
	if err := os.WriteFile(statePath, []byte(`{"number":333,"state":"OPEN","isDraft":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "agent.py")
	const agent = `import json, os, sys
role, state, log = sys.argv[1:]
expected = "/custom-ready 333" if role == "draft" else "$custom-review 333"
count = 0
while True:
    text = input("> ")
    with open(log, "a") as out:
        out.write(role + ":" + text + "\n")
    if text != expected:
        sys.exit(2)
    count += 1
    merged = role == "ready" and count == 2
    value = {"number":333, "state":"MERGED" if merged else "OPEN", "isDraft":role == "ready" and not merged}
    temporary = state + "." + role
    with open(temporary, "w") as out:
        json.dump(value, out)
    os.replace(temporary, state)
`
	if err := os.WriteFile(script, []byte(agent), 0o600); err != nil {
		t.Fatal(err)
	}
	ex := tmux.NewExecutor()
	session := fmt.Sprintf("tmuxai-pr-loop-%d", os.Getpid())
	agentCommand := func(role string) string {
		return "python3 " + shellQuote(script) + " " + role + " " + shellQuote(statePath) + " " + shellQuote(logPath)
	}
	if _, err := ex.Exec("new-session", "-d", "-s", session, "-n", "draft", agentCommand("draft")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = ex.Exec("kill-session", "-t", session) })
	if _, err := ex.Exec("new-window", "-d", "-t", session, "-n", "ready", agentCommand("ready")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Wait for actual foreground programs, rather than relying on a startup sleep.
	for _, pane := range []string{session + ":draft", session + ":ready"} {
		for {
			state := readPaneState(ex, pane)
			if state.Known && strings.HasPrefix(state.Command, "python") {
				break
			}
			if err := waitPRPoll(ctx, 20*time.Millisecond); err != nil {
				t.Fatalf("agent did not start in %s: %v", pane, err)
			}
		}
	}
	command := newPRLoopCommand()
	command.SetContext(ctx)
	command.SetArgs([]string{"333", "--repo", "owner/repo",
		"--draft-pane", session + ":draft", "--ready-pane", session + ":ready",
		"--draft-command", "/custom-ready {pr}", "--ready-command", "$custom-review {pr}",
		"--poll-interval", "25ms", "--submit-delay", "50ms", "--max-actions", "4"})
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.Execute(); err != nil {
		t.Fatalf("PR loop failed: %v\n%s", err, out.String())
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "draft:/custom-ready 333\nready:$custom-review 333\ndraft:/custom-ready 333\nready:$custom-review 333\n"
	if string(commands) != want || !strings.Contains(out.String(), "merged; stopped") {
		t.Fatalf("commands=%q; output=%s", commands, out.String())
	}
}
