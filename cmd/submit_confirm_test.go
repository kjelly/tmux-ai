package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

// submitExecCalls drops the pane-state probes so a test can assert on the keys
// that were actually sent to a pane.
func submitExecCalls(ex *tmux.FakeExecutor) [][]string {
	var calls [][]string
	for _, call := range ex.ExecCalls {
		if len(call) > 0 && call[0] == "display-message" {
			continue
		}
		calls = append(calls, call)
	}
	return calls
}

func paneStateResult(inMode, dead bool, mode, command string) string {
	flag := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	return fmt.Sprintf("%s|%s|%s|%s\n", flag(inMode), flag(dead), mode, command)
}

func stubPaneState(ex *tmux.FakeExecutor, paneID, result string) {
	ex.ExecResult["display-message -p -t "+paneID+" "+paneStateFormat] = result
}

func fastSubmit(t *testing.T) {
	t.Helper()
	oldDelay, oldConfirm, oldRetries := ppSubmitDelay, ppSubmitConfirmTimeout, ppSubmitRetries
	oldShell, oldLines := ppAllowShellPane, ppLines
	ppSubmitDelay, ppSubmitConfirmTimeout, ppSubmitRetries = 0, 0, 0
	ppAllowShellPane, ppLines = false, 100
	t.Cleanup(func() {
		ppSubmitDelay, ppSubmitConfirmTimeout, ppSubmitRetries = oldDelay, oldConfirm, oldRetries
		ppAllowShellPane, ppLines = oldShell, oldLines
	})
}

func TestSendToPaneRefusesPaneThatCannotReceiveInput(t *testing.T) {
	t.Run("dead pane", func(t *testing.T) {
		fastSubmit(t)
		ex := tmux.NewFakeExecutor()
		stubPaneState(ex, "%42", paneStateResult(false, true, "", "claude"))

		_, err := sendToPane(context.Background(), ex, "%42", "run the tests", "")
		if err == nil || !strings.Contains(err.Error(), "程序已結束") {
			t.Fatalf("sendToPane() error = %v", err)
		}
		if len(ex.SendLinesCalls) != 0 {
			t.Fatalf("dead pane received a paste: %#v", ex.SendLinesCalls)
		}
	})

	t.Run("foreground process is a shell", func(t *testing.T) {
		fastSubmit(t)
		ex := tmux.NewFakeExecutor()
		stubPaneState(ex, "%42", paneStateResult(false, false, "", "bash"))

		_, err := sendToPane(context.Background(), ex, "%42", "run the tests", "")
		if err == nil || !strings.Contains(err.Error(), "--allow-shell-pane") {
			t.Fatalf("sendToPane() error = %v", err)
		}
		if len(ex.SendLinesCalls) != 0 {
			t.Fatalf("shell pane received a paste: %#v", ex.SendLinesCalls)
		}

		ppAllowShellPane = true
		if _, err := sendToPane(context.Background(), ex, "%42", "run the tests", ""); err != nil {
			t.Fatalf("sendToPane() with --allow-shell-pane error = %v", err)
		}
	})

	t.Run("unknown state does not block the send", func(t *testing.T) {
		fastSubmit(t)
		ex := tmux.NewFakeExecutor()

		if _, err := sendToPane(context.Background(), ex, "%42", "run the tests", ""); err != nil {
			t.Fatalf("sendToPane() error = %v", err)
		}
		if len(ex.SendLinesCalls) != 1 {
			t.Fatalf("SendLines calls = %#v, want the paste", ex.SendLinesCalls)
		}
	})
}

