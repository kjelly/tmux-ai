package ansi

import (
	"strings"
	"testing"
)

func TestStrip(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"CSI color", "\x1b[31mred\x1b[0m text", "red text"},
		{"CSI 256-color", "\x1b[38;5;196mred\x1b[0m", "red"},
		{"OSC title", "\x1b]0;Window Title\x07rest", "rest"},
		{"OSC with ST", "\x1b]0;Title\x1b\\rest", "rest"},
		{"cursor movement", "\x1b[2J\x1b[Hclean", "clean"},
		{"empty", "", ""},
		{"only escapes", "\x1b[31m\x1b[0m", ""},
		{"mixed", "\x1b[1;32m\x1b]0;T\x07ok\x1b[0m", "ok"},
	}
	for _, c := range cases {
		got := Strip(c.input)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNormalizeStreamSplitsRepaintedRows(t *testing.T) {
	// A full-screen TUI separates rows with cursor motion, not newlines.
	raw := "\x1b[2J\x1b[1;1H\x1b[32mDONE_MARKER\x1b[0m\x1b[2;1Hnext row\rrewritten row"
	got := NormalizeStream(raw)
	want := []string{"DONE_MARKER", "next row", "rewritten row"}
	lines := strings.Split(got, "\n")
	for _, w := range want {
		found := false
		for _, line := range lines {
			if line == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("NormalizeStream() = %q, missing standalone line %q", got, w)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("NormalizeStream() left escape bytes: %q", got)
	}
}

func TestNormalizeStreamKeepsColumnsApartAndDropsOSC(t *testing.T) {
	got := NormalizeStream("left\x1b[4Cright\x1b]0;window title\x07 end")
	if got != "left right end" {
		t.Fatalf("NormalizeStream() = %q", got)
	}
}
