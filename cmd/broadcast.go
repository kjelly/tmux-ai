package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	broadcastPanes      string
	broadcastSession    string
	broadcastProcess    string
	broadcastSequential bool
)

var broadcastCmd = &cobra.Command{
	Use:   "broadcast <instruction>",
	Short: "Run the same AI instruction across multiple panes in parallel",
	Long: `Run an instruction targeting each matching pane. By default agents run in
parallel; use --sequential to run one at a time.

Target selection (at least one required):
  --panes    comma-separated pane IDs or aliases
  --session  all panes in a named tmux session
  --process  all panes running a named process (e.g. "claude", "opencode")

You can combine selectors — duplicate panes are deduplicated.

Examples:
  # Ask all claude sessions to summarize progress
  tmux-ai broadcast "summarize your current task in one sentence" --process claude

  # Send same instruction to specific panes
  tmux-ai broadcast "run the test suite" --panes "build1,build2,%33"

  # Ask every pane in a session to wrap up
  tmux-ai broadcast "finish what you're doing and wait for further instructions" \
    --session github`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		instruction := args[0]
		if broadcastPanes == "" && broadcastSession == "" && broadcastProcess == "" {
			return fmt.Errorf("at least one of --panes, --session, or --process is required")
		}
		return runBroadcast(instruction)
	},
}

func init() {
	rootCmd.AddCommand(broadcastCmd)
	broadcastCmd.Flags().StringVar(&broadcastPanes, "panes", "", "Comma-separated pane IDs or aliases")
	broadcastCmd.Flags().StringVar(&broadcastSession, "session", "", "Broadcast to all panes in this session")
	broadcastCmd.Flags().StringVar(&broadcastProcess, "process", "", "Broadcast to all panes running this process name")
	broadcastCmd.Flags().BoolVar(&broadcastSequential, "sequential", false, "Run one pane at a time instead of in parallel")
}

func runBroadcast(instruction string) error {
	r := newResolver()
	ex := tmux.NewExecutor()

	seen := map[string]bool{}
	var targets []tmux.PaneInfo

	allPanes, err := ex.ListPanesDetailed()
	if err != nil {
		return fmt.Errorf("list panes: %w", err)
	}

	paneByID := map[string]tmux.PaneInfo{}
	for _, p := range allPanes {
		paneByID[p.PaneID] = p
	}

	addPane := func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if p, ok := paneByID[id]; ok {
			targets = append(targets, p)
		} else {
			// unknown pane — still add a placeholder so users get an error per pane
			targets = append(targets, tmux.PaneInfo{PaneID: id, Command: "?"})
		}
	}

	if broadcastPanes != "" {
		for _, part := range strings.Split(broadcastPanes, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				addPane(r.ResolvePane(part))
			}
		}
	}

	if broadcastSession != "" {
		for _, p := range allPanes {
			if p.SessionName == broadcastSession {
				addPane(p.PaneID)
			}
		}
	}

	if broadcastProcess != "" {
		for _, p := range allPanes {
			if strings.Contains(p.Command, broadcastProcess) {
				addPane(p.PaneID)
			}
		}
	}

	if len(targets) == 0 {
		return fmt.Errorf("no matching panes found")
	}

	fmt.Printf("Broadcasting to %d panes: ", len(targets))
	for i, t := range targets {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%s(%s)", t.PaneID, t.Command)
	}
	fmt.Println()
	fmt.Println()

	type result struct {
		pane   tmux.PaneInfo
		output string
		err    error
	}

	runForPane := func(p tmux.PaneInfo) result {
		var buf bytes.Buffer
		w := io.MultiWriter(os.Stdout, &buf)
		fmt.Fprintf(w, "\033[33m=== %s (%s) ===\033[0m\n", p.PaneID, p.Command)

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
		defer cancel()

		r := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
		r.TargetPane = p.PaneID

		// Capture stdout from the runner by temporarily redirecting isn't easy,
		// so we just run it normally — output will interleave in parallel mode.
		err := r.Run(ctx, instruction)
		return result{pane: p, output: buf.String(), err: err}
	}

	if broadcastSequential {
		for _, t := range targets {
			res := runForPane(t)
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "\033[31merror for pane %s: %v\033[0m\n", t.PaneID, res.err)
			}
		}
		return nil
	}

	// Parallel execution
	var wg sync.WaitGroup
	results := make([]result, len(targets))
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, pane tmux.PaneInfo) {
			defer wg.Done()
			results[idx] = runForPane(pane)
		}(i, t)
	}
	wg.Wait()

	fmt.Println("\n\033[32m=== Broadcast complete ===\033[0m")
	for _, res := range results {
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "  %s: \033[31merror: %v\033[0m\n", res.pane.PaneID, res.err)
		}
	}
	return nil
}