// A paste into a pane that is in copy-mode is discarded by tmux outright, so the
// mode has to be cancelled before the paste rather than diagnosed after it.
func TestSendToPaneCancelsCopyModeBeforePasting(t *testing.T) {
	fastSubmit(t)
	ex := tmux.NewFakeExecutor()
	reads := 0
	ex.ExecHook = func(args []string) (string, bool, error) {
		if len(args) > 0 && args[0] == "display-message" {
			reads++
			// The first read finds copy-mode; the read after cancel does not.
			return paneStateResult(reads == 1, false, "copy-mode", "claude"), true, nil
		}
		return "", false, nil
	}

	if _, err := sendToPane(context.Background(), ex, "%42", "run the tests", ""); err != nil {
		t.Fatalf("sendToPane() error = %v", err)
	}
	want := [][]string{
		{"send-keys", "-X", "-t", "%42", "cancel"},
		{"send-keys", "-t", "%42", "Enter"},
	}
	got := submitExecCalls(ex)
	if len(got) != len(want) {
		t.Fatalf("submit calls = %#v, want %#v", got, want)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Fatalf("submit call %d = %v, want %v", i, got[i], want[i])
		}
	}
	if len(ex.SendLinesCalls) != 1 {
		t.Fatalf("SendLines calls = %#v, want the paste after the mode was cancelled", ex.SendLinesCalls)
	}
}

func TestSendToPaneStopsWhenOnlyChromeRepaints(t *testing.T) {
	fastSubmit(t)
	oldPoll, oldSettle := submitConfirmPollInterval, submitChromeSettle
	submitConfirmPollInterval, submitChromeSettle = time.Millisecond, 20*time.Millisecond
	ppSubmitConfirmTimeout, ppSubmitRetries = 300, 1
	t.Cleanup(func() { submitConfirmPollInterval, submitChromeSettle = oldPoll, oldSettle })

	// The pane holds the pasted prompt and repaints nothing but its footer
	// clock, which is exactly what an unsubmitted prompt looks like.
	ticks := 0
	ex := tmux.NewFakeExecutor()
	ex.CaptureHook = func(string, int) (string, error) {
		ticks++
		return strings.Join([]string{
			"> review the diff and report",
			"",
			"",
			"",
			fmt.Sprintf("  %ds · esc to interrupt", ticks),
		}, "\n"), nil
	}

	_, err := sendToPane(context.Background(), ex, "%42", "review the diff and report", "")
	if err == nil || !strings.Contains(err.Error(), "只出現持續重繪的 UI chrome 變化") {
		t.Fatalf("sendToPane() error = %v", err)
	}
	enters := 0
	for _, call := range submitExecCalls(ex) {
		if len(call) == 4 && call[0] == "send-keys" && call[3] == "Enter" {
			enters++
		}
	}
	if enters != 2 {
		t.Fatalf("Enter presses = %d, want the initial attempt plus one retry", enters)
	}
}

func TestSendToPaneConfirmsWhenTheInputAreaIsConsumed(t *testing.T) {
	fastSubmit(t)
	oldPoll, oldSettle := submitConfirmPollInterval, submitChromeSettle
	submitConfirmPollInterval, submitChromeSettle = time.Millisecond, 20*time.Millisecond
	ppSubmitConfirmTimeout, ppSubmitRetries = 300, 0
	t.Cleanup(func() { submitConfirmPollInterval, submitChromeSettle = oldPoll, oldSettle })

	ticks := 0
	ex := tmux.NewFakeExecutor()
	ex.CaptureHook = func(string, int) (string, error) {
		ticks++
		rows := []string{"> review the diff and report", "", "", ""}
		if ticks > 12 {
			// The composer emptied and the prompt moved into the transcript:
			// rows that are not repainting now differ.
			rows = []string{"user: review the diff and report", "assistant: reading the diff", "", ">"}
		}
		return strings.Join(append(rows, fmt.Sprintf("  %ds · esc to interrupt", ticks)), "\n"), nil
	}

	result, err := sendToPane(context.Background(), ex, "%42", "review the diff and report", "")
	if err != nil {
		t.Fatalf("sendToPane() error = %v", err)
	}
	if !result.Confirmed || result.Attempts != 1 {
		t.Fatalf("submit result = %+v, want confirmed on the first attempt", result)
	}
	if result.PendingScreen == "" {
		t.Fatal("submit result carries no pending screen for the round to compare against")
	}
}

