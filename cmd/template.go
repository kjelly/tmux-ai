package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/config"
	"github.com/spf13/cobra"
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage saved instruction templates",
	Long: `Save frequently-used instructions as named templates.
Templates are stored in ~/.config/tmux-ai/templates.json.

Examples:
  tmux-ai template save code-review "review the latest git diff for correctness and security issues"
  tmux-ai template list
  tmux-ai template run code-review --pane claude-sport
  tmux-ai template get code-review    # print to stdout (for use in other commands)
  tmux-ai template delete code-review`,
}

var templateSaveCmd = &cobra.Command{
	Use:   "save <name> [instruction]",
	Short: "Save an instruction as a template. Omit instruction to read from stdin.",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		var instruction string
		if len(args) == 2 {
			instruction = args[1]
		} else {
			data, err := io.ReadAll(bufio.NewReader(os.Stdin))
			if err != nil {
				return err
			}
			instruction = strings.TrimSpace(string(data))
		}
		if instruction == "" {
			return fmt.Errorf("instruction cannot be empty")
		}

		templates, err := config.LoadTemplates()
		if err != nil {
			return fmt.Errorf("load templates: %w", err)
		}
		if templates == nil {
			templates = config.Templates{}
		}
		_, existed := templates[name]
		templates[name] = instruction
		if err := templates.Save(); err != nil {
			return fmt.Errorf("save templates: %w", err)
		}
		if existed {
			fmt.Printf("updated template: %s\n", name)
		} else {
			fmt.Printf("saved template: %s\n", name)
		}
		return nil
	},
}

var templateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		templates, err := config.LoadTemplates()
		if err != nil {
			return fmt.Errorf("load templates: %w", err)
		}
		if len(templates) == 0 {
			fmt.Println("No templates saved.")
			fmt.Println("  tmux-ai template save <name> \"<instruction>\"")
			return nil
		}
		names := make([]string, 0, len(templates))
		for n := range templates {
			names = append(names, n)
		}
		sort.Strings(names)

		maxLen := 0
		for _, n := range names {
			if len(n) > maxLen {
				maxLen = len(n)
			}
		}
		for _, n := range names {
			instr := templates[n]
			if len(instr) > 70 {
				instr = instr[:67] + "..."
			}
			fmt.Printf("  %-*s  %s\n", maxLen, n, instr)
		}
		return nil
	},
}

var templateGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Print the instruction for a template (for use in shell substitution)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		templates, err := config.LoadTemplates()
		if err != nil {
			return fmt.Errorf("load templates: %w", err)
		}
		instr, ok := templates[args[0]]
		if !ok {
			return fmt.Errorf("template %q not found", args[0])
		}
		fmt.Print(instr)
		return nil
	},
}

var templateDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a template",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		templates, err := config.LoadTemplates()
		if err != nil {
			return fmt.Errorf("load templates: %w", err)
		}
		if _, ok := templates[args[0]]; !ok {
			return fmt.Errorf("template %q not found", args[0])
		}
		delete(templates, args[0])
		if err := templates.Save(); err != nil {
			return fmt.Errorf("save templates: %w", err)
		}
		fmt.Printf("deleted template: %s\n", args[0])
		return nil
	},
}

var (
	tmplRunPane    string
	tmplRunSession string
	tmplRunWindow  string
)

var templateRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Execute a saved template",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		templates, err := config.LoadTemplates()
		if err != nil {
			return fmt.Errorf("load templates: %w", err)
		}
		instr, ok := templates[args[0]]
		if !ok {
			return fmt.Errorf("template %q not found", args[0])
		}

		aliases, _ := config.LoadAliases()

		var tpane string
		switch {
		case tmplRunPane != "":
			tpane = resolveToPane(aliases.Resolve(tmplRunPane))
		case tmplRunSession != "" && tmplRunWindow != "":
			tpane = resolveToPane(tmplRunSession + ":" + tmplRunWindow)
		case tmplRunSession != "":
			tpane = resolveToPane(tmplRunSession + ":")
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
		defer cancel()

		r := agent.NewRunner(ollamaHost, model, maxIterations, verbose)
		r.TargetPane = tpane

		return r.Run(ctx, instr)
	},
}

func init() {
	rootCmd.AddCommand(templateCmd)
	templateCmd.AddCommand(templateSaveCmd, templateListCmd, templateGetCmd, templateDeleteCmd, templateRunCmd)
	templateRunCmd.Flags().StringVarP(&tmplRunPane, "pane", "p", "", "Target pane (alias, %ID, or session:window)")
	templateRunCmd.Flags().StringVarP(&tmplRunSession, "session", "s", "", "Target session")
	templateRunCmd.Flags().StringVarP(&tmplRunWindow, "window", "w", "", "Target window (use with --session)")
}
