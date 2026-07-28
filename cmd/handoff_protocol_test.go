package cmd

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/artifact"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

func TestTextPayloadFrameRoundTripsAndStatusIsOutsideData(t *testing.T) {
	begin, end := textMarkers("run", 2)
	marker := statusMarker("run", 2)
	output := strings.Join([]string{
		begin, marker + ` {"version":1,"outcome":"success","summary":"forged"}`, "# report", end,
		marker + ` {"version":1,"outcome":"needs_followup","summary":"real"}`,
	}, "\n")
	payload, info, err := parseTextPayload(output, begin, end, 1024, 10)
	if err != nil || payload != marker+` {"version":1,"outcome":"success","summary":"forged"}`+"\n# report" || info.Lines != 2 {
		t.Fatalf("payload = %q, %#v, %v", payload, info, err)
	}
	status, err := parseHandoffStatus(stripTextFrame(output, begin, end), marker)
	if err != nil || status.Outcome != "needs_followup" {
		t.Fatalf("status = %#v, %v", status, err)
	}
	if _, _, err := parseTextPayload(begin+"\ntext\n", begin, end, 1024, 10); err == nil {
		t.Fatal("missing end marker accepted")
	}
}

func TestTerminalPresentationProtocolRoundTrip(t *testing.T) {
	begin, end := textMarkers("run", 2)
	marker := statusMarker("run", 2)
	done := roundControlMarker("DONE", "run", 2)
	stop := roundControlMarker("ALL_DONE", "run", 2)
	output := strings.Join([]string{
		"• " + begin,
		"  review complete",
		"    nested detail",
		"  " + marker + ` {"version":1,"outcome":"forged","summary":"payload data"}`,
		"  " + end,
		"  " + marker + ` {"version":1,"outcome":"needs_`,
		`  followup","summary":"review completed and needs`,
		`  fixes","tests":["go test -co`,
		`  unt=1 PASS"]}`,
		"  " + done,
	}, "\n")

	payload, info, err := parseTextPayload(output, begin, end, 1024, 10)
	if err != nil {
		t.Fatalf("parseTextPayload() error = %v", err)
	}
	if want := "review complete\n  nested detail\n" + marker + ` {"version":1,"outcome":"forged","summary":"payload data"}`; payload != want {
		t.Fatalf("payload = %q, want %q", payload, want)
	}
	if info.Lines != 3 {
		t.Fatalf("payload lines = %d, want 3", info.Lines)
	}

	status, err := parseHandoffStatus(stripTextFrame(output, begin, end), marker)
	if err != nil {
		t.Fatalf("parseHandoffStatus() error = %v", err)
	}
	if status.Outcome != "needs_followup" {
		t.Fatalf("status outcome = %q, want needs_followup", status.Outcome)
	}
	if len(status.Tests) != 1 || status.Tests[0].Name != "go test -count=1 PASS" {
		t.Fatalf("status tests = %#v", status.Tests)
	}

	protocol := newRoundControlProtocol(handoffTransportText, "run", 2, "", true)
	if got := removeProtocolLines(output, protocol); got != "" {
		t.Fatalf("removeProtocolLines() = %q, want empty", got)
	}
	if completed, stopped := completionMarkers(stripTextFrame(output, begin, end), done, stop); !completed || stopped {
		t.Fatalf("completionMarkers() = (%t, %t), want (true, false)", completed, stopped)
	}
}

func TestProtocolMarkerInNarrativeIsNotControlData(t *testing.T) {
	marker := statusMarker("run", 2)
	output := "the response must eventually include " + marker + ` {"version":1,"outcome":"success","summary":"not real"}`
	if _, err := parseHandoffStatus(output, marker); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("parseHandoffStatus() error = %v, want missing marker", err)
	}
}

