package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/llmore/tmux-ai/internal/artifact"
)

type testClaim struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}
type handoffStatus struct {
	Version   int              `json:"version"`
	Outcome   string           `json:"outcome"`
	Summary   string           `json:"summary"`
	Tests     testClaims       `json:"tests,omitempty"`
	Artifacts []artifact.Claim `json:"artifacts,omitempty"`
}

type testClaims []testClaim

func (claims *testClaims) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	result := make(testClaims, 0, len(values))
	for _, value := range values {
		var claim testClaim
		if len(value) > 0 && value[0] == '"' {
			if err := json.Unmarshal(value, &claim.Name); err != nil {
				return err
			}
		} else if err := json.Unmarshal(value, &claim); err != nil {
			return err
		}
		result = append(result, claim)
	}
	*claims = result
	return nil
}

func statusMarker(runToken string, round int) string {
	return fmt.Sprintf("HANDOFF_STATUS__TMUXAI_%s_%d", runToken, round)
}

func parseHandoffStatus(output, marker string) (*handoffStatus, error) {
	block, err := extractHandoffStatusBlock(output, marker)
	if err != nil {
		return nil, err
	}
	var status handoffStatus
	if err := json.Unmarshal([]byte(block.JSON), &status); err != nil {
		return nil, fmt.Errorf("parse handoff status: %w", err)
	}
	if status.Version != 1 {
		return nil, fmt.Errorf("unsupported handoff status version %d", status.Version)
	}
	if status.Outcome != "success" && status.Outcome != "needs_followup" && status.Outcome != "blocked" {
		return nil, fmt.Errorf("invalid handoff status outcome %q", status.Outcome)
	}
	if strings.TrimSpace(status.Summary) == "" {
		return nil, fmt.Errorf("handoff status summary is required")
	}
	return &status, nil
}

type handoffStatusBlock struct {
	JSON       string
	Start, End int
}

// receiptRepairProtocol bounds a replacement receipt after a completed file
// handoff. Its token is fresh, so a repair cannot accidentally consume an
// earlier prompt or completion rendered in the pane scrollback.
type receiptRepairProtocol struct {
	BeginMarker string
	DoneMarker  string
}

func newReceiptRepairProtocol(original roundControlProtocol, token string) receiptRepairProtocol {
	return receiptRepairProtocol{
		BeginMarker: roundControlMarker("HANDOFF_RECEIPT_BEGIN", token, protocolRound(original)),
		DoneMarker:  roundControlMarker("HANDOFF_RECEIPT_DONE", token, protocolRound(original)),
	}
}

// parseReceiptRepair accepts a status only when it is inside the fresh repair
// frame. The receipt retains the original round's status marker: that marker
// remains the binding between the repaired control plane and its artifact
// outbox.
func parseReceiptRepair(output, originalStatusMarker string, repair receiptRepairProtocol) (*handoffStatus, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	doneAt := -1
	for i, line := range lines {
		if !protocolMarkerLine(line, repair.DoneMarker) {
			continue
		}
		if doneAt >= 0 {
			return nil, fmt.Errorf("duplicate receipt repair completion marker %s", repair.DoneMarker)
		}
		doneAt = i
	}
	if doneAt < 0 {
		return nil, fmt.Errorf("missing receipt repair completion marker %s", repair.DoneMarker)
	}
	beginAt := -1
	for i := doneAt - 1; i >= 0; i-- {
		if protocolMarkerLine(lines[i], repair.BeginMarker) {
			beginAt = i
			break
		}
	}
	if beginAt < 0 {
		return nil, fmt.Errorf("missing receipt repair begin marker %s", repair.BeginMarker)
	}
	return parseHandoffStatus(strings.Join(lines[beginAt+1:doneAt], "\n"), originalStatusMarker)
}

func extractHandoffStatusBlock(output, marker string) (handoffStatusBlock, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	var matches []int
	var prefixes, remainders []string
	for i, line := range lines {
		prefix, remainder, ok := matchProtocolMarker(line, marker, true)
		if !ok {
			continue
		}
		matches = append(matches, i)
		prefixes = append(prefixes, prefix)
		remainders = append(remainders, remainder)
	}
	if len(matches) == 0 {
		return handoffStatusBlock{}, fmt.Errorf("missing handoff status marker %s", marker)
	}
	if len(matches) > 1 {
		return handoffStatusBlock{}, fmt.Errorf("duplicate handoff status marker %s", marker)
	}

	start := matches[0]
	var joined strings.Builder
	joined.WriteString(strings.TrimSpace(remainders[0]))
	for end := start; end < len(lines); end++ {
		if end > start {
			joined.WriteString(stripPresentationContinuation(lines[end], prefixes[0]))
		}
		if joined.Len() > 16*1024 {
			return handoffStatusBlock{}, fmt.Errorf("invalid handoff status JSON")
		}
		if object, ok := completeJSONObject(joined.String()); ok {
			return handoffStatusBlock{JSON: object, Start: start, End: end}, nil
		}
	}
	return handoffStatusBlock{}, fmt.Errorf("invalid handoff status JSON")
}

