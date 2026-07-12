package workflow

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/pane"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

func (e *Executor) runWatchStep(ctx context.Context, s *Step) (string, error) {
	ex := tmux.NewExecutor()

	paneID := e.resolvePane(s, tmuxutil.NewResolver())
	pattern := e.expand(s.Pattern)
	rangeExpr := e.expand(s.Range)
	lines := s.Lines
	if lines == 0 {
		lines = 50
	}
	interval := time.Duration(s.Interval) * time.Millisecond
	if interval == 0 {
		interval = 2 * time.Second
	}
	stepTimeout := e.timeout
	if s.Timeout > 0 {
		stepTimeout = s.Timeout
	}
	deadline := time.Now().Add(time.Duration(stepTimeout) * time.Second)

	var re *regexp.Regexp
	if pattern != "" {
		re, _ = regexp.Compile(pattern)
	}

	// For range-only mode (no pattern): track previous slice content and trigger on change.
	prevSlice := ""
	firstPoll := true

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}

		if time.Now().After(deadline) {
			verb := "for pattern"
			if s.Absent {
				verb = "for pattern to disappear"
			}
			if rangeExpr != "" && pattern == "" {
				verb = "for change in range"
			}
			return "", fmt.Errorf("timeout waiting %s %q in pane %s", verb, pattern, paneID)
		}

		content, err := ex.CapturePaneClean(paneID, lines)
		if err != nil {
			continue
		}

		// [~:] — dynamic "from first changed line" range.
		if pane.IsChangeRange(rangeExpr) {
			if firstPoll {
				prevSlice = content
				firstPoll = false
				continue
			}
			startIdx, changed := pane.FromChange(prevSlice, content)
			prevSlice = content
			if startIdx < 0 {
				continue
			}
			return changed, nil
		}

		// Apply static range slice if specified.
		target := content
		if rangeExpr != "" {
			sliced, _, sliceErr := pane.SliceLines(content, rangeExpr)
			if sliceErr != nil {
				return "", fmt.Errorf("range %q: %w", rangeExpr, sliceErr)
			}
			target = sliced
		}

		// Range-only mode: trigger when the slice content changes.
		if rangeExpr != "" && pattern == "" {
			if firstPoll {
				prevSlice = target
				firstPoll = false
				continue
			}
			if target != prevSlice {
				return target, nil
			}
			continue
		}

		// Pattern mode (with optional range): check pattern presence.
		found := strings.Contains(target, pattern) || (re != nil && re.MatchString(target))
		if found != s.Absent {
			return target, nil
		}
	}
}
