package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/artifact"
	"github.com/llmore/tmux-ai/internal/tmux"
)

func journalResumeEngine(t *testing.T, root, statePath string, ex tmux.Executor, route handoffRouter) handoffEngine {
	t.Helper()
	return handoffEngine{
		Executor: ex, Guard: newHandoffGuard(),
		Members: map[string]handoffMember{
			"pane1": {ID: "pane1", Label: "worker", Target: "%1"},
		},
		StartID: "pane1", InitialInstruction: "do work", InitialFingerprint: "do work",
		Transport: resolvedHandoffConfig{Transport: handoffTransportFile, Root: root, Keep: true, Limits: artifact.DefaultLimits()},
		Route:     route, ResumeHandoffPath: statePath,
	}
}

func writeJournalState(t *testing.T, engine handoffEngine, statePath string, phase handoffJournalPhase, protocol roundControlProtocol, source *journalSource, message string) {
	t.Helper()
	store, err := artifact.New(engine.Transport.Root, true, engine.Transport.Limits)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	compactor := newCompactor(engine.Compaction, engine.Guard)
	state, err := engine.journalState(phase, "resume-token", store, compactor, 1, engine.Members["pane1"], engine.Members["pane1"], protocol, nil, source, message)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := newHandoffJournal(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.save(state); err != nil {
		t.Fatal(err)
	}
}

func TestHandoffJournalResumePreparedRelaySendsWithoutSourceReplay(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "run-state.json")
	ex := tmux.NewFakeExecutor()
	oldDelay, oldConfirm := ppSubmitDelay, ppSubmitConfirmTimeout
	ppSubmitDelay, ppSubmitConfirmTimeout = 0, 0
	defer func() { ppSubmitDelay, ppSubmitConfirmTimeout = oldDelay, oldConfirm }()

	protocol := newRoundControlProtocol(handoffTransportFile, "resume-token", 1, filepath.Join(root, "rounds", "0001-pane1", "outbox"), true)
	protocol.HeadNonce = "head"
	if err := os.MkdirAll(protocol.Outbox, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileReceiptPath(protocol), []byte(`{"version":1,"outcome":"success","summary":"done","prompt_head":"head"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	message := "resume this relay\n" + protocol.EchoMarker
	routeCalls := 0
	engine := journalResumeEngine(t, root, statePath, ex, func(context.Context, int, handoffMember, turnResult, bool) (handoffRoute, error) {
		routeCalls++
		return handoffRoute{Stop: true, Reason: "done"}, nil
	})
	writeJournalState(t, engine, statePath, journalRelayPrepared, protocol, nil, message)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].Content != message {
		t.Fatalf("resumed sends = %#v", ex.SendLinesCalls)
	}
	if routeCalls != 1 {
		t.Fatalf("route calls = %d, want 1", routeCalls)
	}
}

func TestHandoffJournalAmbiguousRelayNeverAutoResends(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "run-state.json")
	ex := tmux.NewFakeExecutor()
	protocol := newRoundControlProtocol(handoffTransportFile, "resume-token", 1, filepath.Join(root, "rounds", "0001-pane1", "outbox"), true)
	protocol.HeadNonce = "head"
	engine := journalResumeEngine(t, root, statePath, ex, func(context.Context, int, handoffMember, turnResult, bool) (handoffRoute, error) {
		return handoffRoute{Stop: true}, nil
	})
	writeJournalState(t, engine, statePath, journalRelaySubmitStarted, protocol, nil, "ambiguous\n"+protocol.EchoMarker)
	err := engine.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "resume-relay-action") {
		t.Fatalf("resume error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("ambiguous relay was resent: %#v", ex.SendLinesCalls)
	}
}

func TestHandoffJournalRejectsConfigurationMismatch(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "run-state.json")
	ex := tmux.NewFakeExecutor()
	protocol := newRoundControlProtocol(handoffTransportFile, "resume-token", 1, filepath.Join(root, "rounds", "0001-pane1", "outbox"), true)
	engine := journalResumeEngine(t, root, statePath, ex, func(context.Context, int, handoffMember, turnResult, bool) (handoffRoute, error) {
		return handoffRoute{Stop: true}, nil
	})
	writeJournalState(t, engine, statePath, journalRelayPrepared, protocol, nil, "message\n"+protocol.EchoMarker)
	member := engine.Members["pane1"]
	member.Target = "%99"
	engine.Members["pane1"] = member
	err := engine.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "configuration differs") {
		t.Fatalf("resume error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("mismatched configuration sent a prompt: %#v", ex.SendLinesCalls)
	}
}

func TestHandoffJournalSourceCommittedDoesNotRerunMember(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "run-state.json")
	ex := tmux.NewFakeExecutor()
	protocol := newRoundControlProtocol(handoffTransportFile, "resume-token", 1, filepath.Join(root, "rounds", "0001-pane1", "outbox"), true)
	if err := os.MkdirAll(protocol.Outbox, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileReceiptPath(protocol), []byte(`{"version":1,"outcome":"success","summary":"finished"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := journalResumeEngine(t, root, statePath, ex, func(context.Context, int, handoffMember, turnResult, bool) (handoffRoute, error) {
		return handoffRoute{Stop: true, Reason: "already handled"}, nil
	})
	status := &handoffStatus{Version: 1, Outcome: "success", Summary: "finished"}
	source := &journalSource{MemberID: "pane1", Narrative: "finished", Status: status, StatusSHA256: digestStatus(status)}
	writeJournalState(t, engine, statePath, journalSourceCommitted, protocol, source, "original prompt\n"+protocol.EchoMarker)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("committed source was rerun: %#v", ex.SendLinesCalls)
	}
}

func TestHandoffJournalConfirmedRelayWaitsWithoutResending(t *testing.T) {
	for _, phase := range []handoffJournalPhase{journalRelaySubmitConfirmed, journalAwaitingMember} {
		t.Run(string(phase), func(t *testing.T) {
			root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "run-state.json")
			ex := tmux.NewFakeExecutor()
			protocol := newRoundControlProtocol(handoffTransportFile, "resume-token", 1, filepath.Join(root, "rounds", "0001-pane1", "outbox"), true)
			protocol.HeadNonce = "head"
			if err := os.MkdirAll(protocol.Outbox, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fileReceiptPath(protocol), []byte(`{"version":1,"outcome":"success","summary":"done","prompt_head":"head"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			engine := journalResumeEngine(t, root, statePath, ex, func(context.Context, int, handoffMember, turnResult, bool) (handoffRoute, error) {
				return handoffRoute{Stop: true}, nil
			})
			writeJournalState(t, engine, statePath, phase, protocol, nil, "already submitted\n"+protocol.EchoMarker)
			if err := engine.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(ex.SendLinesCalls) != 0 {
				t.Fatalf("confirmed relay was resent: %#v", ex.SendLinesCalls)
			}
		})
	}
}

func TestLoadHandoffRunStateRejectsMessageDigestMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-state.json")
	data := `{"version":1,"updated_at":"2026-01-01T00:00:00Z","phase":"awaiting_member","run_token":"token","config_sha256":"config","members":[{"ID":"pane1","Target":"%1","Role":"","GoalMode":false}],"round":1,"current_id":"pane1","protocol":{},"message":"hello","message_sha256":"wrong"}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHandoffRunState(path); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("load error = %v", err)
	}
}