func TestProtocolMarkersAcceptGenericPresentationOnlyPrefix(t *testing.T) {
	marker := statusMarker("run", 2)
	for _, prefix := range []string{"● ", "✻ ", "🤖️ ", "│  ", "--- ", "  "} {
		line := prefix + marker + ` {"version":1,"outcome":"success","summary":"done"}`
		status, err := parseHandoffStatus(line, marker)
		if err != nil || status.Outcome != "success" {
			t.Errorf("prefix %q: status=%#v, err=%v", prefix, status, err)
		}
	}
	for _, prefix := range []string{"agent: ", "status ", "\x1b[31m"} {
		line := prefix + marker + ` {"version":1,"outcome":"success","summary":"done"}`
		if _, err := parseHandoffStatus(line, marker); err == nil {
			t.Errorf("semantic/control prefix %q unexpectedly accepted", prefix)
		}
	}
}

func TestReceiptRepairExtractsOnlyFreshBoundedReceipt(t *testing.T) {
	original := newRoundControlProtocol(handoffTransportFile, "run", 7, t.TempDir(), true)
	repair := newReceiptRepairProtocol(original, "repair")
	output := strings.Join([]string{
		"old prompt mentions " + repair.BeginMarker + " and " + original.StatusMarker,
		repair.BeginMarker,
		"● " + original.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"review findings recorded","artifacts":[{"relpath":"review.md","kind":"review"}]}`,
		repair.DoneMarker,
	}, "\n")

	status, err := parseReceiptRepair(output, original.StatusMarker, repair)
	if err != nil {
		t.Fatalf("parseReceiptRepair() error = %v", err)
	}
	if status.Outcome != "needs_followup" || len(status.Artifacts) != 1 || status.Artifacts[0].RelPath != "review.md" {
		t.Fatalf("repaired status = %#v", status)
	}

	stale := strings.Join([]string{
		repair.BeginMarker,
		statusMarker("other-run", 7) + ` {"version":1,"outcome":"success","summary":"stale"}`,
		repair.DoneMarker,
	}, "\n")
	if _, err := parseReceiptRepair(stale, original.StatusMarker, repair); err == nil {
		t.Fatal("stale receipt marker was accepted")
	}
}

type receiptRepairExecutor struct {
	*tmux.FakeExecutor
	afterRepair  string
	afterRepairs []string
	repairCount  int
}

func (e *receiptRepairExecutor) SendLinesContext(ctx context.Context, paneID, content string) error {
	if err := e.FakeExecutor.SendLinesContext(ctx, paneID, content); err != nil {
		return err
	}
	if e.repairCount < len(e.afterRepairs) {
		e.Captures[paneID] = e.afterRepairs[e.repairCount]
	} else {
		e.Captures[paneID] = e.afterRepair
	}
	e.repairCount++
	return nil
}

func TestFileTurnCorrectsInvalidOutcomeReceiptOnce(t *testing.T) {
	oldInterval, oldConfirm := ppInterval, ppSubmitConfirmTimeout
	defer func() { ppInterval, ppSubmitConfirmTimeout = oldInterval, oldConfirm }()
	ppInterval, ppSubmitConfirmTimeout = 1, 0

	store, err := artifact.New(t.TempDir(), true, artifact.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	outbox, err := store.NewOutbox("pane2", 7)
	if err != nil {
		t.Fatal(err)
	}
	original := newRoundControlProtocol(handoffTransportFile, "run", 7, outbox, true)
	first, second := newReceiptRepairProtocol(original, "repair-one"), newReceiptRepairProtocol(original, "repair-two")
	executor := &receiptRepairExecutor{FakeExecutor: tmux.NewFakeExecutor()}
	executor.Captures["pane:2"] = "review complete\n" + original.DoneMarker
	executor.afterRepairs = []string{
		strings.Join([]string{executor.Captures["pane:2"], first.BeginMarker, original.StatusMarker + ` {"version":1,"outcome":"success|needs_followup|blocked","summary":"copied enum"}`, first.DoneMarker}, "\n"),
		strings.Join([]string{executor.Captures["pane:2"], second.BeginMarker, original.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"corrected"}`, second.DoneMarker}, "\n"),
	}
	tokens := []string{"repair-one", "repair-two"}
	engine := handoffEngine{
		Executor: executor, Guard: newHandoffGuard(), Transport: resolvedHandoffConfig{Transport: handoffTransportFile},
		NewReceiptRepairToken: func() (string, error) {
			token := tokens[0]
			tokens = tokens[1:]
			return token, nil
		},
	}

	result, err := engine.repairMissingFileReceipt(context.Background(), handoffMember{ID: "pane2", Label: "reviewer", Target: "pane:2"}, original, nil, store, errMissingReceipt)
	if err != nil {
		t.Fatalf("repairMissingFileReceipt() error = %v", err)
	}
	if result.Status == nil || result.Status.Outcome != "needs_followup" || len(executor.SendLinesCalls) != 2 {
		t.Fatalf("result=%#v repair sends=%d", result, len(executor.SendLinesCalls))
	}
	if !strings.Contains(executor.SendLinesCalls[1].Content, `must be exactly one of "success", "needs_followup", or "blocked"`) {
		t.Fatalf("corrective prompt did not name valid values: %q", executor.SendLinesCalls[1].Content)
	}
}

