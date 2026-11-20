package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type pilotStartupBranch struct {
	branch, sha string
}

func (a *managedPRAgents) verifyPilotStartup(ctx context.Context) error {
	if a.pilotBase == nil {
		ref, err := runPRCheckoutCommand(ctx, a.checkout, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
		if err != nil {
			return err
		}
		branch, ok := strings.CutPrefix(strings.TrimSpace(string(ref)), "refs/remotes/origin/")
		if !ok || branch == "" {
			return fmt.Errorf("could not verify origin's default branch")
		}
		sha, err := runPRCheckoutCommand(ctx, a.checkout, "git", "rev-parse", "--verify", "refs/remotes/origin/HEAD^{commit}")
		if err != nil {
			return err
		}
		if _, err := runPRCheckoutCommand(ctx, a.checkout, "git", "cat-file", "-e",
			"HEAD:.agents/skills/_shared/scripts/pr_loop_result.py"); err != nil {
			return fmt.Errorf("Pilot's default branch does not yet support pr-loop results: %w", err)
		}
		exclude := filepath.Join(a.checkout, ".git", "info", "exclude")
		f, err := os.OpenFile(exclude, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString("\n/.pr-ready/\n/.tmux-ai-pr/\n")
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return fmt.Errorf("configure ignored Pilot evidence directory: %w", err)
		}
		a.pilotBase = &pilotStartupBranch{branch: branch, sha: strings.TrimSpace(string(sha))}
	}
	branch, err := runPRCheckoutCommand(ctx, a.checkout, "git", "branch", "--show-current")
	if err != nil {
		return err
	}
	head, err := runPRCheckoutCommand(ctx, a.checkout, "git", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(branch)) != a.pilotBase.branch || strings.TrimSpace(string(head)) != a.pilotBase.sha {
		return fmt.Errorf("Pilot checkout was not restored to verified %s at %s", a.pilotBase.branch, a.pilotBase.sha)
	}
	status, err := runPRCheckoutCommand(ctx, a.checkout, "git", "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return fmt.Errorf("Pilot checkout %q has uncommitted changes", a.checkout)
	}
	return nil
}

type pilotPRView struct {
	URL    string `json:"url"`
	Head   string `json:"headRefOid"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	phase prPhase
}

func (v pilotPRView) hasLabel(name string) bool {
	for _, label := range v.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}

var pilotSHA = regexp.MustCompile("^[a-f0-9]{40}$")

func readPilotPR(ctx context.Context, repo string, number int) (pilotPRView, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"pr", "view", strconv.Itoa(number), "--json", "number,state,isDraft,labels,headRefOid,url"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	var view pilotPRView
	data, err := runPRCheckoutCommand(ctx, "", "gh", args...)
	if err != nil {
		return view, &prQueryError{err}
	}
	view.phase, err = parsePRPhase(data, number)
	if err != nil {
		return view, err
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return view, fmt.Errorf("invalid Pilot PR #%d response: %w", number, err)
	}
	if view.phase != prMerged && !pilotSHA.MatchString(view.Head) {
		return view, fmt.Errorf("Pilot PR #%d did not supply a verified HEAD SHA", number)
	}
	return view, nil
}

var pilotPersonMarker = regexp.MustCompile("<!-- pr-review-merge:needs-person head=([0-9a-f]{40}) live-base=[0-9a-f]{40} reasons=[0-9a-f]{16} -->")

func queryPilotPRPhase(ctx context.Context, repo string, number int) (prPhase, error) {
	view, err := readPilotPR(ctx, repo, number)
	if err != nil {
		return "", err
	}
	if view.phase == prReady && view.hasLabel("needs-person") {
		return checkPilotNeedsPerson(ctx, view, repo, number)
	}
	return view.phase, nil
}

func checkPilotNeedsPerson(ctx context.Context, view pilotPRView, repo string, number int) (prPhase, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	parsed, err := url.Parse(view.URL)
	if err != nil || parsed.Host != "github.com" {
		return "", fmt.Errorf("expected a GitHub PR URL for Pilot PR #%d", number)
	}
	login, err := runPRCheckoutCommand(ctx, "", "gh", "api", "--hostname", parsed.Host, "user", "--jq", ".login")
	if err != nil {
		return "", &prQueryError{err}
	}
	args := []string{"pr", "view", strconv.Itoa(number), "--json", "comments"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	data, err := runPRCheckoutCommand(ctx, "", "gh", args...)
	if err != nil {
		return "", &prQueryError{err}
	}
	var comments struct {
		Comments []struct {
			Author    struct{ Login string }
			Body, URL string
		}
	}
	if err := json.Unmarshal(data, &comments); err != nil {
		return "", fmt.Errorf("invalid Pilot PR #%d comments: %w", number, err)
	}
	viewer := strings.TrimSpace(string(login))
	if viewer == "" {
		return "", fmt.Errorf("could not verify the GitHub account for Pilot's needs-person hand-off")
	}
	for i := len(comments.Comments) - 1; i >= 0; i-- {
		comment := comments.Comments[i]
		if comment.Author.Login != viewer {
			continue
		}
		matches := pilotPersonMarker.FindAllStringSubmatch(comment.Body, -1)
		if len(matches) == 0 {
			continue
		}
		if matches[len(matches)-1][1] == view.Head {
			latest, err := readPilotPR(ctx, repo, number)
			if err != nil {
				return "", err
			}
			if latest.phase == prReady && latest.Head == view.Head && latest.hasLabel("needs-person") {
				return "", fmt.Errorf("Pilot PR #%d needs a person at HEAD %.12s; see %s", number, view.Head, comment.URL)
			}
			return latest.phase, nil
		}
		break
	}
	return view.phase, nil
}

type pilotTaskIdentity struct {
	Version   int     `json:"version"`
	Repo      string  `json:"repo"`
	PR        int     `json:"pr"`
	Phase     prPhase `json:"phase"`
	Nonce     string  `json:"nonce"`
	StartHead string  `json:"start_head"`
}

type pilotPRTask struct {
	pilotTaskIdentity
	path        string
	ignoredHead bool
}

type pilotPRResult struct {
	pilotTaskIdentity
	Head          string `json:"head_sha"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason"`
	WaitKind      string `json:"wait_kind"`
	PersonHandoff bool   `json:"person_handoff"`
}

func (a *managedPRAgents) preparePilotTask(ctx context.Context, number int, phase prPhase) (*pilotPRTask, error) {
	view, err := readPilotPR(ctx, a.options.Repo, number)
	if err != nil {
		return nil, err
	}
	if view.phase != phase {
		return nil, fmt.Errorf("Pilot PR #%d changed from %s to %s before dispatch", number, phase, view.phase)
	}
	parsed, err := url.Parse(view.URL)
	if err != nil || parsed.Host != "github.com" {
		return nil, fmt.Errorf("expected a GitHub PR URL for Pilot PR #%d", number)
	}
	repo, ok := strings.CutSuffix(strings.TrimPrefix(parsed.Path, "/"), "/pull/"+strconv.Itoa(number))
	if !ok || len(strings.Split(repo, "/")) != 2 {
		return nil, fmt.Errorf("unexpected repository URL for Pilot PR #%d", number)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	outbox := filepath.Join(a.checkout, ".tmux-ai-pr")
	if err := os.MkdirAll(outbox, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(outbox, "tmux-ai-pr-")
	if err != nil {
		return nil, err
	}
	task := &pilotPRTask{
		pilotTaskIdentity: pilotTaskIdentity{Version: 1, Repo: repo, PR: number, Phase: phase,
			Nonce: hex.EncodeToString(nonce), StartHead: view.Head},
		path: filepath.Join(dir, "task.json"),
	}
	data, err := json.Marshal(task.pilotTaskIdentity)
	if err != nil {
		return nil, err
	}
	if err := atomicReplaceFile(task.path, data); err != nil {
		return nil, fmt.Errorf("write Pilot PR task: %w", err)
	}
	return task, nil
}

func readPilotResult(task *pilotPRTask) (*pilotPRResult, error) {
	path := filepath.Join(filepath.Dir(task.path), "result.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("invalid Pilot PR result file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return nil, err
	}
	var result pilotPRResult
	if len(data) > 4096 || json.Unmarshal(data, &result) != nil || result.pilotTaskIdentity != task.pilotTaskIdentity ||
		!pilotSHA.MatchString(result.Head) || strings.IndexFunc(result.Reason, unicode.IsControl) >= 0 ||
		result.WaitKind != "" && result.WaitKind != "person" && result.WaitKind != "transient" ||
		result.Outcome != "WAITING" && result.WaitKind != "" ||
		result.PersonHandoff && (result.Outcome != "WAITING" || result.WaitKind != "person") {
		return nil, errors.New("invalid or mismatched Pilot PR result")
	}
	return &result, nil
}

func (a *managedPRAgents) checkPilotResult(ctx context.Context, phase prPhase) error {
	task := a.pilotTask
	if task == nil || task.Phase != phase || task.ignoredHead {
		return nil
	}
	result, err := readPilotResult(task)
	if err != nil {
		return fmt.Errorf("PR #%d: %w", task.PR, err)
	}
	if result == nil {
		return nil
	}
	switch result.Outcome {
	case "READY", "DRAFT", "ENQUEUED", "MERGED":
		// Only GitHub can confirm these transitions.
		return nil
	case "DRAFT-BLOCKED", "WAITING", "WAIT-INTERRUPTED", "SKIP", "ERROR":
	default:
		return fmt.Errorf("PR #%d: unexpected Pilot result", task.PR)
	}
	view, err := readPilotPR(ctx, task.Repo, task.PR)
	if err != nil {
		// Recheck at the next successful state poll; the result alone is not authority.
		return nil
	}
	if view.phase != phase {
		return nil
	}
	if view.Head != result.Head {
		task.ignoredHead = true
		fmt.Fprintf(a.out, "PR #%d ignored a Pilot result for an older HEAD.\n", task.PR)
		return nil
	}
	if result.WaitKind != "" {
		return fmt.Errorf("Pilot PR #%d %s reported %s (%s, person hand-off: %t) at HEAD %.12s: %s",
			task.PR, phase, result.Outcome, result.WaitKind, result.PersonHandoff, result.Head, result.Reason)
	}
	return fmt.Errorf("Pilot PR #%d %s reported %s at HEAD %.12s: %s", task.PR, phase, result.Outcome, result.Head, result.Reason)
}
