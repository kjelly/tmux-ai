package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type PaneInfo struct {
	SessionName  string
	WindowName   string
	WindowIndex  int
	PaneIndex    int
	PaneID       string
	Command      string
	PaneActive   bool
	WindowActive bool
}

type Executor struct{}

func NewExecutor() *Executor {
	return &Executor{}
}

func (e *Executor) exec(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w, output: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

func (e *Executor) Exec(args ...string) (string, error) {
	return e.exec(args...)
}

func (e *Executor) CapturePane(paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 100
	}
	out, err := e.exec("capture-pane", "-p", "-t", paneID, "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

func (e *Executor) CapturePaneClean(paneID string, lines int) (string, error) {
	raw, err := e.CapturePane(paneID, lines)
	if err != nil {
		return "", err
	}
	return ansiRegex.ReplaceAllString(raw, ""), nil
}

func (e *Executor) SendKeys(paneID, keys string) error {
	_, err := e.exec("send-keys", "-t", paneID, keys, "Enter")
	return err
}

func (e *Executor) SendLines(paneID, content string) error {
	tmpfile, err := os.CreateTemp("", "tmux-ai-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	if _, err := tmpfile.WriteString(content); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	tmpfile.Close()

	if _, err := e.exec("load-buffer", "-t", paneID, tmpfile.Name()); err != nil {
		return fmt.Errorf("load buffer: %w", err)
	}
	if _, err := e.exec("paste-buffer", "-t", paneID, "-r"); err != nil {
		return fmt.Errorf("paste buffer: %w", err)
	}
	return nil
}

func (e *Executor) NewWindow(name, command, cwd string) (string, error) {
	args := []string{"new-window", "-P", "-F", "#{window_id}"}
	if name != "" {
		args = append(args, "-n", name)
	}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	if command != "" {
		args = append(args, command)
	}
	out, err := e.exec(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (e *Executor) SplitPane(target, direction, command string) error {
	args := []string{"split-window", "-t", target}
	if direction == "horizontal" {
		args = append(args, "-h")
	} else if direction == "vertical" {
		args = append(args, "-v")
	}
	if command != "" {
		args = append(args, command)
	}
	_, err := e.exec(args...)
	return err
}

func (e *Executor) SelectPane(paneID string) error {
	// switch-client moves the user's view; fall back to select-pane for headless use
	if _, err := e.exec("switch-client", "-t", paneID); err == nil {
		return nil
	}
	_, err := e.exec("select-pane", "-t", paneID)
	return err
}

func (e *Executor) SelectWindow(windowID string) error {
	// switch-client navigates the client to the target window (including cross-session).
	// select-window only works within the already-visible session.
	if _, err := e.exec("switch-client", "-t", windowID); err == nil {
		return nil
	}
	_, err := e.exec("select-window", "-t", windowID)
	return err
}

func (e *Executor) KillPane(paneID string) error {
	_, err := e.exec("kill-pane", "-t", paneID)
	return err
}

func (e *Executor) KillWindow(windowID string) error {
	_, err := e.exec("kill-window", "-t", windowID)
	return err
}

func (e *Executor) ListPanes(windowID string) ([]string, error) {
	args := []string{"list-panes", "-a"}
	if windowID != "" {
		args = []string{"list-panes", "-t", windowID}
	}
	out, err := e.exec(args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			parts := strings.Split(line, ":")
			if len(parts) >= 1 {
				result = append(result, parts[0])
			}
		}
	}
	return result, nil
}

func (e *Executor) ListWindows(sessionID string) ([]string, error) {
	args := []string{"list-windows", "-a"}
	if sessionID != "" {
		args = []string{"list-windows", "-t", sessionID}
	}
	out, err := e.exec(args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				result = append(result, parts[0])
			}
		}
	}
	return result, nil
}

// ResolveTarget converts any tmux target (alias, session:window, session:window.pane, %id)
// to a canonical %pane_id by asking tmux directly.
func (e *Executor) ResolveTarget(target string) (string, error) {
	out, err := e.exec("display-message", "-p", "-t", target, "#{pane_id}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve target %q: %w", target, err)
	}
	return strings.TrimSpace(out), nil
}

func (e *Executor) ListPanesDetailed() ([]PaneInfo, error) {
	const sep = "|"
	format := strings.Join([]string{
		"#{session_name}",
		"#{window_name}",
		"#{window_index}",
		"#{pane_index}",
		"#{pane_id}",
		"#{pane_current_command}",
		"#{pane_active}",
		"#{window_active}",
	}, sep)
	out, err := e.exec("list-panes", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	var panes []PaneInfo
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, sep, 8)
		if len(parts) < 8 {
			continue
		}
		winIdx, _ := strconv.Atoi(parts[2])
		paneIdx, _ := strconv.Atoi(parts[3])
		panes = append(panes, PaneInfo{
			SessionName:  parts[0],
			WindowName:   parts[1],
			WindowIndex:  winIdx,
			PaneIndex:    paneIdx,
			PaneID:       parts[4],
			Command:      parts[5],
			PaneActive:   parts[6] == "1",
			WindowActive: parts[7] == "1",
		})
	}
	return panes, nil
}