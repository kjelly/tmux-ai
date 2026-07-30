package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func testCoordinateMembers() []coordinateMember {
	return []coordinateMember{
		{Name: "architect", Target: "team:architecture", Role: "plan and review"},
		{Name: "implementer", Target: "team:implementation", Role: "edit and test"},
		{Name: "tester", Target: "team:test", Role: "verify independently"},
	}
}

func TestValidateCoordinateMembers(t *testing.T) {
	if err := validateCoordinateMembers(testCoordinateMembers()); err != nil {
		t.Fatalf("validateCoordinateMembers() error = %v", err)
	}

	tests := []struct {
		name    string
		members []coordinateMember
		want    string
	}{
		{name: "too few", members: testCoordinateMembers()[:1], want: "at least two"},
		{name: "missing role", members: []coordinateMember{{Name: "one", Target: "%1"}, {Name: "two", Target: "%2", Role: "test"}}, want: "name, target, and role"},
		{name: "duplicate name", members: []coordinateMember{{Name: "worker", Target: "%1", Role: "one"}, {Name: "WORKER", Target: "%2", Role: "two"}}, want: "duplicate member name"},
		{name: "duplicate target", members: []coordinateMember{{Name: "one", Target: "%1", Role: "one"}, {Name: "two", Target: "%1", Role: "two"}}, want: "duplicate member target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCoordinateMembers(tt.members)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseCoordinateDecision(t *testing.T) {
	members := testCoordinateMembers()
	got, err := parseCoordinateDecision("```json\n{\"action\":\"send\",\"target\":\"TESTER\",\"instruction\":\"run the race tests\",\"reason\":\"implementation finished\",\"confidence\":0.91}\n```", members)
	if err != nil {
		t.Fatalf("parseCoordinateDecision() error = %v", err)
	}
	if got.Target != "tester" || got.Instruction != "run the race tests" || got.Confidence != 0.91 {
		t.Fatalf("parseCoordinateDecision() = %#v", got)
	}
}

func TestParseCoordinateDecisionRejectsUnknownMember(t *testing.T) {
	_, err := parseCoordinateDecision(`{"action":"send","target":"deployer","instruction":"deploy","confidence":0.9}`, testCoordinateMembers())
	if err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("error = %v, want unknown member", err)
	}
}

func TestBuildMemberTurnMessageGoalMode(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "token", 1, "", true)
	member := handoffMember{ID: "implementer", Label: "implementer", Role: "implement work", GoalMode: true}

	got := buildMemberTurnMessage(member, "fix the regression", "", handoffPayload{Transport: handoffTransportText}, nil, protocol)
	if !strings.HasPrefix(got, "/goal implement work\n\nfix the regression") {
		t.Fatalf("goal-mode message = %q, want /goal prefix before the task packet", got)
	}
	if strings.Count(got, "/goal ") != 1 {
		t.Fatalf("goal-mode message has %d goal prefixes, want 1", strings.Count(got, "/goal "))
	}
}

func TestGoalModeProtocolRequiresGoalCompletionBeforeHandoff(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "token", 1, "", true)
	protocol.GoalDoneMarker = roundControlMarker("GOAL_DONE", "token", 1)
	instruction := controlProtocolInstruction(protocol)
	if !strings.Contains(instruction, "實際完成並關閉目前的 goal") || !strings.Contains(instruction, protocol.GoalDoneMarker) {
		t.Fatalf("goal completion instruction missing from %q", instruction)
	}
	if goalCompletionPrecedes(protocol.DoneMarker, protocol.GoalDoneMarker, protocol.DoneMarker, protocol.StopMarker) {
		t.Fatal("DONE without goal completion marker must not hand off")
	}
	output := protocol.GoalDoneMarker + "\n" + protocol.DoneMarker
	if !goalCompletionPrecedes(output, protocol.GoalDoneMarker, protocol.DoneMarker, protocol.StopMarker) {
		t.Fatal("goal completion marker before DONE must allow handoff")
	}
}

func TestGoalModeDoesNotRequireSyntheticMarkerByDefault(t *testing.T) {
	oldStrict := ppStrictGoalCompletionMarker
	ppStrictGoalCompletionMarker = false
	defer func() { ppStrictGoalCompletionMarker = oldStrict }()

	engine := handoffEngine{Transport: resolvedHandoffConfig{Transport: handoffTransportText}}
	protocol, err := engine.protocolFor(nil, handoffMember{ID: "implementer", GoalMode: true}, "token", 1)
	if err != nil {
		t.Fatalf("protocolFor() error = %v", err)
	}
	if protocol.GoalDoneMarker != "" {
		t.Fatalf("goal mode unexpectedly requires synthetic completion marker %q", protocol.GoalDoneMarker)
	}
	if !protocol.GoalMode {
		t.Fatal("goal mode was not retained in the round protocol")
	}
}

func TestCoordinatorProtocolReservesStopMarkerForCoordinator(t *testing.T) {
	engine := handoffEngine{
		CoordinatorControlsStop: true,
		Transport:               resolvedHandoffConfig{Transport: handoffTransportText},
	}
	protocol, err := engine.protocolFor(nil, handoffMember{ID: "fixer"}, "token", 1)
	if err != nil {
		t.Fatalf("protocolFor() error = %v", err)
	}
	instruction := controlProtocolInstruction(protocol)
	if !strings.Contains(instruction, "不得輸出全任務完成 marker "+protocol.StopMarker) {
		t.Fatalf("coordinator stop reservation missing from %q", instruction)
	}
	if !strings.Contains(instruction, "最後一行只輸出完成 marker value："+protocol.DoneMarker) {
		t.Fatalf("coordinator DONE-only instruction missing from %q", instruction)
	}
}

