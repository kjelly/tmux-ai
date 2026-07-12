// Package timeparser parses natural language time expressions in Chinese and English.
// Examples: "10分鐘後", "今晚七點", "明天早上九點", "in 10 minutes", "at 7pm"
package timeparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Parse converts a natural language time expression to time.Time.
func Parse(expr string) (time.Time, error) {
	return ParseFrom(expr, time.Now())
}

// ParseFrom is like Parse but uses `now` as the reference (useful for tests).
func ParseFrom(expr string, now time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return time.Time{}, fmt.Errorf("empty time expression")
	}

	if d, ok := parseRelative(expr); ok {
		return now.Add(d), nil
	}

	if t, ok := parseAbsolute(expr, now); ok {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("無法解析時間：%q\n支援格式：10分鐘後、2小時後、今晚七點、明天早上九點、下午三點、in 10 minutes、at 9am", expr)
}

// Format returns a human-readable description of a future time relative to now.
func Format(t time.Time) string {
	now := time.Now()
	d := t.Sub(now)
	if d < 0 {
		return t.Format("2006-01-02 15:04:05")
	}
	if d < time.Minute {
		return fmt.Sprintf("%d 秒後（%s）", int(d.Seconds()), t.Format("15:04:05"))
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0f 分鐘後（%s）", d.Minutes(), t.Format("15:04"))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1f 小時後（%s）", d.Hours(), t.Format("15:04"))
	}
	return fmt.Sprintf("%s %s", t.Format("01/02"), t.Format("15:04"))
}

// ─── Chinese number parser ────────────────────────────────────────────────────

var chineseDigitMap = map[rune]int{
	'零': 0, '〇': 0,
	'一': 1,
	'二': 2, '兩': 2,
	'三': 3,
	'四': 4,
	'五': 5,
	'六': 6,
	'七': 7,
	'八': 8,
	'九': 9,
}

// parseNum converts Arabic or Chinese number strings (0–99) to int.
func parseNum(s string) (int, bool) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, true
	}
	runes := []rune(s)
	switch len(runes) {
	case 1:
		if v, ok := chineseDigitMap[runes[0]]; ok {
			return v, true
		}
		if runes[0] == '十' {
			return 10, true
		}
	case 2:
		// 十X → 10+X  (e.g. 十五=15)
		if runes[0] == '十' {
			if v, ok := chineseDigitMap[runes[1]]; ok {
				return 10 + v, true
			}
		}
		// X十 → X*10  (e.g. 二十=20)
		if runes[1] == '十' {
			if v, ok := chineseDigitMap[runes[0]]; ok {
				return v * 10, true
			}
		}
	case 3:
		// X十Y → X*10+Y  (e.g. 二十三=23)
		if runes[1] == '十' {
			v1, ok1 := chineseDigitMap[runes[0]]
			v2, ok2 := chineseDigitMap[runes[2]]
			if ok1 && ok2 {
				return v1*10 + v2, true
			}
		}
	}
	return 0, false
}

// ─── Relative duration ────────────────────────────────────────────────────────

// numPart matches a leading Chinese or Arabic number in a string.
var numPart = regexp.MustCompile(`^(\d+|[零一二三四五六七八九十兩]+)`)

type relativeRule struct {
	suffix string
	unit   time.Duration
}

var relativeRules = []relativeRule{
	{"分鐘後", time.Minute},
	{"分钟后", time.Minute}, // simplified Chinese
	{"分後", time.Minute},
	{"分后", time.Minute},
	{"小時後", time.Hour},
	{"小时后", time.Hour},
	{"小時", time.Hour},
	{"小时", time.Hour},
	{"秒後", time.Second},
	{"秒后", time.Second},
	{"秒", time.Second},
}

var relativeEnRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^in\s+(\d+)\s+minutes?$`),
	regexp.MustCompile(`(?i)^in\s+(\d+)\s+hours?$`),
	regexp.MustCompile(`(?i)^in\s+(\d+)\s+seconds?$`),
}

var relativeEnUnits = []time.Duration{time.Minute, time.Hour, time.Second}

func parseRelative(expr string) (time.Duration, bool) {
	// Special: 半小時後 = 30 minutes
	if expr == "半小時後" || expr == "半小时后" || expr == "半小時" {
		return 30 * time.Minute, true
	}

	// Chinese: find leading number then match suffix
	m := numPart.FindStringSubmatch(expr)
	if len(m) == 2 {
		n, ok := parseNum(m[1])
		if ok {
			rest := expr[len(m[1]):]
			for _, r := range relativeRules {
				if strings.HasPrefix(rest, r.suffix) {
					return time.Duration(n) * r.unit, true
				}
			}
		}
	}

	// English
	for i, re := range relativeEnRe {
		sm := re.FindStringSubmatch(expr)
		if len(sm) == 2 {
			if n, err := strconv.Atoi(sm[1]); err == nil {
				return time.Duration(n) * relativeEnUnits[i], true
			}
		}
	}

	return 0, false
}

// ─── Absolute time ────────────────────────────────────────────────────────────

type period int

const (
	periodUnknown period = iota
	periodAM
	periodPM
	periodNight // 晚上/今晚 — always PM, and hour 1-6 maps to 13-18
)

type dayPeriodPrefixes struct {
	prefix    string
	dayOffset int
	period    period
}

var prefixes = []dayPeriodPrefixes{
	// Two-char day + period combinations first (longer → higher priority)
	{"後天晚上", 2, periodNight},
	{"後天早上", 2, periodAM},
	{"後天下午", 2, periodPM},
	{"後天", 2, periodUnknown},
	{"明天晚上", 1, periodNight},
	{"明天下午", 1, periodPM},
	{"明天早上", 1, periodAM},
	{"明天清晨", 1, periodAM},
	{"明天凌晨", 1, periodAM},
	{"明天上午", 1, periodAM},
	{"明天", 1, periodUnknown},
	{"今天晚上", 0, periodNight},
	{"今天下午", 0, periodPM},
	{"今天早上", 0, periodAM},
	{"今天上午", 0, periodAM},
	{"今天凌晨", 0, periodAM},
	{"今天", 0, periodUnknown},
	{"今日晚上", 0, periodNight},
	{"今日下午", 0, periodPM},
	{"今日早上", 0, periodAM},
	{"今日", 0, periodUnknown},
	{"明日晚上", 1, periodNight},
	{"明日下午", 1, periodPM},
	{"明日早上", 1, periodAM},
	{"明日", 1, periodUnknown},
	// Period only (no day — implies today)
	{"今晚", 0, periodNight},
	{"今夜", 0, periodNight},
	{"晚上", 0, periodNight},
	{"傍晚", 0, periodNight},
	{"深夜", 0, periodNight},
	{"下午", 0, periodPM},
	{"午後", 0, periodPM},
	{"早上", 0, periodAM},
	{"清晨", 0, periodAM},
	{"凌晨", 0, periodAM},
	{"上午", 0, periodAM},
	{"中午", 0, periodPM}, // 中午 → noon area, treat as PM (12:xx)
}

// hourMinRe matches patterns like: 七點、7點、7點半、7點30分、7:30
var hourMinRe = regexp.MustCompile(
	`^([零一二三四五六七八九十兩\d]+)[點点時时:]([半]|([零一二三四五六七八九十兩\d]+)[分分]?)?$`,
)

// English absolute: "at 7pm", "at 9am", "tonight at 7", "tomorrow at 9am"
var englishAbsRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(?:(tomorrow)\s+)?at\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?$`),
	regexp.MustCompile(`(?i)^tonight\s+at\s+(\d{1,2})(?::(\d{2}))?$`),
}

