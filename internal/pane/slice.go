// Package pane provides utilities for working with tmux pane content.
package pane

import (
	"fmt"
	"strconv"
	"strings"
)

// Slice applies a Python-style slice expression to a slice of lines.
// The total used for negative-index arithmetic is len(lines).
//
// Supported syntax: "[start:end]" — both start and end are optional integers.
//
//	"[-5:]"   last 5 lines
//	"[:-5]"   everything except the last 5
//	"[10:20]" lines at indices 10–19
//	"[:10]"   first 10 lines
//	"[5:]"    from index 5 to end
//	"[:]"     all lines (identity)
func Slice(lines []string, expr string) ([]string, error) {
	start, end, err := ParseSlice(expr, len(lines))
	if err != nil {
		return nil, err
	}
	return lines[start:end], nil
}

// ParseSlice resolves a slice expression against a known total length
// and returns the concrete [start, end) indices.
func ParseSlice(expr string, total int) (start, end int, err error) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "[") || !strings.HasSuffix(expr, "]") {
		return 0, 0, fmt.Errorf("slice 格式錯誤：%q（需為 [start:end]）", expr)
	}
	inner := expr[1 : len(expr)-1]

	colon := strings.Index(inner, ":")
	if colon < 0 {
		return 0, 0, fmt.Errorf("slice 格式錯誤：%q（缺少 :）", expr)
	}

	start, err = parseIndex(inner[:colon], 0, total)
	if err != nil {
		return 0, 0, fmt.Errorf("slice start 錯誤 %q: %w", expr, err)
	}
	end, err = parseIndex(inner[colon+1:], total, total)
	if err != nil {
		return 0, 0, fmt.Errorf("slice end 錯誤 %q: %w", expr, err)
	}

	if start > end {
		start = end
	}
	return start, end, nil
}

// SliceLines extracts and joins lines according to a slice expression.
// Returns the joined text (lines separated by "\n") and the raw line slice.
func SliceLines(content, expr string) (text string, lines []string, err error) {
	all := strings.Split(content, "\n")
	// Remove trailing empty line that strings.Split adds for content ending with \n
	if len(all) > 0 && all[len(all)-1] == "" {
		all = all[:len(all)-1]
	}
	sliced, err := Slice(all, expr)
	if err != nil {
		return "", nil, err
	}
	return strings.Join(sliced, "\n"), sliced, nil
}

// parseIndex converts a slice index string ("", "5", "-3") to a concrete
// 0-based index clamped to [0, total]. Empty string → defaultVal.
func parseIndex(s string, defaultVal, total int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return clamp(defaultVal, 0, total), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("無法解析整數：%q", s)
	}
	if n < 0 {
		n = total + n
	}
	return clamp(n, 0, total), nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
