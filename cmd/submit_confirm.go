package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

// This file answers one question the handoff protocol cannot answer for itself:
// did the prompt the runner pasted actually reach the agent? A round that never
// started looks exactly like a round that is still working — no completion
// marker, no receipt — so without a separate signal every such failure is
// reported as "the agent produced no completion marker", which sends the reader
// looking at the agent instead of at the delivery.

// paneState is the tmux-reported state that decides whether a pane can receive
// input at all. Two of these states discard everything the runner sends.
type paneState struct {
	InMode  bool
	Dead    bool
	Mode    string
	Command string
	// Known is false when tmux could not be asked. No gate may act on that:
	// a state the runner cannot read is not evidence of a broken pane.
	Known bool
}

// Command is last so a value containing the separator cannot shift the fields
// that are parsed as flags.
const paneStateFormat = "#{pane_in_mode}|#{pane_dead}|#{pane_mode}|#{pane_current_command}"

func readPaneState(ex tmux.Executor, paneID string) paneState {
	out, err := ex.Exec("display-message", "-p", "-t", paneID, paneStateFormat)
	if err != nil {
		return paneState{}
	}
	fields := strings.SplitN(strings.TrimSpace(out), "|", 4)
	if len(fields) < 4 {
		return paneState{}
	}
	return paneState{
		InMode:  fields[0] == "1",
		Dead:    fields[1] == "1",
		Mode:    fields[2],
		Command: fields[3],
		Known:   true,
	}
}

// ensurePaneAcceptsInput refuses to paste into a pane that cannot deliver the
// text to an agent, and repairs the one such state that is repairable.
//
// While a pane is in copy-mode tmux routes send-keys and paste-buffer into that
// mode's key table, so the prompt is discarded outright — not queued, not
// delivered late. A user who scrolls a pane with the mouse wheel puts it in
// copy-mode, which makes this the most ordinary way for a whole round to go
// missing, and it leaves no trace in the pane for the round to diagnose later.
func ensurePaneAcceptsInput(ex tmux.Executor, paneID string) error {
	state := readPaneState(ex, paneID)
	if !state.Known {
		return nil
	}
	if state.Dead {
		return fmt.Errorf("pane %s 的程序已結束（pane_dead=1），不送出任何內容", paneID)
	}
	if state.InMode {
		mode := ifEmpty(state.Mode, "copy-mode")
		if _, err := ex.Exec("send-keys", "-X", "-t", paneID, "cancel"); err != nil {
			return fmt.Errorf("pane %s 目前在 %s，送出的內容會被 tmux 丟棄，且無法取消該模式：%w", paneID, mode, err)
		}
		if after := readPaneState(ex, paneID); after.InMode {
			return fmt.Errorf("pane %s 仍在 %s，送出的內容會被 tmux 丟棄", paneID, ifEmpty(after.Mode, mode))
		}
		fmt.Printf("⚠️ Pane %s 原本在 %s，送出前已取消；該模式期間送出的任何內容都會被 tmux 直接丟棄\n", paneID, mode)
	}
	if !ppAllowShellPane && isShellCommand(state.Command) {
		return fmt.Errorf("pane %s 的前景程序是 shell（%s），agent 並未在等待輸入；prompt 會被 shell 當成指令執行。請確認該 pane，或加上 --allow-shell-pane 明確允許", paneID, state.Command)
	}
	return nil
}

// isShellCommand reports whether a pane's foreground process is a plain shell.
// pingpong and coordinate always target an interactive agent, so a shell in the
// foreground means the agent is not there to read the prompt: an exited agent,
// a pane that was never started, or the wrong target.
func isShellCommand(command string) bool {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(command), "-")) {
	case "bash", "sh", "zsh", "fish", "nu", "nushell", "dash", "ash", "ksh", "mksh", "csh", "tcsh", "elvish", "xonsh", "pwsh", "powershell":
		return true
	}
	return false
}

// Modes for --input-echo-check. auto stops only when nothing suggests the paste
// arrived; strict also stops when arrival could not be confirmed, which suits an
// agent whose composer always renders the tail of a paste.
const (
	inputEchoAuto   = "auto"
	inputEchoStrict = "strict"
	inputEchoOff    = "off"
)

// promptEchoState is what the runner could establish about the paste before it
// pressed Enter.
type promptEchoState int

