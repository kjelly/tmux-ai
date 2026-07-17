package cmd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
)

func TestCompletionMarkersRequireStandaloneNewLine(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		done    bool
		stopped bool
	}{
		{
			name:   "instruction mentioning markers is not completion",
			output: "完成時請在最後一行輸出 HANDOFF_DONE。如果全部完成請輸出 TEST_ALL_PASSED。",
		},
		{
			name:   "normal completion on its own line",
			output: "test failed: exit status 1\nHANDOFF_DONE\n",
			done:   true,
		},
		{
			name:   "completion with markdown decoration",
			output: "tests passed\nHANDOFF_DONE*****\n",
			done:   true,
		},
		{
			name:   "completion with UI bullet",
			output: "tests passed\n• HANDOFF_DONE\n",
			done:   true,
		},
		{
			name:    "stop with markdown decoration",
			output:  "**TEST_ALL_PASSED**\n",
			done:    true,
			stopped: true,
		},
		{
			name:    "stop takes priority",
			output:  "HANDOFF_DONE\nTEST_ALL_PASSED\n",
			done:    true,
			stopped: true,
		},
		{
			name:   "marker embedded in error is not completion",
			output: "assertion: expected HANDOFF_DONE but got nothing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, stopped := completionMarkers(tt.output, "HANDOFF_DONE", "TEST_ALL_PASSED")
			if done != tt.done || stopped != tt.stopped {
				t.Fatalf("completionMarkers() = (%t, %t), want (%t, %t)", done, stopped, tt.done, tt.stopped)
			}
		})
	}
}

func TestBaseMarkersAfterRoundInstruction(t *testing.T) {
	const (
		done = "HANDOFF_DONE__TMUXAI_run_2"
		stop = "REVIEW_PASSED__TMUXAI_run_2"
	)

	output := strings.Join([]string{
		"old relayed result",
		"HANDOFF_DONE",
		"本輪完成時輸出 " + done + "；全部完成輸出 " + stop,
		"review findings",
		"HANDOFF_DONE*****",
	}, "\n")
	gotDone, gotStopped := baseMarkersAfterRoundInstruction(output, done, stop, "HANDOFF_DONE", "REVIEW_PASSED")
	if !gotDone || gotStopped {
		t.Fatalf("baseMarkersAfterRoundInstruction() = (%t, %t), want (true, false)", gotDone, gotStopped)
	}

	oldOnly := "old result\nHANDOFF_DONE\ncontrol: " + done + " / " + stop + "\nstill working"
	gotDone, gotStopped = baseMarkersAfterRoundInstruction(oldOnly, done, stop, "HANDOFF_DONE", "REVIEW_PASSED")
	if gotDone || gotStopped {
		t.Fatalf("old marker before current instruction completed round: (%t, %t)", gotDone, gotStopped)
	}
}

func testPingPongCoordinatorMembers() []handoffCoordinatorMember {
	return []handoffCoordinatorMember{{ID: "pane1", Label: "reviewer"}, {ID: "pane2", Label: "fixer"}}
}

func TestParseHandoffCoordinatorDecision(t *testing.T) {
	got, err := parseHandoffCoordinatorDecision("```json\n{\"action\":\"send\",\"target\":\"PANE2\",\"instruction\":\"修正競態並執行 go test ./...\",\"reason\":\"review 發現並行問題\",\"confidence\":0.92}\n```", testPingPongCoordinatorMembers())
	if err != nil {
		t.Fatalf("parseHandoffCoordinatorDecision() error = %v", err)
	}
	if got.Action != "send" || got.Target != "pane2" || got.Instruction != "修正競態並執行 go test ./..." || got.Reason != "review 發現並行問題" || got.Confidence != 0.92 {
		t.Fatalf("parseHandoffCoordinatorDecision() = %#v", got)
	}
}

