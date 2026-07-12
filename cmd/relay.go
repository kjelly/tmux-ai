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

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	relayFrom       string
	relayTo         string
	relayTrigger    string
	relayContinuous bool
	relayInterval   int
	relayLines      int
)

var relayCmd = &cobra.Command{
	Use:   "relay <instruction>",
	Short: "Watch one pane and forward captured content to an agent in another pane",
	Long: `Monitor --from pane for an optional --trigger pattern.
When triggered (or immediately if no trigger), capture its content and run
the instruction with the captured content as context, targeting --to pane.

Use --continuous to keep relaying whenever new content appears.

Examples:
  # When build fails, ask claude to fix it
  tmux-ai relay "Fix the error shown in the context" \
    --from build-pane --to claude-sport --trigger "error:"

  # Continuously relay output from one agent to another
  tmux-ai relay "Summarize the latest output and decide next step" \
    --from opencode-pane --to claude-sport --continuous

  # One-shot: capture current state of a pane and ask agent to act
  tmux-ai relay "Review this output and run the suggested fix" \
    --from %25 --to %27`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		instruction := args[0]
		if relayFrom == "" || relayTo == "" {
			return fmt.Errorf("--from and --to are required")
		}
		r := newResolver()
		from := r.ResolvePane(relayFrom)
		to := r.ResolvePane(relayTo)
		return runRelay(instruction, from, to)
	},
}

func init() {
	rootCmd.AddCommand(relayCmd)
	relayCmd.Flags().StringVar(&relayFrom, "from", "", "Source pane to monitor (ID or alias)")
	relayCmd.Flags().StringVar(&relayTo, "to", "", "Target pane for the agent (ID or alias)")
	relayCmd.Flags().StringVar(&relayTrigger, "trigger", "", "Pattern to wait for before relaying (default: relay immediately or on any new content)")
	relayCmd.Flags().BoolVar(&relayContinuous, "continuous", false, "Keep relaying whenever new content appears in --from pane")
	relayCmd.Flags().IntVar(&relayInterval, "interval", 3000, "Poll interval in milliseconds (only used with --continuous or --trigger)")
	relayCmd.Flags().IntVar(&relayLines, "lines", 80, "Lines of source pane content to capture as context")
}

func contentHash(s string) [32]byte {
	return sha256.Sum256([]byte(s))
}

func runRelay(instruction, from, to string) error {
	ex := tmux.NewExecutor()

	var re *regexp.Regexp
	if relayTrigger != "" {
		var err error
		re, err = regexp.Compile(relayTrigger)
		if err != nil {
			return fmt.Errorf("invalid trigger pattern: %w", err)
		}
	}

	doRelay := func(content string) error {
		fmt.Printf("\033[36m[relay]\033[0m from %s → agent in %s\n", from, to)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
		defer cancel()
		r := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
		r.TargetPane = to
		r.ExtraContext = content
		return r.Run(ctx, instruction)
	}

	// One-shot with no trigger: capture now and relay
	if !relayContinuous && relayTrigger == "" {
		content, err := ex.CapturePaneClean(from, relayLines)
		if err != nil {
			return fmt.Errorf("capture pane %s: %w", from, err)
		}
		return doRelay(content)
	}

	// Polling mode: wait for trigger or new content
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if relayTrigger != "" {
		fmt.Printf("Monitoring pane %s for trigger %q, will relay to pane %s\n", from, relayTrigger, to)
	} else {
		fmt.Printf("Monitoring pane %s for new content, will relay to pane %s\n", from, to)
	}
	if relayContinuous {
		fmt.Println("Running continuously. Press Ctrl+C to stop.")
	}
	fmt.Println()

	ticker := time.NewTicker(time.Duration(relayInterval) * time.Millisecond)
	defer ticker.Stop()

	var lastHash [32]byte
	first := true

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nstopped.")
			return nil
		case <-ticker.C:
		}

		content, err := ex.CapturePaneClean(from, relayLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: capture %s: %v\n", from, err)
			continue
		}

		h := contentHash(content)

		if relayTrigger != "" {
			matched := strings.Contains(content, relayTrigger) || re.MatchString(content)
			if !matched {
				continue
			}
			// only trigger once per unique content
			if h == lastHash && !first {
				continue
			}
		} else {
			// trigger on any content change
			if h == lastHash && !first {
				continue
			}
		}

		first = false
		lastHash = h

		if err := doRelay(content); err != nil {
			fmt.Fprintf(os.Stderr, "relay error: %v\n", err)
		}

		if !relayContinuous {
			return nil
		}
	}
}
