package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	wf "github.com/llmore/tmux-ai/internal/workflow"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var workflowParams []string // --param key=value

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Run multi-step workflows defined in YAML",
}

var workflowRunCmd = &cobra.Command{
	Use:   "run <file.yaml>",
	Short: "Execute a workflow file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkflow(args[0])
	},
}

var workflowValidateCmd = &cobra.Command{
	Use:   "validate <file.yaml>",
	Short: "Parse and validate a workflow file without executing it",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := loadWorkflow(args[0])
		if err != nil {
			return err
		}
		if err := validateWorkflow(w); err != nil {
			return workflowValidationError(args[0], err)
		}
		fmt.Printf("OK: workflow %q has %d steps\n", w.Name, len(w.Steps))
		for i, s := range w.Steps {
			deps := ""
			if len(s.Depends) > 0 {
				deps = " (depends: " + strings.Join(s.Depends, ", ") + ")"
			}
			fmt.Printf("  %d. [%s] %s%s\n", i+1, stepType(s), s.Name, deps)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(workflowCmd)
	workflowCmd.AddCommand(workflowRunCmd, workflowValidateCmd)
	workflowRunCmd.Flags().StringArrayVar(&workflowParams, "param", nil, "Override workflow param: key=value (repeatable)")
}

func loadWorkflow(path string) (*wf.Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workflow file: %w", err)
	}
	var w wf.Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, appendYAMLCommandHint(fmt.Errorf("parse workflow YAML: %w", err), data, "workflow")
	}
	return &w, nil
}

func validateWorkflow(w *wf.Workflow) error {
	if w.Name == "" {
		return fmt.Errorf("workflow must have a name")
	}
	if len(w.Steps) == 0 {
		return fmt.Errorf("workflow must have at least one step")
	}
	names := map[string]bool{}
	for _, s := range w.Steps {
		if s.Name == "" {
			return fmt.Errorf("all steps must have a name")
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate step name: %q", s.Name)
		}
		names[s.Name] = true
	}
	for _, s := range w.Steps {
		for _, dep := range s.Depends {
			if !names[dep] {
				return fmt.Errorf("step %q depends on unknown step %q", s.Name, dep)
			}
		}
	}

	// Detect cycles via DFS
	visited := map[string]int{} // 0=unvisited, 1=in-progress, 2=done
	var stepByName = map[string]wf.Step{}
	for _, s := range w.Steps {
		stepByName[s.Name] = s
	}
	var hasCycle func(name string) bool
	hasCycle = func(name string) bool {
		if visited[name] == 1 {
			return true
		}
		if visited[name] == 2 {
			return false
		}
		visited[name] = 1
		s := stepByName[name]
		for _, dep := range s.Depends {
			if hasCycle(dep) {
				return true
			}
		}
		visited[name] = 2
		return false
	}
	for _, s := range w.Steps {
		if hasCycle(s.Name) {
			return fmt.Errorf("circular dependency detected involving step %q", s.Name)
		}
	}
	return nil
}

func runWorkflow(path string) error {
	w, err := loadWorkflow(path)
	if err != nil {
		return err
	}
	if err := validateWorkflow(w); err != nil {
		return workflowValidationError(path, err)
	}

	// Parse --param key=value overrides
	params := map[string]string{}
	for _, p := range workflowParams {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return fmt.Errorf("invalid --param %q: expected key=value", p)
		}
		params[k] = v
	}

	ex := wf.NewExecutor(w, params, ollamaHost, model, maxIterations, timeoutSeconds, verbose)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	return ex.Run(ctx)
}

func workflowValidationError(path string, err error) error {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return fmt.Errorf("invalid workflow: %w", err)
	}
	return appendYAMLCommandHint(fmt.Errorf("invalid workflow: %w", err), data, "workflow")
}

func stepType(s wf.Step) string {
	if s.Type == "" {
		return "run"
	}
	return s.Type
}
