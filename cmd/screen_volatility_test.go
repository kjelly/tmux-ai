package cmd

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/tmux"
)

// settleVolatility feeds enough captures for the filter to form a judgement and
// returns the stability of the final one.
func settleVolatility(t *testing.T, protocol roundControlProtocol, screens []string) (screenStability, []string) {
	t.Helper()
	var v screenVolatility
	keys := make([]string, 0, len(screens))
	var last screenStability
	for _, screen := range screens {
		last = v.observe(screen, protocol)
		keys = append(keys, last.Key)
	}
	return last, keys
}

func TestVolatilityIgnoresARepaintingFooter(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "chrome", 1, t.TempDir(), true)
	screens := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		rows := []string{"review finished", "no further findings"}
		for row := 0; row < 10; row++ {
			rows = append(rows, "finding "+strconv.Itoa(row))
		}
		rows = append(rows, "context left: "+strconv.Itoa(90-i)+"%")
		screens = append(screens, strings.Join(rows, "\n"))
	}

	stability, keys := settleVolatility(t, protocol, screens)
	if stability.Excluded != 1 {
		t.Fatalf("excluded = %d, want exactly the footer row", stability.Excluded)
	}
	if strings.Contains(stability.Key, "context left") {
		t.Fatalf("key still contains the footer: %q", stability.Key)
	}
	if !strings.Contains(stability.Key, "no further findings") {
		t.Fatalf("key dropped real content: %q", stability.Key)
	}
	if keys[len(keys)-1] != keys[len(keys)-2] {
		t.Fatal("key kept changing while only the footer repainted")
	}
	if screens[len(screens)-1] == screens[len(screens)-2] {
		t.Fatal("test screens are not actually repainting")
	}
}

// The pending-input watch compares live captures against one taken before
// Enter, so the repainting rows learned from the live ones have to be excluded
// from that earlier screen too.
func TestRepaintingRowsSeparateAFooterFromAConsumedInputArea(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "mask", 1, "", true)
	var v screenVolatility
	body := []string{"> please review the diff", "", "", ""}
	pending := strings.Join(append(body, "  0s · esc to interrupt"), "\n")
	for i := 0; i < 12; i++ {
		v.observe(strings.Join(append(body, "  "+strconv.Itoa(i)+"s · esc to interrupt"), "\n"), protocol)
	}
	if got := countMasked(v.repaintingRows(submitRepaintMinChanges)); got != 1 {
		t.Fatalf("repainting rows = %d, want exactly the footer", got)
	}

	stillPending := strings.Join(append(body, "  9s · esc to interrupt"), "\n")
	if inputAreaConsumed(&v, stillPending, pending) {
		t.Fatal("a repainting footer was read as the input area being consumed")
	}
	consumed := strings.Join([]string{"user: please review the diff", "assistant: reading it", "", ">", "  9s · esc to interrupt"}, "\n")
	if !inputAreaConsumed(&v, consumed, pending) {
		t.Fatal("a cleared input area was explained away as chrome")
	}
}

func TestVolatilityRefusesToFilterABusyScreen(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "busy", 1, t.TempDir(), true)
	// A streaming agent rewrites most of the pane. Reading that as "mostly
	// decoration, therefore idle" would be exactly backwards.
	screens := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		rows := make([]string, 0, 4)
		for row := 0; row < 4; row++ {
			rows = append(rows, "streamed output "+strconv.Itoa(i*4+row))
		}
		screens = append(screens, strings.Join(rows, "\n"))
	}

	stability, keys := settleVolatility(t, protocol, screens)
	if stability.Excluded != 0 {
		t.Fatalf("excluded = %d, want none on a busy screen", stability.Excluded)
	}
	if stability.Key != screens[len(screens)-1] {
		t.Fatal("busy screen did not fall back to the whole capture")
	}
	if keys[len(keys)-1] == keys[len(keys)-2] {
		t.Fatal("busy screen was reported as stable")
	}
}

func TestVolatilityNeverExcludesProtocolRows(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "protocol-row", 1, t.TempDir(), true)
	screens := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		screens = append(screens, strings.Join([]string{
			"stable output",
			"stable output two",
			// The terminal keeps repainting the receipt line itself.
			protocol.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"` + strconv.Itoa(i) + `"}`,
		}, "\n"))
	}

	stability, _ := settleVolatility(t, protocol, screens)
	if !strings.Contains(stability.Key, protocol.StatusMarker) {
		t.Fatalf("a repainting receipt row was treated as chrome: %q", stability.Key)
	}
}

func TestVolatilityWithholdsJudgementUntilEnoughSamples(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "early", 1, t.TempDir(), true)
	var v screenVolatility
	for i := 0; i < volatileMinSamples-1; i++ {
		screen := "content\nclock " + strconv.Itoa(i)
		stability := v.observe(screen, protocol)
		if stability.Excluded != 0 || stability.Key != screen {
			t.Fatalf("sample %d filtered before enough history: %#v", i, stability)
		}
	}
}

func TestReceiptIdleFallbackFiresWhileChromeRepaints(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "idle-chrome", 1, t.TempDir(), true)
	receipt := strings.Join([]string{
		protocol.StatusBeginMarker,
		`{"version":1,"outcome":"needs_followup","summary":"handed off despite a live footer"}`,
		protocol.StatusEndMarker,
	}, "\n")

	captures := 0
	ex := tmux.NewFakeExecutor()
	ex.CaptureHook = func(string, int) (string, error) {
		captures++
		return receipt + "\nesc to interrupt (" + strconv.Itoa(captures) + "s)", nil
	}

	oldInterval, oldTimeout, oldReceiptIdle := ppInterval, ppRoundTimeout, ppReceiptIdleTimeout
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = 1, 5, 1
	defer func() {
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = oldInterval, oldTimeout, oldReceiptIdle
	}()

	output, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, nil, submitResult{})
	if err != nil {
		t.Fatalf("waitForCompletionWithFallback() error = %v", err)
	}
	status, err := parseProtocolStatus(output, protocol)
	if err != nil || status.Summary != "handed off despite a live footer" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

func TestSlowProgressRowLooksStableButTheReceiptGateStillHolds(t *testing.T) {
	// Frequency alone cannot separate a spinner from "Analyzing 37/512": both
	// are one row rewritten in place. The filter therefore does call this
	// screen stable — and the receipt requirement is what keeps that from
	// becoming a premature handoff.
	protocol := newRoundControlProtocol(handoffTransportFile, "progress", 1, t.TempDir(), true)
	captures := 0
	ex := tmux.NewFakeExecutor()
	ex.CaptureHook = func(string, int) (string, error) {
		captures++
		rows := make([]string, 0, 12)
		for row := 0; row < 11; row++ {
			rows = append(rows, "context line "+strconv.Itoa(row))
		}
		return strings.Join(append(rows, "Analyzing "+strconv.Itoa(captures)+"/512"), "\n"), nil
	}

	oldInterval, oldTimeout, oldReceiptIdle := ppInterval, ppRoundTimeout, ppReceiptIdleTimeout
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = 1, 1, 1
	defer func() {
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = oldInterval, oldTimeout, oldReceiptIdle
	}()

	if _, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, nil, submitResult{}); err == nil {
		t.Fatal("a working pane with no receipt was handed off")
	}
}
