package workflow

import (
	"context"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

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
