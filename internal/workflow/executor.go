package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/pane"
	"github.com/llmore/tmux-ai/internal/timeparser"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
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
				continue
			}

			if !depsOK {
				continue
			}

			running[s.Name] = true
			go func(step *Step) {
				res := e.runStep(ctx, step)
				e.mu.Lock()
				e.results[step.Name] = res
				e.mu.Unlock()
				resultCh <- res
			}(s)
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

func (e *Executor) runAgentStep(ctx context.Context, s *Step) (string, error) {

	target := e.resolvePane(s, tmuxutil.NewResolver())
	extraCtx := e.buildExtraContext(s)
	instruction := e.expand(s.Instruction)

	stepTimeout := e.timeout
	if s.Timeout > 0 {
		stepTimeout = s.Timeout
	}
	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(stepTimeout)*time.Second)
	defer cancel()

	r := agent.NewRunner(e.ollamaHost, e.model, e.maxIterations, e.verbose)
	r.TargetPane = target
	r.ExtraContext = extraCtx

	if err := r.Run(stepCtx, instruction); err != nil {
		return "", err
	}
	return r.LastOutput, nil
}

func (e *Executor) runWatchStep(ctx context.Context, s *Step) (string, error) {
	ex := tmux.NewExecutor()

	paneID := e.resolvePane(s, tmuxutil.NewResolver())
	pattern := e.expand(s.Pattern)
	rangeExpr := e.expand(s.Range)
	lines := s.Lines
	if lines == 0 {
		lines = 50
	}
	interval := time.Duration(s.Interval) * time.Millisecond
	if interval == 0 {
		interval = 2 * time.Second
	}
	stepTimeout := e.timeout
	if s.Timeout > 0 {
		stepTimeout = s.Timeout
	}
	deadline := time.Now().Add(time.Duration(stepTimeout) * time.Second)

	var re *regexp.Regexp
	if pattern != "" {
		re, _ = regexp.Compile(pattern)
	}

	// For range-only mode (no pattern): track previous slice content and trigger on change.
	prevSlice := ""
	firstPoll := true

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}

		if time.Now().After(deadline) {
			verb := "for pattern"
			if s.Absent {
				verb = "for pattern to disappear"
			}
			if rangeExpr != "" && pattern == "" {
				verb = "for change in range"
			}
			return "", fmt.Errorf("timeout waiting %s %q in pane %s", verb, pattern, paneID)
		}

		content, err := ex.CapturePaneClean(paneID, lines)
		if err != nil {
			continue
		}

		// [~:] — dynamic "from first changed line" range.
		if pane.IsChangeRange(rangeExpr) {
			if firstPoll {
				prevSlice = content
				firstPoll = false
				continue
			}
			startIdx, changed := pane.FromChange(prevSlice, content)
			prevSlice = content
			if startIdx < 0 {
				continue
			}
			return changed, nil
		}

		// Apply static range slice if specified.
		target := content
		if rangeExpr != "" {
			sliced, _, sliceErr := pane.SliceLines(content, rangeExpr)
			if sliceErr != nil {
				return "", fmt.Errorf("range %q: %w", rangeExpr, sliceErr)
			}
			target = sliced
		}

		// Range-only mode: trigger when the slice content changes.
		if rangeExpr != "" && pattern == "" {
			if firstPoll {
				prevSlice = target
				firstPoll = false
				continue
			}
			if target != prevSlice {
				return target, nil
			}
			continue
		}

		// Pattern mode (with optional range): check pattern presence.
		found := strings.Contains(target, pattern) || (re != nil && re.MatchString(target))
		if found != s.Absent {
			return target, nil
		}
	}
}

func (e *Executor) runShellStep(ctx context.Context, s *Step) (string, error) {
	command := e.expand(s.Command)
	stepCtx := ctx
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, time.Duration(s.Timeout)*time.Second)
		defer cancel()
	}
	out, err := exec.CommandContext(stepCtx, "bash", "-c", command).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w\noutput: %s", err, string(out))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// runLaunchStep opens a new tmux window, runs a program, waits for a ready
