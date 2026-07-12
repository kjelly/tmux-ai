// Package tmuxutil provides shared helpers for tmux target resolution,
// line sanitization, and pane idle detection.
//
// These utilities were previously duplicated across cmd/ and internal/
// packages. Centralizing them ensures consistent behavior and makes
// future changes a single-file edit.
package tmuxutil

import (
	"github.com/llmore/tmux-ai/internal/config"
	"github.com/llmore/tmux-ai/internal/tmux"
)

// TmuxResolver is the subset of tmux.Executor needed for target resolution.
type TmuxResolver interface {
	ResolveTarget(target string) (string, error)
}

// Resolver unifies tmux target resolution (alias → %pane_id) across all
// packages. It replaces the duplicated resolveToPane (cmd/run.go),
// resolveTarget (workflow/executor.go), and resolveScheduleTarget
// (cmd/schedule.go).
type Resolver struct {
	Aliases config.Aliases
	Tmux    TmuxResolver
}

// NewResolver creates a Resolver with loaded aliases and a real tmux executor.
func NewResolver() *Resolver {
	aliases, _ := config.LoadAliases()
	return &Resolver{
		Aliases: aliases,
		Tmux:    tmux.NewExecutor(),
	}
}

// NewResolverWith creates a Resolver with explicit dependencies (for testing).
func NewResolverWith(aliases config.Aliases, tmux TmuxResolver) *Resolver {
	return &Resolver{Aliases: aliases, Tmux: tmux}
}

// ResolvePane resolves a single tmux target string to a canonical %pane_id.
// Priority: alias lookup → tmux display-message → original string fallback.
func (r *Resolver) ResolvePane(raw string) string {
	if raw == "" {
		return ""
	}
	resolved := r.Aliases.Resolve(raw)
	if id, err := r.Tmux.ResolveTarget(resolved); err == nil {
		return id
	}
	return resolved
}

// ResolvePaneFromFlags resolves a pane target from the standard
// --pane / --session / --window flag combination.
//
// Priority: --pane > --session + --window > --session alone > empty.
func (r *Resolver) ResolvePaneFromFlags(pane, session, window string) string {
	if pane != "" {
		return r.ResolvePane(pane)
	}
	if session != "" {
		if window != "" {
			return r.ResolvePane(session + ":" + window)
		}
		return r.ResolvePane(session + ":")
	}
	return ""
}