const (
	// echoSkipped means this message carried no echo marker (a bare command
	// such as /new), so nothing was checked.
	echoSkipped promptEchoState = iota
	// echoFound means this round's own marker was rendered in the pane. The
	// paste arrived, and it is this round's paste rather than a leftover.
	echoFound
	// echoUnverifiable means the marker is not visible but the pane clearly
	// received something — a TUI that collapses a large paste into a
	// placeholder never renders the tail.
	echoUnverifiable
	// echoAbsent means no marker and no sign that anything arrived.
	echoAbsent
)

// promptEchoTimeout bounds how long the runner waits for the pasted tail to be
// rendered. It is a variable so tests need not wait it out.
var promptEchoTimeout = 3 * time.Second

// pasteCollapsed matches the placeholder TUIs substitute for a large paste, for
// example "[Pasted text #1 +2103 lines]". Such a pane never renders the tail of
// the paste, so the echo marker cannot be found however long the runner waits.
var pasteCollapsed = regexp.MustCompile(`(?i)\[[^\]]*paste[^\]]*\]`)

// verifyPromptEcho decides whether the pasted prompt reached the pane, before
// Enter is pressed rather than after.
//
// The marker is this round's own, so finding it proves more than a screen diff
// can: it is the tail of this paste, at the cursor, not a leftover from an
// earlier round. Not finding it is weaker evidence, because a pane may collapse
// a large paste into a placeholder or keep a tall composer scrolled away — hence
// the middle verdict. The last capture is returned so the caller can use it as
// the pre-Enter reference without racing a repaint.
func verifyPromptEcho(ctx context.Context, ex tmux.Executor, paneID, marker, before string) (promptEchoState, string, error) {
	if marker == "" {
		return echoSkipped, "", nil
	}
	deadline := time.NewTimer(promptEchoTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(submitConfirmPollInterval)
	defer ticker.Stop()
	last := ""
	for {
		content, err := ex.CaptureScreenClean(paneID)
		if err == nil {
			last = content
			if strings.Contains(content, marker) {
				return echoFound, last, nil
			}
		}
		select {
		case <-ctx.Done():
			return echoAbsent, last, ctx.Err()
		case <-deadline.C:
			// Establishing nothing is not the same as establishing absence: with
			// no capture at all, or no pre-paste screen to compare against, the
			// runner has no grounds to call the paste lost.
			if last == "" || before == "" {
				return echoUnverifiable, last, nil
			}
			if pasteCollapsed.MatchString(last) || differingRows(last, before) >= echoMinDifferingRows {
				return echoUnverifiable, last, nil
			}
			return echoAbsent, last, nil
		case <-ticker.C:
		}
	}
}

// echoMinDifferingRows is the fallback evidence that a paste landed when its
// tail is not rendered. One row is not enough: a repainting footer changes that
// much on its own, while a pasted prompt fills the composer.
const echoMinDifferingRows = 2

func promptEchoAbsentError(paneID, marker string) error {
	return fmt.Errorf("pane %s 沒有出現本輪 prompt 的送達標記 %s，畫面也看不出收到任何內容；判定 paste 沒有進到 agent 的輸入區，不送出 Enter。請確認 pane 目標正確、agent 是否被 modal 或 copy-mode 攔住", paneID, marker)
}

// submitResult is what the send step observed, so a later stall can be
// attributed to delivery rather than to the agent.
type submitResult struct {
	// PendingScreen is the capture taken with the prompt sitting in the pane's
	// input area, immediately before Enter. Empty when it could not be taken,
	// which disables the pending-input watch for that round rather than
	// guessing.
	PendingScreen string
	Confirmed     bool
	Attempts      int
}

// submitObservation is one Enter attempt's evidence.
type submitObservation struct {
	// NonChromeChange means rows that are not continuously repainting differ
	// from the pending screen. Submitting empties the input area and moves the
	// prompt into the transcript, so this is what a consumed prompt looks like.
	NonChromeChange bool
	// RawChange means any byte of the capture differs. On its own this proves
	// nothing: agent TUIs repaint elapsed timers, token counters and rotating
	// footer hints forever, so an unsubmitted prompt produces a different
	// capture on every poll.
	RawChange bool
	// Conclusive reports whether the pane was watched long enough to separate
	// repainting chrome from real output. Without it NonChromeChange is only
	// RawChange under another name.
	Conclusive bool
}

const (
	// submitRepaintMinChanges is looser than the idle filter's threshold. This
	// decision compares against a known reference screen rather than deciding
	// whether a pane is idle, so a row that has already moved twice is enough
	// to treat it as repainting.
	submitRepaintMinChanges = 2
	// submitStructuralRows is how many differing rows count as an accepted
	// prompt without waiting for chrome to be identified at all: repainting
	// decoration is a row or two, while a consumed input area empties the
	// composer and grows the transcript at once. It keeps a healthy send fast.
	submitStructuralRows = 5
)

var (
	// submitConfirmPollInterval and submitChromeSettle are variables so tests
	// can compress the observation the chrome filter needs.
	submitConfirmPollInterval = 200 * time.Millisecond
	// submitChromeSettle is how long the pane must be watched before a
	// chrome-aware verdict is possible. A row is only recognisable as chrome
	// once it has been seen repainting, so this has to outlast the update
	// period of the slowest such row — the one-second elapsed timers agent TUIs
	// draw. Below this the runner has no more information than "something
	// changed", and says so instead of guessing.
	submitChromeSettle = 4 * time.Second
)

// observeSubmission watches a pane after Enter and reports what changed.
func observeSubmission(ctx context.Context, ex tmux.Executor, paneID, pending string, window time.Duration) (submitObservation, error) {
	var volatility screenVolatility
	// Seed the row history with the state Enter was pressed against, so the
	// first real comparison already describes the transition.
	volatility.observe(pending, roundControlProtocol{})

	deadline := time.NewTimer(window)
	defer deadline.Stop()
	ticker := time.NewTicker(submitConfirmPollInterval)
	defer ticker.Stop()

	observation := submitObservation{}
	failures := 0
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return observation, ctx.Err()
		case <-deadline.C:
			observation.Conclusive = time.Since(start) >= submitChromeSettle
			return observation, nil
		case <-ticker.C:
		}
		content, err := ex.CaptureScreenClean(paneID)
		if err != nil {
			// A pane can be briefly unavailable while tmux updates a window.
			// Repeated failures mean the verdict cannot be trusted either way.
			if failures++; failures >= 3 {
				return observation, nil
			}
			continue
		}
		failures = 0
		volatility.observe(content, roundControlProtocol{})
		if content != pending {
			observation.RawChange = true
		}
		if differingRows(content, pending) >= submitStructuralRows {
			observation.NonChromeChange, observation.Conclusive = true, true
			return observation, nil
		}
		if time.Since(start) < submitChromeSettle {
			continue
		}
		observation.Conclusive = true
		if inputAreaConsumed(&volatility, content, pending) {
			observation.NonChromeChange = true
			return observation, nil
		}
	}
}

