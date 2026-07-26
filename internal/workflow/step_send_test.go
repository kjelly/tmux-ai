package workflow

import (
	"context"
	"reflect"
	"testing"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func TestSendWorkflowInstructionPastesAndSubmits(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	if err := sendWorkflowInstruction(context.Background(), ex, "%23", "implement the spec"); err != nil {
		t.Fatalf("sendWorkflowInstruction() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].PaneID != "%23" || ex.SendLinesCalls[0].Content != "implement the spec" {
		t.Fatalf("SendLinesCalls = %#v", ex.SendLinesCalls)
	}
	want := []string{"send-keys", "-t", "%23", "Enter"}
	if len(ex.ExecCalls) != 1 || !reflect.DeepEqual(ex.ExecCalls[0], want) {
		t.Fatalf("ExecCalls = %#v, want %#v", ex.ExecCalls, want)
	}
}
