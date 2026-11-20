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
		nil,
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
		func(o *prLoopOptions) { o.Repo = " " },
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

func TestPRLoopNumericTargetsUseCallerSession(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.ResolveResult = map[string]string{"$7:1": "%11", "$7:2": "%12", "1": "%wrong-session", "2": "%wrong-pane"}
	ex.ExecHook = func(args []string) (string, bool, error) {
		if !reflect.DeepEqual(args, []string{"display-message", "-p", "-t", "%99", "#{session_id}"}) {
			t.Fatalf("session lookup did not use caller pane: %v", args)
		}
		return "$7\n", true, nil
	}
	identity := func(target string) string { return target }
	draft, ready, err := resolvePRLoopTargets(ex, identity, "1", "2", "%99")
	if err != nil || draft != "%11" || ready != "%12" || len(ex.ExecCalls) != 1 {
		t.Fatalf("resolved=(%q,%q), session lookups=%d, err=%v", draft, ready, len(ex.ExecCalls), err)
	}
}

func TestPRLoopExplicitTargetsAndAliasesNeedNoCallerSession(t *testing.T) {
	for _, test := range []struct{ draft, ready string }{
		{"project:1", "project:2.0"},
		{"fixer", "reviewer"},
		{"1", "2"}, // Existing numeric aliases retain their precedence.
		{"%11", "%12"},
	} {
		ex := tmux.NewFakeExecutor()
		ex.ResolveResult = map[string]string{"project:1": "%11", "project:2.0": "%12"}
		aliases := map[string]string{"fixer": "%11", "reviewer": "%12", "1": "%11", "2": "%12"}
		alias := func(target string) string {
			if mapped, ok := aliases[target]; ok {
				return mapped
			}
			return target
		}
		draft, ready, err := resolvePRLoopTargets(ex, alias, test.draft, test.ready, "")
		if err != nil || draft != "%11" || ready != "%12" || len(ex.ExecCalls) != 0 {
			t.Fatalf("targets=%+v: resolved=(%q,%q), err=%v", test, draft, ready, err)
		}
	}
}

func TestPRLoopTargetResolutionFailures(t *testing.T) {
	identity := func(target string) string { return target }
	ex := tmux.NewFakeExecutor()
	if _, _, err := resolvePRLoopTargets(ex, identity, "1", "2", ""); err == nil || !strings.Contains(err.Error(), "session:window") {
		t.Fatalf("numeric targets outside tmux should fail: %v", err)
	}
	if _, _, err := resolvePRLoopTargets(ex, identity, "1", "2", "%99"); err == nil {
		t.Fatal("accepted an unknown caller session")
	}
	ex.ExecError = errors.New("caller pane no longer exists")
	if _, _, err := resolvePRLoopTargets(ex, identity, "1", "2", "%99"); !errors.Is(err, ex.ExecError) {
		t.Fatalf("lost session lookup failure: %v", err)
	}
	ex.ResolveResult = map[string]string{"project:1": "%11", "same-pane": "%11"}
	if _, _, err := resolvePRLoopTargets(ex, identity, "project:1", "same-pane", ""); err == nil {
		t.Fatal("accepted two targets resolving to the same pane")
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
		nil, nil, nil, time.Now, &bytes.Buffer{})
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
case "$*" in
  'pr view 333 --repo owner/repo --json number,url,headRefName'|'pr view 333 --json number,url,headRefName')
    printf '%s' '{"number":333,"url":"https://github.com/owner/repo/pull/333","headRefName":"pr-333"}'
    exit 0 ;;
  'pr checkout https://github.com/owner/repo/pull/333')
    printf '%s\n' "$PWD" >> "$TMUXAI_TEST_CHECKOUT_LOG"
    exec git switch pr-333 ;;
  'pr view 334 --repo owner/repo --json number,url,headRefName')
    printf '%s' '{"number":334,"url":"https://github.com/owner/repo/pull/334","headRefName":"pr-334"}'
    exit 0 ;;
  'pr checkout https://github.com/owner/repo/pull/334')
    printf '%s\n' "$PWD" >> "$TMUXAI_TEST_CHECKOUT_LOG"
    exec git switch pr-334 ;;
  'pr view 334 --repo owner/repo --json number,state,isDraft')
    case "$(cat "$TMUXAI_TEST_PR_STATE")" in
      *MERGED*) exec cat "$TMUXAI_TEST_PR_STATE_NEXT" ;;
      *) exit 2 ;;
    esac ;;
