package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/llmore/tmux-ai/internal/config"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

var aliasCmd = &cobra.Command{
	Use:   "alias",
	Short: "Manage named aliases for tmux pane IDs",
	Long: `Manage named aliases so you can refer to panes by name instead of %ID.

Aliases are stored in ~/.config/tmux-ai/aliases.json.

Examples:
  tmux-ai alias set claude-sport        # alias current pane as "claude-sport"
  tmux-ai alias set build-pane %25      # alias pane %25 as "build-pane"
  tmux-ai alias list
  tmux-ai alias delete claude-sport

  tmux-ai run --pane claude-sport "explain what you just did"
  tmux-ai watch claude-sport "Task complete" --notify "done"`,
}

var aliasSetCmd = &cobra.Command{
	Use:   "set <name> [pane-id]",
	Short: "Set a named alias. Omit pane-id to use the current pane ($TMUX_PANE).",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		var paneID string
		if len(args) == 2 {
			paneID = args[1]
		} else {
			paneID = os.Getenv("TMUX_PANE")
			if paneID == "" {
				return fmt.Errorf("no pane-id provided and $TMUX_PANE is not set (are you inside tmux?)")
			}
		}

		aliases, err := config.LoadAliases()
		if err != nil {
			return fmt.Errorf("load aliases: %w", err)
		}
		if aliases == nil {
			aliases = config.Aliases{}
		}
		old, existed := aliases[name]
		aliases[name] = paneID
		if err := aliases.Save(); err != nil {
			return fmt.Errorf("save aliases: %w", err)
		}
		if existed {
			fmt.Printf("updated alias: %s = %s  (was %s)\n", name, paneID, old)
		} else {
			fmt.Printf("alias set: %s = %s\n", name, paneID)
		}
		return nil
	},
}

var aliasListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all aliases with current pane status",
	RunE: func(cmd *cobra.Command, args []string) error {
		aliases, err := config.LoadAliases()
		if err != nil {
			return fmt.Errorf("load aliases: %w", err)
		}
		if len(aliases) == 0 {
			fmt.Println("No aliases defined.")
			fmt.Println("  tmux-ai alias set <name> [pane-id]")
			return nil
		}

		ex := tmux.NewExecutor()
		panes, _ := ex.ListPanesDetailed()
		paneCmd := map[string]string{}
		for _, p := range panes {
			paneCmd[p.PaneID] = p.Command
		}

		names := make([]string, 0, len(aliases))
		for n := range aliases {
			names = append(names, n)
		}
		sort.Strings(names)

		maxName := 0
		for _, n := range names {
			if len(n) > maxName {
				maxName = len(n)
			}
		}

		fmt.Printf("  %-*s  %-6s  %s\n", maxName, "NAME", "PANE", "RUNNING")
		fmt.Printf("  %-*s  %-6s  %s\n", maxName, "----", "----", "-------")
		for _, n := range names {
			id := aliases[n]
			running := paneCmd[id]
			if running == "" {
				running = "(pane not found)"
			}
			fmt.Printf("  %-*s  %-6s  %s\n", maxName, n, id, running)
		}
		return nil
	},
}

var aliasDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an alias",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		aliases, err := config.LoadAliases()
		if err != nil {
			return fmt.Errorf("load aliases: %w", err)
		}
		if _, ok := aliases[name]; !ok {
			return fmt.Errorf("alias %q not found", name)
		}
		delete(aliases, name)
		if err := aliases.Save(); err != nil {
			return fmt.Errorf("save aliases: %w", err)
		}
		fmt.Printf("deleted alias: %s\n", name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(aliasCmd)
	aliasCmd.AddCommand(aliasSetCmd, aliasListCmd, aliasDeleteCmd)
}
