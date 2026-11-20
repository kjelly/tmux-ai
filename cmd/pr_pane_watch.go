package cmd

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

type prPaneActivity int

const (
	prPaneUnknown prPaneActivity = iota + 1
	prPaneWorking
	prPaneNeedsInput
	prPanePrompt
	prPaneReturnedToPrompt
	prPaneExited
	prPaneShell
	prPaneInMode
	prPaneUnavailable
)

var (
	prWorkingLine = regexp.MustCompile(`(?i)(?:^|[ (·•])(?:esc|ctrl\+c) to interrupt\)?$`)
	prInputLine   = regexp.MustCompile(`(?i)^[│\s]*(?:waiting for (?:your )?(?:approval|input)[.!…]?|would you like to run (?:the following|this) command\?|do you want to proceed\?|press enter to confirm or esc to cancel\.?)[│\s]*$`)
	prPromptLine  = regexp.MustCompile(`^(?:[›❯](?:\s.*)?|>)$`)
)

// Inspect controls near the bottom of the visible screen so older transcript
// text is less likely to be mistaken for a current prompt. Compare output
// separately from recognizable control rows like the busy timer.
func inspectPRPaneScreen(screen string) (prPaneActivity, string) {
	lines := strings.Split(strings.TrimRight(screen, " \t\r\n"), "\n")
	firstControlRow := max(0, len(lines)-16)
	firstPromptRow := max(0, len(lines)-6)
	var working, needsInput, prompt bool
	output := make([]string, 0, len(lines))
	for row, line := range lines {
		switch trimmed := strings.TrimSpace(line); {
		case trimmed == "":
		case row >= firstControlRow && prInputLine.MatchString(trimmed):
			needsInput = true
		case row >= firstControlRow && prWorkingLine.MatchString(trimmed):
			working = true
		case row >= firstPromptRow && prPromptLine.MatchString(trimmed):
			prompt = true
		default:
			output = append(output, line)
		}
	}
	activity := prPaneUnknown
	switch {
	case needsInput:
		activity = prPaneNeedsInput
	case working:
		activity = prPaneWorking
	case prompt:
		activity = prPanePrompt
	}
	return activity, strings.TrimSpace(strings.Join(output, "\n"))
}

type prPaneWatch struct {
	executor tmux.Executor
	number   int
	out      io.Writer
	quiet    bool

	diagnoseAfter time.Duration
	diagnose      func(context.Context, string) (prPaneDiagnosis, error)

	phase        prPhase
	last         prPaneActivity
	lastActivity prPaneActivity
	lastOutput   string
	lastProgress time.Time
	haveCapture  bool
	sawActivity  bool
	diagnosed    bool
	unavailable  int
}

func (w *prPaneWatch) poll(ctx context.Context, phase prPhase, paneID string, now time.Time) error {
	if w.phase != phase {
		w.phase, w.last, w.lastActivity, w.lastOutput = phase, 0, 0, ""
		w.haveCapture, w.sawActivity, w.diagnosed = false, false, false
		w.unavailable = 0
		w.lastProgress = now
	}
	state := readPaneState(w.executor, paneID)
	var unavailable prPaneActivity
	switch {
	case paneID == "" || !state.Known || state.Command == "" && !state.Dead:
		unavailable = prPaneUnavailable
	case state.Dead:
		unavailable = prPaneExited
	case isShellCommand(state.Command):
		unavailable = prPaneShell
	}
	if unavailable != 0 {
		w.report(phase, paneID, unavailable)
		w.unavailable++
		// Recheck GitHub before treating an exit as fatal. A successful
		// command can exit as its final GitHub update becomes visible.
		if w.unavailable >= 2 {
			return fmt.Errorf("PR #%d %s pane %s: %s before the PR state changed", w.number, phase, paneID, prPaneUnavailableReason(unavailable))
		}
		return nil
	}
	w.unavailable = 0
	if state.InMode {
		w.report(phase, paneID, prPaneInMode)
		return nil
	}
	if w.quiet && w.diagnose == nil {
		return nil
	}
	screen, err := w.executor.CaptureScreenClean(paneID)
	if err != nil {
		w.report(phase, paneID, prPaneUnavailable)
		return nil
	}
	activity, output := inspectPRPaneScreen(screen)
	changed := w.haveCapture && output != "" && output != w.lastOutput
	if !w.haveCapture || output != w.lastOutput || activity != w.lastActivity {
		w.lastProgress, w.diagnosed = now, false
	}
	w.haveCapture, w.lastActivity, w.lastOutput = true, activity, output
	if changed && !w.quiet {
		fmt.Fprintf(w.out, "PR #%d %s pane %s: visible output changed.\n", w.number, phase, paneID)
	}
	w.sawActivity = w.sawActivity || changed || activity == prPaneWorking || activity == prPaneNeedsInput
	if activity == prPanePrompt && w.sawActivity {
		activity = prPaneReturnedToPrompt
	}
	w.report(phase, paneID, activity)
	if w.diagnose != nil && !w.diagnosed && strings.TrimSpace(screen) != "" &&
		now.Sub(w.lastProgress) >= w.diagnoseAfter && ctx.Err() == nil {
		w.diagnosed = true
		diagnosis, err := w.diagnose(ctx, screen)
		if err != nil {
			fmt.Fprintf(w.out, "PR #%d %s pane %s: Ollama diagnostic unavailable: %v\n", w.number, phase, paneID, err)
		} else {
			fmt.Fprintf(w.out, "PR #%d %s pane %s: Ollama diagnostic (%s): %s\n", w.number, phase, paneID, diagnosis.State, diagnosis.Summary)
		}
	}
	return nil
}

func prPaneUnavailableReason(activity prPaneActivity) string {
	switch activity {
	case prPaneExited:
		return "agent exited"
	case prPaneShell:
		return "agent returned to a shell"
	default:
		return "agent pane became unavailable"
	}
}

func (w *prPaneWatch) report(phase prPhase, paneID string, activity prPaneActivity) {
	if w.quiet || w.last == activity {
		return
	}
	w.last = activity
	var message string
	var alert bool
	switch activity {
	case prPaneWorking:
		message = "agent shows a running indicator"
	case prPaneNeedsInput:
		message, alert = "agent appears to be waiting for approval or input; check the pane", true
	case prPanePrompt:
		message = "input prompt visible; waiting for signs of activity"
	case prPaneReturnedToPrompt:
		message, alert = "agent returned to an input prompt while the PR is still in this phase; check the pane", true
	case prPaneExited:
		message, alert = "pane process exited; check the pane", true
	case prPaneShell:
		message, alert = "foreground process is a shell; check the pane", true
	case prPaneInMode:
		message = "pane monitoring paused while tmux is in copy-mode"
	case prPaneUnavailable:
		message, alert = "pane could not be resolved or read; check the tmux target", true
	default:
		message = "no recognized agent status on screen"
	}
	if alert {
		fmt.Fprint(w.out, "\a")
	}
	if paneID == "" {
		paneID = "(unresolved)"
	}
	fmt.Fprintf(w.out, "PR #%d %s pane %s: %s.\n", w.number, phase, paneID, message)
}
