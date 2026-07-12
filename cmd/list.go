package cmd

import (
	"fmt"
	"strings"

	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Show current tmux sessions, windows, and panes",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printTmuxState()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func printTmuxState() error {
	ex := tmux.NewExecutor()
	panes, err := ex.ListPanesDetailed()
	if err != nil {
		return fmt.Errorf("list tmux state: %w", err)
	}
	if len(panes) == 0 {
		fmt.Println("No active tmux sessions.")
		return nil
	}

	type winKey struct {
		session string
		winIdx  int
	}
	type sessionKey = string

	sessionOrder := []sessionKey{}
	sessionSeen := map[sessionKey]bool{}
	winOrder := []winKey{}
	winMap := map[winKey][]tmux.PaneInfo{}
	winMeta := map[winKey]tmux.PaneInfo{}

	for _, p := range panes {
		if !sessionSeen[p.SessionName] {
			sessionOrder = append(sessionOrder, p.SessionName)
			sessionSeen[p.SessionName] = true
		}
		k := winKey{p.SessionName, p.WindowIndex}
		if _, ok := winMap[k]; !ok {
			winOrder = append(winOrder, k)
			winMeta[k] = p
		}
		winMap[k] = append(winMap[k], p)
	}

	// shells map now comes from tmuxutil.ShellCommands

	for _, sess := range sessionOrder {
		fmt.Printf("Session: %s\n", sess)
		for _, k := range winOrder {
			if k.session != sess {
				continue
			}
			meta := winMeta[k]
			activeMarker := ""
			if meta.WindowActive {
				activeMarker = " [active]"
			}
			fmt.Printf("  Window %d: %s%s\n", k.winIdx, meta.WindowName, activeMarker)
			for _, p := range winMap[k] {
				pActive := "  "
				if p.PaneActive {
					pActive = "* "
				}
				status := "busy"
				if tmuxutil.IsIdle(p.Command) {
					status = "idle"
				}
				fmt.Printf("    %s%s  cmd=%-12s  [%s]\n", pActive, p.PaneID, p.Command, status)

				content, err := ex.CapturePaneClean(p.PaneID, 5)
				if err != nil {
					continue
				}
				shown := 0
				lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
				for i := len(lines) - 1; i >= 0 && shown < 3; i-- {
					clean := tmuxutil.SanitizeLine(lines[i], 80)
					if clean != "" {
						fmt.Printf("       | %s\n", clean)
						shown++
					}
				}
			}
		}
	}
	return nil
}
