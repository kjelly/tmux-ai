package tmux

import "context"

// FakeExecutor is a test double for the Executor interface.
// It records all calls and returns configurable responses.
type FakeExecutor struct {
	Captures          map[string]string // paneID → captured content
	CaptureCalls      []captureCall
	SendKeysCalls     []sendKeysCall
	SendLinesCalls    []sendLinesCall
	SendLinesCalled   chan struct{}
	NewWindowCalls    []newWindowCall
	NewWindowResult   string
	ResolveResult     map[string]string // target → resolved pane ID
	ListPanesResult   []string
	ListWindowsResult []string
	PanesDetailed     []PaneInfo
	ExecResult        map[string]string // joined args → result
	ExecError         error
	CaptureError      error
	ExecCalls         [][]string
}

type captureCall struct {
	PaneID string
	Lines  int
}
type sendKeysCall struct {
	PaneID string
	Keys   string
}
type sendLinesCall struct {
	PaneID  string
	Content string
}
type newWindowCall struct {
	Name    string
	Command string
	Cwd     string
}

func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{
		Captures:      map[string]string{},
		ResolveResult: map[string]string{},
		ExecResult:    map[string]string{},
	}
}

func (f *FakeExecutor) CapturePane(paneID string, lines int) (string, error) {
	f.CaptureCalls = append(f.CaptureCalls, captureCall{paneID, lines})
	if f.CaptureError != nil {
		return "", f.CaptureError
	}
	return f.Captures[paneID], nil
}

func (f *FakeExecutor) CapturePaneClean(paneID string, lines int) (string, error) {
	f.CaptureCalls = append(f.CaptureCalls, captureCall{paneID, lines})
	if f.CaptureError != nil {
		return "", f.CaptureError
	}
	return f.Captures[paneID], nil
}

func (f *FakeExecutor) SendKeys(paneID, keys string) error {
	f.SendKeysCalls = append(f.SendKeysCalls, sendKeysCall{paneID, keys})
	return nil
}

func (f *FakeExecutor) SendLines(paneID, content string) error {
	f.SendLinesCalls = append(f.SendLinesCalls, sendLinesCall{paneID, content})
	if f.SendLinesCalled != nil {
		select {
		case f.SendLinesCalled <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *FakeExecutor) SendKeysContext(ctx context.Context, paneID, keys string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.SendKeys(paneID, keys)
}

func (f *FakeExecutor) SendLinesContext(ctx context.Context, paneID, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.SendLines(paneID, content)
}

func (f *FakeExecutor) SendTextContext(ctx context.Context, paneID, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.SendLines(paneID, content)
}

func (f *FakeExecutor) NewWindow(name, command, cwd string) (string, error) {
	f.NewWindowCalls = append(f.NewWindowCalls, newWindowCall{name, command, cwd})
	if f.NewWindowResult != "" {
		return f.NewWindowResult, nil
	}
	return "@1", nil
}

func (f *FakeExecutor) SplitPane(target, direction, command string) error {
	return nil
}

func (f *FakeExecutor) SelectPane(paneID string) error     { return nil }
func (f *FakeExecutor) SelectWindow(windowID string) error { return nil }
func (f *FakeExecutor) KillPane(paneID string) error       { return nil }
func (f *FakeExecutor) KillWindow(windowID string) error   { return nil }

func (f *FakeExecutor) ListPanes(windowID string) ([]string, error) {
	return f.ListPanesResult, nil
}

func (f *FakeExecutor) ListWindows(sessionID string) ([]string, error) {
	return f.ListWindowsResult, nil
}

func (f *FakeExecutor) ListPanesDetailed() ([]PaneInfo, error) {
	return f.PanesDetailed, nil
}

func (f *FakeExecutor) ResolveTarget(target string) (string, error) {
	if id, ok := f.ResolveResult[target]; ok {
		return id, nil
	}
	return target, nil
}

func (f *FakeExecutor) Exec(args ...string) (string, error) {
	f.ExecCalls = append(f.ExecCalls, append([]string(nil), args...))
	key := joinArgs(args)
	if f.ExecError != nil {
		return "", f.ExecError
	}
	if r, ok := f.ExecResult[key]; ok {
		return r, nil
	}
	return "", nil
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}
