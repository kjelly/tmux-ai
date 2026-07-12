package pane

import (
	"strings"
	"testing"
)

func TestParseSlice(t *testing.T) {
	total := 100
	cases := []struct {
		expr       string
		wantStart  int
		wantEnd    int
		wantErr    bool
	}{
		{"[-5:]", 95, 100, false},
		{"[:-5]", 0, 95, false},
		{"[10:20]", 10, 20, false},
		{"[:10]", 0, 10, false},
		{"[5:]", 5, 100, false},
		{"[:]", 0, 100, false},
		{"[-5:-2]", 95, 98, false},
		// clamp beyond total
		{"[90:200]", 90, 100, false},
		// errors
		{"5:", 0, 0, true},
		{"[abc:]", 0, 0, true},
	}

	for _, c := range cases {
		s, e, err := ParseSlice(c.expr, total)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got none", c.expr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.expr, err)
			continue
		}
		if s != c.wantStart || e != c.wantEnd {
			t.Errorf("%q: got [%d:%d], want [%d:%d]", c.expr, s, e, c.wantStart, c.wantEnd)
		}
	}
}

func TestSliceLines(t *testing.T) {
	content := strings.Join([]string{"a", "b", "c", "d", "e"}, "\n")
	text, lines, err := SliceLines(content, "[-3:]")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || text != "c\nd\ne" {
		t.Errorf("got %q (%d lines), want \"c\\nd\\ne\" (3 lines)", text, len(lines))
	}
}
