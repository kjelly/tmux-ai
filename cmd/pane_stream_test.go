package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func TestPaneStreamCollapsesRepaintsAndKeepsTheLatestWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pane.raw")
	stream := &paneStream{paneID: "%1", path: path, maxLines: 3}

	// The same row painted four times must not consume the whole window.
	raw := strings.Repeat("\x1b[1;1Hworking…", 4) + "\x1b[2;1Hdone"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	text, ok := stream.snapshot()
	if !ok {
		t.Fatal("snapshot() ok = false, want true")
	}
	if lines := strings.Split(text, "\n"); len(lines) > 3 || !strings.Contains(text, "done") {
		t.Fatalf("snapshot() = %q", text)
	}

	// A later read must append only what is new, not re-ingest the file.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\x1b[3;1Hlater"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	text, _ = stream.snapshot()
	if !strings.HasSuffix(text, "later") || strings.Count(text, "done") != 1 {
		t.Fatalf("snapshot() after append = %q", text)
	}
}

func TestPaneStreamWithholdsAPartialEscapeSequence(t *testing.T) {
	consume, hold := splitAtIncompleteEscape([]byte("visible\x1b[12"))
	if string(consume) != "visible" || string(hold) != "\x1b[12" {
		t.Fatalf("consume=%q hold=%q", consume, hold)
	}
	consume, hold = splitAtIncompleteEscape([]byte("visible\x1b[12Hmore"))
	if string(consume) != "visible\x1b[12Hmore" || len(hold) != 0 {
		t.Fatalf("complete sequence was withheld: consume=%q hold=%q", consume, hold)
	}
}

func TestStartPaneStreamLeavesAnAlreadyPipedPaneAlone(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.ExecResult["display-message -p -t %9 #{pane_pipe}"] = "1\n"
	if _, err := startPaneStream(ex, "%9", t.TempDir()); err == nil {
		t.Fatal("startPaneStream() took over a pane that was already piped")
	}
	for _, call := range ex.ExecCalls {
		if len(call) > 0 && call[0] == "pipe-pane" {
			t.Fatalf("startPaneStream() ran pipe-pane anyway: %v", call)
		}
	}
}

func TestWaitForCompletionRecoversARepaintedCompletionFromTheStream(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "stream", 1, t.TempDir(), true)
	// A TUI that repaints its whole screen leaves no trace of the completed
	// turn in the capture, but every painted row is still in the stream.
	row := func(n int, text string) string { return "\x1b[" + strconv.Itoa(n) + ";1H" + text }
	raw := strings.Join([]string{
		row(1, "\x1b[2Jworking on the review"),
		row(2, protocol.StatusBeginMarker),
		row(3, `{"version":1,"outcome":"needs_followup","summary":"recovered from the stream"}`),
		row(4, protocol.StatusEndMarker),
		row(5, protocol.DoneMarker),
		row(1, "\x1b[2J> "),
	}, "")

	dir := t.TempDir()
	path := filepath.Join(dir, "pane.raw")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	stream := &paneStream{paneID: "trec:2", path: path, maxLines: paneStreamMaxLines}

	ex := tmux.NewFakeExecutor()
	ex.Captures["trec:2"] = "> "

	oldInterval, oldTimeout, oldSettle := ppInterval, ppRoundTimeout, ppCompletionSettleTimeout
	ppInterval, ppRoundTimeout, ppCompletionSettleTimeout = 1, 3, 0
	defer func() {
		ppInterval, ppRoundTimeout, ppCompletionSettleTimeout = oldInterval, oldTimeout, oldSettle
	}()

	output, stopped, selected, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, stream)
	if err != nil {
		t.Fatalf("waitForCompletionWithFallback() error = %v", err)
	}
	if stopped || selected {
		t.Fatalf("stopped=%t selected=%t", stopped, selected)
	}
	status, err := parseProtocolStatus(output, protocol)
	if err != nil {
		t.Fatalf("recovered envelope did not parse: %v (output=%q)", err, output)
	}
	if status.Summary != "recovered from the stream" {
		t.Fatalf("status = %#v", status)
	}
}

func TestStreamCannotCompleteARoundWithoutAValidEnvelope(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "stream-noise", 1, t.TempDir(), true)
	dir := t.TempDir()
	path := filepath.Join(dir, "pane.raw")
	// The completion marker alone, with no receipt anywhere, must not hand off.
	if err := os.WriteFile(path, []byte("\x1b[1;1H"+protocol.DoneMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	stream := &paneStream{paneID: "trec:2", path: path, maxLines: paneStreamMaxLines}

	ex := tmux.NewFakeExecutor()
	ex.Captures["trec:2"] = "> "

	oldInterval, oldTimeout, oldReceiptIdle := ppInterval, ppRoundTimeout, ppReceiptIdleTimeout
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = 1, 1, 0
	defer func() {
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = oldInterval, oldTimeout, oldReceiptIdle
	}()

	if _, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, stream); err == nil {
		t.Fatal("a bare stream marker with no receipt completed the round")
	}
}

func TestStreamMarkerDoesNotBlockTheReceiptIdleFallback(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "stream-blocks", 1, t.TempDir(), true)
	path := filepath.Join(t.TempDir(), "pane.raw")
	// The stream saw a completion marker it cannot back with a receipt. That
	// is weaker evidence than the pane, which does hold a valid receipt.
	if err := os.WriteFile(path, []byte("\x1b[1;1H"+protocol.DoneMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	stream := &paneStream{paneID: "trec:2", path: path, maxLines: paneStreamMaxLines}

	ex := tmux.NewFakeExecutor()
	ex.Captures["trec:2"] = strings.Join([]string{
		protocol.StatusBeginMarker,
		`{"version":1,"outcome":"needs_followup","summary":"receipt without a marker"}`,
		protocol.StatusEndMarker,
	}, "\n")

	oldInterval, oldTimeout, oldReceiptIdle := ppInterval, ppRoundTimeout, ppReceiptIdleTimeout
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = 1, 5, 1
	defer func() {
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout = oldInterval, oldTimeout, oldReceiptIdle
	}()

	output, _, _, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", protocol, nil, stream)
	if err != nil {
		t.Fatalf("waitForCompletionWithFallback() error = %v", err)
	}
	status, err := parseProtocolStatus(output, protocol)
	if err != nil || status.Summary != "receipt without a marker" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}
