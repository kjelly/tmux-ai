package pane

import "testing"

func TestFromChange(t *testing.T) {
	cases := []struct {
		name      string
		prev      string
		curr      string
		wantIdx   int
		wantText  string
	}{
		{
			name:    "no change",
			prev:    "a\nb\nc",
			curr:    "a\nb\nc",
			wantIdx: -1,
		},
		{
			name:     "middle line changed",
			prev:     "a\nb\nc",
			curr:     "a\nX\nc",
			wantIdx:  1,
			wantText: "X\nc",
		},
		{
			name:     "lines appended",
			prev:     "a\nb",
			curr:     "a\nb\nc\nd",
			wantIdx:  2,
			wantText: "c\nd",
		},
		{
			name:     "first line changed",
			prev:     "a\nb\nc",
			curr:     "X\nb\nc",
			wantIdx:  0,
			wantText: "X\nb\nc",
		},
		{
			name:     "trailing newlines ignored",
			prev:     "a\nb\n",
			curr:     "a\nb\nc\n",
			wantIdx:  2,
			wantText: "c",
		},
	}

	for _, c := range cases {
		idx, text := FromChange(c.prev, c.curr)
		if idx != c.wantIdx {
			t.Errorf("%s: startIdx got %d, want %d", c.name, idx, c.wantIdx)
		}
		if idx >= 0 && text != c.wantText {
			t.Errorf("%s: text got %q, want %q", c.name, text, c.wantText)
		}
	}
}