// pattern, then sends an initial input — all without an LLM agent.
// Fields used: Window (name), Cwd, Command, Pattern (ready signal), Timeout, Input.
func (e *Executor) runLaunchStep(ctx context.Context, s *Step) (string, error) {
	ex := tmux.NewExecutor()

	windowName := e.expand(s.Window)
	cwd := e.expand(s.Cwd)
	command := e.expand(s.Command)
	pattern := e.expand(s.Pattern)
	input := e.expand(s.Input)

	timeout := s.Timeout
	if timeout == 0 {
		timeout = 30
	}

	// Create a new window with the given working directory (no startup command —
	// opens a clean interactive shell so the user can take over later).
	windowID, err := ex.NewWindow(windowName, "", cwd)
	if err != nil {
		return "", fmt.Errorf("create window: %w", err)
	}

	// Grab the pane that was just created inside the new window.
	panes, err := ex.ListPanes(windowID)
	if err != nil || len(panes) == 0 {
		return "", fmt.Errorf("list panes in window %s: %w", windowID, err)
	}
	paneID := panes[0]

	// Type the startup command (e.g. "claude").
	if command != "" {
		if err := ex.SendKeys(paneID, command+"\n"); err != nil {
			return "", fmt.Errorf("send command: %w", err)
		}
	}

	// Poll until the ready pattern appears or the timeout expires.
	if pattern != "" {
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-ticker.C:
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout waiting for %q in window %q", pattern, windowName)
			}
			content, err := ex.CapturePaneClean(paneID, 50)
			if err == nil && strings.Contains(content, pattern) {
				break
			}
		}
	}

	// Send the initial input to the program (e.g. "/review <url>").
	if input != "" {
		if err := ex.SendKeys(paneID, input+"\n"); err != nil {
			return "", fmt.Errorf("send input: %w", err)
		}
	}

	return fmt.Sprintf("launched %q in window %q (pane %s)", command, windowName, paneID), nil
}

// runWaitStep blocks until the natural language time expression in s.Until is reached.
func (e *Executor) runWaitStep(_ context.Context, s *Step) (string, error) {
	untilExpr := e.expand(s.Until)
	if untilExpr == "" {
		return "", fmt.Errorf("wait step %q: 'until' field is required", s.Name)
	}

	target, err := timeparser.Parse(untilExpr)
	if err != nil {
		return "", fmt.Errorf("wait step %q: %w", s.Name, err)
	}

	wait := time.Until(target)
	if wait <= 0 {
		return fmt.Sprintf("時間已過（%s），跳過等待", target.Format("15:04:05")), nil
	}

	fmt.Printf("\033[36m[wait]\033[0m  等待至 %s\n", timeparser.Format(target))

	timer := time.NewTimer(wait)
	defer timer.Stop()
	<-timer.C

	return fmt.Sprintf("等待完成（%s）", target.Format("15:04:05")), nil
}

func (e *Executor) runRelayStep(ctx context.Context, s *Step) (string, error) {
	resolver := tmuxutil.NewResolver()
	ex := tmux.NewExecutor()

	fromRaw := e.expand(s.From)
	toRaw := e.expand(s.To)
	from := resolver.ResolvePane(fromRaw)
	to := resolver.ResolvePane(toRaw)

	lines := s.Lines
	if lines == 0 {
		lines = 80
	}
	content, err := ex.CapturePaneClean(from, lines)
	if err != nil {
		return "", fmt.Errorf("capture from pane %s: %w", from, err)
	}

	instruction := e.expand(s.Instruction)
	extraCtx := content

	// prepend any context_from outputs
	if cf := e.buildExtraContext(s); cf != "" {
		extraCtx = cf + "\n\n" + extraCtx
	}

	stepTimeout := e.timeout
	if s.Timeout > 0 {
		stepTimeout = s.Timeout
	}
	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(stepTimeout)*time.Second)
	defer cancel()

	r := agent.NewRunner(e.ollamaHost, e.model, e.maxIterations, e.verbose)
	r.TargetPane = to
	r.ExtraContext = extraCtx

	if err := r.Run(stepCtx, instruction); err != nil {
		return "", err
	}
	return r.LastOutput, nil
}

