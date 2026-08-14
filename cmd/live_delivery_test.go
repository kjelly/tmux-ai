package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

// These tests check prompt delivery against a real tmux server and a real
// repainting TUI, because that is the only place the assumption underneath the
// detection can be checked: that an unsubmitted prompt is distinguishable from
// a working agent in what tmux actually captures. They are opt-in — they need a
// tmux server, python3, and about a minute — so run them with
// TMUXAI_LIVE_TMUX=1 go test ./cmd/ -run TestLiveDelivery.
//
// fakeAgentScript stands in for a coding-agent TUI: it repaints an elapsed-time
// footer forever, and its submit behaviour is selectable. In "silent" mode it
// reads input without ever displaying it, which is the one shape of pane the
// paste check cannot clear — see the test that pins that behaviour.
const fakeAgentScript = `
import os, sys, termios, time, tty

mode = sys.argv[1]
fd = sys.stdin.fileno()
old = termios.tcgetattr(fd)
tty.setraw(fd)
os.set_blocking(fd, False)

composer, transcript, submits = "", [], 0
start = time.time()
try:
    while True:
        buf = sys.stdin.buffer.read()
        if buf:
            for byte in buf:
                ch = chr(byte)
                if ch in ("\r", "\n"):
                    submits += 1
                    accept = mode == "submit" or (mode == "swallow-once" and submits > 1)
                    if accept and composer.strip():
                        transcript.append("user: " + composer.strip()[:60])
                        transcript.append("assistant: working on it")
                        composer = ""
                    continue
                if ch == "\x1b":
                    continue
                composer += ch
        if mode == "silent":
            rows = [""]
        else:
            rows = transcript[-4:] + ["> " + composer[-60:]]
        while len(rows) < 6:
            rows.insert(0, "")
        rows.append("  %ds - esc to interrupt" % int(time.time() - start))
        sys.stdout.write("\x1b[H\x1b[J" + "\r\n".join(rows))
        sys.stdout.flush()
        time.sleep(0.2)
except Exception:
    pass
finally:
    termios.tcsetattr(fd, termios.TCSADRAIN, old)
`

func livePane(t *testing.T, mode string) (tmux.Executor, string) {
	t.Helper()
	if os.Getenv("TMUXAI_LIVE_TMUX") != "1" {
		t.Skip("set TMUXAI_LIVE_TMUX=1 to run the live tmux delivery tests")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for the fake agent TUI")
	}
	script := filepath.Join(t.TempDir(), "fake_agent.py")
	if err := os.WriteFile(script, []byte(fakeAgentScript), 0o600); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	session := "tmuxai-live-" + mode + "-" + strings.ReplaceAll(t.Name(), "/", "-")
	ex := tmux.NewExecutorWithOptions(tmux.Options{PasteChunkRunes: 500, PasteChunkDelay: 0})
	_, _ = ex.Exec("kill-session", "-t", session)
	if _, err := ex.Exec("new-session", "-d", "-s", session, "-x", "80", "-y", "12", "python3 "+script+" "+mode); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	t.Cleanup(func() { _, _ = ex.Exec("kill-session", "-t", session) })
	// Let the TUI paint its first frames so the pending capture is a settled one.
	time.Sleep(time.Second)
	target, err := ex.ResolveTarget(session)
	if err != nil {
		t.Fatalf("resolve %s: %v", session, err)
	}
	return ex, target
}

func liveDefaults(t *testing.T) {
	t.Helper()
	old := []int{ppLines, ppSubmitDelay, ppSubmitConfirmTimeout, ppSubmitRetries, ppInterval, ppRoundTimeout, ppReceiptIdleTimeout, ppScreenIdleTimeout, ppPendingInputTimeout, ppPendingInputNudges}
	oldShell := ppAllowShellPane
	ppLines, ppSubmitDelay, ppSubmitConfirmTimeout, ppSubmitRetries = 100, 200, 8000, 0
	ppInterval, ppRoundTimeout, ppReceiptIdleTimeout, ppScreenIdleTimeout = 500, 20, 0, 0
	ppPendingInputTimeout, ppPendingInputNudges, ppAllowShellPane = 5, 1, false
	t.Cleanup(func() {
		ppLines, ppSubmitDelay, ppSubmitConfirmTimeout, ppSubmitRetries = old[0], old[1], old[2], old[3]
		ppInterval, ppRoundTimeout, ppReceiptIdleTimeout, ppScreenIdleTimeout = old[4], old[5], old[6], old[7]
		ppPendingInputTimeout, ppPendingInputNudges, ppAllowShellPane = old[8], old[9], oldShell
	})
}

