package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// runWithRetry retries an entire workflow after a failure that escaped its
// round-level recovery. The operation is rebuilt by the caller so a restart
// gets a fresh engine/guard state instead of reusing partially consumed state.
//
// Keep this wording distinct from askRetry's "retry this round" prompt: the
// two operations have deliberately different scopes, and treating a restart
// as a round retry can send the initial task to a pane twice.
func runWithRetry(ctx context.Context, run func() error, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := run(); err == nil {
			return nil
		} else {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(out, "\n工作流程發生錯誤：%v\n", err)
			fmt.Fprint(out, "是否要從頭重啟整個 workflow？[y/N] ")
		}

		lineCh := make(chan string, 1)
		errCh := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil && len(line) == 0 {
				errCh <- err
				return
			}
			lineCh <- line
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			// EOF/non-interactive stdin means there is no safe retry decision.
			return err
		case line := <-lineCh:
			answer := strings.ToLower(strings.TrimSpace(line))
			if answer != "y" && answer != "yes" && answer != "是" && answer != "重試" {
				return fmt.Errorf("workflow aborted after error: retry declined")
			}
		}
	}
}

func runWithInteractiveRetry(ctx context.Context, run func() error) error {
	return runWithRetry(ctx, run, os.Stdin, os.Stderr)
}

type roundRetryAction uint8

const (
	roundRetryAbort roundRetryAction = iota
	roundRetryWait
	roundRetryLLMRecovery
	roundRetryResendPrompt
	roundRetryCancelWorkflow
)

func askRetry(ctx context.Context, err error) bool {
	return askRoundRetryAction(ctx, err, false, false) == roundRetryWait
}

// askPendingRelayRetry is intentionally separate from the ordinary round
// menu. A source round that has already produced a validated result must never
// be replayed merely because delivery to the next pane failed.
func askPendingRelayRetry(ctx context.Context, err error, next handoffMember, interrupt <-chan os.Signal) bool {
	return askPendingRelayRetryIO(ctx, err, next, interrupt, os.Stdin, os.Stderr)
}

func askPendingRelayRetryIO(ctx context.Context, err error, next handoffMember, interrupt <-chan os.Signal, in io.Reader, out io.Writer) bool {
	if ctx.Err() != nil {
		return false
	}
	fmt.Fprintf(out, "\n交接給 %s (%s) 失敗：%v\n", handoffMemberDisplayName(next), next.Target, err)
	fmt.Fprint(out, "輸入 r 只會重送這份待交接訊息給目標 pane（使用新的送達標記）；不會重新執行來源 pane、重新收集 artifact，或重跑 coordinator。是否重送？[r/N] ")
	type readResult struct {
		line string
		err  error
	}
	lineCh := make(chan readResult, 1)
	go func() {
		line, readErr := bufio.NewReader(in).ReadString('\n')
		lineCh <- readResult{line: line, err: readErr}
	}()
	for {
		select {
		case <-ctx.Done():
			return false
		case sig := <-interrupt:
			if sig == os.Interrupt {
				return false
			}
		case result := <-lineCh:
			if result.err != nil && len(result.line) == 0 {
				return false
			}
			switch strings.ToLower(strings.TrimSpace(result.line)) {
			case "r", "resend", "重送":
				return true
			default:
				return false
			}
		}
	}
}

// askRoundRetryAction gives eligible receipt failures and long-static panes an
// explicit, opt-in recovery path. The LLM option is only offered by callers
// that can turn a captured pane into locally validated control data; it never
// resends the original task.
func askRoundRetryAction(ctx context.Context, err error, allowLLMRecovery, allowPromptResend bool) roundRetryAction {
	return askRoundRetryActionWithSignal(ctx, err, allowLLMRecovery, allowPromptResend, nil)
}

func askRoundRetryActionWithSignal(ctx context.Context, err error, allowLLMRecovery, allowPromptResend bool, interrupt <-chan os.Signal) roundRetryAction {
	return askRoundRetryActionWithSignalIO(ctx, err, allowLLMRecovery, allowPromptResend, interrupt, os.Stdin, os.Stderr)
}

func askRoundRetryActionWithSignalIO(ctx context.Context, err error, allowLLMRecovery, allowPromptResend bool, interrupt <-chan os.Signal, in io.Reader, out io.Writer) roundRetryAction {
	if ctx.Err() != nil {
		return roundRetryAbort
	}
	fmt.Fprint(out, roundRetryPromptForOptions(err, allowLLMRecovery, allowPromptResend))
	type readResult struct {
		line string
		err  error
	}
	lineCh := make(chan readResult, 1)
	go func() {
		line, readErr := bufio.NewReader(in).ReadString('\n')
		lineCh <- readResult{line: line, err: readErr}
	}()
	for {
		select {
		case <-ctx.Done():
			return roundRetryAbort
		case sig := <-interrupt:
			if sig == os.Interrupt {
				return roundRetryCancelWorkflow
			}
			// SIGUSR1 asks to open the menu. It is already open, so keep waiting
			// for a real answer instead of turning it into an abort.
		case result := <-lineCh:
			if result.err != nil && len(result.line) == 0 {
				return roundRetryAbort
			}
			return parseRoundRetryAction(result.line, allowLLMRecovery, allowPromptResend)
		}
	}
}

func parseRoundRetryAction(input string, allowLLMRecovery, allowPromptResend bool) roundRetryAction {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "y", "yes", "是", "重試":
		return roundRetryWait
	case "l", "llm", "ai":
		if allowLLMRecovery {
			return roundRetryLLMRecovery
		}
	case "r", "resend", "重送":
		if allowPromptResend {
			return roundRetryResendPrompt
		}
	}
	return roundRetryAbort
}

// roundRetryPrompt makes the safe default scope explicit. The expanded prompt
// may offer an explicit `r` override, because replaying an implementer prompt
// can run a non-idempotent operation twice.
func roundRetryPrompt(err error) string {
	return roundRetryPromptForOptions(err, false, false)
}

func roundRetryPromptForOptions(err error, allowLLMRecovery, allowPromptResend bool) string {
	prompt := fmt.Sprintf("\nRound 失敗：%v\n輸入 y 只會重新等待目前 pane；不會重送任務或重新執行 agent。", err)
	options := []string{"y"}
	if allowLLMRecovery {
		prompt += "\n輸入 l 讓 LLM 只讀取目前 pane 畫面，判斷 receipt 是否可修復或靜止畫面是否已完成；不會送訊息給 agent 或重做任務。靜止畫面只會交給 coordinator 複核，不會直接停止 workflow。"
		options = append(options, "l")
	}
	if allowPromptResend {
		prompt += "\n輸入 r 重送本輪原始 prompt；agent 可能再次執行工作，請只在確認它尚未收到或未處理 prompt 時使用。"
		options = append(options, "r")
	} else {
		prompt += "若需要修正 agent 輸出，請先在 pane 處理後再繼續等待。"
	}
	return fmt.Sprintf("%s\n是否繼續等待此 round？[%s/N] ", prompt, strings.Join(options, "/"))
}
