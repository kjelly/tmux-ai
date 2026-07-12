package ansi

import "testing"

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