func TestLiveDeliverySubmitConfirmation(t *testing.T) {
	t.Run("swallowed Enter is reported instead of confirmed", func(t *testing.T) {
		liveDefaults(t)
		ex, target := livePane(t, "swallow")
		result, err := sendToPane(context.Background(), ex, target, "please review the diff and report", "")
		if err == nil || !strings.Contains(err.Error(), "只出現持續重繪的 UI chrome 變化") {
			t.Fatalf("sendToPane() error = %v (confirmed=%v)", err, result.Confirmed)
		}
	})

	t.Run("healthy submit is confirmed", func(t *testing.T) {
		liveDefaults(t)
		ex, target := livePane(t, "submit")
		result, err := sendToPane(context.Background(), ex, target, "please review the diff and report", "")
		if err != nil || !result.Confirmed {
			screen, _ := ex.CaptureScreenClean(target)
			t.Fatalf("sendToPane() error = %v, confirmed = %v, screen:\n%s", err, result.Confirmed, screen)
		}
	})

	t.Run("copy-mode is cancelled so the paste is not discarded", func(t *testing.T) {
		liveDefaults(t)
		ex, target := livePane(t, "submit")
		if _, err := ex.Exec("copy-mode", "-t", target); err != nil {
			t.Fatalf("copy-mode: %v", err)
		}
		if _, err := sendToPane(context.Background(), ex, target, "please review the diff and report", ""); err != nil {
			t.Fatalf("sendToPane() error = %v", err)
		}
		screen, err := ex.CaptureScreenClean(target)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if !strings.Contains(screen, "user: please review the diff") {
			t.Fatalf("prompt never reached the agent, screen:\n%s", screen)
		}
	})
}

func TestLiveDeliveryPendingInputWatch(t *testing.T) {
	t.Run("a prompt the agent never consumed is named as such", func(t *testing.T) {
		liveDefaults(t)
		// With confirmation disabled the round-long watch is the only line of
		// defence, which is exactly what this checks.
		ppSubmitConfirmTimeout = 0
		ex, target := livePane(t, "swallow")
		baseline, err := ex.CapturePaneClean(target, ppLines)
		if err != nil {
			t.Fatalf("baseline: %v", err)
		}
		submit, err := sendToPane(context.Background(), ex, target, "please review the diff and report", "")
		if err != nil {
			t.Fatalf("sendToPane() error = %v", err)
		}
		protocol := newRoundControlProtocol(handoffTransportText, "live-unsent", 1, "", true)
		_, _, _, err = waitForCompletionWithFallback(context.Background(), ex, target, baseline, protocol, nil, nil, submit)
		if err == nil || !strings.Contains(err.Error(), "沒有被 agent 接收") {
			t.Fatalf("waitForCompletionWithFallback() error = %v", err)
		}
	})

	t.Run("re-sent Enter recovers a swallowed submit", func(t *testing.T) {
		liveDefaults(t)
		ppSubmitConfirmTimeout = 0
		ex, target := livePane(t, "swallow-once")
		baseline, err := ex.CapturePaneClean(target, ppLines)
		if err != nil {
			t.Fatalf("baseline: %v", err)
		}
		submit, err := sendToPane(context.Background(), ex, target, "please review the diff and report", "")
		if err != nil {
			t.Fatalf("sendToPane() error = %v", err)
		}
		protocol := newRoundControlProtocol(handoffTransportText, "live-nudge", 1, "", true)
		_, _, _, err = waitForCompletionWithFallback(context.Background(), ex, target, baseline, protocol, nil, nil, submit)
		// The fake agent never prints a completion marker, so the round still
		// times out — but on the agent's silence, not on delivery, and the
		// diagnosis has to say the prompt needed an extra Enter.
		if err == nil || strings.Contains(err.Error(), "沒有被 agent 接收") {
			t.Fatalf("waitForCompletionWithFallback() error = %v", err)
		}
		if !strings.Contains(err.Error(), "已補送 Enter 1 次") {
			t.Fatalf("stall diagnosis lost the nudge: %v", err)
		}
		screen, err := ex.CaptureScreenClean(target)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if !strings.Contains(screen, "user: please review the diff") {
			t.Fatalf("nudge did not deliver the prompt, screen:\n%s", screen)
		}
	})
}

