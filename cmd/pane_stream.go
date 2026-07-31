package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/llmore/tmux-ai/internal/ansi"
	"github.com/llmore/tmux-ai/internal/tmux"
)

// paneStream mirrors a pane's output through tmux pipe-pane into a local file.
//
// capture-pane can only ever show what currently fits on the pane and what the
// TUI last painted, which loses two things the handoff protocol cares about: a
// completion that scrolled out of an alternate screen with no scrollback, and
// one that was painted over between two polls. pipe-pane sees every byte tmux
// writes to the pane, and it does so on the tmux host — so it works the same
// whether the agent runs locally, in a container, or over ssh.
//
// The stream is a fallback, never the authority. It is only ever handed to the
// same nonce-bound envelope extractor the pane capture goes through, so it can
// recover a completion that was displayed but cannot invent one.
type paneStream struct {
	paneID, path string
	file         *os.File
	pending      []byte
	lines        []string
	maxLines     int
}

const (
	// paneStreamFileCap bounds the on-disk mirror so an orphaned pipe — one
	// left behind by a SIGKILLed runner — cannot fill the disk. head exits at
	// the cap and tmux drops the pipe with it.
	paneStreamFileCap = 64 << 20
	// paneStreamMaxLines bounds what is kept in memory. Repainting TUIs are
	// verbose, but consecutive duplicate rows collapse on ingest, so this holds
	// far more real output than the number suggests.
	paneStreamMaxLines = 20000
	// paneStreamMaxPending bounds an unterminated read. Only a partial escape
	// sequence is ever withheld, so this is a safety valve, not a buffer.
	paneStreamMaxPending = 64 << 10
)

func newPaneStreamDir() (string, error) {
	return os.MkdirTemp("", "tmux-ai-stream-")
}

// startPaneStream begins mirroring paneID. It refuses to touch a pane that is
// already piped: that pipe belongs to whoever set it up, and pipe-pane has no
// way to add a second consumer.
func startPaneStream(ex tmux.Executor, paneID, dir string) (*paneStream, error) {
	piped, err := ex.Exec("display-message", "-p", "-t", paneID, "#{pane_pipe}")
	if err != nil {
		return nil, fmt.Errorf("check pipe state of %s: %w", paneID, err)
	}
	if strings.TrimSpace(piped) == "1" {
		return nil, fmt.Errorf("pane %s is already piped elsewhere", paneID)
	}
	path := filepath.Join(dir, sanitizePaneFileName(paneID)+".raw")
	if _, err := ex.Exec("pipe-pane", "-o", "-t", paneID, fmt.Sprintf("head -c %d >> %s", paneStreamFileCap, shellQuote(path))); err != nil {
		return nil, fmt.Errorf("start output stream for %s: %w", paneID, err)
	}
	return &paneStream{paneID: paneID, path: path, maxLines: paneStreamMaxLines}, nil
}

func (s *paneStream) stop(ex tmux.Executor) {
	if s == nil {
		return
	}
	if _, err := ex.Exec("pipe-pane", "-t", s.paneID); err != nil {
		fmt.Fprintf(os.Stderr, "warn: stop output stream for %s: %v\n", s.paneID, err)
	}
	if s.file != nil {
		s.file.Close()
		s.file = nil
	}
}

// snapshot returns the normalized stream so far. A false result means nothing
// has been mirrored yet, which is the normal state until the agent writes.
func (s *paneStream) snapshot() (string, bool) {
	if s == nil {
		return "", false
	}
	if err := s.refresh(); err != nil {
		// The mirror is an optional recovery channel: a read failure must not
		// turn a healthy screen-driven handoff into a workflow error.
		fmt.Fprintf(os.Stderr, "warn: read output stream for %s: %v\n", s.paneID, err)
		return "", false
	}
	if len(s.lines) == 0 {
		return "", false
	}
	return strings.Join(s.lines, "\n"), true
}

func (s *paneStream) refresh() error {
	if s.file == nil {
		file, err := os.Open(s.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		s.file = file
	}
	chunk, err := io.ReadAll(s.file)
	if err != nil {
		return err
	}
	if len(chunk) == 0 {
		return nil
	}
	s.pending = append(s.pending, chunk...)
	consume, hold := splitAtIncompleteEscape(s.pending)
	if len(hold) > paneStreamMaxPending {
		consume, hold = s.pending, nil
	}
	s.pending = append([]byte(nil), hold...)
	s.appendLines(ansi.NormalizeStream(string(consume)))
	return nil
}

// appendLines collapses consecutive identical rows. A repainting TUI rewrites
// the same row many times per second; without this the retained window would
// hold seconds of redraw instead of the whole turn.
func (s *paneStream) appendLines(text string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		if n := len(s.lines); n > 0 && s.lines[n-1] == line {
			continue
		}
		s.lines = append(s.lines, line)
	}
	if extra := len(s.lines) - s.maxLines; extra > 0 {
		s.lines = append([]string(nil), s.lines[extra:]...)
	}
}

// splitAtIncompleteEscape withholds a trailing escape sequence that has not
// arrived in full, so a marker is never split across two ingests.
func splitAtIncompleteEscape(data []byte) (consume, hold []byte) {
	const maxEscape = 64
	start := len(data) - maxEscape
	if start < 0 {
		start = 0
	}
	for i := len(data) - 1; i >= start; i-- {
		if data[i] != 0x1b {
			continue
		}
		if _, terminated := escapeTerminated(data[i:]); terminated {
			break
		}
		return data[:i], data[i:]
	}
	return data, nil
}

func escapeTerminated(data []byte) (int, bool) {
	if len(data) < 2 {
		return 0, false
	}
	switch data[1] {
	case '[':
		for i := 2; i < len(data); i++ {
			if data[i] >= 0x40 && data[i] <= 0x7e {
				return i + 1, true
			}
		}
		return 0, false
	case ']':
		for i := 2; i < len(data); i++ {
			if data[i] == 0x07 {
				return i + 1, true
			}
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				return i + 2, true
			}
		}
		return 0, false
	default:
		return 2, true
	}
}

func sanitizePaneFileName(paneID string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, paneID)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
