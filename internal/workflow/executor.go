package workflow

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/llmore/tmux-ai/internal/artifact"
)

// Executor runs a Workflow as a DAG.
type Executor struct {
	wf            *Workflow
	params        map[string]string // merged: workflow defaults + CLI overrides
	ollamaHost    string
	model         string
	maxIterations int
	timeout       int
	verbose       bool
	artifactStore artifact.Store

	mu      sync.RWMutex
	results map[string]*StepResult
}

func NewExecutor(wf *Workflow, params map[string]string, host, model string, maxIter, timeout int, verbose bool) *Executor {
	merged := make(map[string]string)
	for k, v := range wf.Params {
		merged[k] = v
	}
	for k, v := range params {
		merged[k] = v
	}
	return &Executor{
		wf:            wf,
		params:        merged,
		ollamaHost:    host,
		model:         model,
		maxIterations: maxIter,
		timeout:       timeout,
		verbose:       verbose,
		results:       make(map[string]*StepResult),
	}
}

func (e *Executor) Run(ctx context.Context) error {
	transport := strings.ToLower(strings.TrimSpace(e.wf.HandoffTransport))
	if transport != "" && transport != "file" {
		return fmt.Errorf("unsupported workflow handoff_transport %q", e.wf.HandoffTransport)
	}
	if transport == "file" {
		store, err := artifact.New(e.wf.Artifacts.Root, e.wf.Artifacts.Keep, artifact.DefaultLimits())
		if err != nil {
			return fmt.Errorf("create workflow artifact store: %w", err)
		}
		e.artifactStore = store
		defer func() { _ = store.Close() }()
		if e.wf.Artifacts.Keep {
			fmt.Printf("📁 Workflow artifact root: %s\n", store.Root())
		}
	}
	steps := e.wf.Steps
	total := len(steps)
	if total == 0 {
		return fmt.Errorf("workflow %q has no steps", e.wf.Name)
	}

	resultCh := make(chan *StepResult, total)
	running := map[string]bool{}

	fmt.Printf("\033[1;35m[workflow]\033[0m %s\n", e.wf.Name)
	if e.wf.Description != "" {
		fmt.Printf("           %s\n", e.wf.Description)
	}
	fmt.Println()

	for {
		// Check completion
		e.mu.RLock()
		done := len(e.results) == total
		e.mu.RUnlock()
		if done {
			break
		}

		// Launch all newly-ready steps
		progressed := false
		for i := range steps {
			s := &steps[i]
			e.mu.RLock()
			_, completed := e.results[s.Name]
			isRunning := running[s.Name]
			e.mu.RUnlock()

			if completed || isRunning {
				continue
			}

			e.mu.RLock()
			depsOK, shouldSkip := e.evalDeps(s)
			e.mu.RUnlock()

			if shouldSkip {
				e.mu.Lock()
				e.results[s.Name] = &StepResult{Name: s.Name, Status: "skipped"}
				e.mu.Unlock()
				fmt.Printf("\033[2m[skip]\033[0m  %s\n", s.Name)
				resultCh <- &StepResult{Name: s.Name, Status: "skipped"}
				progressed = true
				continue
			}

			if !depsOK {
				continue
			}

			running[s.Name] = true
			progressed = true
			go func(step *Step) {
				res := e.runStep(ctx, step)
				e.mu.Lock()
				e.results[step.Name] = res
				e.mu.Unlock()
				resultCh <- res
			}(s)
		}

		// Deadlock detection: if nothing progressed and nothing is running,
		// remaining steps have unsatisfiable deps.
		if !progressed && len(running) == 0 {
			// Identify stuck steps
			e.mu.RLock()
			var stuck []string
			for _, s := range steps {
				if _, ok := e.results[s.Name]; !ok {
					stuck = append(stuck, s.Name)
				}
			}
			e.mu.RUnlock()
			return fmt.Errorf("workflow deadlocked: steps %v have unsatisfiable dependencies", stuck)
		}

		// Wait for any step to finish
		select {
		case <-ctx.Done():
			return ctx.Err()
		case res := <-resultCh:
			delete(running, res.Name)
			if res.Status == "failed" {
				step := e.findStep(res.Name)
				if step == nil || step.OnFailure != "continue" {
					return fmt.Errorf("step %q failed: %v", res.Name, res.Err)
				}
			}
		}
	}

	// Summary
	fmt.Println()
	e.mu.RLock()
	defer e.mu.RUnlock()
	ok, failed, skipped := 0, 0, 0
	for _, r := range e.results {
		switch r.Status {
		case "success":
			ok++
		case "failed":
			failed++
		case "skipped":
			skipped++
		}
	}
	color := "\033[32m"
	if failed > 0 {
		color = "\033[31m"
	}
	fmt.Printf("%s[workflow done]\033[0m  success=%d  failed=%d  skipped=%d\n", color, ok, failed, skipped)
	return nil
}

