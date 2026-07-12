package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/spf13/cobra"
)

var keepHistory bool

var replCmd = &cobra.Command{
	Use:   "repl",
	Short: "Start an interactive session for repeated tmux instructions",
	Long: `Start an interactive REPL where you can type natural language instructions
repeatedly without restarting the program. Each instruction runs the agent
against fresh tmux state.

Special commands:
  list    Show current tmux state
  exit    Quit the REPL
  quit    Quit the REPL

Use --keep-history to maintain conversation context across instructions.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRepl()
	},
}

func init() {
	rootCmd.AddCommand(replCmd)
	replCmd.Flags().BoolVar(&keepHistory, "keep-history", false, "Maintain conversation context across instructions")
}

func runRepl() error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("tmux-ai repl (model: %s, host: %s)\n", model, ollamaHost)
	fmt.Println("Type an instruction, 'list' to show tmux state, or 'exit' to quit.")
	fmt.Println()

	var runner *agent.Runner
	if keepHistory {
		runner = agent.NewRunner(ollamaHost, model, maxIterations, verbose)
	}

	for {
		fmt.Print("tmux-ai> ")
		if !scanner.Scan() {
			fmt.Println()
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("Bye.")
			return nil
		case "list":
			if err := printTmuxState(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)

		if keepHistory {
			if err := runner.Run(ctx, input); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		} else {
			r := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
			if err := r.Run(ctx, input); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		}
		cancel()
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input error: %w", err)
	}
	return nil
}
