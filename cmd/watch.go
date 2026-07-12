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
	"github.com/llmore/tmux-ai/internal/pane"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	watchThen     string
	watchThenPane string
	watchKeys     string
	watchNotify   string
	watchInterval int
	watchOnce     bool
	watchLines    int
	watchAbsent   bool
	watchRange    string
)

var watchCmd = &cobra.Command{
	Use:   "watch <pane> <pattern>",
	Short: "Monitor a pane and trigger actions when a pattern appears",
	Long: `Poll a tmux pane until its content matches a pattern (substring or regex),
then optionally run an AI instruction, send raw keys, or show a notification.

The captured pane content is passed as context to any --then instruction so the
agent knows what triggered it.

Examples:
  # Notify when a job finishes
  tmux-ai watch build-pane "Build succeeded" --notify "Build is done!"

  # Ask claude to fix a build error as soon as one appears
  tmux-ai watch build-pane "error:" --then "fix the build error shown above" --pane claude-sport

  # Auto-answer a prompt in another pane
  tmux-ai watch deploy-pane "Continue? \[y/N\]" --keys "y"

  # Watch for regex pattern, keep watching after match
  tmux-ai watch %25 "FAIL|ERROR" --notify "Something failed" --once=false`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := newResolver()
		paneID := r.ResolvePane(args[0])
		pattern := args[1]

		thenPane := paneID
		if watchThenPane != "" {
			thenPane = r.ResolvePane(watchThenPane)
		}

		return runWatch(paneID, pattern, thenPane, watchAbsent, watchRange)
	},
}

func init() {
	rootCmd.AddCommand(watchCmd)
	watchCmd.Flags().StringVar(&watchThen, "then", "", "AI instruction to run when pattern matches (captured pane content is passed as context)")
	watchCmd.Flags().StringVar(&watchThenPane, "pane", "", "Target pane for --then (default: watched pane)")
	watchCmd.Flags().StringVar(&watchKeys, "keys", "", "Raw keys to send to target pane when pattern matches (use \\n for Enter)")
	watchCmd.Flags().StringVar(&watchNotify, "notify", "", "Notification message to print when pattern matches")
	watchCmd.Flags().IntVar(&watchInterval, "interval", 2000, "Poll interval in milliseconds")
	watchCmd.Flags().BoolVar(&watchOnce, "once", true, "Stop after first match")
	watchCmd.Flags().IntVar(&watchLines, "lines", 50, "Lines of pane content to capture for matching and context")
	watchCmd.Flags().BoolVar(&watchAbsent, "absent", false, "Trigger when pattern is NOT found (wait for text to disappear)")
	watchCmd.Flags().StringVar(&watchRange, "range", "", "Python slice applied before matching, e.g. [-5:] for last 5 lines. Without --pattern triggers on any change in the range.")
}

func runWatch(paneID, pattern, thenPane string, absent bool, rangeExpr string) error {
	ex := tmux.NewExecutor()

	var re *regexp.Regexp
	if pattern != "" {
		re, _ = regexp.Compile(pattern)
	}

	// Describe what we're watching
	switch {
	case rangeExpr != "" && pattern == "":
		fmt.Printf("Watching pane %s range %s for any change\n", paneID, rangeExpr)
	case rangeExpr != "" && absent:
		fmt.Printf("Watching pane %s range %s until %q disappears\n", paneID, rangeExpr, pattern)
	case rangeExpr != "":
		fmt.Printf("Watching pane %s range %s for: %q\n", paneID, rangeExpr, pattern)
	case absent:
		fmt.Printf("Watching pane %s until %q disappears\n", paneID, pattern)
	default:
		fmt.Printf("Watching pane %s for: %q\n", paneID, pattern)
	}
	if watchThen != "" {
		fmt.Printf("  on match → run instruction in pane %s\n", thenPane)
	}
	if watchKeys != "" {
		fmt.Printf("  on match → send keys %q to pane %s\n", watchKeys, thenPane)
	}
	if watchNotify != "" {
		fmt.Printf("  on match → notify: %q\n", watchNotify)
	}
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(time.Duration(watchInterval) * time.Millisecond)
	defer ticker.Stop()

	prevSlice := ""
	firstPoll := true

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nstopped.")
			return nil
		case <-ticker.C:
		}

		content, err := ex.CapturePaneClean(paneID, watchLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: capture %s: %v\n", paneID, err)
			continue
		}

		// [~:] — trigger at first changed line, return content from that line.
		if pane.IsChangeRange(rangeExpr) {
			if firstPoll {
				prevSlice = content
				firstPoll = false
				continue
			}
			startIdx, changed := pane.FromChange(prevSlice, content)
			prevSlice = content
			if startIdx < 0 {
				continue
			}
			fmt.Printf("\033[32m[change]\033[0m pane %s changed from line %d\n", paneID, startIdx)
			content = changed
		} else if rangeExpr != "" {
			// Apply static range slice.
			sliced, _, sliceErr := pane.SliceLines(content, rangeExpr)
			if sliceErr != nil {
				return fmt.Errorf("--range %q: %w", rangeExpr, sliceErr)
			}

			// Range-only mode: trigger on any change.
			if pattern == "" {
				if firstPoll {
					prevSlice = sliced
					firstPoll = false
					continue
				}
				if sliced == prevSlice {
					continue
				}
				fmt.Printf("\033[32m[change]\033[0m range %s changed in pane %s\n", rangeExpr, paneID)
				content = sliced
			} else {
				// Pattern mode within range.
				found := strings.Contains(sliced, pattern) || (re != nil && re.MatchString(sliced))
				if found == absent {
					continue
				}
				if absent {
					fmt.Printf("\033[32m[match]\033[0m pattern %q gone from range %s in pane %s\n", pattern, rangeExpr, paneID)
				} else {
					fmt.Printf("\033[32m[match]\033[0m pattern %q found in range %s of pane %s\n", pattern, rangeExpr, paneID)
				}
				content = sliced
			}
		} else {
			// No range: pattern mode on full content.
			found := strings.Contains(content, pattern) || (re != nil && re.MatchString(content))
			if found == absent {
				continue
			}
			if absent {
				fmt.Printf("\033[32m[match]\033[0m pattern %q gone from pane %s\n", pattern, paneID)
			} else {
				fmt.Printf("\033[32m[match]\033[0m pattern %q found in pane %s\n", pattern, paneID)
			}
		}

		if watchNotify != "" {
			fmt.Printf("\a\033[36m[notify] %s\033[0m\n", watchNotify)
		}

		if watchKeys != "" {
			keys := strings.ReplaceAll(watchKeys, `\n`, "\n")
			if err := ex.SendKeys(thenPane, keys); err != nil {
				fmt.Fprintf(os.Stderr, "error sending keys: %v\n", err)
			}
		}

		if watchThen != "" {
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
			r := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
			r.TargetPane = thenPane
			r.ExtraContext = content
			if err := r.Run(runCtx, watchThen); err != nil {
				fmt.Fprintf(os.Stderr, "error running instruction: %v\n", err)
			}
			cancel()
		}

		if watchOnce {
			return nil
		}
	}
}
