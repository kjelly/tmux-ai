package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

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