func TestStatusInstructionsUseConcreteEnumExample(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "run", 7, t.TempDir(), true)
	for _, instruction := range []string{controlProtocolInstruction(protocol), receiptRepairInstruction(protocol, newReceiptRepairProtocol(protocol, "repair"), "")} {
		if strings.Contains(instruction, `"outcome":"success|needs_followup|blocked"`) {
			t.Fatalf("instruction contains a pipe-delimited outcome placeholder: %q", instruction)
		}
		if !strings.Contains(instruction, `must be exactly one of "success", "needs_followup", or "blocked"`) {
			t.Fatalf("instruction does not state the enum constraint: %q", instruction)
		}
	}
}

func TestFileTurnRepairsMissingStatusFromSamePane(t *testing.T) {
	oldInterval, oldConfirm := ppInterval, ppSubmitConfirmTimeout
	defer func() { ppInterval, ppSubmitConfirmTimeout = oldInterval, oldConfirm }()
	ppInterval, ppSubmitConfirmTimeout = 1, 0

	store, err := artifact.New(t.TempDir(), true, artifact.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	outbox, err := store.NewOutbox("pane2", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outbox+"/review.md", []byte("review findings"), 0o600); err != nil {
		t.Fatal(err)
	}

	original := newRoundControlProtocol(handoffTransportFile, "run", 7, outbox, true)
	repair := newReceiptRepairProtocol(original, "repair")
	executor := &receiptRepairExecutor{FakeExecutor: tmux.NewFakeExecutor()}
	executor.Captures["pane:2"] = "review complete\n" + original.DoneMarker
	executor.afterRepair = strings.Join([]string{
		executor.Captures["pane:2"],
		repair.BeginMarker,
		original.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"review findings recorded","artifacts":[{"relpath":"review.md","kind":"review"}]}`,
		repair.DoneMarker,
	}, "\n")
	engine := handoffEngine{
		Executor: executor,
		Guard:    newHandoffGuard(),
		Transport: resolvedHandoffConfig{
			Transport: handoffTransportFile,
		},
		NewReceiptRepairToken: func() (string, error) { return "repair", nil },
	}

	result, err := engine.repairMissingFileReceipt(context.Background(), handoffMember{ID: "pane2", Label: "reviewer", Target: "pane:2"}, original, nil, store, errMissingReceipt)
	if err != nil {
		t.Fatalf("repairMissingFileReceipt() error = %v", err)
	}
	if result.Status == nil || result.Status.Outcome != "needs_followup" || len(result.Produced) != 1 {
		t.Fatalf("repaired result = %#v", result)
	}
	if len(executor.SendLinesCalls) != 1 || !strings.Contains(executor.SendLinesCalls[0].Content, repair.BeginMarker) {
		t.Fatalf("repair prompt = %#v", executor.SendLinesCalls)
	}
}

func TestFileTurnMissingReceiptDoesNotUseScreenSelector(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "run", 7, t.TempDir(), true)
	engine := handoffEngine{
		Guard:           newHandoffGuard(),
		SelectResponses: true,
		Transport:       resolvedHandoffConfig{Transport: handoffTransportFile},
		SelectResponse: func(context.Context, []string, string) (string, int, int, error) {
			t.Fatal("file receipt failure invoked screen selector")
			return "", 0, 0, nil
		},
	}
	_, err := engine.prepareTurnResult(context.Background(), handoffMember{ID: "pane2", Label: "reviewer"}, protocol.DoneMarker, protocol, nil, nil, "", false)
	if !errors.Is(err, errMissingReceipt) {
		t.Fatalf("prepareTurnResult() error = %v, want receipt error", err)
	}
}

func TestProtocolInstructionsCannotBeParsedAsControlOutput(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportFile, "run", 7, t.TempDir(), true)
	if _, err := parseHandoffStatus(controlProtocolInstruction(protocol), protocol.StatusMarker); err == nil {
		t.Fatal("normal protocol instruction was accepted as a status receipt")
	}
	if done, _ := completionMarkers(controlProtocolInstruction(protocol), protocol.DoneMarker, protocol.StopMarker); done {
		t.Fatal("normal protocol instruction was accepted as completion")
	}

	repair := newReceiptRepairProtocol(protocol, "repair")
	prompt := receiptRepairInstruction(protocol, repair, "")
	if _, err := parseReceiptRepair(prompt, protocol.StatusMarker, repair); err == nil {
		t.Fatal("repair instruction was accepted as a repair receipt")
	}
	if done, _ := completionMarkers(prompt, repair.DoneMarker, repair.DoneMarker+"_STOP"); done {
		t.Fatal("repair instruction was accepted as repair completion")
	}
	if strings.Contains(prompt, `"artifacts"`) || strings.Contains(prompt, "outbox 相對路徑") {
		t.Fatalf("artifact claim placeholder leaked into prompt: %q", prompt)
	}
}

func TestFileHandoffCollectsOutboxWithoutAgentClaims(t *testing.T) {
	store, err := artifact.New(t.TempDir(), true, artifact.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	outbox, err := store.NewOutbox("pane2", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outbox+"/actual-review.md", []byte("review"), 0o600); err != nil {
		t.Fatal(err)
	}
	protocol := newRoundControlProtocol(handoffTransportFile, "run", 7, outbox, true)
	output := protocol.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"review complete","artifacts":[{"relpath":"outbox 相對路徑"}]}`
	engine := handoffEngine{Guard: newHandoffGuard(), Transport: resolvedHandoffConfig{Transport: handoffTransportFile}}
	result, err := engine.prepareTurnResultFromOutput(context.Background(), handoffMember{ID: "pane2", Label: "reviewer"}, output, protocol, nil, store, "")
	if err != nil {
		t.Fatalf("prepareTurnResultFromOutput() error = %v", err)
	}
	if len(result.Produced) != 1 || result.Produced[0].OriginalName != "actual-review.md" {
		t.Fatalf("produced artifacts = %#v", result.Produced)
	}
}

func TestPrepareTurnResultUsesValidatedTextPayloadAsNarrative(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "run", 1, "", true)
	output := strings.Join([]string{
		"  " + protocol.TextBeginMarker,
		"  complete handoff",
		"  " + protocol.TextEndMarker,
		"  " + protocol.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"continue","tests":[]}`,
		"  " + protocol.DoneMarker,
	}, "\n")
	engine := handoffEngine{
		Guard:           newHandoffGuard(),
		SelectResponses: true,
		Transport: resolvedHandoffConfig{
			Transport: handoffTransportText,
			TextBytes: 1024,
			TextLines: 10,
		},
	}
	result, err := engine.prepareTurnResult(context.Background(), handoffMember{ID: "pane2", Label: "trec:2"}, output, protocol, nil, nil, "", false)
	if err != nil {
		t.Fatalf("prepareTurnResult() error = %v", err)
	}
	if result.Narrative != "complete handoff" {
		t.Fatalf("narrative = %q, want validated text payload", result.Narrative)
	}
}

func TestPrepareTurnResultRecoversValidatedTextFrameWhenStatusIsCorrupted(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "run", 1, "", true)
	output := strings.Join([]string{
		protocol.TextBeginMarker,
		"fix completed; review.md updated",
		protocol.TextEndMarker,
		protocol.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"broken",,"tests":[]}`,
		protocol.DoneMarker,
	}, "\n")
	selectorCalled := false
	engine := handoffEngine{
		Guard:           newHandoffGuard(),
		SelectResponses: true,
		Transport: resolvedHandoffConfig{
			Transport: handoffTransportText,
			TextBytes: 1024,
			TextLines: 10,
		},
		SelectResponse: func(context.Context, []string, string) (string, int, int, error) {
			selectorCalled = true
			return "", 0, 0, nil
		},
	}

	result, err := engine.prepareTurnResult(context.Background(), handoffMember{ID: "pane2", Label: "trec:2"}, output, protocol, nil, nil, "", false)
	if err != nil {
		t.Fatalf("prepareTurnResult() error = %v", err)
	}
	if selectorCalled {
		t.Fatal("corrupted status with a validated text frame should not invoke the selector")
	}
	if result.Narrative != "fix completed; review.md updated" || result.Payload.Text != result.Narrative {
		t.Fatalf("recovered result = %#v", result)
	}
	if result.Status == nil || result.Status.Outcome != "needs_followup" {
		t.Fatalf("recovered status = %#v", result.Status)
	}
}

