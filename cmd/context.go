package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/llmore/tmux-ai/internal/config"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage the global context file (~/.config/tmux-ai/context.md)",
	Long: `The global context file is automatically prepended to every instruction
sent to the LLM. Use it to describe your environment, preferences, or
anything the agent should always know.

The file is plain text / Markdown at ~/.config/tmux-ai/context.md.`,
}

var contextShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the current global context",
	RunE: func(cmd *cobra.Command, args []string) error {
		gc := config.LoadGlobalContext()
		if gc == "" {
			path, _ := config.GlobalContextPath()
			fmt.Printf("No global context set. Create %s to get started.\n", path)
			return nil
		}
		fmt.Println(gc)
		return nil
	},
}

var contextEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open the global context file in $EDITOR",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.GlobalContextPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(pathDir(path), 0755); err != nil {
			return err
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		c := exec.Command(editor, path)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

var contextSetCmd = &cobra.Command{
	Use:   "set <text>",
	Short: "Set the global context to the given text",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.GlobalContextPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(pathDir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(args[0]+"\n"), 0644); err != nil {
			return err
		}
		fmt.Printf("Global context updated (%d chars)\n", len(args[0]))
		return nil
	},
}

var contextClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Delete the global context file",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.GlobalContextPath()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No global context to clear.")
				return nil
			}
			return err
		}
		fmt.Println("Global context cleared.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(contextCmd)
	contextCmd.AddCommand(contextShowCmd, contextEditCmd, contextSetCmd, contextClearCmd)
}

func pathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
