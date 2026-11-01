package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/llmore/tmux-ai/internal/artifact"
	"github.com/spf13/cobra"
)

const maxCompletionSummaryBytes = 4096

type handoffTaskManifest struct {
	Version                  int    `json:"version"`
	TaskID                   string `json:"task_id"`
	MemberID                 string `json:"member_id"`
	Round                    int    `json:"round"`
	Outbox                   string `json:"outbox"`
	ExpectedPromptHeadSHA256 string `json:"expected_prompt_head_sha256"`
	StatusBeginMarker        string `json:"status_begin_marker"`
	StatusEndMarker          string `json:"status_end_marker"`
	DoneMarker               string `json:"done_marker"`
	StopMarker               string `json:"stop_marker"`
	AllowStop                bool   `json:"allow_stop"`
}

type handoffCompleteRequest struct {
	TaskPath, PromptHead, Outcome, Summary, Completion string
	Tests                                              testClaims
}

var (
	handoffTaskPath    string
	handoffPromptHead  string
	handoffOutcome     string
	handoffSummary     string
	handoffSummaryFile string
	handoffTestsFile   string
	handoffCompletion  string
)

var handoffCmd = &cobra.Command{
	Use:               "handoff",
	Short:             "Submit and inspect handoff control data",
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
}

var handoffCompleteCmd = &cobra.Command{
	Use:   "complete",
	Short: "Atomically submit a file-handoff receipt",
	RunE: func(cmd *cobra.Command, args []string) error {
		summary, err := completionSummary(handoffSummary, handoffSummaryFile)
		if err != nil {
			return err
		}
		claims, err := completionTests(handoffTestsFile)
		if err != nil {
			return err
		}
		return completeHandoff(handoffCompleteRequest{
			TaskPath: handoffTaskPath, PromptHead: handoffPromptHead, Outcome: handoffOutcome,
			Summary: summary, Tests: claims, Completion: handoffCompletion,
		}, cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(handoffCmd)
	handoffCmd.AddCommand(handoffCompleteCmd)
	flags := handoffCompleteCmd.Flags()
	flags.StringVar(&handoffTaskPath, "task", "", "Absolute path to the runner-owned handoff task manifest")
	flags.StringVar(&handoffPromptHead, "prompt-head", "", "Nonce copied from the prompt head line")
	flags.StringVar(&handoffOutcome, "outcome", "", "Completion outcome: success, needs_followup, or blocked")
	flags.StringVar(&handoffSummary, "summary", "", "Short completion summary")
	flags.StringVar(&handoffSummaryFile, "summary-file", "", "File containing the completion summary")
	flags.StringVar(&handoffTestsFile, "tests-file", "", "JSON file containing receipt tests")
	flags.StringVar(&handoffCompletion, "completion", "done", "Completion marker: done or stop")
	_ = handoffCompleteCmd.MarkFlagRequired("task")
	_ = handoffCompleteCmd.MarkFlagRequired("prompt-head")
	_ = handoffCompleteCmd.MarkFlagRequired("outcome")
}

// completionCommand returns the exact executable running the workflow. Agent
// panes commonly have a different PATH from the runner (and can therefore
// resolve an older tmux-ai that lacks the helper command). Do not silently
// fall back to PATH: helper mode is a versioned control-plane contract.
func (e handoffEngine) completionCommand() (string, error) {
	if command := strings.TrimSpace(e.CompletionCommand); command != "" {
		return command, nil
	}
	command, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate runner executable: %w", err)
	}
	command, err = filepath.Abs(command)
	if err != nil {
		return "", fmt.Errorf("make runner executable path absolute: %w", err)
	}
	info, err := os.Stat(command)
	if err != nil {
		return "", fmt.Errorf("stat runner executable %q: %w", command, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("runner executable %q is not executable", command)
	}
	return command, nil
}

func writeHandoffTaskManifest(p roundControlProtocol, member handoffMember) (string, error) {
	if p.Outbox == "" || p.TaskID == "" || p.HeadNonce == "" {
		return "", fmt.Errorf("incomplete handoff task protocol")
	}
	absOutbox, err := filepath.Abs(p.Outbox)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(p.HeadNonce))
	task := handoffTaskManifest{
		Version: 1, TaskID: p.TaskID, MemberID: member.ID, Round: protocolRound(p), Outbox: absOutbox,
		ExpectedPromptHeadSHA256: hex.EncodeToString(sum[:]), StatusBeginMarker: p.StatusBeginMarker,
		StatusEndMarker: p.StatusEndMarker, DoneMarker: p.DoneMarker, StopMarker: p.StopMarker,
		AllowStop: !p.CoordinatorControlsStop,
	}
	data, err := json.Marshal(task)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absOutbox, artifact.TaskFileName)
	if err := atomicReplaceFile(path, data); err != nil {
		return "", fmt.Errorf("write handoff task manifest: %w", err)
	}
	return path, nil
}

