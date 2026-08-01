package tmux

import "context"

// Executor is the interface for interacting with tmux.
// The concrete implementation in executor.go satisfies this interface.
// Tests can provide a fake implementation to avoid requiring a real tmux server.
type Executor interface {
	CapturePane(paneID string, lines int) (string, error)
	CapturePaneClean(paneID string, lines int) (string, error)
	CaptureScreenClean(paneID string) (string, error)
	SendKeys(paneID, keys string) error
	SendLines(paneID, content string) error
	SendKeysContext(ctx context.Context, paneID, keys string) error
	SendLinesContext(ctx context.Context, paneID, content string) error
	SendTextContext(ctx context.Context, paneID, content string) error
	NewWindow(name, command, cwd string) (string, error)
	SplitPane(target, direction, command string) error
	SelectPane(paneID string) error
	SelectWindow(windowID string) error
	KillPane(paneID string) error
	KillWindow(windowID string) error
	ListPanes(windowID string) ([]string, error)
	ListWindows(sessionID string) ([]string, error)
	ListPanesDetailed() ([]PaneInfo, error)
	ResolveTarget(target string) (string, error)
	Exec(args ...string) (string, error)
}
