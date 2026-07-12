package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func (e *Executor) runShellStep(ctx context.Context, s *Step) (string, error) {
	command := e.expand(s.Command)
	stepCtx := ctx
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, time.Duration(s.Timeout)*time.Second)
		defer cancel()
	}
	out, err := exec.CommandContext(stepCtx, "bash", "-c", command).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w\noutput: %s", err, string(out))
	}
	return strings.TrimRight(string(out), "\n"), nil
}