// A round whose prompt was never consumed must be reported as a delivery
// failure, and must not reach the screen-idle selector: that screen is idle
// precisely because nothing was ever sent to the agent.
func TestRoundReportsAPromptThatWasNeverConsumed(t *testing.T) {
	oldInterval, oldTimeout, oldReceiptIdle := ppInterval, ppRoundTimeout, ppReceiptIdleTimeout
	oldScreenIdle, oldPendingTimeout, oldNudges := ppScreenIdleTimeout, ppPendingInputTimeout, ppPendingInputNudges
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = 1, 30, 0
	ppScreenIdleTimeout, ppPendingInputTimeout, ppPendingInputNudges = 1, 1, 1
	t.Cleanup(func() {
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = oldInterval, oldTimeout, oldReceiptIdle
		ppScreenIdleTimeout, ppPendingInputTimeout, ppPendingInputNudges = oldScreenIdle, oldPendingTimeout, oldNudges
	})

	pending := strings.Join([]string{
		"> please review the diff",
		"",
		"",
		"",
		"  0s · esc to interrupt",
	}, "\n")
	ticks := 0
	ex := tmux.NewFakeExecutor()
	ex.CaptureHook = func(string, int) (string, error) {
		ticks++
		lines := strings.Split(pending, "\n")
		lines[len(lines)-1] = fmt.Sprintf("  %ds · esc to interrupt", ticks)
		return strings.Join(lines, "\n"), nil
	}
	selectorCalls := 0
	fallback := func(context.Context, string) (string, error) {
		selectorCalls++
		return "looks finished to me", nil
	}

	protocol := newRoundControlProtocol(handoffTransportText, "unsent", 1, "", true)
	_, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "%42", pending, protocol, fallback, nil, submitResult{PendingScreen: pending})
	if err == nil || !strings.Contains(err.Error(), "沒有被 agent 接收") {
		t.Fatalf("waitForCompletionWithFallback() error = %v", err)
	}
	if selectorCalls != 0 {
		t.Fatalf("screen-idle selector ran %d times on a prompt that never reached the agent", selectorCalls)
	}
	enters := 0
	for _, call := range submitExecCalls(ex) {
		if len(call) == 4 && call[0] == "send-keys" && call[3] == "Enter" {
			enters++
		}
	}
	if enters != 1 {
		t.Fatalf("nudge Enter presses = %d, want exactly --pending-input-nudges", enters)
	}
}

func TestRoundKeepsWaitingOnceTheAgentStarts(t *testing.T) {
	oldInterval, oldTimeout, oldReceiptIdle := ppInterval, ppRoundTimeout, ppReceiptIdleTimeout
	oldPendingTimeout, oldNudges := ppPendingInputTimeout, ppPendingInputNudges
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = 1, 5, 0
	ppPendingInputTimeout, ppPendingInputNudges = 1, 0
	t.Cleanup(func() {
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = oldInterval, oldTimeout, oldReceiptIdle
		ppPendingInputTimeout, ppPendingInputNudges = oldPendingTimeout, oldNudges
	})

	protocol := newRoundControlProtocol(handoffTransportText, "started", 1, "", false)
	pending := "> please review the diff\n\n\n  0s · esc to interrupt"
	ticks := 0
	ex := tmux.NewFakeExecutor()
	ex.CaptureHook = func(string, int) (string, error) {
		ticks++
		body := "user: please review the diff\nassistant: reading it now"
		if ticks > 15 {
			body += "\n" + protocol.TextBeginMarker + "\nreview body\n" + protocol.TextEndMarker + "\n" + protocol.DoneMarker
		}
		return fmt.Sprintf("%s\n  %ds · esc to interrupt", body, ticks), nil
	}

	_, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "%42", pending, protocol, nil, nil, submitResult{PendingScreen: pending})
	if err != nil {
		t.Fatalf("waitForCompletionWithFallback() error = %v", err)
	}
}

func fastEcho(t *testing.T) {
	t.Helper()
	oldPoll, oldEcho, oldMode := submitConfirmPollInterval, promptEchoTimeout, ppInputEchoCheck
	submitConfirmPollInterval, promptEchoTimeout, ppInputEchoCheck = time.Millisecond, 30*time.Millisecond, inputEchoAuto
	t.Cleanup(func() {
		submitConfirmPollInterval, promptEchoTimeout, ppInputEchoCheck = oldPoll, oldEcho, oldMode
	})
}

