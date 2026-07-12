package pane

import "strings"

// IsChangeRange reports whether expr is the special "from-change" sentinel "[~:]".
// When used in watch, the system snapshots the pane content and, on each poll,
// slices from the first line that differs relative to the snapshot.
func IsChangeRange(expr string) bool {
	return strings.TrimSpace(expr) == "[~:]"
}

// FromChange compares prev and curr line-by-line and returns the index of the
// first differing line together with the text from that line to the end of curr.
//
// Returns (startIdx, text):
//   - startIdx >= 0  → change found; text is curr[startIdx:]
//   - startIdx == -1 → no change
//
// If curr has more lines than prev, the appended lines count as a change
// starting at len(prevLines).
func FromChange(prev, curr string) (startIdx int, text string) {
	prevLines := splitLines(prev)
	currLines := splitLines(curr)

	minLen := len(prevLines)
	if len(currLines) < minLen {
		minLen = len(currLines)
	}

	for i := 0; i < minLen; i++ {
		if prevLines[i] != currLines[i] {
			return i, strings.Join(currLines[i:], "\n")
		}
	}

	// curr grew: new lines appended
	if len(currLines) > len(prevLines) {
		start := len(prevLines)
		return start, strings.Join(currLines[start:], "\n")
	}

	return -1, ""
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	// Drop trailing empty string from a newline-terminated input.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