// evalDeps returns (depsReady, shouldSkip).
// shouldSkip is true when all deps are done but the `when` condition is false.
func (e *Executor) evalDeps(s *Step) (ready bool, skip bool) {
	for _, dep := range s.Depends {
		r, ok := e.results[dep]
		if !ok {
			return false, false // dep not done yet
		}
		if r.Status == "failed" {
			// dep failed — only continue if when=on_failure or when=always
			w := strings.ToLower(strings.TrimSpace(s.When))
			if w != "on_failure" && w != "always" {
				return false, true // skip this step
			}
		}
	}

	// All deps done — evaluate when condition
	w := strings.TrimSpace(s.When)
	if w == "" || strings.ToLower(w) == "on_success" {
		return true, false
	}
	if strings.ToLower(w) == "always" {
		return true, false
	}
	if strings.ToLower(w) == "on_failure" {
		// Only run if at least one dep failed
		for _, dep := range s.Depends {
			if r := e.results[dep]; r != nil && r.Status == "failed" {
				return true, false
			}
		}
		return false, true // no dep failed → skip
	}

	// contains / not_contains expressions
	if ok, result := e.evalExpr(w); ok {
		if result {
			return true, false
		}
		return false, true
	}

	return true, false
}

// evalExpr handles: contains(steps.NAME.output,"text") and not_contains(...)
func (e *Executor) evalExpr(expr string) (handled bool, result bool) {
	expr = strings.TrimSpace(expr)

	negated := false
	check := expr
	if strings.HasPrefix(expr, "not_contains(") {
		negated = true
		check = "contains(" + expr[len("not_contains("):]
	}

	re := regexp.MustCompile(`^contains\(steps\.([^.]+)\.output\s*,\s*"([^"]*)"\s*\)$`)
	m := re.FindStringSubmatch(check)
	if len(m) != 3 {
		return false, false
	}
	stepName, text := m[1], m[2]

	r, ok := e.results[stepName]
	if !ok {
		return true, false // step not done yet → false
	}
	found := strings.Contains(r.Output, text)
	if negated {
		found = !found
	}
	return true, found
}

func (e *Executor) runStep(ctx context.Context, s *Step) *StepResult {
	name := e.expand(s.Name)
	fmt.Printf("\033[36m[step]\033[0m  %s\n", name)

	var err error
	var output string

	switch strings.ToLower(s.Type) {
	case "run", "":
		output, err = e.runAgentStep(ctx, s)
	case "send":
		output, err = e.runSendStep(ctx, s)
	case "watch":
		output, err = e.runWatchStep(ctx, s)
	case "shell":
		output, err = e.runShellStep(ctx, s)
	case "launch":
		output, err = e.runLaunchStep(ctx, s)
	case "wait":
		output, err = e.runWaitStep(ctx, s)
	case "relay":
		output, err = e.runRelayStep(ctx, s)
	case "broadcast":
		output, err = e.runBroadcastStep(ctx, s)
	default:
		err = fmt.Errorf("unknown step type %q", s.Type)
	}

	status := "success"
	if err != nil {
		status = "failed"
		fmt.Fprintf(os.Stderr, "\033[31m[fail]\033[0m   %s: %v\n", name, err)
	} else {
		fmt.Printf("\033[32m[done]\033[0m  %s\n", name)
	}

	if s.Notify != "" && err == nil {
		fmt.Printf("\a\033[33m[notify]\033[0m %s\n", e.expand(s.Notify))
	}

	return &StepResult{Name: s.Name, Status: status, Output: output, Err: err}
}
