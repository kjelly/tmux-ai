package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

type managedPRAgents struct {
	executor tmux.Executor
	options  prLoopOptions
	out      io.Writer

	checkout     string
	checkedOutPR int
	session      string
	panes        map[prPhase]string
	activePane   string
}

var prNumberedSelection = regexp.MustCompile("^[›❯>][ \\t]*[0-9]+[.)][ \\t]")

func managedPRStartupReady(screen string) bool {
	activity, _ := inspectPRPaneScreen(screen)
	if activity != prPanePrompt {
		return false
	}
	lines := strings.Split(strings.TrimRight(screen, " \t\r\n"), "\n")
	for _, line := range lines[max(0, len(lines)-6):] {
		if prNumberedSelection.MatchString(strings.TrimSpace(line)) {
			return false
		}
	}
	return true
}

func (a *managedPRAgents) send(ctx context.Context, number int, phase prPhase, prompt string) error {
	if a.activePane != "" {
		// The last agent can still be finishing its turn after changing PR state.
		if err := a.waitForActiveAgent(ctx); err != nil {
			return err
		}
	}
	if err := a.prepareCheckout(ctx, number); err != nil {
		return fmt.Errorf("%w: %w", errPRBranchPreparation, err)
	}
	pane := a.panes[phase]
	if pane == "" {
		var err error
		pane, err = a.startAgent(ctx, number, phase)
		if err != nil {
			return err
		}
	}
	if err := sendNativePRCommand(ctx, a.executor, pane, prompt, a.options.SubmitDelay); err != nil {
		return err
	}
	a.activePane = pane
	return nil
}

func (a *managedPRAgents) prepareCheckout(ctx context.Context, number int) error {
	target, err := queryPRCheckoutTarget(ctx, a.options.Repo, number)
	if err != nil {
		return err
	}
	if a.checkout == "" {
		baseURL, ok := strings.CutSuffix(target.URL, "/pull/"+strconv.Itoa(number))
		if !ok {
			return fmt.Errorf("cannot determine repository to clone from %q", target.URL)
		}
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		cache = filepath.Join(cache, "tmux-ai")
		if err := os.MkdirAll(cache, 0o700); err != nil {
			return err
		}
		a.checkout, err = os.MkdirTemp(cache, "pr-loop-")
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "Managed PR checkout: %s\n", a.checkout)
		if _, err := runPRCheckoutCommand(ctx, "", "gh", "repo", "clone", baseURL, a.checkout); err != nil {
			return err
		}
	}
	branch, err := runPRCheckoutCommand(ctx, a.checkout, "git", "branch", "--show-current")
	if err != nil {
		return err
	}
	if a.checkedOutPR != number || strings.TrimSpace(string(branch)) != target.HeadRefName {
		// A new PR may come from a fork whose head branch happens to have
		// the same name as a branch already in the working clone.
		if err := switchPRBranch(ctx, a.checkout, target, a.out); err != nil {
			return err
		}
		a.checkedOutPR = number
		return nil
	}
	status, err := runPRCheckoutCommand(ctx, a.checkout, "git", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return fmt.Errorf("managed checkout %q has uncommitted changes", a.checkout)
	}
	return nil
}

