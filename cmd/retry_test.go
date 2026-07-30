package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunWithRetryRetriesAfterError(t *testing.T) {
	var calls int
	var out bytes.Buffer
	err := runWithRetry(context.Background(), func() error {
		calls++
		if calls == 1 {
			return errors.New("quota exhausted")
		}
		return nil
	}, strings.NewReader("y\n"), &out)
	if err != nil {
		t.Fatalf("runWithRetry() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("run calls = %d, want 2", calls)
	}
	if !strings.Contains(out.String(), "是否要重試") {
		t.Fatalf("prompt missing from output: %q", out.String())
	}
}

func TestRunWithRetryDeclinesByDefault(t *testing.T) {
	var out bytes.Buffer
	err := runWithRetry(context.Background(), func() error {
		return errors.New("agent unavailable")
	}, strings.NewReader("n\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "retry declined") {
		t.Fatalf("runWithRetry() error = %v, want retry declined", err)
	}
}
