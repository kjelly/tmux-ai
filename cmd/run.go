package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/config"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	filePath      string
	resumePath    string
	targetPane    string
	targetSession string
	targetWindow  string
	reportPath    string
)

var runCmd = &cobra.Command{
	Use:   "run [\"instruction\"]",
	Short: "Run tmux automation with natural language instruction or script file",
	Args: func(cmd *cobra.Command, args []string) error {
		if filePath == "" && len(args) == 0 && resumePath == "" {
			return errors.New("either --file flag, --resume flag, or instruction argument is required")
		}
		if filePath != "" && len(args) > 0 {
			return errors.New("cannot use both --file flag and instruction argument")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		var instruction string

		if filePath != "" {
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %w", filePath, err)
			}
			instruction = string(data)
		} else if len(args) > 0 {
			instruction = args[0]
		}

		return runAgent(instruction)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVarP(&filePath, "file", "f", "", "Path to script file containing the instruction")
	runCmd.Flags().StringVar(&resumePath, "resume", "", "Path to checkpoint file to resume from, or path to save new checkpoints")
	runCmd.Flags().StringVarP(&targetPane, "pane", "p", "", "Focus agent on this pane: %ID, alias, or session:window.pane")
	runCmd.Flags().StringVarP(&targetSession, "session", "s", "", "Target session name (combine with --window for session:window)")
	runCmd.Flags().StringVarP(&targetWindow, "window", "w", "", "Target window index or name (combine with --session)")
	runCmd.Flags().StringVar(&reportPath, "report", "", "Path to generate HTML visualization report on completion")
}

// buildTarget resolves the effective tmux target from flags in priority order:
// --pane (or alias) > --session + --window > --session alone
func buildTarget() string {
	aliases, _ := config.LoadAliases()

	if targetPane != "" {
		return aliases.Resolve(targetPane)
	}
	if targetSession != "" {
		if targetWindow != "" {
			return targetSession + ":" + targetWindow
		}
		return targetSession + ":"
	}
	return ""
}

// resolveToPane resolves a tmux target string to a canonical %pane_id.
// Falls back to the original string if resolution fails (tmux still accepts most formats).
func resolveToPane(target string) string {
	if target == "" {
		return ""
	}
	ex := tmux.NewExecutor()
	if id, err := ex.ResolveTarget(target); err == nil {
		return id
	}
	return target
}

// windowIndex tries to parse a string as an integer window index.
// Returns the string unchanged if it is not numeric (window name case).
func windowIndex(s string) string {
	if _, err := strconv.Atoi(s); err == nil {
		return s // numeric, leave as-is for tmux
	}
	return s
}

func runAgent(instruction string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	runner := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
	runner.CheckpointFile = resumePath
	runner.ReportFile = reportPath

	if t := buildTarget(); t != "" {
		_ = windowIndex // used implicitly via targetWindow
		runner.TargetPane = resolveToPane(t)
	}

	if err := runner.Run(ctx, instruction); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	return nil
}