func TestParseHandoffCoordinatorDecisionRejectsInvalidDecision(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "missing JSON", output: "pane2 should continue", want: "does not contain"},
		{name: "missing action", output: `{"target":"pane2","instruction":"test","confidence":0.9}`, want: "invalid action"},
		{name: "unknown member", output: `{"action":"send","target":"pane3","instruction":"test","confidence":0.9}`, want: "unknown member"},
		{name: "empty instruction", output: `{"action":"send","target":"pane1","instruction":"  ","confidence":0.9}`, want: "empty instruction"},
		{name: "invalid confidence", output: `{"action":"send","target":"pane1","instruction":"test","confidence":1.5}`, want: "invalid confidence"},
		{name: "retry without reason", output: `{"action":"retry_selection"}`, want: "requires a reason"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseHandoffCoordinatorDecision(tt.output, testPingPongCoordinatorMembers())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseHandoffCoordinatorDecision() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestCoordinatorPolicyAndStopRequestAreInTrustedPrompt(t *testing.T) {
	prompt := buildHandoffCoordinatorPrompt(handoffCoordinatorRequest{
		Initial: "完成 review-fix", Policy: "只有 pane1 reviewer 可以核准 stop。",
		Round: 2, CurrentID: "pane2", CurrentOutput: "tests passed", StopRequested: true,
		Members: testPingPongCoordinatorMembers(), StopMembers: []string{"pane1"},
	})
	for _, want := range []string{"只有 pane1 reviewer 可以核准 stop。", "member 是否要求停止: true", "id: pane1", "允許核准 stop 的 member（程式硬性檢查）: pane1", "tests passed"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("coordinator prompt missing %q", want)
		}
	}
}

func TestCoordinatorStopMembersAreEnforced(t *testing.T) {
	members := testPingPongCoordinatorMembers()
	allowed, err := normalizeCoordinatorStopMembers([]string{"PANE1", "pane1"}, members)
	if err != nil || !reflect.DeepEqual(allowed, []string{"pane1"}) {
		t.Fatalf("normalized stop members = %#v, err = %v", allowed, err)
	}
	if _, err := normalizeCoordinatorStopMembers([]string{"pane3"}, members); err == nil {
		t.Fatal("unknown stop member unexpectedly accepted")
	}
	stop := handoffCoordinatorDecision{Action: "stop", Reason: "done"}
	if err := enforceCoordinatorStopMembers(stop, "pane2", allowed); err == nil || !strings.Contains(err.Error(), "policy violation") {
		t.Fatalf("unauthorized stop error = %v", err)
	}
	if err := enforceCoordinatorStopMembers(stop, "pane1", allowed); err != nil {
		t.Fatalf("authorized stop rejected: %v", err)
	}
}

func TestRouteFromCoordinatorDecisionPreservesControlActions(t *testing.T) {
	retry, err := routeFromCoordinatorDecision(handoffCoordinatorDecision{Action: "retry_selection", Reason: "range contains spinner"})
	if err != nil || !retry.RetrySelection || retry.Stop || retry.NeedsHuman {
		t.Fatalf("retry route = %#v, err = %v", retry, err)
	}
	human, err := routeFromCoordinatorDecision(handoffCoordinatorDecision{Action: "needs_human", Reason: "ambiguous requirement"})
	if err != nil || !human.NeedsHuman {
		t.Fatalf("needs-human route = %#v, err = %v", human, err)
	}
}

func TestResolvePingPongPresetInitialForAuto(t *testing.T) {
	got, err := resolvePingPongPresetInitial(pingPongPreset{Initial: "neutral goal", InitialPane1: "review", InitialPane2: "fix"}, "auto")
	if err != nil || got != "neutral goal" {
		t.Fatalf("resolve neutral initial = (%q, %v)", got, err)
	}
	_, err = resolvePingPongPresetInitial(pingPongPreset{InitialPane1: "review", InitialPane2: "fix"}, "auto")
	if err == nil || !strings.Contains(err.Error(), "neutral initial") {
		t.Fatalf("auto without neutral initial error = %v", err)
	}
}

func TestLastLinesReturnsExactTail(t *testing.T) {
	got := lastLines("old-1\nold-2\nnew-1\nnew-2\n", 2)
	want := []string{"new-1", "new-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lastLines() = %#v, want %#v", got, want)
	}
}

func TestParseResponseRange(t *testing.T) {
	got, err := parseResponseRange("```json\n{\"start\":3,\"end\":8}\n```", 10)
	if err != nil {
		t.Fatalf("parseResponseRange() error = %v", err)
	}
	if got.Start != 3 || got.End != 8 {
		t.Fatalf("parseResponseRange() = %#v", got)
	}
}