// inputAreaConsumed compares a capture against the screen Enter was pressed
// against, ignoring rows that have been repainting on their own.
func inputAreaConsumed(volatility *screenVolatility, current, pending string) bool {
	current, pending = bottomAligned(current, pending)
	mask := volatility.repaintingRows(submitRepaintMinChanges)
	if countMasked(mask)*volatileMaxDenominator > len(mask)*volatileMaxNumerator {
		// Most of the pane is in motion, which is what real output looks like.
		// Explaining that away as decoration would invert the answer.
		return current != pending
	}
	return applyVolatileMask(current, mask) != applyVolatileMask(pending, mask)
}

// differingRows counts how many bottom-aligned rows differ between two
// captures.
func differingRows(current, reference string) int {
	current, reference = bottomAligned(current, reference)
	currentLines := strings.Split(current, "\n")
	referenceLines := strings.Split(reference, "\n")
	differing := 0
	for i := range currentLines {
		if currentLines[i] != referenceLines[i] {
			differing++
		}
	}
	return differing
}

// bottomAligned trims two captures to the rows they have in common, counted
// from the bottom. A pane holds its input area and footer at the same distance
// from the bottom whether or not rows were added above them, so aligning that
// way keeps a comparison about content rather than about height.
func bottomAligned(a, b string) (string, string) {
	aLines, bLines := strings.Split(a, "\n"), strings.Split(b, "\n")
	shared := len(aLines)
	if len(bLines) < shared {
		shared = len(bLines)
	}
	return strings.Join(aLines[len(aLines)-shared:], "\n"), strings.Join(bLines[len(bLines)-shared:], "\n")
}

