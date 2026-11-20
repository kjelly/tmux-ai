package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

type prPhase string

const (
	prDraft  prPhase = "draft"
	prReady  prPhase = "ready"
	prQueued prPhase = "waiting for merge train"
	prMerged prPhase = "merged"
)

type prLoopOptions struct {
	Number                         int
	Repo, DraftPane, ReadyPane     string
	DraftAgent, ReadyAgent         string
	DraftCommand, ReadyCommand     string
	PollInterval, PhaseTimeout     time.Duration
	SubmitDelay, AgentStartTimeout time.Duration
	DiagnoseIdleAfter              time.Duration
	StablePolls, MaxActions        int
	Observe, DryRun, WatchPane     bool
	PilotSkills                    bool
}

func init() { rootCmd.AddCommand(newPRLoopCommand()) }

func newPRLoopCommand() *cobra.Command {
	var options prLoopOptions
	command := &cobra.Command{
		Use:         "pr-loop <PR-number> [PR-number...]",
		Short:       "Run native agent commands according to PR state: draft, ready, merged",
		Annotations: map[string]string{noLLMAnnotation: "true"},
		Long: `Poll GitHub PR state with gh, without an LLM.
Draft PRs receive --draft-command; ready PRs receive --ready-command; merged PRs are done.
Ready PRs labeled merge-queue wait for the merge train without dispatching a command.
PRs are processed in the given order. Each must merge before the next starts.
Use --draft-agent and --ready-agent to launch fresh agents per PR in a dedicated checkout.
Use --pilot-skills to launch from the verified default branch and let Pilot's skills own checkout and report results.
With prestarted panes, wait for both panes and send /new between PRs.
An error stops the batch. A PR that is already merged is skipped.
Before each draft or ready command, check out the PR branch in the target pane's local directory.
Switching branches requires a clean worktree; an already checked-out PR branch is kept.
If the branch is in another local worktree, append instructions to work there instead.
This guides the agent; it does not change the running agent's working directory.
Commands keep the native skill first and are pasted with Enter, without a role or receipt.
Each observed phase is dispatched once; unchanged state never resends a command.
When using --draft-pane and --ready-pane, start both agents before invoking pr-loop.
Use --observe to wait for the next state change on the first listed PR when resuming.
Use --watch-pane to report visible agent activity and alert on prompts that may need attention.
Use --diagnose-idle-after to optionally ask Ollama for a diagnostic when a pane stops progressing.
Without --repo, gh selects the repository from the working directory (or GH_REPO).
Bare window numbers select windows in the session containing $TMUX_PANE.

Example:
  tmux-ai pr-loop 333 334 335 \
    --draft-pane 1 --ready-pane 2 \
    --draft-command '/pr-ready {pr}' --ready-command '$pr-review-merge {pr}'`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			numbers, err := parsePRLoopNumbers(args)
			if err != nil {
				return err
			}
			runOptions := options
			if err := runOptions.validate(); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ex := tmux.NewExecutor()
			out := cmd.OutOrStdout()
			var diagnose func(context.Context, string) (prPaneDiagnosis, error)
			if runOptions.DiagnoseIdleAfter > 0 && !runOptions.DryRun {
				client := ollama.NewClient(ollamaHost, model)
				diagnose = func(ctx context.Context, screen string) (prPaneDiagnosis, error) {
					return diagnosePRPane(ctx, client, screen)
				}
			}
			var managed *managedPRAgents
			if runOptions.launchAgents() {
				managed = &managedPRAgents{executor: ex, options: runOptions, out: out}
			}
			aliases := newResolver().Aliases
			var draftPane, readyPane string
			paneForPhase := func(phase prPhase) (string, error) {
				if draftPane == "" {
					draft, ready, err := resolvePRLoopTargets(ex, aliases.Resolve, runOptions.DraftPane, runOptions.ReadyPane, os.Getenv("TMUX_PANE"))
					if err != nil {
						return "", err
					}
					draftPane, readyPane = draft, ready
				}
				if phase == prDraft {
					return draftPane, nil
				}
				return readyPane, nil
			}
			for index, number := range numbers {
				runOptions.Number = number
				runOptions.Observe = options.Observe && index == 0
				if len(numbers) > 1 {
					fmt.Fprintf(out, "[%d/%d] PR #%d\n", index+1, len(numbers), number)
				}
				send := func(ctx context.Context, phase prPhase, text string) error {
					if managed != nil {
						return managed.send(ctx, number, phase, text)
					}
					pane, err := paneForPhase(phase)
					if err != nil {
						return err
					}
					return sendPreparedPRCommand(ctx, ex, pane, runOptions.Repo, number, text, runOptions.SubmitDelay, out)
				}

				resetPanes := managed == nil && index+1 < len(numbers) && !runOptions.DryRun
				prActive := false
				watch := prPaneWatch{executor: ex, number: number, out: out, quiet: !runOptions.WatchPane,
					diagnoseAfter: runOptions.DiagnoseIdleAfter, diagnose: diagnose}
				checkPane := func(ctx context.Context, phase prPhase) error {
					prActive = true
					if managed != nil {
						if runOptions.PilotSkills {
							if err := managed.checkPilotResult(ctx, phase); err != nil {
								return err
							}
						}
						if pane := managed.panes[phase]; pane != "" {
							return watch.poll(ctx, phase, pane, time.Now())
						}
						return nil
					}
					pane, err := paneForPhase(phase)
					if err != nil {
						return err
					}
					return watch.poll(ctx, phase, pane, time.Now())
				}
				query := queryPRPhase
				if runOptions.PilotSkills {
					query = queryPilotPRPhase
				}
				if err := runPRLoop(ctx, runOptions, query, send, checkPane, waitPRPoll, time.Now, out); err != nil {
					if len(numbers) == 1 {
						return err
					}
					return fmt.Errorf("PR #%d [%d/%d]: %w", number, index+1, len(numbers), err)
				}
				if managed != nil {
					if err := managed.finishPR(ctx); err != nil {
						return fmt.Errorf("after PR #%d: %w", number, err)
					}
				} else if resetPanes && prActive {
					if _, err := paneForPhase(prDraft); err != nil {
						return fmt.Errorf("after PR #%d: %w", number, err)
					}
					if err := resetPRPanes(ctx, ex, []string{draftPane, readyPane}, runOptions, out); err != nil {
						return fmt.Errorf("after PR #%d: %w", number, err)
					}
				}
			}
			if managed != nil {
				managed.cleanupCheckout(ctx)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.Repo, "repo", "", "GitHub repository: [HOST/]OWNER/REPO (default: gh's current repository)")
	flags.StringVar(&options.DraftPane, "draft-pane", "", "Prestarted agent for draft PRs: window number, alias, %ID, or session:window.pane")
	flags.StringVar(&options.ReadyPane, "ready-pane", "", "Prestarted agent for ready PRs: window number, alias, %ID, or session:window.pane")
	flags.StringVar(&options.DraftAgent, "draft-agent", "", "Executable to launch for draft PRs, e.g. claude")
	flags.StringVar(&options.ReadyAgent, "ready-agent", "", "Executable to launch for ready PRs, e.g. codex")
	flags.BoolVar(&options.PilotSkills, "pilot-skills", false, "Start managed agents on the verified default branch and read Pilot skill results")
	flags.DurationVar(&options.AgentStartTimeout, "agent-start-timeout", time.Minute, "Maximum wait for a launched agent's input prompt")
	flags.StringVar(&options.DraftCommand, "draft-command", "/pr-ready {pr}", "Native draft-phase skill/command; {pr} is replaced by the PR number")
	flags.StringVar(&options.ReadyCommand, "ready-command", "$pr-review-merge {pr}", "Native ready-phase skill/command; {pr} is replaced by the PR number")
	flags.DurationVar(&options.PollInterval, "poll-interval", 10*time.Second, "Interval between GitHub state queries")
	flags.IntVar(&options.StablePolls, "stable-polls", 2, "Consecutive observations required before dispatch")
	flags.DurationVar(&options.PhaseTimeout, "phase-timeout", 24*time.Hour, "Maximum wait for a state change or for panes to return to a prompt between PRs (0 disables)")
	flags.DurationVar(&options.SubmitDelay, "submit-delay", time.Second, "Delay between pasting the native command and pressing Enter")
	flags.IntVar(&options.MaxActions, "max-actions", 20, "Maximum native command dispatches per PR")
	flags.BoolVar(&options.Observe, "observe", false, "Skip the first listed PR's initial command and wait for a state change")
	flags.BoolVar(&options.WatchPane, "watch-pane", false, "Report visible activity in the active agent pane and alert when it may need attention")
	flags.DurationVar(&options.DiagnoseIdleAfter, "diagnose-idle-after", 0, "Ask Ollama for a diagnostic after this long without visible pane progress (0 disables)")
	flags.BoolVar(&options.DryRun, "dry-run", false, "Show the initial action for each PR without sending input or connecting to tmux")
	return command
}

func parsePRLoopNumbers(args []string) ([]int, error) {
	numbers := make([]int, 0, len(args))
	seen := make(map[int]bool, len(args))
	for _, arg := range args {
		number, err := strconv.Atoi(arg)
		if err != nil || number <= 0 {
			return nil, fmt.Errorf("PR number must be a positive integer: %q", arg)
		}
		if seen[number] {
			return nil, fmt.Errorf("duplicate PR number: %d", number)
		}
		seen[number] = true
		numbers = append(numbers, number)
	}
	return numbers, nil
}

// Anchor numeric windows to the caller pane's session, never tmux's most recent
// client or a numeric session name. Resolve both targets once to pin pane IDs.
func resolvePRLoopTargets(ex tmux.Executor, alias func(string) string, draft, ready, callerPane string) (string, string, error) {
	targets := []string{alias(draft), alias(ready)}
	session := ""
	for i, target := range targets {
		if target != "" && strings.Trim(target, "0123456789") == "" {
			if callerPane == "" {
				return "", "", fmt.Errorf("window numbers require a current tmux pane (TMUX_PANE); use session:window outside tmux")
			}
			if session == "" {
				out, err := ex.Exec("display-message", "-p", "-t", callerPane, "#{session_id}")
				if err != nil {
					return "", "", fmt.Errorf("find current tmux session: %w", err)
				}
				session = strings.TrimSpace(out)
				if session == "" {
					return "", "", fmt.Errorf("cannot determine the current tmux session; use session:window")
				}
			}
			target = session + ":" + target
		}
		pane, err := ex.ResolveTarget(target)
		if err != nil {
			return "", "", fmt.Errorf("resolve pane %q: %w", target, err)
		}
		if pane == "" {
			return "", "", fmt.Errorf("cannot resolve pane %q", target)
		}
		targets[i] = pane
	}
	if targets[0] == targets[1] {
		return "", "", fmt.Errorf("draft and ready targets resolve to the same pane")
	}
	return targets[0], targets[1], nil
}

func (o prLoopOptions) validate() error {
	if o.PilotSkills && !o.launchAgents() {
		return fmt.Errorf("--pilot-skills requires --draft-agent and --ready-agent")
	}
	if o.Repo != "" {
		parts := strings.Split(o.Repo, "/")
		if len(parts) < 2 || len(parts) > 3 || strings.IndexFunc(o.Repo, unicode.IsSpace) >= 0 || strings.ContainsAny(o.Repo, "\\:") {
			return fmt.Errorf("--repo must be [HOST/]OWNER/REPO")
		}
		for _, part := range parts {
			if part == "" || strings.HasPrefix(part, "-") || part == "." || part == ".." {
				return fmt.Errorf("--repo must be [HOST/]OWNER/REPO")
			}
		}
	}
	if o.launchAgents() {
		if strings.TrimSpace(o.DraftAgent) == "" || strings.TrimSpace(o.ReadyAgent) == "" {
			return fmt.Errorf("--draft-agent and --ready-agent must be specified together")
		}
		if o.DraftPane != "" || o.ReadyPane != "" {
			return fmt.Errorf("choose either --draft-agent and --ready-agent or --draft-pane and --ready-pane")
		}
		if o.AgentStartTimeout <= 0 {
			return fmt.Errorf("--agent-start-timeout must be positive")
		}
	} else if strings.TrimSpace(o.DraftPane) == "" || strings.TrimSpace(o.ReadyPane) == "" || o.DraftPane == o.ReadyPane {
		return fmt.Errorf("--draft-pane and --ready-pane must be nonempty, different targets")
	}
	for _, text := range []string{o.DraftCommand, o.ReadyCommand} {
		if strings.TrimSpace(text) == "" || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return fmt.Errorf("native commands must be nonempty single lines without control characters")
		}
	}
	if o.PollInterval <= 0 || o.StablePolls <= 0 || o.MaxActions <= 0 || o.PhaseTimeout < 0 || o.SubmitDelay < 0 {
		return fmt.Errorf("poll interval, stable polls, and max actions must be positive; timeouts/delays must not be negative")
	}
	if o.DiagnoseIdleAfter < 0 {
		return fmt.Errorf("--diagnose-idle-after must not be negative")
	}
	return nil
}

