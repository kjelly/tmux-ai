package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

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

func runAgent(instruction string) error {
	runner := newRunner()
	runner.CheckpointFile = resumePath
	runner.ReportFile = reportPath

	r := newResolver()
	if t := r.ResolvePaneFromFlags(targetPane, targetSession, targetWindow); t != "" {
		runner.TargetPane = t
	}

	return runWithTimeout(func(ctx context.Context) error {
		if err := runner.Run(ctx, instruction); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return err
		}
		return nil
	})
}