func TestParseResponseRangeRejectsInvalidRanges(t *testing.T) {
	for _, output := range []string{
		`{"start":0,"end":2}`,
		`{"start":5,"end":4}`,
		`{"start":1,"end":11}`,
	} {
		if _, err := parseResponseRange(output, 10); err == nil {
			t.Fatalf("parseResponseRange(%q) unexpectedly succeeded", output)
		}
	}
}

func TestInitialSourcePane(t *testing.T) {
	pane, label, err := initialSourcePane("pane1", "%1", "%2", "reviewer", "implementer")
	if err != nil || pane != "%1" || label != "reviewer" {
		t.Fatalf("initialSourcePane() = (%q, %q, %v)", pane, label, err)
	}
	if _, _, err := initialSourcePane("pane3", "%1", "%2", "one", "two"); err == nil {
		t.Fatal("initialSourcePane() accepted pane3")
	}
}

func TestSelectLatestCompletedResponsePreservesOriginalText(t *testing.T) {
	oldTimeout := ppCoordTimeout
	ppCoordTimeout = 1
	defer func() {
		ppCoordTimeout = oldTimeout
	}()
	fake := ollama.NewFakeClient()
	fake.Responses = []ollama.ChatResponse{{
		Message: ollama.Message{Content: `{"start":2,"end":4}`},
		Done:    true,
	}}

	lines := []string{
		"older output",
		"• 最新任務結果",
		"  - 完成第一階段",
		"  - 下一步交給 agent 2",
		"─ Worked for 1m ─",
	}
	got, start, end, err := selectLatestCompletedResponseWithClient(context.Background(), lines, fake)
	if err != nil {
		t.Fatalf("selectLatestCompletedResponse() error = %v", err)
	}
	want := "• 最新任務結果\n  - 完成第一階段\n  - 下一步交給 agent 2"
	if got != want || start != 2 || end != 4 {
		t.Fatalf("selectLatestCompletedResponse() = (%q, %d, %d), want (%q, 2, 4)", got, start, end, want)
	}
	if len(fake.Calls) != 1 || len(fake.Calls[0].Tools) != 0 {
		t.Fatalf("selector calls = %#v, want one tool-free LLM call", fake.Calls)
	}
}

func TestSelectionRetryGuidanceIsPassedToSelector(t *testing.T) {
	fake := ollama.NewFakeClient()
	fake.Responses = []ollama.ChatResponse{{
		Message: ollama.Message{Content: `{"start":1,"end":2,"confidence":0.9}`},
		Done:    true,
	}}
	guidance := "前次範圍混入 spinner，請只選 HANDOFF_STATUS"
	_, _, _, err := selectLatestCompletedResponseWithGuidanceAndClient(
		context.Background(), []string{"outcome: fixed", "tests: passed"}, guidance, fake)
	if err != nil {
		t.Fatalf("guided response selection error = %v", err)
	}
	if len(fake.Calls) != 1 || len(fake.Calls[0].Messages) < 2 || !strings.Contains(fake.Calls[0].Messages[1].Content, guidance) {
		t.Fatalf("selector did not receive retry guidance: %#v", fake.Calls)
	}
}

func TestValidateSelectedResponseRejectsTerminalArtifacts(t *testing.T) {
	if err := validateSelectedResponse("summary\nRead(file.go)\nchanged tests"); err == nil {
		t.Fatal("validateSelectedResponse accepted tool transcript")
	}
	if err := validateSelectedResponse("summary\nall tests passed\nHANDOFF_DONE"); err != nil {
		t.Fatalf("validateSelectedResponse rejected substantive response: %v", err)
	}
}

func TestFilterMarkerLinesOnlyRemovesControlLines(t *testing.T) {
	content := "error mentions HANDOFF_DONE in a message\nHANDOFF_DONE*****\n**TEST_ALL_PASSED**\n"
	got := filterMarkerLines(content, "HANDOFF_DONE", "TEST_ALL_PASSED")
	want := "error mentions HANDOFF_DONE in a message"
	if got != want {
		t.Fatalf("filterMarkerLines() = %q, want %q", got, want)
	}
}

