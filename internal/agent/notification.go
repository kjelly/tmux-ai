package agent

import (
	"fmt"
	"os"
	"strings"
)

// sendTerminalNotification emits OSC escape sequences for desktop
// notifications, compatible with various terminal emulators. When running
// inside tmux, the sequences are wrapped in passthrough envelopes.
func (r *Runner) sendTerminalNotification(title, message string) {
	osc99 := fmt.Sprintf("\x1b]9;9;%s: %s\x07", title, message)
	osc9 := fmt.Sprintf("\x1b]9;%s: %s\x07", title, message)
	osc777 := fmt.Sprintf("\x1b]777;notify;%s;%s\x07", title, message)

	if os.Getenv("TMUX") != "" {
		osc99 = wrapTmuxPassthrough(osc99)
		osc9 = wrapTmuxPassthrough(osc9)
		osc777 = wrapTmuxPassthrough(osc777)
	}

	fmt.Print(osc99)
	fmt.Print(osc9)
	fmt.Print(osc777)
}

// wrapTmuxPassthrough wraps an escape sequence in a tmux DCS passthrough
// envelope so it reaches the outer terminal.
func wrapTmuxPassthrough(seq string) string {
	escaped := strings.ReplaceAll(seq, "\x1b", "\x1b\x1b")
	return "\x1bPtmux;" + escaped + "\x1b\\"
}
