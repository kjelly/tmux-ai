package cmd

import (
	"context"
	"time"

	"github.com/llmore/tmux-ai/internal/agent"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

// newRunner creates an agent.Runner pre-configured with the global flags.
func newRunner() *agent.Runner {
	return agent.NewRunner(ollamaHost, model, maxIterations, verbose)
}

// newResolver creates a tmuxutil.Resolver with loaded aliases.
func newResolver() *tmuxutil.Resolver {
	return tmuxutil.NewResolver()
}

// runWithTimeout wraps fn with the global timeout context.
func runWithTimeout(fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	return fn(ctx)
}
