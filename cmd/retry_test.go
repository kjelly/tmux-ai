package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRunWithRetryRetriesAfterError(t *testing.T) {
	var calls int
	var out bytes.Buffer
	err := runWithRetry(context.Background(), func() error {
		calls++
		if calls == 1 {
			return errors.New("quota exhausted")
		}
		return nil
	}, strings.NewReader("y\n"), &out)
	if err != nil {
		t.Fatalf("runWithRetry() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("run calls = %d, want 2", calls)
	}
	if !strings.Contains(out.String(), "從頭重啟整個 workflow") {
		t.Fatalf("prompt missing from output: %q", out.String())
	}
}

func TestRunWithRetryDeclinesByDefault(t *testing.T) {
	var out bytes.Buffer
	err := runWithRetry(context.Background(), func() error {
		return errors.New("agent unavailable")
	}, strings.NewReader("n\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "retry declined") {
		t.Fatalf("runWithRetry() error = %v, want retry declined", err)
	}
}

func TestRunWithRetryDoesNotPromptAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := runWithRetry(ctx, func() error {
		t.Fatal("run must not execute after context cancellation")
		return nil
	}, strings.NewReader("y\n"), &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runWithRetry() error = %v, want context canceled", err)
	}
	if out.Len() != 0 {
		t.Fatalf("canceled retry wrote a prompt: %q", out.String())
	}
}

func TestRoundRetryMenuSecondCtrlCRequestsWorkflowCancellation(t *testing.T) {
	interrupt := make(chan os.Signal, 1)
	interrupt <- os.Interrupt
	var out bytes.Buffer
	got := askRoundRetryActionWithSignalIO(context.Background(), errors.New("agent quota reached"), false, true, interrupt, strings.NewReader(""), &out)
	if got != roundRetryCancelWorkflow {
		t.Fatalf("action = %v, want workflow cancellation", got)
	}
	if !strings.Contains(out.String(), "輸入 r") {
		t.Fatalf("retry menu output = %q", out.String())
	}
}

func TestRoundRetryPromptExplainsItOnlyWaits(t *testing.T) {
	prompt := roundRetryPrompt(errors.New("receipt missing"))
	for _, want := range []string{"只會重新等待目前 pane", "不會重送任務", "是否繼續等待此 round"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("round retry prompt missing %q: %q", want, prompt)
		}
	}
}

func TestRoundRetryPromptOffersOptInLLMReceiptRecovery(t *testing.T) {
	prompt := roundRetryPromptForOptions(errors.New("receipt repair failed"), true, true)
	for _, want := range []string{"輸入 l", "目前 pane 畫面", "靜止畫面", "不會送訊息給 agent", "輸入 r", "可能再次執行", "[y/l/r/N]"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("LLM recovery prompt missing %q: %q", want, prompt)
		}
	}
	for input, want := range map[string]roundRetryAction{
		"y":   roundRetryWait,
		"l":   roundRetryLLMRecovery,
		"LLM": roundRetryLLMRecovery,
		"r":   roundRetryResendPrompt,
		"n":   roundRetryAbort,
	} {
		if got := parseRoundRetryAction(input, true, true); got != want {
			t.Errorf("parseRoundRetryAction(%q) = %v, want %v", input, got, want)
		}
	}
	if got := parseRoundRetryAction("l", false, false); got != roundRetryAbort {
		t.Fatalf("LLM recovery without permission = %v, want abort", got)
	}
	if got := parseRoundRetryAction("r", false, false); got != roundRetryAbort {
		t.Fatalf("prompt resend without permission = %v, want abort", got)
	}
}

func TestPendingRelayRetryOnlyAcceptsExplicitResend(t *testing.T) {
	next := handoffMember{Label: "fixer", Target: "hufu:2"}
	for input, want := range map[string]bool{"r\n": true, "resend\n": true, "重送\n": true, "y\n": false, "n\n": false} {
		var out bytes.Buffer
		got := askPendingRelayRetryIO(context.Background(), errors.New("paste absent"), next, nil, strings.NewReader(input), &out)
		if got != want {
			t.Errorf("askPendingRelayRetryIO(%q) = %t, want %t", input, got, want)
		}
		for _, required := range []string{"目標 pane", "不會重新執行來源 pane", "[r/N]"} {
			if !strings.Contains(out.String(), required) {
				t.Errorf("pending relay prompt missing %q: %q", required, out.String())
			}
		}
	}
}
