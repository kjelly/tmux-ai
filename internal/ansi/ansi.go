// Package ansi provides utilities for stripping ANSI escape sequences
// from terminal output. It is kept in a separate package to avoid
// import cycles between tmux and tmuxutil.
package ansi

import (
	"regexp"
	"strings"
)

// ansiRegex matches the most common ANSI escape sequences:
// - CSI (Control Sequence Introducer): \x1b[...m  (colors, cursor, etc.)
// - OSC (Operating System Command): \x1b]...\x07 or \x1b]...\x1b\\
// - Simple escape sequences: \x1b followed by a single character
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[=>]`)

// Strip removes ANSI escape sequences from a string.
// This is more complete than a CSI-only regex, as it also handles
// OSC sequences (terminal titles, notifications) and single-char escapes.
func Strip(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// NormalizeStream turns the raw byte stream a pane writes into plain text.
//
// Strip is meant for capture-pane output, which tmux has already rendered into
// rows. A live stream is different: a full-screen TUI does not separate rows
// with newlines at all, it moves the cursor. Dropping those sequences would
// weld every painted row into one line and destroy the line structure the
// handoff protocol depends on, so cursor motion becomes a line break here.
//
// The result is not a screen reconstruction — a repainted row appears once per
// paint. It is a superset of what was displayed, which is exactly what a
// marker or receipt-frame search needs.
func NormalizeStream(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			consumed, br, text := escapeSequence(s[i:])
			if br {
				b.WriteByte('\n')
			}
			b.WriteString(text)
			i += consumed
		case c == '\r' || c == '\n':
			// A carriage return rewrites the current row from column 0, so the
			// text that follows is a distinct rendering of that row.
			b.WriteByte('\n')
			i++
		case c == '\t':
			b.WriteByte('\t')
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// escapeSequence reports how many bytes the sequence at the start of s
// occupies, whether it moves the cursor off the current row, and any text it
// stands in for.
func escapeSequence(s string) (consumed int, lineBreak bool, text string) {
	if len(s) < 2 {
		return len(s), false, ""
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				switch s[i] {
				case 'H', 'f', 'A', 'B', 'E', 'F', 'J', 'u', 'd', 'r', 'S', 'T':
					// Absolute or vertical positioning, screen erase, scroll,
					// and cursor restore all end the current row.
					return i + 1, true, ""
				case 'C':
					// Cursor forward is padding: keep the columns apart so two
					// cells do not merge into one word.
					return i + 1, false, " "
				default:
					return i + 1, false, ""
				}
			}
		}
		return len(s), false, ""
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1, false, ""
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2, false, ""
			}
		}
		return len(s), false, ""
	case '7', '8', 'M', 'D', 'E':
		return 2, true, ""
	default:
		return 2, false, ""
	}
}