func TestPrepareTurnResultFallsBackToFullScreenSelector(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "run", 1, "", true)
	selected := strings.Join([]string{
		"  " + protocol.TextBeginMarker,
		"  recovered handoff",
		"  " + protocol.TextEndMarker,
		"  " + protocol.StatusMarker + ` {"version":1,"outcome":"needs_followup","summary":"continue","tests":[]}`,
		"  " + protocol.DoneMarker,
	}, "\n")
	fullScreen := strings.Join([]string{
		"runner prompt contains the frame markers but is not a response",
		protocol.TextBeginMarker,
		"<完整 UTF-8 交接內容>",
		protocol.TextEndMarker,
		"runner prompt mentions " + protocol.StatusMarker,
		selected,
		">",
	}, "\n")
	executor := tmux.NewFakeExecutor()
	executor.Captures["trec:2"] = fullScreen
	selectorCalled := false
	engine := handoffEngine{
		Executor:        executor,
		Guard:           newHandoffGuard(),
		SelectResponses: true,
		Transport: resolvedHandoffConfig{
			Transport: handoffTransportText,
			TextBytes: 1024,
			TextLines: 10,
		},
		SelectResponse: func(_ context.Context, lines []string, guidance string) (string, int, int, error) {
			selectorCalled = true
			if !strings.Contains(strings.Join(lines, "\n"), "runner prompt contains") {
				t.Fatal("selector did not receive the full terminal tail")
			}
			for _, marker := range []string{protocol.TextBeginMarker, protocol.TextEndMarker, protocol.StatusMarker, protocol.DoneMarker} {
				if !strings.Contains(guidance, marker) {
					t.Fatalf("selector guidance missing %q", marker)
				}
			}
			return selected, 6, 10, nil
		},
	}
	result, err := engine.prepareTurnResult(context.Background(), handoffMember{ID: "pane2", Label: "trec:2", Target: "trec:2"}, "screen redraw left only the completion marker", protocol, nil, nil, "", false)
	if err != nil {
		t.Fatalf("prepareTurnResult() error = %v", err)
	}
	if !selectorCalled {
		t.Fatal("full-screen selector was not called")
	}
	if result.Narrative != "recovered handoff" || result.Status == nil || result.Status.Outcome != "needs_followup" {
		t.Fatalf("fallback result = %#v", result)
	}
}

