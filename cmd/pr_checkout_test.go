package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/tmux"
)

func initPRCheckoutRepository(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	checkoutGit(t, path, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkoutGit(t, path, "add", "tracked.txt")
	checkoutGit(t, path, "-c", "user.name=PR loop test", "-c", "user.email=pr-loop@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "initial")
	checkoutGit(t, path, "branch", "pr-333")
	return path
}

func checkoutGit(t *testing.T, path string, args ...string) string {
	t.Helper()
	data, err := runPRCheckoutCommand(context.Background(), path, "git", args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func checkoutFixture(t *testing.T) (string, string, *tmux.FakeExecutor) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := initPRCheckoutRepository(t, filepath.Join(dir, "repo"))
	log := filepath.Join(dir, "checkout.log")
	t.Setenv("TMUXAI_TEST_CHECKOUT_LOG", log)
	t.Setenv("TMUXAI_TEST_CHECKOUT_MODE", "")
	t.Setenv("TMUXAI_TEST_CHECKOUT_JSON", `{"number":333,"url":"https://github.com/owner/repo/pull/333","headRefName":"pr-333"}`)
	const gh = `#!/bin/sh
case "$*" in
  'pr view 333 --repo owner/repo --json number,state,isDraft,labels')
    printf '{"number":333,"state":"OPEN","isDraft":%s}' "${TMUXAI_TEST_IS_DRAFT:-true}" ;;
  'pr view 333 --repo owner/repo --json number,url,headRefName'|'pr view 333 --json number,url,headRefName')
    printf '%s' "$TMUXAI_TEST_CHECKOUT_JSON" ;;
  'pr checkout https://github.com/owner/repo/pull/333')
    printf '%s\n' "$PWD" >> "$TMUXAI_TEST_CHECKOUT_LOG"
    case "$TMUXAI_TEST_CHECKOUT_MODE" in
      fail) echo 'checkout rejected' >&2; exit 1 ;;
      wrong-branch) exit 0 ;;
    esac
    exec git switch pr-333 ;;
  *) echo 'unexpected gh command' >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ex := tmux.NewFakeExecutor()
	ex.ExecHook = func(args []string) (string, bool, error) {
		switch args[len(args)-1] {
		case paneStateFormat:
			return "0|0||claude", true, nil
		case "#{pane_current_path}":
			return repo + "\n", true, nil
		default:
			t.Fatalf("unexpected tmux command: %v", args)
			return "", true, nil
		}
	}
	return repo, log, ex
}

func TestPRCheckoutSwitchesInTargetPaneDirectory(t *testing.T) {
	for _, detached := range []bool{false, true} {
		repo, log, ex := checkoutFixture(t)
		if detached {
			checkoutGit(t, repo, "checkout", "--detach", "-q", "HEAD")
		}
		var out bytes.Buffer
		if _, err := checkoutPRBranch(context.Background(), ex, "%1", "owner/repo", 333, &out); err != nil {
			t.Fatal(err)
		}
		if branch := checkoutGit(t, repo, "branch", "--show-current"); branch != "pr-333" {
			t.Fatalf("branch=%q", branch)
		}
		data, err := os.ReadFile(log)
		if err != nil || strings.TrimSpace(string(data)) != repo {
			t.Fatalf("checkout ran outside pane directory: log=%q, err=%v", data, err)
		}
		if !strings.Contains(out.String(), "Checked out PR branch") {
			t.Fatalf("missing checkout result: %s", out.String())
		}
	}
}

func TestPRCheckoutRefusesDirtyOtherBranch(t *testing.T) {
	for _, state := range []string{"tracked", "staged", "untracked"} {
		t.Run(state, func(t *testing.T) {
			repo, log, ex := checkoutFixture(t)
			name := "tracked.txt"
			if state == "untracked" {
				name = "untracked.txt"
			}
			if err := os.WriteFile(filepath.Join(repo, name), []byte("keep my work\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if state == "staged" {
				checkoutGit(t, repo, "add", name)
			}
			_, err := checkoutPRBranch(context.Background(), ex, "%1", "owner/repo", 333, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
				t.Fatalf("did not refuse dirty branch: %v", err)
			}
			if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("checkout was invoked on dirty branch: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(repo, name))
			if err != nil || string(data) != "keep my work\n" || checkoutGit(t, repo, "branch", "--show-current") != "main" {
				t.Fatalf("existing work was changed: %q, %v", data, err)
			}
		})
	}
}

func TestPRCheckoutKeepsWorkAlreadyOnPRBranch(t *testing.T) {
	repo, log, ex := checkoutFixture(t)
	checkoutGit(t, repo, "switch", "pr-333")
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("partial fix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkoutPRBranch(context.Background(), ex, "%1", "", 333, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unnecessary checkout: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "partial fix\n" {
		t.Fatalf("partial fix was changed: %q, %v", data, err)
	}
}

func TestPRBranchWorktreeFindsExactBranchAndUnquotedPath(t *testing.T) {
	repo, _, _ := checkoutFixture(t)
	worktree := filepath.Join(filepath.Dir(repo), "PR worktree\nwith newline")
	checkoutGit(t, repo, "worktree", "add", "-q", worktree, "pr-333")
	checkoutGit(t, repo, "branch", "pr-33")
	for _, test := range []struct {
		branch string
		want   string
	}{
		{"pr-333", worktree},
		{"main", repo},
		{"pr-33", ""},
	} {
		got, err := prBranchWorktree(context.Background(), repo, test.branch)
		if err != nil || got != test.want {
			t.Fatalf("branch %q: path=%q, want=%q, err=%v", test.branch, got, test.want, err)
		}
	}
	checkoutGit(t, worktree, "checkout", "--detach", "-q", "HEAD")
	if got, err := prBranchWorktree(context.Background(), repo, "pr-333"); err != nil || got != "" {
		t.Fatalf("detached worktree still matched branch: path=%q, err=%v", got, err)
	}
}

func TestPRPreparedCommandUsesExistingWorktree(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(strconv.FormatBool(dirty), func(t *testing.T) {
			repo, log, ex := checkoutFixture(t)
			worktree := filepath.Join(filepath.Dir(repo), "PR worktree \"quoted\"\n$literal")
			checkoutGit(t, repo, "worktree", "add", "-q", worktree, "pr-333")
			if dirty {
				for _, path := range []string{repo, worktree} {
					if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("keep my work\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			originalHook := ex.ExecHook
			ex.ExecHook = func(args []string) (string, bool, error) {
				if args[0] == "send-keys" {
					return "", true, nil
				}
				return originalHook(args)
			}
			var out bytes.Buffer
			if err := sendPreparedPRCommand(context.Background(), ex, "%1", "owner/repo", 333, "$pr-ready 333", 0, &out); err != nil {
				t.Fatal(err)
			}
			if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].PaneID != "%1" {
				t.Fatalf("native command not sent once to original agent: %+v", ex.SendLinesCalls)
			}
			text := ex.SendLinesCalls[0].Content
			if !strings.HasPrefix(text, "$pr-ready 333 ") || !strings.Contains(text, strconv.Quote(worktree)) || strings.ContainsAny(text, "\r\n") {
				t.Fatalf("native skill or quoted worktree missing from single-line instruction: %q", text)
			}
			enters := 0
			for _, args := range ex.ExecCalls {
				if args[0] == "send-keys" {
					if strings.Join(args, " ") != "send-keys -t %1 Enter" {
						t.Fatalf("unexpected key delivery: %v", args)
					}
					enters++
				}
			}
			if enters != 1 || !strings.Contains(out.String(), "instructing the agent") {
				t.Fatalf("enters=%d, output=%s", enters, out.String())
			}
			if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("gh checkout was invoked despite existing worktree: %v", err)
			}
			if checkoutGit(t, repo, "branch", "--show-current") != "main" || checkoutGit(t, worktree, "branch", "--show-current") != "pr-333" {
				t.Fatal("existing worktree branches were changed")
			}
			if dirty {
				for _, path := range []string{repo, worktree} {
					data, err := os.ReadFile(filepath.Join(path, "tracked.txt"))
					if err != nil || string(data) != "keep my work\n" {
						t.Fatalf("work changed in %q: %q, %v", path, data, err)
					}
				}
			}
		})
	}
}

func TestPRCheckoutFindsPRBranchInMainWorktree(t *testing.T) {
	repo, log, ex := checkoutFixture(t)
	worktree := filepath.Join(filepath.Dir(repo), "draft-pane")
	checkoutGit(t, repo, "worktree", "add", "-q", "-b", "other", worktree)
	checkoutGit(t, repo, "switch", "pr-333")
	originalHook := ex.ExecHook
	ex.ExecHook = func(args []string) (string, bool, error) {
		if args[len(args)-1] == "#{pane_current_path}" {
			return worktree + "\n", true, nil
		}
		return originalHook(args)
	}
	got, err := checkoutPRBranch(context.Background(), ex, "%1", "owner/repo", 333, &bytes.Buffer{})
	if err != nil || got != repo {
		t.Fatalf("did not select main worktree: path=%q, err=%v", got, err)
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unnecessary checkout: %v", err)
	}
}

func TestPRCheckoutVerifiesCheckoutResult(t *testing.T) {
	for _, mode := range []string{"fail", "wrong-branch"} {
		repo, _, ex := checkoutFixture(t)
		t.Setenv("TMUXAI_TEST_CHECKOUT_MODE", mode)
		_, err := checkoutPRBranch(context.Background(), ex, "%1", "owner/repo", 333, &bytes.Buffer{})
		if err == nil || checkoutGit(t, repo, "branch", "--show-current") != "main" {
			t.Fatalf("checkout mode %s was accepted: %v", mode, err)
		}
	}
}

func TestPRCheckoutRejectsInvalidMetadata(t *testing.T) {
	_, _, ex := checkoutFixture(t)
	for _, data := range []string{
		`{`,
		`{"number":334,"url":"https://github.com/owner/repo/pull/333","headRefName":"pr-333"}`,
		`{"number":333,"url":"https://github.com/owner/repo/pull/334","headRefName":"pr-333"}`,
		`{"number":333,"url":"https://github.com/owner/repo/pull/333","headRefName":""}`,
		`{"number":333,"url":"file:///repo/pull/333","headRefName":"pr-333"}`,
	} {
		t.Setenv("TMUXAI_TEST_CHECKOUT_JSON", data)
		if _, err := checkoutPRBranch(context.Background(), ex, "%1", "owner/repo", 333, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid checkout metadata: %s", data)
		}
	}
}

func TestPRCheckoutChecksPaneAndCancellationBeforeGit(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkoutPRBranch(ctx, ex, "%1", "", 333, &bytes.Buffer{}); !errors.Is(err, context.Canceled) || len(ex.ExecCalls) != 0 {
		t.Fatalf("canceled checkout: %v, calls=%v", err, ex.ExecCalls)
	}
	if _, err := checkoutPRBranch(context.Background(), ex, "%1", "", 333, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted a pane without a foreground agent")
	}
}

func TestPRLoopDoesNotSendSkillWhenCheckoutFails(t *testing.T) {
	for _, scenario := range []string{"draft/dirty", "draft/fail", "draft/wrong-branch", "draft/missing-worktree", "ready/dirty", "ready/fail", "ready/wrong-branch", "ready/missing-worktree"} {
		t.Run(scenario, func(t *testing.T) {
			phase, mode, _ := strings.Cut(scenario, "/")
			repo, _, _ := checkoutFixture(t)
			t.Setenv("TMUXAI_TEST_IS_DRAFT", strconv.FormatBool(phase == "draft"))
			dir := filepath.Dir(repo)
			sendLog := filepath.Join(dir, "tmux-send.log")
			t.Setenv("TMUXAI_TEST_DRAFT_DIR", repo)
			t.Setenv("TMUXAI_TEST_SEND_LOG", sendLog)
			t.Setenv("TMUXAI_TEST_CHECKOUT_MODE", mode)
			if mode == "dirty" {
				if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-worktree" {
				worktree := filepath.Join(dir, "missing-worktree")
				checkoutGit(t, repo, "worktree", "add", "-q", worktree, "pr-333")
				if err := os.RemoveAll(worktree); err != nil {
					t.Fatal(err)
				}
			}
			const fakeTmux = `#!/bin/sh
if [ "$1" != 'display-message' ]; then
  printf '%s\n' "$*" >> "$TMUXAI_TEST_SEND_LOG"
  exit 1
fi
case "$5" in
  '#{pane_id}')
    if [ "$4" = 'project:1' ]; then printf '%%11\n'; else printf '%%12\n'; fi ;;
  '#{pane_current_path}') printf '%s\n' "$TMUXAI_TEST_DRAFT_DIR" ;;
  *) printf '%s' '0|0||claude' ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(fakeTmux), 0o700); err != nil {
				t.Fatal(err)
			}
			command := newPRLoopCommand()
			command.SetArgs([]string{"333", "--repo", "owner/repo", "--draft-pane", "project:1", "--ready-pane", "project:2", "--stable-polls", "1"})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), "native command was not sent") {
				t.Fatalf("checkout failure was not propagated: %v", err)
			}
			if strings.Contains(err.Error(), "delivery may be partial") {
				t.Fatalf("preparation failure incorrectly reported partial delivery: %v", err)
			}
			if _, err := os.Stat(sendLog); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("skill was sent after checkout failure: %v", err)
			}
			if branch := checkoutGit(t, repo, "branch", "--show-current"); branch != "main" {
				t.Fatalf("branch changed despite preparation failure: %q", branch)
			}
		})
	}
}
