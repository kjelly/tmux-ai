package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/config"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	popupPane    string
	popupSession string
	popupWindow  string
	popupMulti   bool
)

var popupCmd = &cobra.Command{
	Use:   "popup",
	Short: "Quick interactive prompt designed to run inside tmux display-popup",
	Long: `Minimal prompt intended to run inside a tmux popup overlay.

Bind a key in tmux.conf to open it instantly:

  # Single-shot: type one instruction, popup closes when done
  bind-key i run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id}\""

  # Multi-shot: keep popup open for multiple instructions
  bind-key I run-shell "tmux display-popup -E -w 80% -h 60% -b rounded -T ' tmux-ai ' \"tmux-ai popup -p #{pane_id} --multi\""

The #{pane_id} is expanded by tmux before running the command, so the agent
knows which pane triggered the popup and targets it automatically.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPopup()
	},
}

func init() {
	rootCmd.AddCommand(popupCmd)
	popupCmd.Flags().StringVarP(&popupPane, "pane", "p", "", "Target pane: %ID, alias, or session:window.pane (pass #{pane_id} from tmux binding)")
	popupCmd.Flags().StringVarP(&popupSession, "session", "s", "", "Target session (alternative to --pane)")
	popupCmd.Flags().StringVarP(&popupWindow, "window", "w", "", "Target window index or name (use with --session)")
	popupCmd.Flags().BoolVarP(&popupMulti, "multi", "m", false, "Keep popup open for multiple instructions (default: close after one)")
}

func runPopup() error {
	ex := tmux.NewExecutor()
	aliases, _ := config.LoadAliases()

	// Resolve target pane
	var target string
	switch {
	case popupPane != "":
		target = resolveToPane(aliases.Resolve(popupPane))
	case popupSession != "" && popupWindow != "":
		target = resolveToPane(popupSession + ":" + popupWindow)
	case popupSession != "":
		target = resolveToPane(popupSession + ":")
	}

	printPopupHeader(ex, target)

	scanner := bufio.NewScanner(os.Stdin)
	first := true

	for {
		if !first {
			fmt.Println()
		}
		first = false

		fmt.Print("\033[1;36m❯\033[0m ")

		if !scanner.Scan() {
			break
		}
		instruction := strings.TrimSpace(scanner.Text())
		if instruction == "" {
			if !popupMulti {
				break
			}
			continue
		}

		fmt.Println()

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
		r := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
		if target != "" {
			r.TargetPane = target
		}

		if err := r.Run(ctx, instruction); err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror: %v\033[0m\n", err)
		}
		cancel()

		if !popupMulti {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func printPopupHeader(ex *tmux.Executor, paneID string) {
	const reset = "\033[0m"
	const bold = "\033[1m"
	const dim = "\033[2m"
	const magenta = "\033[35m"
	const cyan = "\033[36m"
	const green = "\033[32m"
	const yellow = "\033[33m"

	fmt.Printf("%s%s tmux-ai %s", bold, magenta, reset)

	if paneID != "" {
		panes, err := ex.ListPanesDetailed()
		if err == nil {
			for _, p := range panes {
				if p.PaneID != paneID {
					continue
				}
				shells := map[string]bool{
					"bash": true, "zsh": true, "sh": true,
					"fish": true, "ksh": true, "nu": true, "tmux": true,
				}
				statusColor := green
				status := "idle"
				if !shells[p.Command] {
					statusColor = yellow
					status = "busy"
				}
				fmt.Printf(
					" %s│%s %s%s:%d%s  %s%s%s  %s[%s]%s  %s%s%s",
					dim, reset,
					cyan, p.SessionName, p.WindowIndex, reset,
					dim, p.PaneID, reset,
					statusColor, p.Command, reset,
					dim, status, reset,
				)
				break
			}
		}
	}

	fmt.Println()
	fmt.Printf("%s%s%s\n", dim, strings.Repeat("─", 60), reset)
}
