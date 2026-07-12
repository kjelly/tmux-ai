package agent

import (
	"fmt"
	"strings"

	"github.com/llmore/tmux-ai/internal/tmux"
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

	type winKey struct {
		session string
		winIdx  int
	}
	type winInfo struct {
		name         string
		sessionName  string
		winIdx       int
		windowActive bool
		panes        []tmux.PaneInfo
	}
	winOrder := []winKey{}
	winMap := map[winKey]*winInfo{}

	for _, p := range panes {
		k := winKey{p.SessionName, p.WindowIndex}
		if _, ok := winMap[k]; !ok {
			winOrder = append(winOrder, k)
			winMap[k] = &winInfo{
				name:         p.WindowName,
				sessionName:  p.SessionName,
				winIdx:       p.WindowIndex,
				windowActive: p.WindowActive,
			}
		}
		winMap[k].panes = append(winMap[k].panes, p)
	}

	var sb strings.Builder
	for _, k := range winOrder {
		w := winMap[k]
		active := ""
		if w.windowActive {
			active = " (active)"
		}
		target := fmt.Sprintf("%s:%d", w.sessionName, w.winIdx)
		sb.WriteString(fmt.Sprintf("Session %s - Window %d [target: %s]: %s%s\n", w.sessionName, w.winIdx, target, w.name, active))
		for _, p := range w.panes {
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
