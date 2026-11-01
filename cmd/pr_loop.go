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
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/cobra"
)

type prPhase string

const (
	prDraft  prPhase = "draft"
	prReady  prPhase = "ready"
	prMerged prPhase = "merged"
)

type prLoopOptions struct {
	Number                     int
	Repo, DraftPane, ReadyPane string
	DraftCommand, ReadyCommand string
	PollInterval, PhaseTimeout time.Duration
	SubmitDelay                time.Duration
	StablePolls, MaxActions    int
	Observe, DryRun            bool
}

func init() { rootCmd.AddCommand(newPRLoopCommand()) }

func newPRLoopCommand() *cobra.Command {
	var options prLoopOptions
	command := &cobra.Command{
		Use:   "pr-loop <PR-number>",
		Short: "Run native agent commands according to PR state: draft, ready, merged",
		Long: `Poll GitHub PR state with gh, without an LLM or agent completion detection.
Draft PRs receive --draft-command; ready PRs receive --ready-command; merged PRs stop.
Commands are pasted verbatim with Enter, with no role, prompt wrapper, or receipt.
Each observed phase is dispatched once; unchanged state never resends a command.
Both panes must already be running agents in the correct repository with empty inputs.
Use --observe to wait for the next state change when resuming an already running task.

Example:
  tmux-ai pr-loop 333 --repo owner/repo \
    --draft-pane project:1 --ready-pane project:2 \
    --draft-command '/pr-ready {pr}' --ready-command '$pr-review-merge {pr}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			number, err := strconv.Atoi(args[0])
			if err != nil || number <= 0 {
				return fmt.Errorf("PR number must be a positive integer")
			}
			options.Number = number
			if err := options.validate(); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ex := tmux.NewExecutor()
			aliases := newResolver().Aliases
			resolved := false
			send := func(ctx context.Context, phase prPhase, text string) error {
				if !resolved {
					for _, target := range []*string{&options.DraftPane, &options.ReadyPane} {
						pane, err := ex.ResolveTarget(aliases.Resolve(*target))
						if err != nil {
							return fmt.Errorf("resolve pane %q: %w", *target, err)
						}
						*target = pane
					}
					if options.DraftPane == options.ReadyPane {
						return fmt.Errorf("draft and ready targets resolve to the same pane")
					}
					resolved = true
				}
				pane := options.ReadyPane
				if phase == prDraft {
					pane = options.DraftPane
				}
				return sendNativePRCommand(ctx, ex, pane, text, options.SubmitDelay)
			}
			return runPRLoop(ctx, options, queryPRPhase, send, waitPRPoll, time.Now, cmd.OutOrStdout())
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.Repo, "repo", "", "GitHub repository: [HOST/]OWNER/REPO (required)")
	flags.StringVar(&options.DraftPane, "draft-pane", "", "Agent for draft PRs: alias, %ID, or session:window.pane (required)")
	flags.StringVar(&options.ReadyPane, "ready-pane", "", "Agent for ready PRs: alias, %ID, or session:window.pane (required)")
	flags.StringVar(&options.DraftCommand, "draft-command", "/pr-ready {pr}", "Native draft-phase skill/command; {pr} is replaced by the PR number")
	flags.StringVar(&options.ReadyCommand, "ready-command", "$pr-review-merge {pr}", "Native ready-phase skill/command; {pr} is replaced by the PR number")
	flags.DurationVar(&options.PollInterval, "poll-interval", 10*time.Second, "Interval between GitHub state queries")
	flags.IntVar(&options.StablePolls, "stable-polls", 2, "Consecutive observations required before dispatch")
	flags.DurationVar(&options.PhaseTimeout, "phase-timeout", time.Hour, "Maximum wait for a state change after dispatch/observe (0 disables)")
	flags.DurationVar(&options.SubmitDelay, "submit-delay", time.Second, "Delay between pasting the native command and pressing Enter")
	flags.IntVar(&options.MaxActions, "max-actions", 20, "Maximum native command dispatches")
	flags.BoolVar(&options.Observe, "observe", false, "Skip the initial phase's command and wait for a state change")
	flags.BoolVar(&options.DryRun, "dry-run", false, "Show the initial action without sending input or connecting to tmux")
	return command
}

func (o prLoopOptions) validate() error {
	parts := strings.Split(o.Repo, "/")
	if len(parts) < 2 || len(parts) > 3 || strings.IndexFunc(o.Repo, unicode.IsSpace) >= 0 || strings.ContainsAny(o.Repo, "\\:") {
		return fmt.Errorf("--repo must be [HOST/]OWNER/REPO")
	}
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, "-") || part == "." || part == ".." {
			return fmt.Errorf("--repo must be [HOST/]OWNER/REPO")
		}
	}
	if strings.TrimSpace(o.DraftPane) == "" || strings.TrimSpace(o.ReadyPane) == "" || o.DraftPane == o.ReadyPane {
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
	return nil
}

// GitHub has OPEN/CLOSED/MERGED and a separate isDraft boolean. Only these
// three workflow phases are actionable; CLOSED without merge is an error.
func parsePRPhase(data []byte, number int) (prPhase, error) {
	var pr struct {
		Number  int    `json:"number"`
		State   string `json:"state"`
		IsDraft *bool  `json:"isDraft"`
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
	command := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "number,state,isDraft")
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

func runPRLoop(ctx context.Context, o prLoopOptions, query prPhaseQuery, send prCommandSender, wait func(context.Context, time.Duration) error, now func() time.Time, out io.Writer) error {
	var dispatched, candidate prPhase
	var phaseStarted time.Time
	stable, actions, queryErrors := 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		phase, err := query(ctx, o.Repo, o.Number)
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
			if phase == prMerged {
				fmt.Fprintf(out, "PR %s#%d: merged; stopped.\n", o.Repo, o.Number)
				return nil
			}
			if phase != prDraft && phase != prReady {
				return fmt.Errorf("invalid PR workflow phase %q", phase)
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
					text = strings.ReplaceAll(text, "{pr}", strconv.Itoa(o.Number))
					fmt.Fprintf(out, "PR %s#%d: %s -> %s: %s\n", o.Repo, o.Number, phase, pane, text)
					if o.DryRun {
						return nil
					}
					if err := send(ctx, phase, text); err != nil {
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
		}
		if !phaseStarted.IsZero() && o.PhaseTimeout > 0 && now().Sub(phaseStarted) >= o.PhaseTimeout {
			return fmt.Errorf("PR stayed in %s for %s; inspect the agent, no command resent", dispatched, o.PhaseTimeout)
		}
		if err := wait(ctx, o.PollInterval); err != nil {
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

func sendNativePRCommand(ctx context.Context, ex tmux.Executor, pane, text string, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state := readPaneState(ex, pane)
	if !state.Known || state.Dead || state.InMode || isShellCommand(state.Command) || state.Command == "" {
		return fmt.Errorf("pane %s must have a live foreground agent outside copy-mode; state=%+v", pane, state)
	}
	if err := ex.SendTextContext(ctx, pane, text); err != nil {
		return err
	}
	if err := waitPRPoll(ctx, delay); err != nil {
		return err
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
	_, err := ex.Exec("send-keys", "-t", pane, "Enter")
	return err
}