func (a *managedPRAgents) startAgent(ctx context.Context, number int, phase prPhase) (string, error) {
	program := a.options.ReadyAgent
	if phase == prDraft {
		program = a.options.DraftAgent
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return "", fmt.Errorf("find %s agent %q: %w", phase, program, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	paneName := string(phase)
	name := fmt.Sprintf("tmux-ai-pr-%d-%d", number, os.Getpid())
	command := "exec " + shellQuote(path)
	var response string
	if a.session == "" {
		response, err = a.executor.Exec("new-session", "-d", "-P", "-F", "#{session_id} #{pane_id}",
			"-s", name, "-n", paneName, "-c", a.checkout, "--", command)
	} else {
		response, err = a.executor.Exec("new-window", "-d", "-P", "-F", "#{pane_id}",
			"-t", a.session+":", "-n", paneName, "-c", a.checkout, "--", command)
	}
	if err != nil {
		return "", fmt.Errorf("launch %s agent: %w", phase, err)
	}
	ids := strings.Fields(response)
	if a.session == "" {
		if len(ids) != 2 || !strings.HasPrefix(ids[0], "$") || !strings.HasPrefix(ids[1], "%") {
			return "", fmt.Errorf("unexpected tmux session and pane IDs: %q", response)
		}
		a.session = ids[0]
		a.panes = make(map[prPhase]string, 2)
		ids = ids[1:]
		fmt.Fprintf(a.out, "PR #%d tmux session: %s\n", number, name)
	}
	if len(ids) != 1 || !strings.HasPrefix(ids[0], "%") {
		return "", fmt.Errorf("unexpected tmux pane ID: %q", response)
	}
	pane := ids[0]
	a.panes[phase] = pane
	fmt.Fprintf(a.out, "PR #%d %s agent %s started in pane %s.\n", number, phase, program, pane)
	if _, err := a.executor.Exec("set-option", "-w", "-t", pane, "remain-on-exit", "on"); err != nil {
		return "", fmt.Errorf("preserve agent pane %s: %w", pane, err)
	}
	if err := a.waitForStartup(ctx, pane); err != nil {
		return "", err
	}
	return pane, nil
}

func (a *managedPRAgents) waitForStartup(ctx context.Context, pane string) error {
	ctx, cancel := context.WithTimeout(ctx, a.options.AgentStartTimeout)
	defer cancel()
	poll := min(a.options.PollInterval, 250*time.Millisecond)
	for ctx.Err() == nil {
		state := readPaneState(a.executor, pane)
		if !state.Known || state.Dead {
			return fmt.Errorf("agent in pane %s exited or became unavailable during startup", pane)
		}
		if state.Command != "" && !state.InMode && !isShellCommand(state.Command) {
			screen, err := a.executor.CaptureScreenClean(pane)
			if err != nil {
				return fmt.Errorf("read agent startup in pane %s: %w", pane, err)
			}
			if managedPRStartupReady(screen) {
				return nil
			}
		}
		if err := waitPRPoll(ctx, poll); err != nil {
			break
		}
	}
	return fmt.Errorf("waiting for the launched agent's input prompt in pane %s: %w", pane, ctx.Err())
}

func (a *managedPRAgents) waitForActiveAgent(ctx context.Context) error {
	if readPaneState(a.executor, a.activePane).Dead {
		return nil
	}
	return waitForPRPaneScreen(ctx, a.executor, a.activePane, a.options.PollInterval, a.options.PhaseTimeout,
		"return to its input prompt", a.out, func(screen string) (bool, error) {
			return managedPRStartupReady(screen), nil
		})
}

func (a *managedPRAgents) finishPR(ctx context.Context) error {
	if a.activePane != "" {
		if err := a.waitForActiveAgent(ctx); err != nil {
			return err
		}
	}
	for _, phase := range []prPhase{prDraft, prReady} {
		if pane := a.panes[phase]; pane != "" {
			if err := a.executor.KillPane(pane); err != nil {
				return fmt.Errorf("close managed %s agent pane %s: %w", phase, pane, err)
			}
		}
	}
	a.session, a.activePane, a.panes = "", "", nil
	return nil
}

func (a *managedPRAgents) cleanupCheckout(ctx context.Context) {
	if a.checkout == "" {
		return
	}
	for _, args := range [][]string{
		{"status", "--porcelain=v1", "--untracked-files=normal"},
		{"rev-list", "--max-count=1", "HEAD", "--branches", "--not", "--remotes"},
		{"stash", "list"},
	} {
		data, err := runPRCheckoutCommand(ctx, a.checkout, "git", args...)
		if err != nil || len(data) != 0 {
			fmt.Fprintf(a.out, "Retained managed checkout at %s (local work is present or could not be ruled out).\n", a.checkout)
			return
		}
	}
	if err := os.RemoveAll(a.checkout); err != nil {
		fmt.Fprintf(a.out, "Could not remove managed checkout at %s: %v\n", a.checkout, err)
	}
}