func (o prLoopOptions) launchAgents() bool {
	return o.DraftAgent != "" || o.ReadyAgent != ""
}

// GitHub has OPEN/CLOSED/MERGED and a separate isDraft boolean. A merge-train
// label on a ready PR indicates a handoff to the train, not a completed merge.
func parsePRPhase(data []byte, number int) (prPhase, error) {
	var pr struct {
		Number  int    `json:"number"`
		State   string `json:"state"`
		IsDraft *bool  `json:"isDraft"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(data, &pr); err != nil {
		return "", fmt.Errorf("invalid GitHub PR JSON: %w", err)
	}
	if pr.Number != number {
		return "", fmt.Errorf("GitHub returned PR #%d, expected #%d", pr.Number, number)
	}
	switch pr.State {
	case "MERGED":
		return prMerged, nil
	case "OPEN":
		if pr.IsDraft == nil {
			return "", fmt.Errorf("GitHub PR response is missing isDraft")
		}
		if *pr.IsDraft {
			return prDraft, nil
		}
		for _, label := range pr.Labels {
			if strings.EqualFold(label.Name, "merge-queue") {
				return prQueued, nil
			}
		}
		return prReady, nil
	case "CLOSED":
		return "", fmt.Errorf("PR #%d is closed without being merged; stopping", number)
	default:
		return "", fmt.Errorf("unknown GitHub PR state %q", pr.State)
	}
}

func queryPRPhase(ctx context.Context, repo string, number int) (prPhase, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"pr", "view", strconv.Itoa(number)}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	args = append(args, "--json", "number,state,isDraft,labels")
	command := exec.CommandContext(ctx, "gh", args...)
	// gh must never open a browser or prompt for credentials inside the runner.
	command.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")
	data, err := command.Output()
	if err != nil {
		return "", &prQueryError{fmt.Errorf("gh pr view failed (check gh authentication and repository access): %w", err)}
	}
	return parsePRPhase(data, number)
}

// Only execution failures are retryable; a valid CLOSED response or invalid
// JSON must stop immediately rather than participate in state selection.
type prQueryError struct{ error }

type prPhaseQuery func(context.Context, string, int) (prPhase, error)
type prCommandSender func(context.Context, prPhase, string) error

func runPRLoop(ctx context.Context, o prLoopOptions, query prPhaseQuery, send prCommandSender, checkPane func(context.Context, prPhase) error, wait func(context.Context, time.Duration) error, now func() time.Time, out io.Writer) error {
	var dispatched, candidate prPhase
	var phaseStarted time.Time
	stable, actions, queryErrors := 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		phase, err := query(ctx, o.Repo, o.Number)
		var inspect prPhase
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var retryable *prQueryError
			if !errors.As(err, &retryable) {
				return err
			}
			queryErrors++
			candidate, stable = "", 0
			if queryErrors >= 3 {
				return fmt.Errorf("PR query failed 3 consecutive times; no command resent: %w", err)
			}
			fmt.Fprintf(out, "PR query failed (%d/3): %v\n", queryErrors, err)
		} else {
			queryErrors = 0
			switch phase {
			case prMerged:
				fmt.Fprintf(out, "PR %s#%d: merged; stopped.\n", o.Repo, o.Number)
				return nil
			case prQueued:
				if dispatched != prQueued {
					fmt.Fprintf(out, "PR #%d is in the merge train (merge-queue); waiting for GitHub to confirm merge.\n", o.Number)
					dispatched, phaseStarted = prQueued, now()
				}
				if o.DryRun {
					return nil
				}
				o.Observe = false
				candidate, stable = "", 0
			case prDraft, prReady:
				if phase == prReady && dispatched == prQueued {
					return fmt.Errorf("PR #%d left the merge train while still ready; inspect GitHub before retrying", o.Number)
				}
				if phase != candidate {
					candidate, stable = phase, 1
				} else if stable < o.StablePolls {
					stable++
				}
				if stable >= o.StablePolls && phase != dispatched {
					if o.Observe && dispatched == "" {
						fmt.Fprintf(out, "PR %s#%d: observing %s; initial command skipped.\n", o.Repo, o.Number, phase)
						o.Observe = false
					} else {
						if actions >= o.MaxActions {
							return fmt.Errorf("reached --max-actions=%d before merge", o.MaxActions)
						}
						text, pane := o.ReadyCommand, o.ReadyPane
						if phase == prDraft {
							text, pane = o.DraftCommand, o.DraftPane
						}
						if o.launchAgents() {
							pane = o.ReadyAgent
							if phase == prDraft {
								pane = o.DraftAgent
							}
						}
						text = strings.ReplaceAll(text, "{pr}", strconv.Itoa(o.Number))
						fmt.Fprintf(out, "PR %s#%d: %s -> %s: %s\n", o.Repo, o.Number, phase, pane, text)
						if o.DryRun && o.PilotSkills {
							fmt.Fprintln(out, "Would launch on the verified default branch and let the Pilot skill check out the PR.")
						} else if o.DryRun && o.launchAgents() {
							fmt.Fprintln(out, "Would prepare a managed checkout, launch the agent in tmux, and send the native command.")
						} else if o.DryRun {
							fmt.Fprintln(out, "Would prepare the PR branch in the target pane's working directory, or append instructions to use its existing worktree, before sending the native command.")
						}
						if o.DryRun {
							return nil
						}
						if err := send(ctx, phase, text); err != nil {
							if errors.Is(err, errPRBranchPreparation) {
								return err
							}
							return fmt.Errorf("native command delivery failed; delivery may be partial, inspect pane before restarting: %w", err)
						}
						actions++
					}
					if o.DryRun {
						return nil
					}
					dispatched, phaseStarted = phase, now()
					fmt.Fprintln(out, "Waiting for PR state to change; unchanged state will not resend the command.")
				}
				if phase == dispatched {
					inspect = phase
				}
			default:
				return fmt.Errorf("invalid PR workflow phase %q", phase)
			}
		}
		if inspect != "" && checkPane != nil && ctx.Err() == nil {
			checkCtx, cancel := ctx, func() {}
			if !phaseStarted.IsZero() && o.PhaseTimeout > 0 {
				checkCtx, cancel = context.WithTimeout(ctx, o.PhaseTimeout-now().Sub(phaseStarted))
			}
			err := checkPane(checkCtx, inspect)
			cancel()
			if err != nil {
				return err
			}
		}
		pollDelay := o.PollInterval
		if !phaseStarted.IsZero() && o.PhaseTimeout > 0 {
			remaining := o.PhaseTimeout - now().Sub(phaseStarted)
			if remaining <= 0 {
				return fmt.Errorf("PR #%d stayed in %s for %s; no command resent", o.Number, dispatched, o.PhaseTimeout)
			}
			pollDelay = min(pollDelay, remaining)
		}
		if err := wait(ctx, pollDelay); err != nil {
			return err
		}
	}
}

func waitPRPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

const prNewSessionCommand = "/new"

var (
	prNewInput           = regexp.MustCompile("(?m)^[ \\t]*[›❯>][ \\t]*/new[ \\t]*$")
	prNewCurrentWorktree = regexp.MustCompile("(?m)^[ \\t]*[›❯>][ \\t]*(?:1\\.[ \\t]*)?Use current Git worktree(?:[ \\t]|$)")
	prNewError           = regexp.MustCompile("(?i)(?:unknown command:?\\s*/new|failed to (?:start a fresh session|read new session defaults))")
)

func resetPRPanes(ctx context.Context, ex tmux.Executor, panes []string, o prLoopOptions, out io.Writer) error {
	// GitHub can report a merge while the previous agents are still finishing.
	for _, pane := range panes {
		if err := waitForPRPanePrompt(ctx, ex, pane, o, out); err != nil {
			return err
		}
	}
	for _, pane := range panes {
		fmt.Fprintf(out, "Starting a new conversation in pane %s with %s.\n", pane, prNewSessionCommand)
		if err := resetPRPane(ctx, ex, pane, o, out); err != nil {
			return fmt.Errorf("reset pane %s: %w", pane, err)
		}
	}
	return nil
}

func resetPRPane(ctx context.Context, ex tmux.Executor, pane string, o prLoopOptions, out io.Writer) error {
	poll := min(o.PollInterval, 200*time.Millisecond)
	// Require the slash command to reach the composer before submitting. The
	// prompt can look identical in the old and new conversations.
	confirmPaste := func() error {
		return waitForPRPaneScreen(ctx, ex, pane, poll, promptEchoTimeout, "show /new in its input prompt", io.Discard,
			func(screen string) (bool, error) { return prNewInput.MatchString(screen), nil })
	}
	if err := submitNativePRCommand(ctx, ex, pane, prNewSessionCommand, o.SubmitDelay, confirmPaste); err != nil {
		return err
	}
	sawWorktreePicker, selected := false, false
	return waitForPRPaneScreen(ctx, ex, pane, poll, o.PhaseTimeout, "finish /new and return to its input prompt", out,
		func(screen string) (bool, error) {
			if prNewError.MatchString(screen) {
				return false, fmt.Errorf("agent reported a failure starting a new conversation")
			}
			if strings.Contains(screen, "Where should the new conversation run?") {
				sawWorktreePicker = true
				if !selected && prNewCurrentWorktree.MatchString(screen) {
					if err := ctx.Err(); err != nil {
						return false, err
					}
					if _, err := ex.Exec("send-keys", "-t", pane, "Enter"); err != nil {
						return false, err
					}
					selected = true
				}
				return false, nil
			}
			activity, _ := inspectPRPaneScreen(screen)
			if activity != prPanePrompt || prNewInput.MatchString(screen) {
				return false, nil
			}
			if sawWorktreePicker && !selected {
				return false, fmt.Errorf("/new checkout selection was not confirmed")
			}
			return true, nil
		})
}

func waitForPRPanePrompt(ctx context.Context, ex tmux.Executor, pane string, o prLoopOptions, out io.Writer) error {
	return waitForPRPaneScreen(ctx, ex, pane, o.PollInterval, o.PhaseTimeout, "return to its input prompt", out,
		func(screen string) (bool, error) {
			activity, _ := inspectPRPaneScreen(screen)
			return activity == prPanePrompt, nil
		})
}

func waitForPRPaneScreen(ctx context.Context, ex tmux.Executor, pane string, poll, timeout time.Duration, expectation string, out io.Writer, check func(string) (bool, error)) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	reported := false
	for ctx.Err() == nil {
		if _, err := nativePRPaneState(ex, pane); err != nil {
			return err
		}
		screen, err := ex.CaptureScreenClean(pane)
		if err != nil {
			return fmt.Errorf("read pane %s before starting the next PR: %w", pane, err)
		}
		if ready, err := check(screen); ready || err != nil {
			return err
		}
		if !reported {
			fmt.Fprintf(out, "Waiting for pane %s to %s.\n", pane, expectation)
			reported = true
		}
		if err := waitPRPoll(ctx, poll); err != nil {
			return fmt.Errorf("waiting for pane %s to %s: %w", pane, expectation, err)
		}
	}
	return fmt.Errorf("waiting for pane %s to %s: %w", pane, expectation, ctx.Err())
}

func sendNativePRCommand(ctx context.Context, ex tmux.Executor, pane, text string, delay time.Duration) error {
	return submitNativePRCommand(ctx, ex, pane, text, delay, nil)
}

func submitNativePRCommand(ctx context.Context, ex tmux.Executor, pane, text string, delay time.Duration, beforeEnter func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := nativePRPaneState(ex, pane)
	if err != nil {
		return err
	}
	if err := ex.SendTextContext(ctx, pane, text); err != nil {
		return err
	}
	if err := waitPRPoll(ctx, delay); err != nil {
		return err
	}
	if beforeEnter != nil {
		if err := beforeEnter(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The agent may have exited during the paste/submit delay. Never press
	// Enter into a shell that replaced it, and never retry an ambiguous send.
	after := readPaneState(ex, pane)
	if !after.Known || after.Dead || after.InMode || after.Command != state.Command {
		return fmt.Errorf("pane %s changed during delivery; Enter was not sent", pane)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = ex.Exec("send-keys", "-t", pane, "Enter")
	return err
}

func nativePRPaneState(ex tmux.Executor, pane string) (paneState, error) {
	state := readPaneState(ex, pane)
	if !state.Known || state.Dead || state.InMode || isShellCommand(state.Command) || state.Command == "" {
		return state, fmt.Errorf("pane %s must have a live foreground agent outside copy-mode; state=%+v", pane, state)
	}
	return state, nil
}
