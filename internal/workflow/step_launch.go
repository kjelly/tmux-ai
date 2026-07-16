package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

// runLaunchStep opens a new tmux window, runs a program, waits for a ready
// pattern, then sends an initial input — all without an LLM agent.
// Fields used: Window (name), Cwd, Command, Pattern (ready signal), Timeout, Input.
func (e *Executor) runLaunchStep(ctx context.Context, s *Step) (string, error) {
	ex := tmux.NewExecutor()

	windowName := e.expand(s.Window)
	cwd := e.expand(s.Cwd)
	command := e.expand(s.Command)
	pattern := e.expand(s.Pattern)
	input := e.expand(s.Input)

	timeout := s.Timeout
	if timeout == 0 {
		timeout = 30
	}

	// Create a new window with the given working directory (no startup command —
	// opens a clean interactive shell so the user can take over later).
	windowID, err := ex.NewWindow(windowName, "", cwd)
	if err != nil {
		return "", fmt.Errorf("create window: %w", err)
	}

	// Grab the pane that was just created inside the new window.
	panes, err := ex.ListPanes(windowID)
	if err != nil || len(panes) == 0 {
		return "", fmt.Errorf("list panes in window %s: %w", windowID, err)
	}
	paneID := panes[0]

	// Type the startup command (e.g. "claude").
	if command != "" {
		if err := ex.SendKeysContext(ctx, paneID, command+"\n"); err != nil {
			return "", fmt.Errorf("send command: %w", err)
		}
	}

	// Poll until the ready pattern appears or the timeout expires.
	if pattern != "" {
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-ticker.C:
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout waiting for %q in window %q", pattern, windowName)
			}
			content, err := ex.CapturePaneClean(paneID, 50)
			if err == nil && strings.Contains(content, pattern) {
				break
			}
		}
	}

	// Send the initial input to the program (e.g. "/review <url>").
	if input != "" {
		if err := ex.SendKeysContext(ctx, paneID, input+"\n"); err != nil {
			return "", fmt.Errorf("send input: %w", err)
		}
	}

	return fmt.Sprintf("launched %q in window %q (pane %s)", command, windowName, paneID), nil
}

// runWaitStep blocks until the natural language time expression in s.Until is reached.