esac
if [ "$*" != "$TMUXAI_TEST_GH_ARGS" ]; then
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
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMUXAI_TEST_GH_ARGS", "pr view 333 --repo owner/repo --json number,state,isDraft")
	return dir
}

func TestPRLoopBatchCommand(t *testing.T) {
	for _, tt := range []struct {
		name, wantQueries, wantError string
		args, wantOutput             []string
	}{
		{
			name: "dry-run in argument order", args: []string{"335", "334", "333", "--observe", "--dry-run"},
			wantQueries: "335\n334\n333\n",
			wantOutput:  []string{"[1/3] PR #335", "initial command skipped", "[2/3] PR #334", "$pr-review-merge 334", "[3/3] PR #333", "merged"},
		},
		{
			name: "closed stops before next PR", args: []string{"333", "336", "334"},
			wantQueries: "333\n336\n", wantError: "PR #336 [2/3]: PR #336 is closed without being merged",
		},
		{
			name: "duplicate rejected before GitHub access", args: []string{"333", "0333"},
			wantError: "duplicate PR number: 333",
		},
		{
			name: "invalid number rejected before GitHub access", args: []string{"333", "abc"},
			wantError: "PR number must be a positive integer",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := fakePRGH(t)
			logPath := filepath.Join(dir, "queries.log")
			t.Setenv("TMUXAI_TEST_PR_QUERY_LOG", logPath)
			const gh = `#!/bin/sh
printf '%s\n' "$3" >> "$TMUXAI_TEST_PR_QUERY_LOG"
case "$3" in
  333) printf '%s' '{"number":333,"state":"MERGED"}' ;;
  334) printf '%s' '{"number":334,"state":"OPEN","isDraft":false}' ;;
  335) printf '%s' '{"number":335,"state":"OPEN","isDraft":true}' ;;
  336) printf '%s' '{"number":336,"state":"CLOSED"}' ;;
  *) exit 2 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o700); err != nil {
				t.Fatal(err)
			}
			command := newPRLoopCommand()
			command.SetArgs(append(tt.args, "--draft-pane", "project:1", "--ready-pane", "project:2", "--stable-polls", "1"))
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&out)
			err := command.Execute()
			if tt.wantError == "" && err != nil || tt.wantError != "" && (err == nil || !strings.Contains(err.Error(), tt.wantError)) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
			queries, err := os.ReadFile(logPath)
			if err != nil && !(tt.wantQueries == "" && errors.Is(err, os.ErrNotExist)) || string(queries) != tt.wantQueries {
				t.Fatalf("queries = %q (%v), want %q", queries, err, tt.wantQueries)
			}
			remaining := out.String()
			for _, want := range tt.wantOutput {
				_, after, found := strings.Cut(remaining, want)
				if !found {
					t.Fatalf("output = %q, missing %q after preceding actions", out.String(), want)
				}
				remaining = after
			}
		})
	}
}

func TestPRLoopWaitsForPanePromptBetweenPRs(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.ExecResult["display-message -p -t %1 "+paneStateFormat] = "0|0||codex"
	captures := []string{"• Working (1s • esc to interrupt)\n› Ask anything", "Waiting for your approval", "› Ask anything"}
	ex.CaptureHook = func(_ string, _ int) (string, error) {
		if len(captures) == 0 {
			t.Fatal("wait continued after the agent's prompt returned")
		}
		screen := captures[0]
		captures = captures[1:]
		return screen, nil
	}
	o := testPRLoopOptions()
	o.PollInterval = time.Millisecond
	o.PhaseTimeout = time.Second
	var out bytes.Buffer
	if err := waitForPRPanePrompt(context.Background(), ex, "%1", o, &out); err != nil {
		t.Fatal(err)
	}
	if len(captures) != 0 || strings.Count(out.String(), "Waiting for pane %1") != 1 {
		t.Fatalf("returned too early or reported repeatedly: captures=%v output=%q", captures, out.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ex.CaptureHook = func(_ string, _ int) (string, error) {
		cancel()
		return "Waiting for your approval", nil
	}
	if err := waitForPRPanePrompt(ctx, ex, "%1", o, &out); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}

func TestPRLoopResetsPaneBetweenPRs(t *testing.T) {
	const picker = "Where should the new conversation run?\n› 1. Use current Git worktree  Keep using the current working directory\n  2. Create new Git worktree"
	for _, tt := range []struct {
		name, wantError string
		screens         []string
		wantEnters      int
	}{
		{"direct reset", "", []string{"❯ /new", "❯"}, 1},
		{"worktree picker", "", []string{"› /new", "› /new", picker, picker, "› Ask anything"}, 2},
		{"picker canceled", "checkout selection was not confirmed", []string{"› /new", strings.Replace(picker, "› 1.", "  1.", 1), "› Ask anything"}, 1},
		{"agent rejected reset", "failure starting a new conversation", []string{"› /new", "Unknown command: /new\n› Ask anything"}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ex := tmux.NewFakeExecutor()
			ex.ExecResult["display-message -p -t %1 "+paneStateFormat] = "0|0||codex"
			screens := tt.screens
			ex.CaptureHook = func(_ string, _ int) (string, error) {
				if len(screens) == 0 {
					t.Fatal("kept polling after the last screen")
				}
				screen := screens[0]
				screens = screens[1:]
				return screen, nil
			}
			o := testPRLoopOptions()
			o.PollInterval, o.PhaseTimeout = time.Millisecond, time.Second
			var out bytes.Buffer
			err := resetPRPane(context.Background(), ex, "%1", o, &out)
			gotError := ""
			if err != nil {
				gotError = err.Error()
			}
			if (gotError == "") != (tt.wantError == "") || !strings.Contains(gotError, tt.wantError) {
				t.Fatalf("error = %q, want %q", gotError, tt.wantError)
			}
			enters := 0
			for _, call := range ex.ExecCalls {
				if reflect.DeepEqual(call, []string{"send-keys", "-t", "%1", "Enter"}) {
					enters++
				}
			}
			if len(screens) != 0 || len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].Content != "/new" || enters != tt.wantEnters {
				t.Fatalf("captures remaining = %v, pasted = %v, Enter presses = %d", screens, ex.SendLinesCalls, enters)
			}
		})
	}
}

func TestPRLoopCommandWithFakeGH(t *testing.T) {
	fakePRGH(t)
	for _, repo := range []string{"owner/repo", ""} {
		o := testPRLoopOptions()
		o.Repo = repo
		if err := o.validate(); err != nil {
			t.Fatalf("repo=%q should be accepted: %v", repo, err)
		}
		expected := "pr view 333 --repo owner/repo --json number,state,isDraft"
		if repo == "" {
			expected = "pr view 333 --json number,state,isDraft"
		}
		t.Setenv("TMUXAI_TEST_GH_ARGS", expected)
		for _, state := range []string{"OPEN", "MERGED"} {
			t.Setenv("TMUXAI_TEST_PR_JSON", fmt.Sprintf(`{"number":333,"state":%q,"isDraft":false}`, state))
			command := newPRLoopCommand()
			args := []string{"333", "--draft-pane", "project:1", "--ready-pane", "project:2", "--dry-run", "--stable-polls", "1"}
			if repo != "" {
				args = append(args, "--repo", repo)
			}
			command.SetArgs(args)
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
			if prepared := strings.Contains(out.String(), "Would prepare the PR branch"); prepared != (state == "OPEN") {
				t.Fatalf("unexpected branch preparation preview: %s", out.String())
			}
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
// stand-ins publish draft -> ready -> draft -> ready -> merged for the first PR,
// and draft -> ready -> merged for the second. No GitHub repository is contacted.
func TestLivePRLoopCycleWithFakeGH(t *testing.T) {
	if os.Getenv("TMUXAI_LIVE_TMUX") != "1" {
		t.Skip("set TMUXAI_LIVE_TMUX=1 to run live PR loop tests")
	}
	dir := fakePRGH(t)
	repoDir := initPRCheckoutRepository(t, filepath.Join(dir, "repo"))
	checkoutGit(t, repoDir, "branch", "pr-334")
	statePath := filepath.Join(dir, "pr.json")
	nextStatePath := filepath.Join(dir, "next-pr.json")
	logPath := filepath.Join(dir, "commands.log")
	checkoutLog := filepath.Join(dir, "checkout.log")
	t.Setenv("TMUXAI_TEST_CHECKOUT_LOG", checkoutLog)
	t.Setenv("TMUXAI_TEST_PR_STATE", statePath)
	t.Setenv("TMUXAI_TEST_PR_STATE_NEXT", nextStatePath)
	if err := os.WriteFile(statePath, []byte(`{"number":333,"state":"OPEN","isDraft":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nextStatePath, []byte(`{"number":334,"state":"OPEN","isDraft":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "agent.py")
	const agent = `import json, os, subprocess, sys
role, first_state, next_state, log = sys.argv[1:]
expected = "/custom-ready" if role == "draft" else "$custom-review"
count = 0
while True:
    text = input("> ")
    if text == "/new":
        with open(log, "a") as out:
            out.write(role + ":/new\n")
        print("\033[2J\033[H", end="", flush=True)
        continue
    command, number = text.split(" ")
    if command != expected or number not in ("333", "334"):
        sys.exit(2)
    if subprocess.check_output(["git", "branch", "--show-current"], text=True).strip() != "pr-" + number:
        sys.exit(3)
    with open(log, "a") as out:
        out.write(role + ":" + text + "\n")
    count += 1
    subprocess.run(["git", "switch", "main"], check=True)
    state = first_state if number == "333" else next_state
    merged = role == "ready" and (number == "334" or count == 2)
    value = {"number":int(number), "state":"MERGED" if merged else "OPEN", "isDraft":role == "ready" and not merged}
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
		return "python3 " + shellQuote(script) + " " + role + " " + shellQuote(statePath) + " " + shellQuote(nextStatePath) + " " + shellQuote(logPath)
	}
	if _, err := ex.Exec("new-session", "-d", "-s", session, "-n", "draft", "-c", repoDir, agentCommand("draft")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = ex.Exec("kill-session", "-t", session) })
	if _, err := ex.Exec("new-window", "-d", "-t", session, "-n", "ready", "-c", repoDir, agentCommand("ready")); err != nil {
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
	callerPane, err := ex.ResolveTarget(session + ":draft")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", callerPane)
	draftIndex, err := ex.Exec("display-message", "-p", "-t", callerPane, "#{window_index}")
	if err != nil {
		t.Fatal(err)
	}
	readyIndex, err := ex.Exec("display-message", "-p", "-t", session+":ready", "#{window_index}")
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"333", "334", "--repo", "owner/repo",
		"--draft-pane", strings.TrimSpace(draftIndex), "--ready-pane", strings.TrimSpace(readyIndex),
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
	want := "draft:/custom-ready 333\nready:$custom-review 333\ndraft:/custom-ready 333\nready:$custom-review 333\ndraft:/new\nready:/new\ndraft:/custom-ready 334\nready:$custom-review 334\n"
	if string(commands) != want || strings.Count(out.String(), "merged; stopped") != 2 {
		t.Fatalf("commands=%q; output=%s", commands, out.String())
	}
	checkouts, err := os.ReadFile(checkoutLog)
	if err != nil || string(checkouts) != strings.Repeat(repoDir+"\n", 6) {
		t.Fatalf("each draft and ready phase must check out the PR branch first: %q, err=%v", checkouts, err)
	}
}
