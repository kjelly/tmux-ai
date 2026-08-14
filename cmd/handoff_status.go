package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Version int    `json:"version"`
	Outcome string `json:"outcome"`
	Summary string `json:"summary"`
	// PromptHead is the value the member read from the head line of the prompt
	// it answered. A receipt is proof that the member saw the end of the
	// message — that is where the control protocol lives — but not that it saw
	// the beginning, so this is the only field that can show a prompt arrived
	// whole rather than truncated or split into two submissions.
	PromptHead string           `json:"prompt_head,omitempty"`
	Tests      testClaims       `json:"tests,omitempty"`
	Artifacts  []artifact.Claim `json:"artifacts,omitempty"`
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

// promptHeadMarker labels the first line of a runner-built prompt. Its value is
// a nonce that appears nowhere else in the message, so a member that only
// received the tail of the prompt cannot reproduce it — not even by pattern,
// since the round token in the other markers does not determine it.
func promptHeadMarker(runToken string, round int) string {
	return fmt.Sprintf("TMUXAI_PROMPT_HEAD__TMUXAI_%s_%d", runToken, round)
}

// promptEchoMarker ends a runner-built prompt. It is never asked for in a reply:
// the runner looks for it on screen before pressing Enter, to know that what it
// pasted actually reached the pane rather than being discarded on the way.
func promptEchoMarker(runToken string, round int) string {
	return fmt.Sprintf("TMUXAI_PROMPT_ECHO__TMUXAI_%s_%d", runToken, round)
}

func statusFrameMarkers(runToken string, round int) (string, string) {
	prefix := fmt.Sprintf("TMUXAI_%s_%d", runToken, round)
	return "HANDOFF_STATUS_BEGIN__" + prefix, "HANDOFF_STATUS_END__" + prefix
}

// parseProtocolStatus reads this round's receipt in either supported shape:
// the framed form the runner asks for, or the legacy single line that carries
// the marker and the JSON together. Runner-synthesized receipts also use the
// legacy shape, since nothing renders them.
func parseProtocolStatus(output string, p roundControlProtocol) (*handoffStatus, error) {
	block, err := extractProtocolStatusBlock(output, p)
	if err != nil {
		return nil, err
	}
	return decodeRenderedHandoffStatus([]byte(block.JSON))
}

func extractProtocolStatusBlock(output string, p roundControlProtocol) (handoffStatusBlock, error) {
	if p.StatusBeginMarker == "" || p.StatusEndMarker == "" {
		return extractHandoffStatusBlock(output, p.StatusMarker)
	}
	block, frameErr := extractFramedStatusBlock(output, p.StatusBeginMarker, p.StatusEndMarker)
	if frameErr == nil {
		return block, nil
	}
	block, inlineErr := extractHandoffStatusBlock(output, p.StatusMarker)
	if inlineErr == nil {
		return block, nil
	}
	// A frame that was found but did not hold a usable object is the more
	// specific diagnosis; fall back to the inline error only when no frame was
	// present at all.
	if !errors.Is(frameErr, errNoStatusFrame) {
		return handoffStatusBlock{}, frameErr
	}
	return handoffStatusBlock{}, inlineErr
}

var errNoStatusFrame = errors.New("no handoff status frame")

// extractFramedStatusBlock binds the receipt to the newest complete frame.
// Newest-wins matches how completion markers are resolved and keeps an echoed
// or repainted earlier frame from making the current receipt unreadable.
func extractFramedStatusBlock(output, begin, end string) (handoffStatusBlock, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	endAt := -1
	for i, line := range lines {
		if protocolMarkerLine(line, end) {
			endAt = i
		}
	}
	if endAt < 0 {
		return handoffStatusBlock{}, fmt.Errorf("%w %s", errNoStatusFrame, end)
	}
	beginAt := -1
	for i := endAt - 1; i >= 0; i-- {
		if protocolMarkerLine(lines[i], begin) {
			beginAt = i
			break
		}
	}
	if beginAt < 0 {
		return handoffStatusBlock{}, fmt.Errorf("%w: missing begin marker %s", errNoStatusFrame, begin)
	}
	closingPrefix, _, _ := matchProtocolMarker(lines[endAt], end, false)
	var joined strings.Builder
	for _, line := range lines[beginAt+1 : endAt] {
		joined.WriteString(stripPresentationContinuation(line, closingPrefix))
		joined.WriteByte('\n')
		if joined.Len() > 16*1024 {
			return handoffStatusBlock{}, fmt.Errorf("handoff status frame %s exceeds the size limit", begin)
		}
	}
	object, ok := completeJSONObject(joined.String())
	if !ok {
		return handoffStatusBlock{}, fmt.Errorf("handoff status frame %s does not contain a JSON object", begin)
	}
	return handoffStatusBlock{JSON: object, Start: beginAt, End: endAt}, nil
}

func parseHandoffStatus(output, marker string) (*handoffStatus, error) {
	block, err := extractHandoffStatusBlock(output, marker)
	if err != nil {
		return nil, err
	}
	return decodeRenderedHandoffStatus([]byte(block.JSON))
}