func TestBuildMemberTurnMessageLeavesNormalMembersUnchanged(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "token", 1, "", true)
	member := handoffMember{ID: "tester", Label: "tester", Role: "verify work"}

	got := buildMemberTurnMessage(member, "run tests", "", handoffPayload{Transport: handoffTransportText}, nil, protocol)
	if strings.HasPrefix(got, "/goal ") {
		t.Fatalf("normal member unexpectedly received goal prefix: %q", got)
	}
}

func TestClearCodexGoalIfNeeded(t *testing.T) {
	oldDelay, oldConfirm, oldClearTimeout := ppSubmitDelay, ppSubmitConfirmTimeout, codexGoalClearTimeout
	ppSubmitDelay, ppSubmitConfirmTimeout = 0, 0
	codexGoalClearTimeout = 0
	defer func() {
		ppSubmitDelay, ppSubmitConfirmTimeout, codexGoalClearTimeout = oldDelay, oldConfirm, oldClearTimeout
	}()

	ex := tmux.NewFakeExecutor()
	ex.PanesDetailed = []tmux.PaneInfo{{PaneID: "%42", Command: "codex"}}
	member := handoffMember{Label: "worker", Target: "%42", GoalMode: true}
	if err := clearCodexGoalIfNeeded(context.Background(), ex, member); err != nil {
		t.Fatalf("clearCodexGoalIfNeeded() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].Content != "/goal clear" {
		t.Fatalf("SendLinesCalls = %#v, want one /goal clear", ex.SendLinesCalls)
	}
	if len(ex.ExecCalls) != 1 || ex.ExecCalls[0][0] != "send-keys" {
		t.Fatalf("ExecCalls = %#v, want one Enter submission", ex.ExecCalls)
	}
}

func TestCodexGoalClearScreenStates(t *testing.T) {
	if !codexGoalClearActive("Goal active Objective: clear") {
		t.Fatal("clear active screen was not recognized")
	}
	for _, screen := range []string{"Goal completed", "已完成 clear；未修改工作樹。"} {
		if !codexGoalClearComplete(screen) {
			t.Fatalf("clear completion screen %q was not recognized", screen)
		}
	}
	if codexGoalClearComplete("Goal active Objective: clear") {
		t.Fatal("active clear screen was incorrectly considered complete")
	}
}

func TestWaitForCodexGoalClearTimeoutDoesNotAbortHandoff(t *testing.T) {
	oldTimeout := codexGoalClearTimeout
	codexGoalClearTimeout = time.Millisecond
	defer func() { codexGoalClearTimeout = oldTimeout }()

	ex := tmux.NewFakeExecutor()
	ex.Captures["%42"] = "unchanged Codex screen"
	cleared, err := waitForCodexGoalClear(context.Background(), ex, "%42", "different initial screen")
	if err != nil || cleared {
		t.Fatalf("waitForCodexGoalClear() = (%t, %v), want (false, nil) so caller can start a fresh session", cleared, err)
	}
}

func TestResetCodexGoalIfNeededSkipsOtherProcesses(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.PanesDetailed = []tmux.PaneInfo{{PaneID: "%42", Command: "claude"}}
	member := handoffMember{Label: "worker", Target: "%42", GoalMode: true}
	if err := clearCodexGoalIfNeeded(context.Background(), ex, member); err != nil {
		t.Fatalf("clearCodexGoalIfNeeded() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("SendLinesCalls = %#v, want no reset", ex.SendLinesCalls)
	}
}

func TestResetCodexGoalIfNeededSkipsCodexWithoutGoalMode(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.PanesDetailed = []tmux.PaneInfo{{PaneID: "%42", Command: "codex"}}
	member := handoffMember{Label: "worker", Target: "%42"}
	if err := clearCodexGoalIfNeeded(context.Background(), ex, member); err != nil {
		t.Fatalf("clearCodexGoalIfNeeded() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("SendLinesCalls = %#v, want no reset", ex.SendLinesCalls)
	}
}

func TestLoadCoordinatePresetGoalMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.yaml")
	data := []byte(`members:
  - name: planner
    target: dev:plan
    role: plan
  - name: worker
    target: dev:work
    role: implement
    goal_mode: true
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	preset, err := loadCoordinatePreset(path)
	if err != nil {
		t.Fatalf("loadCoordinatePreset() error = %v", err)
	}
	if preset.Members[0].GoalMode || !preset.Members[1].GoalMode {
		t.Fatalf("goal_mode values = %#v", preset.Members)
	}
}

func TestLoadCoordinatePresetNewSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.yaml")
	data := []byte(`new_session: true
members:
  - name: planner
    target: dev:plan
    role: plan
  - name: worker
    target: dev:work
    role: implement
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	preset, err := loadCoordinatePreset(path)
	if err != nil {
		t.Fatalf("loadCoordinatePreset() error = %v", err)
	}
	if !preset.NewSession {
		t.Fatal("new_session was not loaded")
	}
}
