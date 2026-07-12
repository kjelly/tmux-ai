package agent

import (
	"fmt"
	"strings"

	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

// getTmuxState captures the current tmux environment (sessions, windows,
// panes, and a few lines of each pane's content) and formats it as a
// human/LLM-readable string.
func (r *Runner) getTmuxState() (string, error) {
	panes, err := r.tmux.ListPanesDetailed()
	if err != nil {
		return "", err
	}
	if len(panes) == 0 {
		return "No tmux panes found.\n", nil
	}

	order, winMap := tmuxutil.GroupPanesByWindow(panes)

	var sb strings.Builder
	for _, k := range order {
		w := winMap[k]
		active := ""
		if w.WindowActive {
			active = " (active)"
		}
		target := fmt.Sprintf("%s:%d", w.SessionName, w.WinIdx)
		sb.WriteString(fmt.Sprintf("Session %s - Window %d [target: %s]: %s%s\n", w.SessionName, w.WinIdx, target, w.Name, active))
		for _, p := range w.Panes {
			pActive := ""
			if p.PaneActive {
				pActive = " *"
			}
			sb.WriteString(fmt.Sprintf("  Pane %s (index %d)%s - running: %s\n", p.PaneID, p.PaneIndex, pActive, p.Command))
			if content, err := r.tmux.CapturePaneClean(p.PaneID, 5); err == nil {
				lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
				shown := 0
				for i := len(lines) - 1; i >= 0 && shown < 3; i-- {
					clean := tmuxutil.SanitizeLine(lines[i], 100)
					if clean != "" {
						sb.WriteString(fmt.Sprintf("    | %s\n", clean))
						shown++
					}
				}
			}
		}
	}
	return sb.String(), nil
}