func parseAbsolute(expr string, now time.Time) (time.Time, bool) {
	// ── English patterns ──────────────────────────────────────────────────────
	// "at 9am", "tomorrow at 7pm", etc.
	if m := englishAbsRe[0].FindStringSubmatch(expr); len(m) > 0 {
		tomorrow := m[1] != ""
		hour, _ := strconv.Atoi(m[2])
		min := 0
		if m[3] != "" {
			min, _ = strconv.Atoi(m[3])
		}
		ampm := strings.ToLower(m[4])
		if ampm == "pm" && hour < 12 {
			hour += 12
		} else if ampm == "am" && hour == 12 {
			hour = 0
		}
		dayOffset := 0
		if tomorrow {
			dayOffset = 1
		}
		t := clockTime(now, dayOffset, hour, min)
		if !tomorrow && t.Before(now) {
			t = t.Add(24 * time.Hour)
		}
		return t, true
	}
	// "tonight at 7"
	if m := englishAbsRe[1].FindStringSubmatch(expr); len(m) > 0 {
		hour, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		if hour < 12 {
			hour += 12 // tonight → PM
		}
		t := clockTime(now, 0, hour, min)
		if t.Before(now) {
			t = t.Add(24 * time.Hour)
		}
		return t, true
	}

	// ── Chinese patterns ──────────────────────────────────────────────────────
	dayOffset := 0
	p := periodUnknown
	rest := expr

	for _, pfx := range prefixes {
		if strings.HasPrefix(rest, pfx.prefix) {
			dayOffset = pfx.dayOffset
			p = pfx.period
			rest = rest[len(pfx.prefix):]
			break
		}
	}

	// Special standalone words
	if rest == "中午" || rest == "正午" {
		return clockTime(now, dayOffset, 12, 0), true
	}
	if rest == "午夜" || rest == "午夜十二點" {
		return clockTime(now, dayOffset, 0, 0), true
	}

	m := hourMinRe.FindStringSubmatch(rest)
	if len(m) == 0 {
		return time.Time{}, false
	}

	hour, ok := parseNum(m[1])
	if !ok || hour < 0 || hour > 23 {
		return time.Time{}, false
	}

	min := 0
	if m[2] == "半" {
		min = 30
	} else if m[3] != "" {
		min, ok = parseNum(m[3])
		if !ok {
			return time.Time{}, false
		}
	}

	// Apply period bias to 12-hour values
	switch p {
	case periodPM:
		if hour < 12 {
			hour += 12
		}
	case periodNight:
		// 晚上/今晚: hours 1–6 → 13–18, hours 7–11 → 19–23, 12 stays 12
		if hour >= 1 && hour <= 6 {
			hour += 12
		} else if hour >= 7 && hour <= 11 {
			hour += 12
		}
	case periodAM:
		if hour == 12 {
			hour = 0
		}
	case periodUnknown:
		// No qualifier: pick the nearest future occurrence
		// Try the hour as-is; if past, try +12 (if < 12); if still past, add a day.
	}

	t := clockTime(now, dayOffset, hour, min)

	// For today with no explicit day ("今天" not given) + unknown period:
	// if the time has already passed, advance to the next occurrence.
	if dayOffset == 0 && p == periodUnknown && t.Before(now) {
		if hour < 12 {
			t = t.Add(12 * time.Hour) // try PM
		}
		if t.Before(now) {
			t = t.Add(24 * time.Hour) // next day
		}
	}

	return t, true
}

// clockTime builds a time.Time for a given day offset, hour, and minute.
func clockTime(now time.Time, dayOffset, hour, min int) time.Time {
	base := now.AddDate(0, 0, dayOffset)
	return time.Date(base.Year(), base.Month(), base.Day(), hour, min, 0, 0, base.Location())
}