func TestContentSinceBaselineKeepsOutputBeforeRepeatedPrompt(t *testing.T) {
	baseline := "previous task\n>"
	current := "previous task\n>\ntest failed: exit status 1\nHANDOFF_DONE\n>"
	want := "test failed: exit status 1\nHANDOFF_DONE\n>"

	if got := contentSinceBaseline(baseline, current); got != want {
		t.Fatalf("contentSinceBaseline() = %q, want %q", got, want)
	}
}

func TestSendToPaneSubmitsPastedPrompt(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	oldDelay := ppSubmitDelay
	oldConfirm := ppSubmitConfirmTimeout
	ppSubmitDelay = 0
	ppSubmitConfirmTimeout = 0
	defer func() { ppSubmitDelay = oldDelay; ppSubmitConfirmTimeout = oldConfirm }()
	if err := sendToPane(context.Background(), ex, "%42", "run the tests"); err != nil {
		t.Fatalf("sendToPane() error = %v", err)
	}

	if len(ex.SendLinesCalls) != 1 {
		t.Fatalf("SendLines calls = %d, want 1", len(ex.SendLinesCalls))
	}
	want := []string{"send-keys", "-t", "%42", "Enter"}
	if len(ex.ExecCalls) != 1 || !reflect.DeepEqual(ex.ExecCalls[0], want) {
		t.Fatalf("submit call = %#v, want %#v", ex.ExecCalls, want)
	}
}

func TestSendToPaneCanceledBeforePasteSendsNothing(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sendToPane(ctx, ex, "%42", "must not be sent")
	if err == nil || !strings.Contains(err.Error(), "canceled before paste") {
		t.Fatalf("sendToPane() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 || len(ex.ExecCalls) != 0 {
		t.Fatalf("canceled send performed calls: SendLines=%d Exec=%d", len(ex.SendLinesCalls), len(ex.ExecCalls))
	}
}

func TestSendToPaneCanceledDuringDelayDoesNotPressEnter(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.SendLinesCalled = make(chan struct{}, 1)
	oldDelay := ppSubmitDelay
	ppSubmitDelay = 5000
	defer func() { ppSubmitDelay = oldDelay }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sendToPane(ctx, ex, "%42", "pasted but never submitted") }()

	select {
	case <-ex.SendLinesCalled:
	case <-time.After(time.Second):
		t.Fatal("SendLines was not called")
	}
	cancel()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "Enter was not sent") {
		t.Fatalf("sendToPane() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 1 || len(ex.ExecCalls) != 0 {
		t.Fatalf("canceled delayed send calls: SendLines=%d Exec=%d", len(ex.SendLinesCalls), len(ex.ExecCalls))
	}
}

func TestValidateOutboundSizeStopsOversizedPrompt(t *testing.T) {
	oldLimit, oldTotal, oldSame, oldDup, oldApproval := ppMaxRelayBytes, ppMaxTotalRelayBytes, ppMaxSamePaneRounds, ppMaxDuplicateHandoffs, ppApprovalThresholdBytes
	ppMaxRelayBytes, ppMaxTotalRelayBytes = 5, 100
	ppMaxSamePaneRounds, ppMaxDuplicateHandoffs, ppApprovalThresholdBytes = 3, 2, 0
	defer func() {
		ppMaxRelayBytes, ppMaxTotalRelayBytes = oldLimit, oldTotal
		ppMaxSamePaneRounds, ppMaxDuplicateHandoffs, ppApprovalThresholdBytes = oldSame, oldDup, oldApproval
	}()
	guard := newHandoffGuard()

	if err := guard.reserveOutbound("relay", "pane1", "do it", "12345", "result"); err != nil {
		t.Fatalf("exact-limit prompt rejected: %v", err)
	}
	err := guard.reserveOutbound("relay", "pane2", "do it", "123456", "result")
	if err == nil || !strings.Contains(err.Error(), "stopped without sending") {
		t.Fatalf("oversized prompt error = %v", err)
	}
}

