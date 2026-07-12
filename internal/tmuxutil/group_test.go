package tmuxutil

import (
	"testing"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func TestGroupPanesByWindow(t *testing.T) {
	panes := []tmux.PaneInfo{
		{SessionName: "sport", WindowIndex: 1, WindowName: "crush", PaneID: "%24"},
		{SessionName: "sport", WindowIndex: 1, WindowName: "crush", PaneID: "%25"},
		{SessionName: "sport", WindowIndex: 2, WindowName: "vim", PaneID: "%26"},
		{SessionName: "github", WindowIndex: 3, WindowName: "go", PaneID: "%43"},
	}

	order, m := GroupPanesByWindow(panes)
	if len(order) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(order))
	}
	if order[0] != (WinKey{"sport", 1}) {
		t.Errorf("first window: got %v", order[0])
	}
	if len(m[WinKey{"sport", 1}].Panes) != 2 {
		t.Errorf("sport:1 should have 2 panes")
	}
	if m[WinKey{"github", 3}].Name != "go" {
		t.Errorf("github:3 name: got %q", m[WinKey{"github", 3}].Name)
	}
}

func TestSessionOrder(t *testing.T) {
	panes := []tmux.PaneInfo{
		{SessionName: "github", WindowIndex: 1, PaneID: "%1"},
		{SessionName: "sport", WindowIndex: 1, PaneID: "%2"},
		{SessionName: "github", WindowIndex: 2, PaneID: "%3"},
	}
	order := SessionOrder(panes)
	if len(order) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(order))
	}
	if order[0] != "github" || order[1] != "sport" {
		t.Errorf("got %v, want [github sport]", order)
	}
}

func TestGroupPanesByWindowEmpty(t *testing.T) {
	order, m := GroupPanesByWindow(nil)
	if len(order) != 0 || len(m) != 0 {
		t.Error("expected empty results for nil input")
	}
}
