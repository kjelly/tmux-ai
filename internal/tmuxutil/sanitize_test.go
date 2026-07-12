package tmuxutil

import "testing"

func TestSanitizeLine(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"basic", "hello world", 100, "hello world"},
		{"strip control chars", "hello\x1b[31m world\x1b[0m", 100, "hello[31m world[0m"},
		{"trim trailing", "hello   ", 100, "hello"},
		{"truncate", "abcdefghij", 5, "abcde"},
		{"empty", "", 100, ""},
		{"only control chars", "\x1b\x00\x07", 100, ""},
		{"unicode filtered", "héllo", 100, "hllo"},
	}
	for _, c := range cases {
		got := SanitizeLine(c.input, c.maxLen)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsIdle(t *testing.T) {
	if !IsIdle("bash") {
		t.Error("bash should be idle")
	}
	if !IsIdle("zsh") {
		t.Error("zsh should be idle")
	}
	if IsIdle("python") {
		t.Error("python should not be idle")
	}
	if IsIdle("vim") {
		t.Error("vim should not be idle")
	}
}