func completionSummary(value, file string) (string, error) {
	if value != "" && file != "" {
		return "", errors.New("only one of --summary and --summary-file may be used")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read summary file: %w", err)
		}
		value = string(data)
	}
	value = strings.Join(strings.Fields(strings.ReplaceAll(value, "\r", "\n")), " ")
	if value == "" {
		return "", errors.New("completion summary is required")
	}
	if len([]byte(value)) > maxCompletionSummaryBytes {
		return "", fmt.Errorf("completion summary exceeds %d bytes", maxCompletionSummaryBytes)
	}
	return value, nil
}

func completionTests(path string) (testClaims, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tests file: %w", err)
	}
	var claims testClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, fmt.Errorf("parse tests file: %w", err)
	}
	return claims, nil
}

func completeHandoff(request handoffCompleteRequest, stdout io.Writer) error {
	task, err := readHandoffTaskManifest(request.TaskPath)
	if err != nil {
		return err
	}
	if request.Outcome != "success" && request.Outcome != "needs_followup" && request.Outcome != "blocked" {
		return fmt.Errorf("invalid handoff outcome %q", request.Outcome)
	}
	if request.Completion == "" {
		request.Completion = "done"
	}
	if request.Completion != "done" && request.Completion != "stop" {
		return fmt.Errorf("invalid completion mode %q", request.Completion)
	}
	if request.Completion == "stop" && !task.AllowStop {
		return errors.New("this task is not allowed to stop the workflow")
	}
	summary, err := completionSummary(request.Summary, "")
	if err != nil {
		return err
	}
	headSum := sha256.Sum256([]byte(strings.TrimSpace(request.PromptHead)))
	if hex.EncodeToString(headSum[:]) != task.ExpectedPromptHeadSHA256 {
		return errors.New("prompt head does not match this task")
	}
	if err := validateCompletionOutbox(task.Outbox); err != nil {
		return err
	}
	status := handoffStatus{Version: 1, Outcome: request.Outcome, Summary: summary, PromptHead: strings.TrimSpace(request.PromptHead), Tests: request.Tests}
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if err := publishReceipt(filepath.Join(task.Outbox, artifact.ReceiptFileName), data); err != nil {
		return err
	}
	marker := task.DoneMarker
	if request.Completion == "stop" {
		marker = task.StopMarker
	}
	if _, err := fmt.Fprintf(stdout, "%s\n%s\n%s\n%s\n", task.StatusBeginMarker, data, task.StatusEndMarker, marker); err != nil {
		return fmt.Errorf("print completion envelope: %w", err)
	}
	return nil
}

func readHandoffTaskManifest(path string) (handoffTaskManifest, error) {
	if !filepath.IsAbs(path) {
		return handoffTaskManifest{}, errors.New("task manifest path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return handoffTaskManifest{}, fmt.Errorf("read task manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return handoffTaskManifest{}, errors.New("task manifest is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return handoffTaskManifest{}, err
	}
	var task handoffTaskManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		return handoffTaskManifest{}, fmt.Errorf("parse task manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return handoffTaskManifest{}, errors.New("task manifest must contain exactly one JSON object")
	}
	if task.Version != 1 || task.TaskID == "" || task.MemberID == "" || task.Round <= 0 || task.Outbox == "" || len(task.ExpectedPromptHeadSHA256) != 64 || task.StatusBeginMarker == "" || task.StatusEndMarker == "" || task.DoneMarker == "" || task.StopMarker == "" {
		return handoffTaskManifest{}, errors.New("task manifest is incomplete")
	}
	absOutbox, err := filepath.Abs(task.Outbox)
	if err != nil || absOutbox != task.Outbox || filepath.Dir(path) != task.Outbox {
		return handoffTaskManifest{}, errors.New("task manifest outbox does not match its location")
	}
	return task, nil
}

func validateCompletionOutbox(outbox string) error {
	return filepath.WalkDir(outbox, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == outbox || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(outbox, path)
		if err != nil {
			return err
		}
		if rel == artifact.ReceiptFileName || rel == artifact.TaskFileName {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("outbox artifact %q is a symlink", rel)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("outbox artifact %q is not a regular file", rel)
		}
		return nil
	})
}

func atomicReplaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmux-ai-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func publishReceipt(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("existing receipt is not a regular file")
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Equal(existing, data) {
			return nil
		}
		return errors.New("conflicting receipt already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmux-ai-receipt-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(existing, data) {
				return nil
			}
			return errors.New("conflicting receipt already exists")
		}
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
