package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/llmore/tmux-ai/internal/timeparser"
)

func (e *Executor) runWaitStep(_ context.Context, s *Step) (string, error) {
	untilExpr := e.expand(s.Until)
	if untilExpr == "" {
		return "", fmt.Errorf("wait step %q: 'until' field is required", s.Name)
	}

	target, err := timeparser.Parse(untilExpr)
	if err != nil {
		return "", fmt.Errorf("wait step %q: %w", s.Name, err)
	}

	wait := time.Until(target)
	if wait <= 0 {
		return fmt.Sprintf("時間已過（%s），跳過等待", target.Format("15:04:05")), nil
	}

	fmt.Printf("\033[36m[wait]\033[0m  等待至 %s\n", timeparser.Format(target))

	timer := time.NewTimer(wait)
	defer timer.Stop()
	<-timer.C

	return fmt.Sprintf("等待完成（%s）", target.Format("15:04:05")), nil
}
