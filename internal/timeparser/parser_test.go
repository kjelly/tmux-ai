package timeparser

import (
	"testing"
	"time"
)

func TestParseRelative(t *testing.T) {
	now := time.Date(2026, 6, 11, 14, 0, 0, 0, time.Local)

	cases := []struct {
		expr string
		want time.Duration
	}{
		{"10分鐘後", 10 * time.Minute},
		{"10分後", 10 * time.Minute},
		{"2小時後", 2 * time.Hour},
		{"2小時", 2 * time.Hour},
		{"30秒後", 30 * time.Second},
		{"半小時後", 30 * time.Minute},
		{"in 10 minutes", 10 * time.Minute},
		{"in 2 hours", 2 * time.Hour},
		{"in 30 seconds", 30 * time.Second},
		{"十分鐘後", 10 * time.Minute},
		{"二十分鐘後", 20 * time.Minute},
	}

	for _, c := range cases {
		got, err := ParseFrom(c.expr, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.expr, err)
			continue
		}
		diff := got.Sub(now)
		if diff != c.want {
			t.Errorf("%q: got %v, want %v", c.expr, diff, c.want)
		}
	}
}

func TestParseAbsolute(t *testing.T) {
	now := time.Date(2026, 6, 11, 14, 0, 0, 0, time.Local) // 14:00 today

	cases := []struct {
		expr    string
		wantH   int
		wantM   int
		wantDay int // day of month
	}{
		{"今晚七點", 19, 0, 11},
		{"今晚7點", 19, 0, 11},
		{"晚上八點", 20, 0, 11},
		{"下午三點", 15, 0, 11},
		{"下午三點半", 15, 30, 11},
		{"早上九點", 9, 0, 11},
		{"明天早上九點", 9, 0, 12},
		{"明天晚上七點", 19, 0, 12},
		{"明天下午三點", 15, 0, 12},
		{"at 9pm", 21, 0, 11},
		{"at 9am", 9, 0, 12}, // 9am already passed (it's 14:00) → tomorrow
		{"tonight at 7", 19, 0, 11},
		{"tomorrow at 9am", 9, 0, 12},
	}

	for _, c := range cases {
		got, err := ParseFrom(c.expr, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.expr, err)
			continue
		}
		if got.Hour() != c.wantH || got.Minute() != c.wantM || got.Day() != c.wantDay {
			t.Errorf("%q: got %02d:%02d day=%d, want %02d:%02d day=%d",
				c.expr, got.Hour(), got.Minute(), got.Day(),
				c.wantH, c.wantM, c.wantDay)
		}
	}
}