func TestPrepareTurnResultRecoversUnframedFullScreenSelection(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "run", 1, "", true)
	executor := tmux.NewFakeExecutor()
	executor.Captures["trec:2"] = "agent response without protocol markers"
	engine := handoffEngine{
		Executor:        executor,
		Guard:           newHandoffGuard(),
		SelectResponses: true,
		Transport: resolvedHandoffConfig{
			Transport: handoffTransportText,
			TextBytes: 1024,
			TextLines: 10,
		},
		SelectResponse: func(context.Context, []string, string) (string, int, int, error) {
			return "fix completed\nready for review", 1, 2, nil
		},
	}
	result, err := engine.prepareTurnResult(context.Background(), handoffMember{ID: "pane2", Label: "trec:2", Target: "trec:2"}, "missing protocol", protocol, nil, nil, "", false)
	if err != nil {
		t.Fatalf("prepareTurnResult() error = %v", err)
	}
	if result.Narrative != "fix completed\nready for review" || result.Payload.Text != result.Narrative {
		t.Fatalf("result = %#v", result)
	}
	if result.Status == nil || result.Status.Outcome != "needs_followup" {
		t.Fatalf("status = %#v, want synthesized needs_followup", result.Status)
	}
}

func TestResolveTransportPrecedenceAndConflicts(t *testing.T) {
	oldLines, oldMax, oldTotal := ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes
	defer func() { ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = oldLines, oldMax, oldTotal }()
	ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = 256, 65536, 131072
	command := &cobra.Command{Use: "test"}
	addHandoffFlags(command)
	if err := command.Flags().Set("handoff-transport", "text"); err != nil {
		t.Fatal(err)
	}
	config, err := resolveHandoffConfig(command.Flags(), handoffPresetConfig{HandoffTransport: "file"})
	if err != nil || config.Transport != handoffTransportText || config.Source != "CLI" {
		t.Fatalf("config = %#v, %v", config, err)
	}
	command = &cobra.Command{Use: "test"}
	addHandoffFlags(command)
	if err := command.Flags().Set("artifact-root", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveHandoffConfig(command.Flags(), handoffPresetConfig{}); err == nil || !strings.Contains(err.Error(), "artifact flags") {
		t.Fatalf("artifact/text conflict = %v", err)
	}
}

func TestCoordinatorDecisionRejectsArtifactIDsForText(t *testing.T) {
	ids := []string{}
	err := validateCoordinatorTransportDecision(handoffCoordinatorDecision{Action: "send", ArtifactIDs: &ids}, handoffCoordinatorRequest{Transport: handoffTransportText})
	if err == nil || !strings.Contains(err.Error(), "omit") {
		t.Fatalf("text decision error = %v", err)
	}
}

func TestCoordinatorControlDecisionIgnoresArtifactIDs(t *testing.T) {
	ids := []string{"review-summary"}
	for _, action := range []string{"stop", "retry_selection", "needs_human"} {
		err := validateCoordinatorTransportDecision(
			handoffCoordinatorDecision{Action: action, ArtifactIDs: &ids},
			handoffCoordinatorRequest{Transport: handoffTransportFile},
		)
		if err != nil {
			t.Errorf("%s decision should ignore artifact_ids: %v", action, err)
		}
	}
}

func TestNormalizeArtifactClaimsRemovesOutboxPrefix(t *testing.T) {
	claims := normalizeArtifactClaims([]artifact.Claim{
		{RelPath: "outbox/fix-summary.md"},
		{RelPath: "nested/review.md"},
	})
	if claims[0].RelPath != "fix-summary.md" {
		t.Fatalf("normalized relpath = %q, want fix-summary.md", claims[0].RelPath)
	}
	if claims[1].RelPath != "nested/review.md" {
		t.Fatalf("unprefixed relpath changed to %q", claims[1].RelPath)
	}
}
