package tmuxutil

import "strings"

// SanitizeLine keeps only printable ASCII characters, trims trailing
// whitespace, and truncates to maxLen. Returns empty string if the line
// has no readable ASCII content.
//
// This unifies the two previously duplicated implementations in
// internal/agent/runner.go (100-char cap) and cmd/list.go (80-char cap).
func SanitizeLine(s string, maxLen int) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r < 127 {
			b.WriteRune(r)
		}
	}
	result := strings.TrimRight(b.String(), " \t")
	if maxLen > 0 && len(result) > maxLen {
		result = result[:maxLen]
	}
	return result
}
