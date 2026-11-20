package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/tmux"
)

type prCheckoutTarget struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
}

// Query from the runner's repository context, then use the canonical PR URL
// when checking out in the agent's directory. A same-number PR in a different
// checkout must never be substituted just because that is the pane's cwd.
func queryPRCheckoutTarget(ctx context.Context, repo string, number int) (prCheckoutTarget, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"pr", "view", strconv.Itoa(number)}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	args = append(args, "--json", "number,url,headRefName")
	data, err := runPRCheckoutCommand(ctx, "", "gh", args...)
	if err != nil {
		return prCheckoutTarget{}, err
	}
	var target prCheckoutTarget
	if err := json.Unmarshal(data, &target); err != nil {
		return target, fmt.Errorf("invalid PR checkout response: %w", err)
	}
	parsed, err := url.Parse(target.URL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		!strings.HasSuffix(parsed.Path, "/pull/"+strconv.Itoa(number)) || target.Number != number || target.HeadRefName == "" {
		return target, fmt.Errorf("invalid branch or URL for PR #%d", number)
	}
	return target, nil
}

var errPRBranchPreparation = errors.New("prepare PR branch; native command was not sent")

func sendPreparedPRCommand(ctx context.Context, ex tmux.Executor, pane, repo string, number int, text string, delay time.Duration, out io.Writer) error {
	worktree, err := checkoutPRBranch(ctx, ex, pane, repo, number, out)
	if err != nil {
		return fmt.Errorf("%w: %w", errPRBranchPreparation, err)
	}
	if worktree != "" {
		// Keep the native skill at the beginning. This is an instruction to the
		// agent, not a shell cd: tmux cannot change a running agent's directory.
		text += " Use the existing PR worktree at " + strconv.Quote(worktree) + " for all repository operations, including edits, tests, commits, and pushes; do not check out the PR branch in the original working directory."
	}
	return sendNativePRCommand(ctx, ex, pane, text, delay)
}

// Return an existing PR worktree when the agent must work outside its original
// directory. An empty result means the pane's directory is already prepared.
func checkoutPRBranch(ctx context.Context, ex tmux.Executor, pane, repo string, number int, out io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := nativePRPaneState(ex, pane); err != nil {
		return "", err
	}
	path, err := ex.Exec("display-message", "-p", "-t", pane, "#{pane_current_path}")
	if err != nil {
		return "", fmt.Errorf("read target pane working directory: %w", err)
	}
	path = strings.TrimSuffix(path, "\n")
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("target pane must have an accessible local working directory, got %q", path)
	}
	current, err := runPRCheckoutCommand(ctx, path, "git", "branch", "--show-current")
	if err != nil {
		return "", err
	}
	target, err := queryPRCheckoutTarget(ctx, repo, number)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(current)) == target.HeadRefName {
		fmt.Fprintf(out, "PR branch %q already checked out in %q; keeping existing work.\n", target.HeadRefName, path)
		return "", nil
	}
	worktree, err := prBranchWorktree(ctx, path, target.HeadRefName)
	if err != nil {
		return "", err
	}
	if worktree != "" {
		current, err := runPRCheckoutCommand(ctx, worktree, "git", "branch", "--show-current")
		if err != nil {
			return "", fmt.Errorf("access existing PR worktree %q: %w", worktree, err)
		}
		if strings.TrimSpace(string(current)) != target.HeadRefName {
			return "", fmt.Errorf("existing PR worktree %q is no longer on branch %q", worktree, target.HeadRefName)
		}
		fmt.Fprintf(out, "PR branch %q is already checked out in %q; instructing the agent to use that worktree and keeping existing work.\n", target.HeadRefName, worktree)
		return worktree, nil
	}
	return "", switchPRBranch(ctx, path, target, out)
}

func switchPRBranch(ctx context.Context, path string, target prCheckoutTarget, out io.Writer) error {
	status, err := runPRCheckoutCommand(ctx, path, "git", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return fmt.Errorf("target pane working directory %q has uncommitted changes; cannot switch to PR branch %q", path, target.HeadRefName)
	}
	// No --force, reset, stash, or shell interpolation. gh handles fetching PRs
	// from forks and refuses branch/worktree conflicts itself.
	if _, err := runPRCheckoutCommand(ctx, path, "gh", "pr", "checkout", target.URL); err != nil {
		return err
	}
	current, err := runPRCheckoutCommand(ctx, path, "git", "branch", "--show-current")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(current)) != target.HeadRefName {
		return fmt.Errorf("checkout did not select PR branch %q; native command will not be sent", target.HeadRefName)
	}
	fmt.Fprintf(out, "Checked out PR branch %q in %q.\n", target.HeadRefName, path)
	return nil
}

// Use NUL-delimited porcelain so spaces, newlines, and Git's path quoting do
// not change the worktree path reported to the user.
func prBranchWorktree(ctx context.Context, dir, branch string) (string, error) {
	data, err := runPRCheckoutCommand(ctx, dir, "git", "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", err
	}
	path := ""
	for _, field := range strings.Split(string(data), "\x00") {
		if field == "" {
			path = ""
		} else if value, ok := strings.CutPrefix(field, "worktree "); ok {
			path = value
		} else if field == "branch refs/heads/"+branch && path != "" {
			return path, nil
		}
	}
	return "", nil
}

func runPRCheckoutCommand(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	command.WaitDelay = 2 * time.Second
	data, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		detail := ""
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			detail = strings.TrimSpace(string(exit.Stderr[:min(len(exit.Stderr), 2048)]))
		}
		return nil, fmt.Errorf("%s %s in %q: %w; %s", name, strings.Join(args, " "), dir, err, detail)
	}
	return data, nil
}
