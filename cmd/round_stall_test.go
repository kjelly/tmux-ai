package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func stalledRound(t *testing.T, protocol roundControlProtocol, screen string) string {
	t.Helper()
	ex := tmux.NewFakeExecutor()
	ex.Captures["trec:2"] = screen

	oldInterval, oldTimeout, oldReceiptIdle := ppInterval, ppRoundTimeout, ppReceiptIdleTimeout
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = 1, 1, 0
	defer func() {
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = oldInterval, oldTimeout, oldReceiptIdle
	}()

	_, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, nil, submitResult{})
	if err == nil {
		t.Fatal("round unexpectedly completed")
	}
	return err.Error()
}

func TestRoundTimeoutNamesTheActualStall(t *testing.T) {
	t.Run("no completion marker at all", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportFile, "silent", 1, t.TempDir(), true)
		got := stalledRound(t, protocol, "still thinking about the review")
		if !strings.Contains(got, "未輸出完成標記") {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("marker without any receipt", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportFile, "no-receipt", 1, t.TempDir(), true)
		got := stalledRound(t, protocol, "work done\n"+protocol.DoneMarker)
		if !strings.Contains(got, "沒有可用的 status receipt") || !strings.Contains(got, "missing") {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("marker with an invalid receipt is deferred to receipt repair", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportFile, "bad-outcome", 1, t.TempDir(), true)
		screen := strings.Join([]string{
			protocol.StatusBeginMarker,
			`{"version":1,"outcome":"partially","summary":"nearly there"}`,
			protocol.StatusEndMarker,
			protocol.DoneMarker,
		}, "\n")
		ex := tmux.NewFakeExecutor()
		ex.Captures["trec:2"] = screen
		oldInterval, oldTimeout, oldSettle := ppInterval, ppRoundTimeout, ppCompletionSettleTimeout
		ppInterval, ppRoundTimeout, ppCompletionSettleTimeout = 1, 1, 0
		defer func() {
			ppInterval, ppRoundTimeout, ppCompletionSettleTimeout = oldInterval, oldTimeout, oldSettle
		}()
		output, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, nil, submitResult{})
		if !errors.Is(err, errReceiptRepairRequired) || output != "" {
			t.Fatalf("output=%q err=%v", output, err)
		}
	})

	t.Run("text frame never closed", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportText, "open-frame", 1, "", true)
		screen := strings.Join([]string{
			protocol.TextBeginMarker,
			"the payload the agent forgot to close",
			protocol.StatusBeginMarker,
			`{"version":1,"outcome":"needs_followup","summary":"done"}`,
			protocol.StatusEndMarker,
			protocol.DoneMarker,
		}, "\n")
		got := stalledRound(t, protocol, screen)
		if !strings.Contains(got, "沒有輸出結束 marker") {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("text frame opened but never begun", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportText, "no-begin", 1, "", true)
		screen := strings.Join([]string{
			"the payload without its begin marker",
			protocol.TextEndMarker,
			protocol.StatusBeginMarker,
			`{"version":1,"outcome":"needs_followup","summary":"done"}`,
			protocol.StatusEndMarker,
			protocol.DoneMarker,
		}, "\n")
		got := stalledRound(t, protocol, screen)
		if !strings.Contains(got, "text handoff frame") || !strings.Contains(got, "不完整") {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("receipt without a completion marker", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportFile, "receipt-only", 1, t.TempDir(), true)
		screen := strings.Join([]string{
			protocol.StatusBeginMarker,
			`{"version":1,"outcome":"needs_followup","summary":"finished but silent"}`,
			protocol.StatusEndMarker,
		}, "\n")
		got := stalledRound(t, protocol, screen)
		if !strings.Contains(got, "有效 status receipt") || !strings.Contains(got, "此 fallback 已停用") {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("goal was never closed", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportFile, "open-goal", 1, t.TempDir(), true)
		protocol.GoalMode, protocol.GoalDoneMarker = true, roundControlMarker("GOAL_DONE", "open-goal", 1)
		got := stalledRound(t, protocol, "work done\n"+protocol.DoneMarker)
		if !strings.Contains(got, "goal 尚未實際關閉") {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("goal mode with no marker at all keeps the original message", func(t *testing.T) {
		protocol := newRoundControlProtocol(handoffTransportFile, "quiet-goal", 1, t.TempDir(), true)
		protocol.GoalMode, protocol.GoalDoneMarker = true, roundControlMarker("GOAL_DONE", "quiet-goal", 1)
		got := stalledRound(t, protocol, "still working")
		if !strings.Contains(got, "未先輸出 goal completion marker") {
			t.Fatalf("error = %q", got)
		}
	})
}

func TestRoundStallReportsGoalClearInstabilityInsteadOfStaleReceiptError(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "goal-clear", 1, t.TempDir(), true)
	protocol.GoalMode = true
	diagnosis := roundStallDiagnosis{}
	diagnosis.noteMissingEnvelope(strings.Join([]string{
		protocol.StatusBeginMarker,
		`{"version":2,"outcome":"needs_followup","summary":"unsupported version"}`,
		protocol.StatusEndMarker,
		protocol.DoneMarker,
	}, "\n"), protocol)
	if diagnosis.receiptErr == nil {
		t.Fatal("expected malformed receipt to be recorded before the complete envelope arrives")
	}
	diagnosis.noteValidatedEnvelope()
	diagnosis.noteGoalClearPending()
	err := diagnosis.reason("pane:1", protocol, 1800, "goal UI still repainting")
	if !strings.Contains(err.Error(), "/goal clear") || strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("goal-clear timeout diagnostic = %v", err)
	}
}

func TestRoundTimeoutReportsAnEnvelopeThatKeepsChanging(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "unstable", 1, t.TempDir(), true)
	captures := 0
	ex := tmux.NewFakeExecutor()
	ex.CaptureHook = func(string, int) (string, error) {
		captures++
		return strings.Join([]string{
			protocol.StatusBeginMarker,
			`{"version":1,"outcome":"needs_followup","summary":"rewritten ` + strings.Repeat("x", captures) + `"}`,
			protocol.StatusEndMarker,
			protocol.DoneMarker,
		}, "\n"), nil
	}

	oldInterval, oldTimeout, oldSettle := ppInterval, ppRoundTimeout, ppCompletionSettleTimeout
	ppInterval, ppRoundTimeout, ppCompletionSettleTimeout = 1, 1, 60000
	defer func() {
		ppInterval, ppRoundTimeout, ppCompletionSettleTimeout = oldInterval, oldTimeout, oldSettle
	}()

	_, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, nil, submitResult{})
	if err == nil || !strings.Contains(err.Error(), "始終沒有穩定") {
		t.Fatalf("error = %v", err)
	}
}
