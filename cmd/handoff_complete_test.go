package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/artifact"
	"github.com/llmore/tmux-ai/internal/tmux"
)

func helperTask(t *testing.T) (roundControlProtocol, string) {
	t.Helper()
	outbox := t.TempDir()
	p := newRoundControlProtocol(handoffTransportFile, "helper", 3, outbox, true)
	p.HeadNonce = "head-nonce"
	p.TaskID = "helper:3:pane1"
	p.CompletionMode = completionModeHelper
	path, err := writeHandoffTaskManifest(p, handoffMember{ID: "pane1"})
	if err != nil {
		t.Fatal(err)
	}
	p.TaskPath = path
	return p, path
}

func TestCompleteHandoffWritesAtomicReceiptAndIsIdempotent(t *testing.T) {
	p, taskPath := helperTask(t)
	var output bytes.Buffer
	request := handoffCompleteRequest{TaskPath: taskPath, PromptHead: p.HeadNonce, Outcome: "needs_followup", Summary: "fixed the regression", Completion: "done"}
	if err := completeHandoff(request, &output); err != nil {
		t.Fatal(err)
	}
	status, err := readFileReceipt(p)
	if err != nil {
		t.Fatal(err)
	}
	if status.Outcome != "needs_followup" || status.PromptHead != p.HeadNonce {
		t.Fatalf("status = %#v", status)
	}
	if !strings.Contains(output.String(), p.StatusBeginMarker+"\n") || !strings.HasSuffix(output.String(), p.DoneMarker+"\n") {
		t.Fatalf("output = %q", output.String())
	}
	first, err := os.ReadFile(fileReceiptPath(p))
	if err != nil {
		t.Fatal(err)
	}
	if err := completeHandoff(request, &bytes.Buffer{}); err != nil {
		t.Fatalf("idempotent complete: %v", err)
	}
	second, _ := os.ReadFile(fileReceiptPath(p))
	if !bytes.Equal(first, second) {
		t.Fatal("idempotent completion changed receipt")
	}
	if err := completeHandoff(handoffCompleteRequest{TaskPath: taskPath, PromptHead: p.HeadNonce, Outcome: "success", Summary: "different", Completion: "done"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflicting completion error = %v", err)
	}
}

func TestProtocolForHelperWritesTaskWithoutLeakingHeadNonce(t *testing.T) {
	store, err := artifact.New(t.TempDir(), true, artifact.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine := handoffEngine{
		Transport:          resolvedHandoffConfig{Transport: handoffTransportFile, CompletionMode: completionModeHelper},
		NewPromptHeadNonce: func() (string, error) { return "secret-head", nil },
	}
	p, err := engine.protocolFor(store, handoffMember{ID: "pane1"}, "run", 1)
	if err != nil {
		t.Fatal(err)
	}
	if p.TaskPath == "" || p.TaskID == "" {
		t.Fatalf("helper protocol = %#v", p)
	}
	data, err := os.ReadFile(p.TaskPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), p.HeadNonce) || !strings.Contains(string(data), "expected_prompt_head_sha256") {
		t.Fatalf("task manifest leaked head nonce: %s", data)
	}
	instruction := controlProtocolInstruction(p)
	if !strings.Contains(instruction, "tmux-ai handoff complete") || strings.Contains(instruction, "receipt JSON object") {
		t.Fatalf("helper instruction = %q", instruction)
	}
}

func TestCompleteHandoffRejectsWrongPromptHead(t *testing.T) {
	p, taskPath := helperTask(t)
	err := completeHandoff(handoffCompleteRequest{TaskPath: taskPath, PromptHead: "wrong", Outcome: "success", Summary: "done", Completion: "done"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "prompt head") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(fileReceiptPath(p)); !os.IsNotExist(err) {
		t.Fatalf("receipt exists after rejected completion: %v", err)
	}
}

func TestCompleteHandoffRejectsUnsafeOutboxAndTaskIsNotArtifact(t *testing.T) {
	p, taskPath := helperTask(t)
	if err := os.WriteFile(filepath.Join(p.Outbox, "review.md"), []byte("review"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := artifact.New(t.TempDir(), true, artifact.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// The helper task is control data: collect from its own outbox using a store
	// that has no previous snapshots and verify only the user artifact appears.
	refs, err := store.Collect("pane1", 3, p.Outbox, nil)
	if err != nil || len(refs) != 1 || refs[0].OriginalName != "review.md" {
		t.Fatalf("collect = %#v, %v", refs, err)
	}
	if err := os.Symlink(filepath.Join(p.Outbox, "review.md"), filepath.Join(p.Outbox, "bad-link")); err != nil {
		t.Fatal(err)
	}
	err = completeHandoff(handoffCompleteRequest{TaskPath: taskPath, PromptHead: p.HeadNonce, Outcome: "success", Summary: "done", Completion: "done"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unsafe outbox error = %v", err)
	}
}

func TestWaitForCompletionCommitsValidFileReceiptBeforePaneMarker(t *testing.T) {
	outbox := t.TempDir()
	p := newRoundControlProtocol(handoffTransportFile, "receipt-first", 1, outbox, true)
	p.HeadNonce = "seen-at-head"
	if err := os.WriteFile(fileReceiptPath(p), []byte(`{"version":1,"outcome":"success","summary":"done","prompt_head":"seen-at-head"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ex := tmux.NewFakeExecutor()
	oldInterval, oldTimeout := ppInterval, ppRoundTimeout
	ppInterval, ppRoundTimeout = 1, 2
	defer func() { ppInterval, ppRoundTimeout = oldInterval, oldTimeout }()
	output, stopped, selected, err := waitForCompletionWithFallback(context.Background(), ex, "%42", "baseline", p, nil, nil, submitResult{})
	if err != nil || stopped || selected {
		t.Fatalf("output=%q stopped=%t selected=%t err=%v", output, stopped, selected, err)
	}
	if !strings.Contains(output, p.StatusMarker) || len(ex.CaptureCalls) != 0 {
		t.Fatalf("output=%q capture calls=%#v", output, ex.CaptureCalls)
	}
}

func TestValidatedFileReceiptEnvelopeIgnoresTemporaryReceipt(t *testing.T) {
	p := newRoundControlProtocol(handoffTransportFile, "temporary", 1, t.TempDir(), true)
	p.HeadNonce = "head"
	if err := os.WriteFile(filepath.Join(p.Outbox, artifact.ReceiptFileName+".tmp-1"), []byte(`{"version":1,"outcome":"success","summary":"done","prompt_head":"head"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := validatedFileReceiptEnvelope(p, "pane"); ok {
		t.Fatal("temporary receipt was accepted")
	}
}
