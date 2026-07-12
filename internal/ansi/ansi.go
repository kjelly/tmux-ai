// Package ansi provides utilities for stripping ANSI escape sequences
// from terminal output. It is kept in a separate package to avoid
// import cycles between tmux and tmuxutil.
package ansi

import "regexp"

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
