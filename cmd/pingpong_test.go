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

func TestParseCoordinatorDecision(t *testing.T) {
	got, err := parseCoordinatorDecision("```json\n{\"action\":\"send\",\"next\":2,\"instruction\":\"修正競態並執行 go test ./...\",\"reason\":\"review 發現並行問題\",\"confidence\":0.92}\n```")
	if err != nil {
		t.Fatalf("parseCoordinatorDecision() error = %v", err)
	}
	if got.Action != "send" || got.Next != 2 || got.Instruction != "修正競態並執行 go test ./..." || got.Reason != "review 發現並行問題" || got.Confidence != 0.92 {
		t.Fatalf("parseCoordinatorDecision() = %#v", got)
	}
}

func TestParseCoordinatorDecisionRejectsInvalidDecision(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "missing JSON", output: "pane2 should continue", want: "does not contain"},
		{name: "missing action", output: `{"next":2,"instruction":"test","confidence":0.9}`, want: "invalid action"},
		{name: "invalid pane", output: `{"action":"send","next":3,"instruction":"test","confidence":0.9}`, want: "invalid pane"},
		{name: "empty instruction", output: `{"action":"send","next":1,"instruction":"  ","confidence":0.9}`, want: "empty instruction"},
		{name: "invalid confidence", output: `{"action":"send","next":1,"instruction":"test","confidence":1.5}`, want: "invalid confidence"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCoordinatorDecision(tt.output)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseCoordinatorDecision() error = %v, want substring %q", err, tt.want)
			}
		})
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
	guard := newPingPongGuard()

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

	guard := newPingPongGuard()
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

	guard = newPingPongGuard()
	if err := guard.reserveOutbound("first", "pane1", "a", "1234", "result-a"); err != nil {
		t.Fatal(err)
	}
	if err := guard.reserveOutbound("total", "pane2", "b", "1234", "result-b"); err == nil || !strings.Contains(err.Error(), "total relay bytes") {
		t.Fatalf("total-byte error = %v", err)
	}

	guard = newPingPongGuard()
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

	_, _, err := waitForCompletion(context.Background(), ex, "trec:2", "baseline", "DONE_unique", "STOP_unique")
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

	_, stopped, err := waitForCompletion(context.Background(), ex, "trec:2", "unrelated old screen", "DONE_unique", "STOP_unique")
	if err != nil {
		t.Fatalf("waitForCompletion() error = %v", err)
	}
	if !stopped {
		t.Fatal("waitForCompletion() stopped = false, want true")
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
