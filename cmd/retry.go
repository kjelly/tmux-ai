package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// runWithRetry keeps interactive handoff workflows alive after a runtime
// failure. The operation is rebuilt by the caller so a retry gets a fresh
// engine/guard state instead of reusing partially consumed round state.
func runWithRetry(ctx context.Context, run func() error, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	for {
		if err := run(); err == nil {
			return nil
		} else {
			fmt.Fprintf(out, "\n工作流程發生錯誤：%v\n", err)
			fmt.Fprint(out, "是否要重試？[y/N] ")
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

func askRetry(ctx context.Context, err error) bool {
	fmt.Fprintf(os.Stderr, "\nRound 失敗：%v\n是否重試此 round？[y/N] ", err)
	line, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
	if readErr != nil && len(line) == 0 {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	default:
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes" || answer == "是" || answer == "重試"
}
