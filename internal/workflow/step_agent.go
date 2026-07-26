package workflow

import (
	"context"
	"errors"
	"fmt"
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
		return "", formatAgentStepError(s.Name, stepTimeout, err)
	}
	return r.LastOutput, nil
}

func formatAgentStepError(stepName string, timeoutSeconds int, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("agent step %q timed out after %s while waiting for the model or a tool; increase the step timeout or check model/API availability: %w", stepName, time.Duration(timeoutSeconds)*time.Second, err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("agent step %q was cancelled before completion: %w", stepName, err)
	}
	return err
}
