package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/config"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	followInterval  int
	followUntil     string
	followNotify    string
	followLines     int
	followFromStart bool
	followThen      string
	followThenPane  string
)

var followCmd = &cobra.Command{
	Use:   "follow <pane>",
	Short: "Stream pane output to stdout in real time (like tail -f, no LLM)",
	Long: `Poll a tmux pane and print new lines as they appear, similar to tail -f.
No LLM is involved — this is a pure tmux monitoring utility.

Combine with other tools via shell pipes, or use --until + --then for
LLM-triggered actions when a pattern appears.

Examples:
  # Stream build output
  tmux-ai follow build-pane

  # Pipe to grep
  tmux-ai follow build-pane | grep -i error

  # Stop and notify when done
  tmux-ai follow build-pane --until "Build succeeded" --notify "Build done!"

  # Stop when pattern found, then run AI instruction
  tmux-ai follow build-pane --until "error:" \
    --then "fix the build error" --pane claude-sport

  # Show all existing content too (not just new)
  tmux-ai follow %25 --from-start`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		aliases, _ := config.LoadAliases()
		paneID := resolveToPane(aliases.Resolve(args[0]))
		return runFollow(paneID)
	},
}

func init() {
	rootCmd.AddCommand(followCmd)
	followCmd.Flags().IntVar(&followInterval, "interval", 500, "Poll interval in milliseconds")
	followCmd.Flags().StringVar(&followUntil, "until", "", "Stop when this pattern appears")
	followCmd.Flags().StringVar(&followNotify, "notify", "", "Notification to print when --until matches")
	followCmd.Flags().IntVar(&followLines, "lines", 3000, "Scrollback lines to track")
	followCmd.Flags().BoolVar(&followFromStart, "from-start", false, "Print existing content before following new output")
	followCmd.Flags().StringVar(&followThen, "then", "", "AI instruction to run when --until matches (captured content as context)")
	followCmd.Flags().StringVar(&followThenPane, "pane", "", "Target pane for --then (default: followed pane)")
}

func runFollow(paneID string) error {
	ex := tmux.NewExecutor()

	var re *regexp.Regexp
	if followUntil != "" {
		re, _ = regexp.Compile(followUntil)
	}

	thenPane := paneID
	if followThenPane != "" {
		aliases, _ := config.LoadAliases()
		thenPane = resolveToPane(aliases.Resolve(followThenPane))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(time.Duration(followInterval) * time.Millisecond)
	defer ticker.Stop()

	// Get initial content
	initial, err := ex.CapturePaneClean(paneID, followLines)
	if err != nil {
		return fmt.Errorf("capture pane %s: %w", paneID, err)
	}

	seenLines := strings.Split(strings.TrimRight(initial, "\n"), "\n")

	if followFromStart {
		for _, line := range seenLines {
			fmt.Println(sanitizeLine(line))
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		content, err := ex.CapturePaneClean(paneID, followLines)
		if err != nil {
			continue
		}

		newLines := strings.Split(strings.TrimRight(content, "\n"), "\n")

		// Find new lines by looking for where our known tail ends in the new content
		newStart := findNewStart(seenLines, newLines)
		for _, line := range newLines[newStart:] {
			clean := sanitizeLine(line)
			if clean != "" {
				fmt.Println(clean)
			}
		}
		seenLines = newLines

		// Check --until pattern
		if followUntil == "" {
			continue
		}
		matched := strings.Contains(content, followUntil) || (re != nil && re.MatchString(content))
		if !matched {
			continue
		}

		if followNotify != "" {
			fmt.Fprintf(os.Stderr, "\a\033[32m[follow]\033[0m %s\n", followNotify)
		}

		if followThen != "" {
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
			r := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
			r.TargetPane = thenPane
			r.ExtraContext = content
			if err := r.Run(runCtx, followThen); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			cancel()
		}

		return nil
	}
}

// findNewStart returns the index in newLines where content after seenLines begins.
// Compares the tail of seenLines against a suffix of newLines.
func findNewStart(seen, newLines []string) int {
	if len(seen) == 0 {
		return 0
	}
	// Find the last non-empty seen line in newLines
	lastSeen := ""
	for i := len(seen) - 1; i >= 0; i-- {
		if strings.TrimSpace(seen[i]) != "" {
			lastSeen = seen[i]
			break
		}
	}
	if lastSeen == "" {
		return 0
	}
	// Walk newLines from end to find where lastSeen appears
	for i := len(newLines) - 1; i >= 0; i-- {
		if newLines[i] == lastSeen {
			return i + 1
		}
	}
	// If we can't find it (scrolled off), assume all content is new
	if len(newLines) > len(seen) {
		return len(seen)
	}
	return 0
}
