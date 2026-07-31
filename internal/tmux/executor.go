package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/ansi"
)

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

type executorImpl struct{}

const sendTextChunkSize = 500

var sendTextChunkDelay = time.Second

func NewExecutor() Executor {
	return &executorImpl{}
}

func (e *executorImpl) exec(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w, output: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

func (e *executorImpl) Exec(args ...string) (string, error) {
	return e.exec(args...)
}

func (e *executorImpl) CapturePane(paneID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 100
	}
	// -J rejoins lines the terminal hard-wrapped at the pane width. Without it
	// a receipt marker split across the wrap boundary becomes unmatchable, so
	// a completed turn looks like a missing receipt.
	out, err := e.exec("capture-pane", "-p", "-J", "-t", paneID, "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		return "", err
	}
	return trimLineEnds(strings.TrimRight(out, "\n")), nil
}

// trimLineEnds restores the trailing-space trimming that capture-pane applies
// on its own but -J turns off. Keeping captures free of padding preserves the
// previous relay content and keeps screen-stability comparisons meaningful.
func trimLineEnds(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func (e *executorImpl) CapturePaneClean(paneID string, lines int) (string, error) {
	raw, err := e.CapturePane(paneID, lines)
	if err != nil {
		return "", err
	}
	return ansi.Strip(raw), nil
}

func (e *executorImpl) SendKeys(paneID, keys string) error {
	return e.SendKeysContext(context.Background(), paneID, keys)
}

func (e *executorImpl) SendLines(paneID, content string) error {
	return e.SendLinesContext(context.Background(), paneID, content)
}

func (e *executorImpl) SendKeysContext(ctx context.Context, paneID, keys string) error {
	if err := e.sendTextChunks(ctx, paneID, keys); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := e.exec("send-keys", "-t", paneID, "Enter")
	return err
}

func (e *executorImpl) SendLinesContext(ctx context.Context, paneID, content string) error {
	return e.sendTextChunks(ctx, paneID, content)
}

func (e *executorImpl) SendTextContext(ctx context.Context, paneID, content string) error {
	return e.sendTextChunks(ctx, paneID, content)
}

func (e *executorImpl) sendTextChunks(ctx context.Context, paneID, content string) error {
	return sendMixedTextWith(
		ctx, content, sendTextChunkSize, sendTextChunkDelay,
		func(chunk string) error { return e.pasteTextChunk(paneID, chunk) },
		func(control rune) error {
			_, err := e.exec("send-keys", "-t", paneID, string(control))
			return err
		},
	)
}

func sendTextChunksWith(ctx context.Context, content string, size int, delay time.Duration, send func(string) error) error {
	for _, chunk := range splitTextChunks(content, size) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := send(chunk); err != nil {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func sendMixedTextWith(
	ctx context.Context,
	content string,
	size int,
	delay time.Duration,
	sendText func(string) error,
	sendControl func(rune) error,
) error {
	var text []rune
	flush := func() error {
		if len(text) == 0 {
			return nil
		}
		err := sendTextChunksWith(ctx, string(text), size, delay, sendText)
		text = text[:0]
		return err
	}
	for _, r := range content {
		if !isControlRune(r) {
			text = append(text, r)
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := sendControl(r); err != nil {
			return err
		}
	}
	return flush()
}

func (e *executorImpl) pasteTextChunk(paneID, content string) error {
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
	// Preserve newlines and use bracketed-paste mode when the destination TUI
	// requests it. Agent editors can otherwise treat a large multi-line relay as
	// individually typed lines and swallow the subsequent submit key.
	if _, err := e.exec("paste-buffer", "-t", paneID, "-p", "-r"); err != nil {
		return fmt.Errorf("paste buffer: %w", err)
	}
	return nil
}

func splitTextChunks(content string, size int) []string {
	if content == "" || size <= 0 {
		return nil
	}
	runes := []rune(content)
	chunks := make([]string, 0, (len(runes)+size-1)/size)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func isControlRune(r rune) bool {
	return r < 0x20 && r != '\n' && r != '\r' && r != '\t'
}

func (e *executorImpl) NewWindow(name, command, cwd string) (string, error) {
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

func (e *executorImpl) SplitPane(target, direction, command string) error {
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

func (e *executorImpl) SelectPane(paneID string) error {
	// switch-client moves the user's view; fall back to select-pane for headless use
	if _, err := e.exec("switch-client", "-t", paneID); err == nil {
		return nil
	}
	_, err := e.exec("select-pane", "-t", paneID)
	return err
}

func (e *executorImpl) SelectWindow(windowID string) error {
	// switch-client navigates the client to the target window (including cross-session).
	// select-window only works within the already-visible session.
	if _, err := e.exec("switch-client", "-t", windowID); err == nil {
		return nil
	}
	_, err := e.exec("select-window", "-t", windowID)
	return err
}

func (e *executorImpl) KillPane(paneID string) error {
	_, err := e.exec("kill-pane", "-t", paneID)
	return err
}

func (e *executorImpl) KillWindow(windowID string) error {
	_, err := e.exec("kill-window", "-t", windowID)
	return err
}

func (e *executorImpl) ListPanes(windowID string) ([]string, error) {
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

func (e *executorImpl) ListWindows(sessionID string) ([]string, error) {
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
func (e *executorImpl) ResolveTarget(target string) (string, error) {
	out, err := e.exec("display-message", "-p", "-t", target, "#{pane_id}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve target %q: %w", target, err)
	}
	return strings.TrimSpace(out), nil
}

func (e *executorImpl) ListPanesDetailed() ([]PaneInfo, error) {
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
