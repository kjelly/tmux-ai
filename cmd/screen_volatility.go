package cmd

import (
	"math/bits"
	"strings"
)

// screenVolatility separates a pane's live UI chrome from its actual output.
//
// The idle fallbacks need to know when a pane stopped producing meaningful
// change, but comparing whole captures answers a different question: whether
// anything at all repainted. Coding-agent TUIs repaint elapsed timers, token
// counters and rotating footer hints forever, so a byte-identical capture
// never arrives and the idle fallbacks can never fire.
//
// Rather than encode what any particular agent's chrome looks like, this
// measures it: a row that keeps changing while the rest of the screen holds
// still is chrome, whatever it contains.
type screenVolatility struct {
	prev      []string
	history   []uint32
	samples   int
	announced bool
}

const (
	// volatileWindow is the number of comparisons each row's change history
	// retains, one bit per comparison.
	volatileWindow = 32
	// volatileMinChanges is deliberately a count rather than a fraction of the
	// window. A one-second footer clock changes on every poll at
	// --interval 2000 but only every other poll at 500, so a ratio threshold
	// would silently retune itself whenever the poll interval changed.
	volatileMinChanges = 3
	// volatileMinSamples withholds any judgement until enough of the window is
	// filled to distinguish "keeps changing" from "changed once".
	volatileMinSamples = 8
	// volatileMaxNumerator/Denominator bound how much of a screen may be
	// written off as chrome. Past that the screen is simply busy, and reading
	// it as "mostly decoration, therefore idle" would be exactly backwards.
	volatileMaxNumerator   = 1
	volatileMaxDenominator = 4
)

type screenStability struct {
	// Key is the comparison value for idle decisions. It is never relayed and
	// never shown to a selector — filtering must not reach the data plane.
	Key string
	// Excluded counts the rows treated as chrome. Zero means Key is the whole
	// capture, which is the pre-existing behaviour.
	Excluded int
}

// observe ingests one capture and returns the stability key for it.
func (v *screenVolatility) observe(content string, protocol roundControlProtocol) screenStability {
	lines := strings.Split(content, "\n")
	v.record(lines)
	v.prev = lines

	if v.samples < volatileMinSamples {
		return screenStability{Key: content}
	}
	volatile := make([]bool, len(lines))
	count := 0
	for i := range lines {
		if bits.OnesCount32(v.history[i]) < volatileMinChanges {
			continue
		}
		// A row carrying this round's protocol material is never decoration,
		// even while the terminal is repainting it.
		if carriesProtocolMarker(lines[len(lines)-1-i], protocol) {
			continue
		}
		volatile[i] = true
		count++
	}
	if count == 0 || count*volatileMaxDenominator > len(lines)*volatileMaxNumerator {
		// Too much of the screen is in motion to call any of it chrome. Fall
		// back to the whole capture, which is what the runner compared before
		// this filter existed.
		return screenStability{Key: content}
	}
	return screenStability{Key: applyVolatileMask(content, volatile), Excluded: count}
}

// warmedUp reports whether enough captures have been compared to tell a row
// that keeps repainting from one that changed once. Until then no verdict may
// rest on chrome having been excluded.
func (v *screenVolatility) warmedUp() bool { return v.samples >= volatileMinSamples }

// repaintingRows returns a bottom-indexed mask of the rows that changed at
// least minChanges times in the retained window. The threshold is a parameter
// because a decision that compares against a known reference screen can afford
// to call a row decoration sooner than one deciding a pane is idle.
func (v *screenVolatility) repaintingRows(minChanges int) []bool {
	mask := make([]bool, len(v.history))
	for i, history := range v.history {
		mask[i] = bits.OnesCount32(history) >= minChanges
	}
	return mask
}

func countMasked(mask []bool) int {
	count := 0
	for _, masked := range mask {
		if masked {
			count++
		}
	}
	return count
}

func applyVolatileMask(content string, mask []bool) string {
	if len(mask) == 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		// Rows are indexed from the bottom, so a capture whose scrollback has
		// grown since the mask was built still has its footer masked.
		if bottom := len(lines) - 1 - i; bottom < len(mask) && mask[bottom] {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// record shifts one comparison into every row's history. Rows are indexed from
// the bottom of the capture: capture history grows during a round, so only the
// distance from the last line is stable for a footer, and an alternate-screen
// TUI has a fixed height, which makes it stable for a top status bar too.
func (v *screenVolatility) record(lines []string) {
	for len(v.history) < len(lines) {
		v.history = append(v.history, 0)
	}
	for i := 0; i < len(lines); i++ {
		changed := uint32(0)
		if i < len(v.prev) && lines[len(lines)-1-i] != v.prev[len(v.prev)-1-i] {
			changed = 1
		}
		v.history[i] = v.history[i]<<1 | changed
	}
	if v.samples < volatileWindow {
		v.samples++
	}
}

func carriesProtocolMarker(line string, protocol roundControlProtocol) bool {
	for _, marker := range []string{
		protocol.StatusMarker, protocol.StatusBeginMarker, protocol.StatusEndMarker,
		protocol.TextBeginMarker, protocol.TextEndMarker,
		protocol.DoneMarker, protocol.StopMarker, protocol.GoalDoneMarker,
	} {
		if marker != "" && strings.Contains(line, marker) {
			return true
		}
	}
	return false
}