func TestLiveDeliveryPasteVerification(t *testing.T) {
	const echo = "TMUXAI_PROMPT_ECHO__TMUXAI_live_1"

	t.Run("the pasted tail is found in a real composer", func(t *testing.T) {
		liveDefaults(t)
		ex, target := livePane(t, "submit")
		// One line: this stand-in submits on any newline, and the point here is
		// the paste rather than the submit.
		result, err := sendToPane(context.Background(), ex, target, "please review the diff "+echo, echo)
		if err != nil || !result.Confirmed {
			screen, _ := ex.CaptureScreenClean(target)
			t.Fatalf("sendToPane() error = %v, confirmed = %v, screen:\n%s", err, result.Confirmed, screen)
		}
	})

	t.Run("a pane that shows no sign of the paste stops before Enter", func(t *testing.T) {
		liveDefaults(t)
		ex, target := livePane(t, "silent")
		_, err := sendToPane(context.Background(), ex, target, "please review the diff "+echo, echo)
		if err == nil || !strings.Contains(err.Error(), echo) {
			t.Fatalf("sendToPane() error = %v", err)
		}
	})

	// A pane that reads input without echoing it is indistinguishable from one
	// that never received it, and the runner must not pretend otherwise. Such a
	// destination needs the check turned off, which is what --input-echo-check=off
	// is for.
	t.Run("a silent pane can still be driven with the check off", func(t *testing.T) {
		liveDefaults(t)
		old := ppInputEchoCheck
		ppInputEchoCheck = inputEchoOff
		t.Cleanup(func() { ppInputEchoCheck = old })
		ex, target := livePane(t, "silent")
		if _, err := sendToPane(context.Background(), ex, target, "please review the diff "+echo, echo); err == nil {
			t.Fatal("a pane that renders nothing cannot confirm submission either; want the submit-confirmation error")
		} else if !strings.Contains(err.Error(), "畫面完全沒有變化") && !strings.Contains(err.Error(), "只出現持續重繪的 UI chrome 變化") {
			t.Fatalf("sendToPane() error = %v, want a submit-confirmation verdict rather than a paste verdict", err)
		}
	})
}

func TestLiveDeliveryConcurrentNamedBuffers(t *testing.T) {
	liveDefaults(t)
	firstExecutor, first := livePane(t, "submit")
	secondExecutor, second := livePane(t, "alternate")
	firstNonce := "TMUXAI-LIVE-FIRST-UNIQUE"
	secondNonce := "TMUXAI-LIVE-SECOND-UNIQUE"
	firstPayload := strings.Repeat("a", 64*1024) + "\n" + firstNonce
	secondPayload := strings.Repeat("b", 64*1024) + "\n" + secondNonce
	var wg sync.WaitGroup
	var firstErr, secondErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		firstErr = firstExecutor.SendTextContext(context.Background(), first, firstPayload)
	}()
	go func() {
		defer wg.Done()
		secondErr = secondExecutor.SendTextContext(context.Background(), second, secondPayload)
	}()
	wg.Wait()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("concurrent paste errors: first=%v second=%v", firstErr, secondErr)
	}
	time.Sleep(time.Second)
	firstScreen, err := firstExecutor.CaptureScreenClean(first)
	if err != nil {
		t.Fatal(err)
	}
	secondScreen, err := secondExecutor.CaptureScreenClean(second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstScreen, firstNonce) || strings.Contains(firstScreen, secondNonce) {
		t.Fatalf("first pane received crossed payload:\n%s", firstScreen)
	}
	if !strings.Contains(secondScreen, secondNonce) || strings.Contains(secondScreen, firstNonce) {
		t.Fatalf("second pane received crossed payload:\n%s", secondScreen)
	}
	buffers, err := firstExecutor.Exec("list-buffers", "-F", "#{buffer_name}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffers, "tmux-ai-") {
		t.Fatalf("named buffers were not removed: %q", buffers)
	}
}