// The runner knows exactly what it pasted, so the paste can be checked before
// Enter rather than inferred from the aftermath.
func TestSendToPaneVerifiesThePasteBeforePressingEnter(t *testing.T) {
	const echo = "TMUXAI_PROMPT_ECHO__TMUXAI_run_3"

	t.Run("marker on screen proceeds to Enter", func(t *testing.T) {
		fastSubmit(t)
		fastEcho(t)
		ex := tmux.NewFakeExecutor()
		ex.Captures["%42"] = "> review the diff\n" + echo

		result, err := sendToPane(context.Background(), ex, "%42", "review the diff\n"+echo, echo)
		if err != nil {
			t.Fatalf("sendToPane() error = %v", err)
		}
		if !hasEnterPress(ex) {
			t.Fatalf("Enter was not pressed after a verified paste: %#v", submitExecCalls(ex))
		}
		if result.PendingScreen == "" {
			t.Fatal("verified paste did not record a pending screen")
		}
	})

	t.Run("nothing arrived stops before Enter", func(t *testing.T) {
		fastSubmit(t)
		fastEcho(t)
		ex := tmux.NewFakeExecutor()
		// The pane looks exactly as it did before the paste: no marker, and
		// nothing else to suggest the text landed.
		ex.Captures["%42"] = "> \n  idle"

		_, err := sendToPane(context.Background(), ex, "%42", "review the diff\n"+echo, echo)
		if err == nil || !strings.Contains(err.Error(), echo) {
			t.Fatalf("sendToPane() error = %v", err)
		}
		if hasEnterPress(ex) {
			t.Fatal("Enter was pressed into a pane that never received the paste")
		}
	})

	t.Run("collapsed paste placeholder is not treated as a lost paste", func(t *testing.T) {
		fastSubmit(t)
		fastEcho(t)
		ex := tmux.NewFakeExecutor()
		// A TUI that folds a large paste never renders its tail, so the marker
		// cannot appear however long the runner waits.
		ex.Captures["%42"] = "> [Pasted text #1 +812 lines]\n  ready"

		if _, err := sendToPane(context.Background(), ex, "%42", "review the diff\n"+echo, echo); err != nil {
			t.Fatalf("sendToPane() error = %v", err)
		}
		if !hasEnterPress(ex) {
			t.Fatal("a collapsed paste blocked the submission")
		}

		ppInputEchoCheck = inputEchoStrict
		strictEx := tmux.NewFakeExecutor()
		strictEx.Captures["%42"] = ex.Captures["%42"]
		_, err := sendToPane(context.Background(), strictEx, "%42", "review the diff\n"+echo, echo)
		if err == nil || !strings.Contains(err.Error(), "strict") {
			t.Fatalf("strict mode error = %v", err)
		}
		if hasEnterPress(strictEx) {
			t.Fatal("strict mode pressed Enter on an unverifiable paste")
		}
	})
}

func hasEnterPress(ex *tmux.FakeExecutor) bool {
	for _, call := range submitExecCalls(ex) {
		if len(call) == 4 && call[0] == "send-keys" && call[3] == "Enter" {
			return true
		}
	}
	return false
}

// Establishing nothing is not the same as establishing absence.
func TestPasteVerificationNeedsAPreEnterReferenceToClaimAbsence(t *testing.T) {
	fastEcho(t)
	ex := tmux.NewFakeExecutor()
	ex.Captures["%42"] = "> \n  idle"

	state, _, err := verifyPromptEcho(context.Background(), ex, "%42", "TMUXAI_PROMPT_ECHO__TMUXAI_run_1", "")
	if err != nil {
		t.Fatalf("verifyPromptEcho() error = %v", err)
	}
	if state != echoUnverifiable {
		t.Fatalf("state = %v, want unverifiable without a pre-paste screen", state)
	}
}
