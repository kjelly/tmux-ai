package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

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