// decodeRenderedHandoffStatus keeps file receipts strict while recovering the
// one malformed shape terminal UIs commonly create: a hard line break inside a
// JSON string they rendered. That break is presentation damage, not a JSON
// value supplied by the member, so turn it into a space and retry decoding.
// Raw JSON read from the outbox deliberately never takes this path.
func decodeRenderedHandoffStatus(data []byte) (*handoffStatus, error) {
	status, err := decodeHandoffStatus(data)
	if err == nil {
		return status, nil
	}
	normalized, changed := normalizeRenderedJSONStringBreaks(data)
	if !changed {
		return nil, err
	}
	return decodeHandoffStatus(normalized)
}

// normalizeRenderedJSONStringBreaks removes a terminal-inserted line break
// and its visual indentation only while scanning a JSON string literal. A
// newline outside a string remains normal JSON whitespace. The normalizer is
// intentionally used only for captured pane output; accepting malformed bytes
// from a receipt file would weaken the file transport's validation boundary.
func normalizeRenderedJSONStringBreaks(data []byte) ([]byte, bool) {
	var normalized strings.Builder
	normalized.Grow(len(data))
	inString, escaped, changed := false, false, false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString && (ch == '\n' || ch == '\r') {
			changed = true
			if ch == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
			for i+1 < len(data) && (data[i+1] == ' ' || data[i+1] == '\t') {
				i++
			}
			normalized.WriteByte(' ')
			continue
		}
		normalized.WriteByte(ch)
		switch {
		case escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case ch == '"':
			inString = !inString
		}
	}
	if !changed {
		return data, false
	}
	return []byte(normalized.String()), true
}

