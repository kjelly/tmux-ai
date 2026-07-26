package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFormatAgentStepErrorDeadline(t *testing.T) {
	err := formatAgentStepError("review", 30, context.DeadlineExceeded)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("formatted error should preserve deadline cause: %v", err)
	}
	for _, want := range []string{
		`agent step "review"`,
		"timed out after 30s",
		"model or a tool",
		"increase the step timeout",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestFormatAgentStepErrorCanceled(t *testing.T) {
	err := formatAgentStepError("implement", 60, context.Canceled)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("formatted error should preserve cancellation cause: %v", err)
	}
	if !strings.Contains(err.Error(), `agent step "implement" was cancelled before completion`) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFormatAgentStepErrorOther(t *testing.T) {
	original := errors.New("ollama unavailable")
	if got := formatAgentStepError("review", 30, original); got != original {
		t.Fatalf("non-context error should be returned unchanged: got %v", got)
	}
}
