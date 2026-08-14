package cmd

import (
	"fmt"
	"strings"
	"time"
)

// roundStallDiagnosis records how far a round actually got before it ran out of
// time. Without it every stall reports "no completion marker", which sends the
// reader looking at the agent's output when the real cause was a receipt that
// would not validate, a text frame that never closed, or an envelope the
// terminal kept rewriting.
type roundStallDiagnosis struct {
	completionMarker       bool
	streamOnly             bool
	goalMarkerMissing      bool
	goalClearPending       bool
	envelopeMissing        bool
	receiptErr             error
	receiptWithoutEnvelope bool
	envelopeChanges        int
	validReceipt           bool
	// pendingNudges counts the extra Enter presses this round needed because
	// its prompt was still sitting unconsumed in the pane's input area. A round
	// that had to be nudged and then still timed out is far more likely to have
	// a delivery problem than an agent problem.
	pendingNudges int
	// stableFor is how long the chrome-filtered screen had been unchanged when
	// the round ran out of time.
	stableFor time.Duration
}

// stallIdleNote is the point past which an unchanged screen is worth reporting:
// long enough that the agent is not merely between two renders.
const stallIdleNote = 15 * time.Second

func (d *roundStallDiagnosis) noteCompletionMarker(fromStream bool) {
	if !d.completionMarker {
		d.streamOnly = fromStream
	} else if !fromStream {
		d.streamOnly = false
	}
	d.completionMarker = true
}

// noteMissingEnvelope explains why a detected completion could not be turned
// into a handoff. The extra parse is only ever done on a capture that already
// contains a completion marker, so it costs nothing on the normal path.
func (d *roundStallDiagnosis) noteMissingEnvelope(content string, p roundControlProtocol) {
	d.envelopeMissing = true
	statusOutput := content
	if p.Transport == handoffTransportText {
		statusOutput = stripTextFrame(content, p.TextBeginMarker, p.TextEndMarker)
	}
	if _, err := parseProtocolStatus(statusOutput, p); err != nil {
		d.receiptErr, d.receiptWithoutEnvelope = err, false
		return
	}
	d.receiptErr, d.receiptWithoutEnvelope = nil, true
}

func (d *roundStallDiagnosis) noteEnvelopeChanged() { d.envelopeChanges++ }

// noteValidatedEnvelope discards an earlier, transient parse failure. A pane
// can repaint an incomplete receipt before it renders the complete envelope;
// once that envelope validates, the old failure must not describe a later
// timeout while waiting for /goal clear.
func (d *roundStallDiagnosis) noteValidatedEnvelope() {
	d.envelopeMissing = false
	d.receiptErr = nil
	d.receiptWithoutEnvelope = false
	d.validReceipt = true
}

func (d *roundStallDiagnosis) noteGoalClearPending() { d.goalClearPending = true }

// unclosedTextFrame reports the one stall the runner cannot see past: an agent
// that opened a text handoff frame and never closed it. Everything after the
// begin marker — receipt and completion marker included — is inside untrusted
// payload, so the round looks completely silent from the control plane.
func unclosedTextFrame(screen string, p roundControlProtocol) bool {
	if p.Transport != handoffTransportText || p.TextBeginMarker == "" {
		return false
	}
	begin, end := false, false
	for _, line := range strings.Split(screen, "\n") {
		if protocolMarkerLine(line, p.TextBeginMarker) {
			begin, end = true, false
			continue
		}
		if protocolMarkerLine(line, p.TextEndMarker) {
			end = true
		}
	}
	return begin && !end
}

// err reports the stall, then appends the delivery and idleness context that
// applies whichever stall it was.
func (d *roundStallDiagnosis) err(paneID string, p roundControlProtocol, timeout int, screen string) error {
	err := d.reason(paneID, p, timeout, screen)
	if d.pendingNudges > 0 {
		err = fmt.Errorf("%w（本輪曾偵測到 prompt 未被提交，已補送 Enter %d 次）", err, d.pendingNudges)
	}
	if !d.completionMarker && d.stableFor >= stallIdleNote {
		err = fmt.Errorf("%w（畫面已連續 %s 沒有非 chrome 變化，agent 可能在等待人工確認或已停住）", err, d.stableFor.Round(time.Second))
	}
	return err
}

func (d *roundStallDiagnosis) reason(paneID string, p roundControlProtocol, timeout int, screen string) error {
	suffix := ""
	if d.streamOnly {
		suffix = "（完成標記只出現在 pane output stream，畫面上已看不到）"
	}
	switch {
	case d.goalMarkerMissing:
		return fmt.Errorf("pane %s 在 %d 秒內輸出了完成標記 %q，但沒有先輸出 goal completion marker %q；goal 尚未實際關閉", paneID, timeout, p.DoneMarker, p.GoalDoneMarker)
	case d.goalClearPending:
		return fmt.Errorf("pane %s 已驗證完成 envelope 並送出 /goal clear，但在 %d 秒內畫面沒有連續穩定 %s，無法安全交接", paneID, timeout, completionIdleDuration())
	case d.envelopeChanges > 1:
		return fmt.Errorf("pane %s 的完成 envelope 在 %d 秒內被改寫 %d 次，始終沒有穩定 %s%s", paneID, timeout, d.envelopeChanges, completionSettleDuration(), suffix)
	case d.completionMarker && d.receiptErr != nil:
		return fmt.Errorf("pane %s 在 %d 秒內輸出了完成標記 %q，但沒有可用的 status receipt：%v%s", paneID, timeout, p.DoneMarker, d.receiptErr, suffix)
	case d.completionMarker && d.receiptWithoutEnvelope && p.Transport == handoffTransportText:
		return fmt.Errorf("pane %s 在 %d 秒內輸出了完成標記 %q 與有效 receipt，但 text handoff frame（%s 到 %s）不完整%s", paneID, timeout, p.DoneMarker, p.TextBeginMarker, p.TextEndMarker, suffix)
	case unclosedTextFrame(screen, p):
		return fmt.Errorf("pane %s 在 %d 秒內開啟了 text handoff frame %q 卻沒有輸出結束 marker %q；receipt 與完成標記都被留在未結束的 frame 內，runner 只能視為 untrusted payload", paneID, timeout, p.TextBeginMarker, p.TextEndMarker)
	case d.completionMarker && d.envelopeMissing:
		return fmt.Errorf("pane %s 在 %d 秒內輸出了完成標記 %q，但無法組出完整的交接 envelope%s", paneID, timeout, p.DoneMarker, suffix)
	case d.validReceipt:
		idle := "此 fallback 已停用"
		if ppReceiptIdleTimeout > 0 {
			idle = fmt.Sprintf("畫面也未連續穩定 %s", time.Duration(ppReceiptIdleTimeout)*time.Second)
		}
		return fmt.Errorf("pane %s 在 %d 秒內輸出了有效 status receipt，但沒有輸出完成標記 %q，%s", paneID, timeout, p.DoneMarker, idle)
	case p.GoalDoneMarker != "":
		return fmt.Errorf("pane %s 在 %d 秒內未先輸出 goal completion marker %q，再輸出完成標記 %q", paneID, timeout, p.GoalDoneMarker, p.DoneMarker)
	default:
		return fmt.Errorf("pane %s 在 %d 秒內未輸出完成標記 %q", paneID, timeout, p.DoneMarker)
	}
}