// decodeHandoffStatus applies the same schema validation to a receipt whether
// it was recovered from the pane or read from the outbox receipt file.
func decodeHandoffStatus(data []byte) (*handoffStatus, error) {
	var status handoffStatus
	if err := json.Unmarshal(data, &status); err != nil {
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

// errIncompletePrompt marks a round whose member demonstrably answered
// something other than the whole prompt the runner sent. It is deliberately not
// an errMissingReceipt: the receipt is present and well-formed, and no amount of
// re-asking for a receipt or re-selecting screen text can repair a prompt that
// arrived in pieces.
var errIncompletePrompt = errors.New("prompt did not reach the member intact")

// validatePromptDelivery compares the head nonce the member reported against the
// one the runner put at the top of the prompt. A member can only produce that
// value by having received the beginning of the message, so a mismatch means the
// paste was truncated, split into two submissions, or that the reply belongs to
// a different round.  Some agents report the dynamic marker suffix together
// with the nonce; accept that exact spelling too, as it contains the same
// unguessable nonce and therefore proves the same fact.
func validatePromptDelivery(status *handoffStatus, p roundControlProtocol, label string) error {
	if p.HeadNonce == "" || status == nil {
		return nil
	}
	reported := strings.TrimSpace(status.PromptHead)
	switch {
	case promptHeadMatches(reported, p):
		return nil
	case reported == "":
		return fmt.Errorf("%w: %s 的 receipt 沒有 prompt_head 欄位，無法確認本輪 prompt 是否完整送達", errIncompletePrompt, label)
	case strings.EqualFold(reported, "missing"):
		return fmt.Errorf("%w: %s 回報沒有看到 prompt 開頭的 head 行，本輪 prompt 只有後半段送達（paste 被截斷或被提前提交成兩則訊息）", errIncompletePrompt, label)
	default:
		return fmt.Errorf("%w: %s 回報的 prompt_head 是 %q，本輪應為 %q；該回覆對應的不是本輪完整 prompt", errIncompletePrompt, label, truncate(reported, 40), p.HeadNonce)
	}
}

func promptHeadMatches(reported string, p roundControlProtocol) bool {
	if reported == p.HeadNonce {
		return true
	}
	fullLine := p.HeadMarker + " " + p.HeadNonce
	if reported == fullLine {
		return true
	}
	const markerPrefix = "TMUXAI_PROMPT_HEAD__"
	markerSuffix := strings.TrimPrefix(p.HeadMarker, markerPrefix)
	return markerSuffix != p.HeadMarker && reported == markerSuffix+" "+p.HeadNonce
}

type handoffStatusBlock struct {
	JSON       string
	Start, End int
}

const maxFileReceiptBytes = 64 * 1024

// fileReceiptPath returns the outbox receipt file for a file-transport round.
// File transport already requires the runner and the agent to share a
// filesystem, so this adds no precondition of its own; text transport, which
// carries no such requirement, never uses it.
func fileReceiptPath(p roundControlProtocol) string {
	if p.Transport != handoffTransportFile || p.Outbox == "" {
		return ""
	}
	return filepath.Join(p.Outbox, artifact.ReceiptFileName)
}

// readFileReceipt loads the receipt an agent wrote to its outbox instead of, or
// in addition to, rendering it in the pane. The outbox is created per member
// and per round, so the file is already scoped to this turn and needs no
// separate nonce.
func readFileReceipt(p roundControlProtocol) (*handoffStatus, error) {
	path := fileReceiptPath(p)
	if path == "" {
		return nil, fmt.Errorf("receipt file requires file transport")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("receipt file %s is not a regular file", path)
	}
	if info.Size() > maxFileReceiptBytes {
		return nil, fmt.Errorf("receipt file %s exceeds %d bytes", path, maxFileReceiptBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	status, err := decodeHandoffStatus(data)
	if err != nil {
		return nil, fmt.Errorf("receipt file %s: %w", path, err)
	}
	// Artifact discovery stays runner-owned: the outbox scan is the authority,
	// so a claimed path here must not reach the coordinator.
	status.Artifacts = nil
	return status, nil
}

// fileReceiptEnvelope renders a validated outbox receipt in the same on-screen
// form the rest of the pipeline parses, so a file receipt and a pane receipt
// converge on one code path.
func fileReceiptEnvelope(p roundControlProtocol) (string, bool) {
	if !p.RequireStatus || p.StatusMarker == "" {
		return "", false
	}
	status, err := readFileReceipt(p)
	if err != nil {
		return "", false
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		return "", false
	}
	return p.StatusMarker + " " + string(encoded), true
}

// validatedFileReceiptEnvelope is the receipt-first completion boundary for a
// normal file handoff. Unlike fileReceiptEnvelope's compatibility helper, it
// also proves that the receipt belongs to the full prompt currently in flight.
func validatedFileReceiptEnvelope(p roundControlProtocol, label string) (string, bool) {
	if !p.RequireStatus || p.StatusMarker == "" {
		return "", false
	}
	status, err := readFileReceipt(p)
	if err != nil || validatePromptDelivery(status, p, label) != nil {
		return "", false
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		return "", false
	}
	return p.StatusMarker + " " + string(encoded), true
}

// receiptRepairProtocol bounds a replacement receipt after a completed file
// handoff. Its token is fresh, so a repair cannot accidentally consume an
// earlier prompt or completion rendered in the pane scrollback.
type receiptRepairProtocol struct {
	BeginMarker string
	DoneMarker  string
	// EchoMarker is the repair message's own delivery check. Reusing the
	// original round's marker would find that prompt still on screen and pass
	// without the repair request having arrived at all.
	EchoMarker string
}

func newReceiptRepairProtocol(original roundControlProtocol, token string) receiptRepairProtocol {
	return receiptRepairProtocol{
		BeginMarker: roundControlMarker("HANDOFF_RECEIPT_BEGIN", token, protocolRound(original)),
		DoneMarker:  roundControlMarker("HANDOFF_RECEIPT_DONE", token, protocolRound(original)),
		EchoMarker:  promptEchoMarker(token, protocolRound(original)),
	}
}

// parseReceiptRepair accepts a status only when it is inside the fresh repair
// frame. The receipt retains the original round's status marker: that marker
// remains the binding between the repaired control plane and its artifact
// outbox.
func parseReceiptRepair(output string, original roundControlProtocol, repair receiptRepairProtocol) (*handoffStatus, error) {
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
	return parseProtocolStatus(strings.Join(lines[beginAt+1:doneAt], "\n"), original)
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
		// A standalone marker line already tolerates markdown emphasis. Apply
		// the same tolerance before the payload so an agent that renders
		// "**MARKER** {json}" is not treated as having omitted its receipt.
		// The separating whitespace is still required, so a longer marker
		// sharing this prefix (round 11 vs round 1) cannot match.
		remainder = strings.TrimLeft(remainder, markerDecoration)
		first, _ := utf8.DecodeRuneInString(remainder)
		if remainder == "" || !unicode.IsSpace(first) {
			return "", "", false
		}
		return prefix, remainder, true
	}
	remainder = strings.TrimSpace(remainder)
	remainder = strings.Trim(remainder, markerDecoration)
	return prefix, remainder, remainder == ""
}

// markerDecoration lists the inline markdown characters agents wrap protocol
// markers in. It deliberately excludes anything that can begin a JSON value.
const markerDecoration = "*_~`"

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

func carriesDeliveryMarker(line string, protocol roundControlProtocol) bool {
	for _, marker := range []string{protocol.HeadMarker, protocol.EchoMarker} {
		if marker != "" && strings.Contains(line, marker) {
			return true
		}
	}
	return false
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
	if block, err := extractProtocolStatusBlock(maskTextFrame(output, protocol.TextBeginMarker, protocol.TextEndMarker), protocol); err == nil {
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
		if inText || (i >= statusStart && i <= statusEnd) || protocolMarkerLine(line, protocol.StatusBeginMarker) || protocolMarkerLine(line, protocol.StatusEndMarker) || protocolMarkerLine(line, protocol.GoalDoneMarker) || protocolMarkerLine(line, protocol.DoneMarker) || protocolMarkerLine(line, protocol.StopMarker) {
			continue
		}
		// The delivery markers are runner bookkeeping that the pane renders as
		// part of the prompt. They carry no task content, so they must not be
		// relayed onwards even when a capture includes the prompt itself. The
		// head line carries a value after the marker, so match by containment
		// rather than as a standalone marker line.
		if carriesDeliveryMarker(line, protocol) {
			continue
		}
		result = append(result, line)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}