func (e *Executor) runBroadcastStep(ctx context.Context, s *Step) (string, error) {
	ex := tmux.NewExecutor()
	broadcastResolver := tmuxutil.NewResolver()

	allPanes, err := ex.ListPanesDetailed()
	if err != nil {
		return "", err
	}
	paneByID := map[string]tmux.PaneInfo{}
	for _, p := range allPanes {
		paneByID[p.PaneID] = p
	}

	seen := map[string]bool{}
	var targets []tmux.PaneInfo

	add := func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if p, ok := paneByID[id]; ok {
			targets = append(targets, p)
		}
	}

	if s.Panes != "" {
		for _, part := range strings.Split(e.expand(s.Panes), ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				add(broadcastResolver.ResolvePane(part))
			}
		}
	}
	if s.Session != "" {
		for _, p := range allPanes {
			if p.SessionName == e.expand(s.Session) {
				add(p.PaneID)
			}
		}
	}
	if s.Process != "" {
		proc := e.expand(s.Process)
		for _, p := range allPanes {
			if strings.Contains(p.Command, proc) {
				add(p.PaneID)
			}
		}
	}

	if len(targets) == 0 {
		return "", fmt.Errorf("no matching panes for broadcast step %q", s.Name)
	}

	instruction := e.expand(s.Instruction)
	extraCtx := e.buildExtraContext(s)

	stepTimeout := e.timeout
	if s.Timeout > 0 {
		stepTimeout = s.Timeout
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var outputs []string
	var firstErr error

	for _, t := range targets {
		wg.Add(1)
		go func(pane tmux.PaneInfo) {
			defer wg.Done()
			stepCtx, cancel := context.WithTimeout(ctx, time.Duration(stepTimeout)*time.Second)
			defer cancel()

			r := agent.NewRunner(e.ollamaHost, e.model, e.maxIterations, e.verbose)
			r.TargetPane = pane.PaneID
			r.ExtraContext = extraCtx

			err := r.Run(stepCtx, instruction)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if r.LastOutput != "" {
				outputs = append(outputs, fmt.Sprintf("[%s] %s", pane.PaneID, r.LastOutput))
			}
		}(t)
	}
	wg.Wait()
	return strings.Join(outputs, "\n"), firstErr
}

// resolvePane returns the canonical %pane_id for a step's pane/session/window fields.
func (e *Executor) resolvePane(s *Step, resolver *tmuxutil.Resolver) string {
	pane := e.expand(s.Pane)
	session := e.expand(s.Session)
	window := e.expand(s.Window)
	return resolver.ResolvePaneFromFlags(pane, session, window)
}

// buildExtraContext gathers context_from step outputs and joins them.
func (e *Executor) buildExtraContext(s *Step) string {
	if s.ContextFrom == nil {
		return ""
	}
	var names []string
	switch v := s.ContextFrom.(type) {
	case string:
		names = []string{v}
	case []interface{}:
		for _, item := range v {
			if str, ok := item.(string); ok {
				names = append(names, str)
			}
		}
	}

	var parts []string
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, name := range names {
		if r, ok := e.results[name]; ok && r.Output != "" {
			parts = append(parts, fmt.Sprintf("--- output from step %q ---\n%s", name, r.Output))
		}
	}
	return strings.Join(parts, "\n\n")
}

// expand substitutes {{params.key}} and {{steps.NAME.output}} in a string.
func (e *Executor) expand(s string) string {
	for k, v := range e.params {
		s = strings.ReplaceAll(s, "{{params."+k+"}}", v)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for name, result := range e.results {
		if result != nil {
			s = strings.ReplaceAll(s, "{{steps."+name+".output}}", strings.TrimSpace(result.Output))
		}
	}
	return s
}

func (e *Executor) findStep(name string) *Step {
	for i := range e.wf.Steps {
		if e.wf.Steps[i].Name == name {
			return &e.wf.Steps[i]
		}
	}
	return nil
}
