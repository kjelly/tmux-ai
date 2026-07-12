package workflow

import (
	"fmt"
	"strings"

	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

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