func TestPingPongGuardStopsBudgetsAndLoops(t *testing.T) {
	oldRelay, oldTotal, oldCalls := ppMaxRelayBytes, ppMaxTotalRelayBytes, ppMaxLLMCalls
	oldSame, oldDup, oldApproval := ppMaxSamePaneRounds, ppMaxDuplicateHandoffs, ppApprovalThresholdBytes
	ppMaxRelayBytes, ppMaxTotalRelayBytes, ppMaxLLMCalls = 100, 7, 1
	ppMaxSamePaneRounds, ppMaxDuplicateHandoffs, ppApprovalThresholdBytes = 1, 1, 0
	defer func() {
		ppMaxRelayBytes, ppMaxTotalRelayBytes, ppMaxLLMCalls = oldRelay, oldTotal, oldCalls
		ppMaxSamePaneRounds, ppMaxDuplicateHandoffs, ppApprovalThresholdBytes = oldSame, oldDup, oldApproval
	}()

	guard := newHandoffGuard()
	if err := guard.useLLM("selector"); err != nil {
		t.Fatal(err)
	}
	if err := guard.useLLM("coordinator"); err == nil || !strings.Contains(err.Error(), "LLM call limit") {
		t.Fatalf("LLM limit error = %v", err)
	}
	if err := guard.reserveOutbound("first", "pane1", "a", "1234", "result-a"); err != nil {
		t.Fatal(err)
	}
	if err := guard.reserveOutbound("same pane", "pane1", "b", "1", "result-b"); err == nil || !strings.Contains(err.Error(), "same-pane") {
		t.Fatalf("same-pane error = %v", err)
	}

	guard = newHandoffGuard()
	if err := guard.reserveOutbound("first", "pane1", "a", "1234", "result-a"); err != nil {
		t.Fatal(err)
	}
	if err := guard.reserveOutbound("total", "pane2", "b", "1234", "result-b"); err == nil || !strings.Contains(err.Error(), "total relay bytes") {
		t.Fatalf("total-byte error = %v", err)
	}

	guard = newHandoffGuard()
	if err := guard.reserveOutbound("first", "pane1", "same", "x", "same-result"); err != nil {
		t.Fatal(err)
	}
	guard.lastTarget = ""
	guard.sameTargetCount = 0
	if err := guard.reserveOutbound("duplicate", "pane1", "same", "y", "same-result"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestValidateResponseLines(t *testing.T) {
	oldMin, oldMax := ppMinResponseLines, ppMaxResponseLines
	ppMinResponseLines, ppMaxResponseLines = 2, 3
	defer func() { ppMinResponseLines, ppMaxResponseLines = oldMin, oldMax }()

	if err := validateResponseLines("one\ntwo"); err != nil {
		t.Fatal(err)
	}
	if err := validateResponseLines("one"); err == nil {
		t.Fatal("one-line response unexpectedly accepted")
	}
	if err := validateResponseLines("1\n2\n3\n4"); err == nil {
		t.Fatal("four-line response unexpectedly accepted")
	}
}

func TestWaitForCompletionStopsAfterRepeatedCaptureFailures(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.CaptureError = errors.New("pane not found")

	oldInterval, oldTimeout := ppInterval, ppRoundTimeout
	ppInterval, ppRoundTimeout = 1, 1
	defer func() {
		ppInterval, ppRoundTimeout = oldInterval, oldTimeout
	}()

	protocol := roundControlProtocol{DoneMarker: "DONE_unique", StopMarker: "STOP_unique"}
	_, _, err := waitForCompletion(context.Background(), ex, "trec:2", "baseline", protocol)
	if err == nil || !strings.Contains(err.Error(), "3 consecutive times") {
		t.Fatalf("waitForCompletion() error = %v", err)
	}
	if len(ex.CaptureCalls) != 3 {
		t.Fatalf("capture calls = %d, want 3", len(ex.CaptureCalls))
	}
}

func TestWaitForCompletionFindsUniqueMarkerAfterTUIRedraw(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.Captures["trec:2"] = "completely redrawn alternate screen\nSTOP_unique\n>"

	oldInterval, oldTimeout := ppInterval, ppRoundTimeout
	ppInterval, ppRoundTimeout = 1, 1
	defer func() {
		ppInterval, ppRoundTimeout = oldInterval, oldTimeout
	}()

	protocol := roundControlProtocol{DoneMarker: "DONE_unique", StopMarker: "STOP_unique"}
	_, stopped, err := waitForCompletion(context.Background(), ex, "trec:2", "unrelated old screen", protocol)
	if err != nil {
		t.Fatalf("waitForCompletion() error = %v", err)
	}
	if !stopped {
		t.Fatal("waitForCompletion() stopped = false, want true")
	}
}

func TestWaitForCompletionUsesScreenIdleFallbackWithoutMarker(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.Captures["trec:2"] = "agent response\nall fixes completed\nready for next task"

	oldInterval, oldTimeout, oldIdle := ppInterval, ppRoundTimeout, ppScreenIdleTimeout
	ppInterval, ppRoundTimeout, ppScreenIdleTimeout = 1, 3, 1
	defer func() {
		ppInterval, ppRoundTimeout, ppScreenIdleTimeout = oldInterval, oldTimeout, oldIdle
	}()

	fallbackCalls := 0
	output, stopped, selected, err := waitForCompletionWithFallback(context.Background(), ex, "trec:2", "old prompt", roundControlProtocol{}, func(_ context.Context, screen string) (string, error) {
		fallbackCalls++
		return "all fixes completed\nready for next task", nil
	})
	if err != nil {
		t.Fatalf("waitForCompletionWithFallback() error = %v", err)
	}
	if stopped || !selected || output != "all fixes completed\nready for next task" || fallbackCalls != 1 {
		t.Fatalf("output=%q stopped=%t selected=%t calls=%d", output, stopped, selected, fallbackCalls)
	}
}

func TestParseResponseRangeAllowsExplicitNoCompletion(t *testing.T) {
	got, err := parseResponseRange(`{"start":0,"end":0,"confidence":0.1}`, 10)
	if err != nil || got.Start != 0 || got.End != 0 {
		t.Fatalf("parseResponseRange() = %#v, %v", got, err)
	}
}

func TestWaitForCompletionExtractsProtocolEnvelopeAfterPromptRedraw(t *testing.T) {
	protocol := newRoundControlProtocol(handoffTransportText, "antigravity", 1, "", true)
	current := strings.Join([]string{
		"redrawn alternate screen",
		">",
		protocol.TextBeginMarker,
		"<完整 UTF-8 交接內容>",
		protocol.TextEndMarker,
		"runner instruction mentioning " + protocol.StatusMarker,
		"  " + protocol.TextBeginMarker,
		"  fixes complete",
		"  " + protocol.TextEndMarker,
		"  " + protocol.StatusMarker + ` {"version":1,"outcome":"suc`,
		`  cess","summary":"fixed","tests":[]}`,
		"  " + protocol.StopMarker,
		">",
	}, "\n")
	baseline := "old alternate screen\n>"
	if got := contentSinceBaseline(baseline, current); got != "" {
		t.Fatalf("contentSinceBaseline() = %q, want empty to reproduce redraw collision", got)
	}

	ex := tmux.NewFakeExecutor()
	ex.Captures["trec:2"] = current
	oldInterval, oldTimeout := ppInterval, ppRoundTimeout
	ppInterval, ppRoundTimeout = 1, 1
	defer func() {
		ppInterval, ppRoundTimeout = oldInterval, oldTimeout
	}()

	output, stopped, err := waitForCompletion(context.Background(), ex, "trec:2", baseline, protocol)
	if err != nil {
		t.Fatalf("waitForCompletion() error = %v", err)
	}
	if !stopped {
		t.Fatal("waitForCompletion() stopped = false, want true")
	}
	payload, _, err := parseTextPayload(output, protocol.TextBeginMarker, protocol.TextEndMarker, 1024, 10)
	if err != nil || payload != "fixes complete" {
		t.Fatalf("payload = %q, error = %v", payload, err)
	}
	status, err := parseHandoffStatus(stripTextFrame(output, protocol.TextBeginMarker, protocol.TextEndMarker), protocol.StatusMarker)
	if err != nil || status.Outcome != "success" {
		t.Fatalf("status = %#v, error = %v", status, err)
	}
}

func TestRoundControlMarkersAreUnique(t *testing.T) {
	first := roundControlMarker("HANDOFF_DONE", "run123", 1)
	second := roundControlMarker("HANDOFF_DONE", "run123", 2)
	if first == second || first != "HANDOFF_DONE__TMUXAI_run123_1" {
		t.Fatalf("round markers = %q and %q", first, second)
	}
	oldOutput := "previous work\nHANDOFF_DONE\nHANDOFF_DONE__TMUXAI_old_1"
	if done, _ := completionMarkers(oldOutput, first, "REVIEW_PASSED__TMUXAI_run123_1"); done {
		t.Fatal("old completion markers completed a new round")
	}
}