func submitNotConfirmedError(paneID string, attempts int, observation submitObservation) error {
	if observation.RawChange {
		return fmt.Errorf("pane %s 在 %d 次 Enter 後只出現持續重繪的 UI chrome 變化，輸入區的 prompt 沒有被消化；判定 Enter 未生效，為安全停止。可調整 --submit-delay/--submit-retries，或確認該 pane 是否有 modal 攔住輸入", paneID, attempts)
	}
	return fmt.Errorf("pane %s 在 %d 次 Enter 後畫面完全沒有變化；判定 prompt 未被送進 agent，為安全停止", paneID, attempts)
}

// pendingInputWatch decides, over a whole round rather than the few seconds a
// submit confirmation can afford, whether the pane ever consumed this round's
// prompt. Its evidence is the strongest available: the pane still shows exactly
// what it showed while the prompt sat unsubmitted in the input area, with only
// repainting chrome differing.
type pendingInputWatch struct {
	pending string
	// startedAt is when the round began waiting; since restarts with every
	// nudge so each extra Enter gets its own observation window.
	startedAt time.Time
	since     time.Time
	nudges    int
	announced bool
	// consumed latches once the pane has moved on, so a screen that later
	// happens to render like the pending one is never re-read as unsubmitted.
	consumed bool
	// volatility tracks the visible screen only. The completion detector's own
	// tracker follows captures that include scrollback, which answers a
	// different question and cannot be compared against a pre-Enter screen.
	volatility screenVolatility
}

func newPendingInputWatch(submit submitResult, now time.Time) *pendingInputWatch {
	if submit.PendingScreen == "" || ppPendingInputTimeout <= 0 {
		return &pendingInputWatch{consumed: true}
	}
	return &pendingInputWatch{pending: submit.PendingScreen, startedAt: now, since: now}
}

// stillPending reports whether the pane still shows what it showed before
// Enter, once its own repainting rows are set aside.
func (w *pendingInputWatch) stillPending(ex tmux.Executor, paneID string) bool {
	if w.consumed {
		return false
	}
	content, err := ex.CaptureScreenClean(paneID)
	if err != nil {
		// This watch is an extra signal, never the authority: a capture failure
		// must not turn a healthy round into a delivery error.
		return false
	}
	w.volatility.observe(content, roundControlProtocol{})
	// Until chrome has been identified every capture differs from the pending
	// screen, because a footer clock is enough to change one row. Latching on
	// that would retire the watch on its first tick and detect nothing.
	if !w.volatility.warmedUp() {
		return false
	}
	if inputAreaConsumed(&w.volatility, content, w.pending) {
		w.consumed = true
		return false
	}
	return true
}

func (w *pendingInputWatch) expired(now time.Time) bool {
	return now.Sub(w.since) >= time.Duration(ppPendingInputTimeout)*time.Second
}

// nudge presses Enter again. This is only ever reached while the pane is
// byte-identical to its pre-Enter state outside of chrome, which is what makes
// the extra key safe: nothing new has been rendered, so there is no dialog or
// prompt on screen for it to answer. A confirmation dialog would itself be a
// non-chrome change and would have ended the watch.
func (w *pendingInputWatch) nudge(ex tmux.Executor, paneID string, now time.Time) error {
	if err := ensurePaneAcceptsInput(ex, paneID); err != nil {
		return fmt.Errorf("pane %s 的本輪 prompt 未被提交，且無法補送 Enter：%w", paneID, err)
	}
	if _, err := ex.Exec("send-keys", "-t", paneID, "Enter"); err != nil {
		return fmt.Errorf("pane %s 的本輪 prompt 未被提交，補送 Enter 失敗：%w", paneID, err)
	}
	w.nudges++
	w.since = now
	return nil
}

func (w *pendingInputWatch) exhausted() bool { return w.nudges >= ppPendingInputNudges }

func (w *pendingInputWatch) err(paneID string, now time.Time) error {
	return fmt.Errorf("pane %s 的本輪 prompt 沒有被 agent 接收：送出後 %s 畫面只有持續重繪的 chrome 變化，其餘部分與 prompt 尚未提交時完全相同（已補送 %d 次 Enter）。請確認該 pane 的 agent 是否在等待輸入，或是否被 modal、copy-mode 攔住",
		paneID, now.Sub(w.startedAt).Round(time.Second), w.nudges)
}
