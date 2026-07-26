package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/artifact"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
)

// runSendStep dispatches an instruction directly to the target pane. Unlike
// runAgentStep, it does not ask an Ollama planner to decide whether or how to
// send the instruction. This is intended for workflows whose target pane is
// already running an interactive agent.
func (e *Executor) runSendStep(ctx context.Context, s *Step) (string, error) {
	target := e.resolvePane(s, tmuxutil.NewResolver())
	if target == "" {
		return "", fmt.Errorf("send step %q has no target pane", s.Name)
	}
	instruction := e.expand(s.Instruction)
	if instruction == "" {
		return "", fmt.Errorf("send step %q has an empty instruction", s.Name)
	}
	if extra := e.buildExtraContext(s); extra != "" {
		instruction = extra + "\n\n" + instruction
	}
	outbox := ""
	if e.artifactStore != nil {
		var err error
		outbox, err = e.artifactStore.NewOutbox(s.Name, 1)
		if err != nil {
			return "", fmt.Errorf("create artifact outbox for %s: %w", s.Name, err)
		}
		instruction = strings.Join([]string{
			"FILE HANDOFF RULES (runner-enforced):",
			"Write handoff artifacts only inside this outbox:",
			outbox,
			"Create at least one concise Markdown handoff file before declaring this step complete.",
			"Do not claim a file unless it exists in that outbox.",
			"",
			instruction,
		}, "\n")
	}

	ex := tmux.NewExecutor()
	baseline, err := ex.CapturePaneClean(target, s.Lines)
	if err != nil {
		return "", fmt.Errorf("capture target pane %s before send: %w", target, err)
	}
	if err := sendWorkflowInstruction(ctx, ex, target, instruction); err != nil {
		return "", err
	}

	// A direct send is useful even when the target agent does not emit a
	// workflow marker. Wait for visible output and then for it to settle so a
	// dependent review step does not start immediately after the paste.
	interval := time.Duration(s.Interval) * time.Millisecond
	if interval <= 0 {
		interval = 2 * time.Second
	}
	stablePolls := 3
	deadline := time.Now().Add(time.Duration(e.timeout) * time.Second)
	if s.Timeout > 0 {
		deadline = time.Now().Add(time.Duration(s.Timeout) * time.Second)
	}
	changed := false
	stable := 0
	last := baseline
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for target pane %s after direct send", target)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
		content, captureErr := ex.CapturePaneClean(target, s.Lines)
		if captureErr != nil {
			continue
		}
		if content == baseline {
			continue
		}
		changed = true
		if content == last {
			stable++
		} else {
			stable = 0
		}
		last = content
		if changed && stable >= stablePolls {
			if e.artifactStore == nil {
				return content, nil
			}
			refs, collectErr := e.artifactStore.Collect(s.Name, 1, outbox, nil)
			if collectErr != nil {
				return "", fmt.Errorf("collect artifacts from %s: %w", outbox, collectErr)
			}
			if len(refs) == 0 {
				return "", fmt.Errorf("send step %q completed without a handoff artifact in %s", s.Name, outbox)
			}
			return formatArtifactHandoff(refs), nil
		}
	}
}

func formatArtifactHandoff(refs []artifact.Ref) string {
	var b strings.Builder
	b.WriteString("FILE_HANDOFF_ARTIFACTS:\n")
	for _, ref := range refs {
		fmt.Fprintf(&b, "- id: %s\n  path: %s\n  producer: %s\n  title: %s\n  bytes: %d\n  sha256: %s\n", ref.ID, ref.RuntimePath, ref.ProducerID, ref.Title, ref.SizeBytes, ref.SHA256)
	}
	return strings.TrimRight(b.String(), "\n")
}

func sendWorkflowInstruction(ctx context.Context, ex tmux.Executor, target, instruction string) error {
	if err := ex.SendLinesContext(ctx, target, instruction); err != nil {
		return fmt.Errorf("send instruction to %s: %w", target, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := ex.Exec("send-keys", "-t", target, "Enter"); err != nil {
		return fmt.Errorf("submit instruction to %s: %w", target, err)
	}
	return nil
}