func completeJSONObject(value string) (string, bool) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	if value == "" || value[0] != '{' {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case inString && value[i] == '\\':
			escaped = true
		case value[i] == '"':
			inString = !inString
		case inString:
		case value[i] == '{':
			depth++
		case value[i] == '}':
			depth--
			if depth == 0 {
				return value[:i+1], true
			}
		}
	}
	return "", false
}

func matchProtocolMarker(line, marker string, allowRemainder bool) (prefix, remainder string, ok bool) {
	line = strings.TrimSuffix(line, "\r")
	at := strings.Index(line, marker)
	if at < 0 || !isProtocolPresentationPrefix(line[:at]) {
		return "", "", false
	}
	prefix, remainder = line[:at], line[at+len(marker):]
	if allowRemainder {
		if remainder == "" || !unicode.IsSpace(rune(remainder[0])) {
			return "", "", false
		}
		return prefix, remainder, true
	}
	remainder = strings.TrimSpace(remainder)
	remainder = strings.Trim(remainder, "*_~`")
	return prefix, remainder, remainder == ""
}

func isProtocolPresentationPrefix(value string) bool {
	for _, r := range value {
		// Terminal UIs commonly add a leading glyph to assistant output (for
		// example a bullet, tree edge, prompt chevron, or emoji).  This must
		// not be a per-client allowlist: terminals and agents choose those
		// glyphs independently.  A prefix is presentation-only when it has no
		// letters or numbers; accept Unicode whitespace and graphical
		// decoration, but reject semantic text and control characters.
		//
		// The per-round marker remains the authority.  This predicate merely
		// removes the visual framing applied by a terminal, rather than making
		// a marker embedded in a sentence into protocol data.
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsMark(r) {
			continue
		}
		return false
	}
	return true
}

func stripPresentationContinuation(line, markerPrefix string) string {
	line = strings.TrimSuffix(line, "\r")
	if markerPrefix != "" && strings.HasPrefix(line, markerPrefix) {
		return line[len(markerPrefix):]
	}
	columns := utf8.RuneCountInString(markerPrefix)
	for columns > 0 && len(line) > 0 {
		r, size := utf8.DecodeRuneInString(line)
		if !unicode.IsSpace(r) {
			break
		}
		line = line[size:]
		columns--
	}
	return line
}

func protocolMarkerLine(line, marker string) bool {
	_, _, ok := matchProtocolMarker(line, marker, false)
	return ok
}

func stripTextFrame(output, begin, end string) string {
	var result []string
	inFrame := false
	for _, line := range strings.Split(output, "\n") {
		if protocolMarkerLine(line, begin) {
			inFrame = true
			continue
		}
		if protocolMarkerLine(line, end) {
			inFrame = false
			continue
		}
		if !inFrame {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func maskTextFrame(output, begin, end string) string {
	lines := strings.Split(output, "\n")
	inFrame := false
	for i, line := range lines {
		if protocolMarkerLine(line, begin) {
			inFrame = true
			lines[i] = ""
			continue
		}
		if protocolMarkerLine(line, end) {
			inFrame = false
			lines[i] = ""
			continue
		}
		if inFrame {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func removeProtocolLines(output string, protocol roundControlProtocol) string {
	var result []string
	inText := false
	statusStart, statusEnd := -1, -1
	if block, err := extractHandoffStatusBlock(maskTextFrame(output, protocol.TextBeginMarker, protocol.TextEndMarker), protocol.StatusMarker); err == nil {
		statusStart, statusEnd = block.Start, block.End
	}
	for i, line := range strings.Split(output, "\n") {
		if protocolMarkerLine(line, protocol.TextBeginMarker) {
			inText = true
			continue
		}
		if protocolMarkerLine(line, protocol.TextEndMarker) {
			inText = false
			continue
		}
		if inText || (i >= statusStart && i <= statusEnd) || protocolMarkerLine(line, protocol.GoalDoneMarker) || protocolMarkerLine(line, protocol.DoneMarker) || protocolMarkerLine(line, protocol.StopMarker) {
			continue
		}
		result = append(result, line)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}
