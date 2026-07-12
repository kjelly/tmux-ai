package tmuxutil

import "github.com/llmore/tmux-ai/internal/tmux"

// WinKey identifies a unique window by session name and window index.
type WinKey struct {
	Session string
	WinIdx  int
}

// WinInfo groups panes belonging to the same window, preserving metadata
// and insertion order.
type WinInfo struct {
	Name         string
	SessionName  string
	WinIdx       int
	WindowActive bool
	Panes        []tmux.PaneInfo
}

// GroupPanesByWindow groups pane info by (session, window) and returns
// the windows in the order they first appear in the input slice.
//
// This eliminates the duplicated grouping logic previously found in
// agent/tmux_state.go and cmd/list.go.
func GroupPanesByWindow(panes []tmux.PaneInfo) ([]WinKey, map[WinKey]WinInfo) {
	order := []WinKey{}
	m := map[WinKey]WinInfo{}
	for _, p := range panes {
		k := WinKey{p.SessionName, p.WindowIndex}
		if _, ok := m[k]; !ok {
			order = append(order, k)
			m[k] = WinInfo{
				Name:         p.WindowName,
				SessionName:  p.SessionName,
				WinIdx:       p.WindowIndex,
				WindowActive: p.WindowActive,
			}
		}
		w := m[k]
		w.Panes = append(w.Panes, p)
		m[k] = w
	}
	return order, m
}

// SessionOrder returns session names in first-appearance order.
func SessionOrder(panes []tmux.PaneInfo) []string {
	var order []string
	seen := map[string]bool{}
	for _, p := range panes {
		if !seen[p.SessionName] {
			order = append(order, p.SessionName)
			seen[p.SessionName] = true
		}
	}
	return order
}
