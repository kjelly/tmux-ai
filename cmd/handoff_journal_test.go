package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestHandoffJournalAmbiguousRelayObserveShowsTargetWithoutSending(t *testing.T) {
	root, statePath := t.TempDir(), filepath.Join(t.TempDir(), "run-state.json")
	ex := tmux.NewFakeExecutor()
	protocol := newRoundControlProtocol(handoffTransportFile, "resume-token", 1, filepath.Join(root, "rounds", "0001-pane1", "outbox"), true)
	protocol.HeadNonce = "head"
	engine := journalResumeEngine(t, root, statePath, ex, func(context.Context, int, handoffMember, turnResult, bool) (handoffRoute, error) {
		return handoffRoute{Stop: true}, nil
	})
	engine.ResumeRelayAction = "observe"
	writeJournalState(t, engine, statePath, journalRelaySubmitStarted, protocol, nil, "ambiguous\n"+protocol.EchoMarker)
	err := engine.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ambiguous relay observed") {
		t.Fatalf("resume error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("observe resent the relay: %#v", ex.SendLinesCalls)
	}
}

func TestHandoffJournalAmbiguousRelayResendSendsSavedMessage(t *testing.T) {
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
	message := "resend this relay\n" + protocol.EchoMarker
	engine := journalResumeEngine(t, root, statePath, ex, func(context.Context, int, handoffMember, turnResult, bool) (handoffRoute, error) {
		return handoffRoute{Stop: true, Reason: "done"}, nil
	})
	engine.ResumeRelayAction = "resend"
	writeJournalState(t, engine, statePath, journalRelaySubmitStarted, protocol, nil, message)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].Content != message {
		t.Fatalf("resumed sends = %#v", ex.SendLinesCalls)
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

// runStateJSON builds a run-state document that loadHandoffRunState accepts,
// so each case can break exactly one field.
func runStateJSON(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	state := map[string]any{
		"version":        1,
		"updated_at":     "2026-01-01T00:00:00Z",
		"phase":          string(journalAwaitingMember),
		"run_token":      "token",
		"config_sha256":  "config",
		"members":        []map[string]any{{"ID": "pane1", "Target": "%1", "Role": "", "GoalMode": false}},
		"round":          1,
		"current_id":     "pane1",
		"protocol":       map[string]any{},
		"message":        "hello",
		"message_sha256": digestString("hello"),
	}
	if mutate != nil {
		mutate(state)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLoadHandoffRunStateRejectsInvalidState(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"unknown phase", func(s map[string]any) { s["phase"] = "made_up_phase" }, "unknown handoff run-state phase"},
		{"missing run token", func(s map[string]any) { delete(s, "run_token") }, "incomplete"},
		{"missing members", func(s map[string]any) { delete(s, "members") }, "incomplete"},
		{"zero round", func(s map[string]any) { s["round"] = 0 }, "incomplete"},
		{"unknown field", func(s map[string]any) { s["unexpected"] = true }, "parse handoff run state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run-state.json")
			if err := os.WriteFile(path, []byte(runStateJSON(t, tc.mutate)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadHandoffRunState(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadHandoffRunStateRejectsMultipleObjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-state.json")
	if err := os.WriteFile(path, []byte(runStateJSON(t, nil)+" "+runStateJSON(t, nil)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHandoffRunState(path); err == nil || !strings.Contains(err.Error(), "exactly one JSON object") {
		t.Fatalf("load error = %v", err)
	}
}

func TestNewHandoffJournalRequiresAbsolutePath(t *testing.T) {
	if _, err := newHandoffJournal("relative/run-state.json"); err == nil {
		t.Fatal("relative run-state path was accepted")
	}
	journal, err := newHandoffJournal(filepath.Join(t.TempDir(), "run-state.json"))
	if err != nil || journal == nil {
		t.Fatalf("newHandoffJournal() = %v, %v", journal, err)
	}
	disabled, err := newHandoffJournal("")
	if err != nil || disabled != nil {
		t.Fatalf("empty path must disable the journal: %v, %v", disabled, err)
	}
}

func TestRestoreGuardRejectsCorruptState(t *testing.T) {
	valid := hex.EncodeToString(make([]byte, sha256.Size))
	cases := []struct {
		name  string
		state journalGuard
	}{
		{"negative total bytes", journalGuard{TotalBytes: -1}},
		{"negative llm calls", journalGuard{LLMCalls: -1}},
		{"negative same target count", journalGuard{SameTargetCount: -1}},
		{"invalid handoff hash", journalGuard{HandoffCounts: map[string]int{"not-hex": 1}}},
		{"short handoff hash", journalGuard{HandoffCounts: map[string]int{"abcd": 1}}},
		{"non positive handoff count", journalGuard{HandoffCounts: map[string]int{valid: 0}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guard := newHandoffGuard()
			if err := restoreGuard(guard, tc.state); err == nil {
				t.Fatalf("restoreGuard() accepted invalid state: %#v", tc.state)
			}
		})
	}
}

func TestRestoreGuardRestoresCounters(t *testing.T) {
	guard := newHandoffGuard()
	state := journalGuard{
		TotalBytes: 128, LLMCalls: 2, LastTarget: "%1", SameTargetCount: 1,
		HandoffCounts: map[string]int{hex.EncodeToString(make([]byte, sha256.Size)): 3},
	}
	if err := restoreGuard(guard, state); err != nil {
		t.Fatal(err)
	}
	if guard.totalBytes != 128 || guard.llmCalls != 2 || guard.lastTarget != "%1" || guard.sameTargetCount != 1 {
		t.Fatalf("guard = %#v", guard)
	}
	if len(guard.handoffCounts) != 1 {
		t.Fatalf("handoff counts = %#v", guard.handoffCounts)
	}
	for _, count := range guard.handoffCounts {
		if count != 3 {
			t.Fatalf("handoff count = %d", count)
		}
	}
}

func TestRestoreCompactorRejectsInvalidState(t *testing.T) {
	cases := []struct {
		name  string
		state journalCompaction
	}{
		{"negative round", journalCompaction{LastRound: map[string]int{"pane1": -1}}},
		{"empty member", journalCompaction{Estimated: map[string]int{"": 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compactor := newCompactor(resolvedCompactionConfig{}, newHandoffGuard())
			if err := restoreCompactor(compactor, tc.state); err == nil {
				t.Fatalf("restoreCompactor() accepted invalid state: %#v", tc.state)
			}
		})
	}
}

func TestRestoreCompactorRestoresState(t *testing.T) {
	compactor := newCompactor(resolvedCompactionConfig{}, newHandoffGuard())
	state := journalCompaction{LastRound: map[string]int{"pane1": 4}, Estimated: map[string]int{"pane1": 100}}
	if err := restoreCompactor(compactor, state); err != nil {
		t.Fatal(err)
	}
	if compactor.lastRound["pane1"] != 4 || compactor.estimated["pane1"] != 100 {
		t.Fatalf("compactor = %#v", compactor)
	}
}

func TestJournalSourceRestoreRejectsTamperedState(t *testing.T) {
	status := &handoffStatus{Version: 1, Outcome: "success", Summary: "done"}
	source := journalSource{MemberID: "pane1", Narrative: "done", Status: status, StatusSHA256: digestStatus(status)}
	if _, err := source.restore(nil, handoffTransportText); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	tampered := source
	tampered.StatusSHA256 = "deadbeef"
	if _, err := tampered.restore(nil, handoffTransportText); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("restore() error = %v", err)
	}
}

func TestJournalSourceRestoreRequiresFileTransportForArtifacts(t *testing.T) {
	status := &handoffStatus{Version: 1, Outcome: "success", Summary: "done"}
	source := journalSource{MemberID: "pane1", Status: status, StatusSHA256: digestStatus(status), ProducedIDs: []string{"a-r0001-01-abcdefgh"}}
	if _, err := source.restore(nil, handoffTransportText); err == nil || !strings.Contains(err.Error(), "require file transport") {
		t.Fatalf("restore() error = %v", err)
	}
}

func TestJournalConfigDigestDetectsConfigurationChanges(t *testing.T) {
	engine := handoffEngine{
		StartID: "pane1", InitialFingerprint: "do work",
		Transport: resolvedHandoffConfig{Transport: handoffTransportFile},
		Members:   map[string]handoffMember{"pane1": {ID: "pane1", Target: "%1"}},
	}
	base, err := engine.journalConfigDigest()
	if err != nil {
		t.Fatal(err)
	}
	same, err := engine.journalConfigDigest()
	if err != nil {
		t.Fatal(err)
	}
	if base != same {
		t.Fatalf("digest is not stable: %q != %q", base, same)
	}
	moved := engine
	moved.Members = map[string]handoffMember{"pane1": {ID: "pane1", Target: "%2"}}
	movedDigest, err := moved.journalConfigDigest()
	if err != nil {
		t.Fatal(err)
	}
	if movedDigest == base {
		t.Fatal("member target change did not change the configuration digest")
	}
	otherStart := engine
	otherStart.StartID = "pane2"
	otherStartDigest, err := otherStart.journalConfigDigest()
	if err != nil {
		t.Fatal(err)
	}
	if otherStartDigest == base {
		t.Fatal("start member change did not change the configuration digest")
	}
	otherTransport := engine
	otherTransport.Transport = resolvedHandoffConfig{Transport: handoffTransportText}
	transportDigest, err := otherTransport.journalConfigDigest()
	if err != nil {
		t.Fatal(err)
	}
	if transportDigest == base {
		t.Fatal("transport change did not change the configuration digest")
	}
	otherCompaction := engine
	otherCompaction.Compaction.Meter.WindowTokens = 8192
	compactionDigest, err := otherCompaction.journalConfigDigest()
	if err != nil {
		t.Fatal(err)
	}
	if compactionDigest == base {
		t.Fatal("compaction change did not change the configuration digest")
	}
}
