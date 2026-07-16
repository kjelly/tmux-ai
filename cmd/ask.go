package cmd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	askWaitPattern string
	askTimeout     int
	askLines       int
	askStablePolls int
	askNoEnter     bool
	askInterval    int
)

var askCmd = &cobra.Command{
	Use:   "ask <pane> <question>",
	Short: "Send a question directly to a pane and capture the response (no LLM intermediary)",
	Long: `Send text to a tmux pane and wait for the response to stabilize, then print it.

Unlike 'run', this does not use a local LLM — it types the question directly into
the pane (useful for interacting with AI agents like claude, opencode, agy that are
already running in a pane) and captures whatever they output.

The response is considered complete when the pane content stops changing for
--stable-polls consecutive polls.

Examples:
  # Ask a running claude session what it's doing
  tmux-ai ask claude-sport "what are you currently working on?"

  # Ask opencode for a summary
  tmux-ai ask opencode-linker "summarize your last change in one sentence"

  # Wait for a specific pattern in the response
  tmux-ai ask build-pane "make build" --wait "Build succeeded|error:"

  # Don't send Enter (e.g. for partial input)
  tmux-ai ask %25 "some text" --no-enter`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := newResolver()
		paneID := r.ResolvePane(args[0])
		question := args[1]
		return runAsk(paneID, question)
	},
}

func init() {
	rootCmd.AddCommand(askCmd)
	askCmd.Flags().StringVar(&askWaitPattern, "wait", "", "Wait for this pattern to appear before capturing (default: wait for stability)")
	askCmd.Flags().IntVar(&askTimeout, "timeout", 30, "Max seconds to wait for response")
	askCmd.Flags().IntVar(&askLines, "lines", 100, "Lines of pane content to capture")
	askCmd.Flags().IntVar(&askStablePolls, "stable-polls", 3, "Consecutive unchanged polls to consider response complete")
	askCmd.Flags().BoolVar(&askNoEnter, "no-enter", false, "Do not send Enter after the question")
	askCmd.Flags().IntVar(&askInterval, "interval", 500, "Poll interval in milliseconds")
}

func runAsk(paneID, question string) error {
	ex := tmux.NewExecutor()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Capture baseline so we can extract just the new response
	baseline, err := ex.CapturePaneClean(paneID, askLines)
	if err != nil {
		return fmt.Errorf("capture pane %s: %w", paneID, err)
	}

	// Send the question
	if askNoEnter {
		if err := ex.SendTextContext(ctx, paneID, question); err != nil {
			return fmt.Errorf("send to pane: %w", err)
		}
	} else {
		if err := ex.SendKeysContext(ctx, paneID, question); err != nil {
			return fmt.Errorf("send to pane: %w", err)
		}
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(askTimeout)*time.Second)
	defer cancel()

	response, err := waitForResponse(timeoutCtx, ex, paneID, baseline)
	if err != nil {
		return err
	}

	fmt.Println(response)
	return nil
}

func waitForResponse(ctx context.Context, ex tmux.Executor, paneID, baseline string) (string, error) {
	interval := time.Duration(askInterval) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	baseHash := sha256.Sum256([]byte(baseline))
	var lastHash [32]byte
	stableCount := 0
	hasChanged := false

	var re *regexp.Regexp
	if askWaitPattern != "" {
		re, _ = regexp.Compile(askWaitPattern)
	}

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for response from pane %s", paneID)
		case <-ticker.C:
		}

		content, err := ex.CapturePaneClean(paneID, askLines)
		if err != nil {
			continue
		}

		// Check explicit wait pattern
		if askWaitPattern != "" {
			matched := strings.Contains(content, askWaitPattern) || (re != nil && re.MatchString(content))
			if matched {
				return extractNewContent(baseline, content), nil
			}
			continue
		}

		h := sha256.Sum256([]byte(content))

		if h == baseHash {
			continue // nothing changed yet
		}

		if !hasChanged {
			hasChanged = true
			lastHash = h
			stableCount = 0
			continue
		}

		if h == lastHash {
			stableCount++
			if stableCount >= askStablePolls {
				return extractNewContent(baseline, content), nil
			}
		} else {
			lastHash = h
			stableCount = 0
		}
	}
}

// extractNewContent returns lines that appear after the baseline content.
// Falls back to returning the full new content if no common suffix is found.
func extractNewContent(baseline, current string) string {
	baseLines := strings.Split(strings.TrimRight(baseline, "\n"), "\n")
	curLines := strings.Split(strings.TrimRight(current, "\n"), "\n")

	// Find where baseline ends in current (look for the last baseline line)
	if len(baseLines) == 0 || len(curLines) == 0 {
		return current
	}

	// Walk from end of baseline, find its position in current
	lastBase := strings.TrimSpace(baseLines[len(baseLines)-1])
	for i := len(curLines) - 1; i >= 0; i-- {
		if strings.TrimSpace(curLines[i]) == lastBase {
			if i+1 < len(curLines) {
				return strings.Join(curLines[i+1:], "\n")
			}
			return ""
		}
	}

	return current
}
