package cmd

import (
	"fmt"
	"context"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	pipePaneFlag    string
	pipeSessionFlag string
	pipeWindowFlag  string
	pipeLabel       string
)

var pipeCmd = &cobra.Command{
	Use:   "pipe <instruction>",
	Short: "Read stdin as context and pass it to the agent",
	Long: `Reads stdin and uses it as extra context for the given instruction.
Useful for piping command output, files, or diffs directly into tmux-ai.

Examples:
  git diff | tmux-ai pipe "review this diff for correctness" --pane claude-sport
  cat build.log | tmux-ai pipe "what went wrong?"
  tmux-ai list | tmux-ai pipe "which agents look stuck?"
  kubectl logs pod/app | tmux-ai pipe "summarize the errors" -p dev-shell`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		stdin := strings.TrimSpace(string(data))
		if stdin == "" {
			return fmt.Errorf("stdin is empty")
		}

		label := pipeLabel
		if label == "" {
			label = "stdin"
		}
		extraCtx := fmt.Sprintf("%s:\n%s", label, stdin)

		resolver := newResolver()
		tpane := resolver.ResolvePaneFromFlags(pipePaneFlag, pipeSessionFlag, pipeWindowFlag)

		runner := newRunner()
		runner.ExtraContext = extraCtx
		runner.TargetPane = tpane

		return runWithTimeout(func(ctx context.Context) error {
			return runner.Run(ctx, args[0])
		})
	},
}

func init() {
	rootCmd.AddCommand(pipeCmd)
	pipeCmd.Flags().StringVarP(&pipePaneFlag, "pane", "p", "", "Target pane (alias, %ID, or session:window)")
	pipeCmd.Flags().StringVarP(&pipeSessionFlag, "session", "s", "", "Target session")
	pipeCmd.Flags().StringVarP(&pipeWindowFlag, "window", "w", "", "Target window (use with --session)")
	pipeCmd.Flags().StringVar(&pipeLabel, "label", "", "Label for the stdin content in context (default: \"stdin\")")
}
