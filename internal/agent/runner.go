package agent

import (
	"bufio"
	"context"
	cfg "github.com/llmore/tmux-ai/internal/config"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/llmore/tmux-ai/internal/tmuxutil"
)


type Runner struct {
	ollamaClient      ollama.Client
	tmux              tmux.Executor
	maxIterations     int
	verbose           bool
	CheckpointFile    string
	ReportFile        string
	TargetPane        string // hint: agent should focus on this pane
	ExtraContext      string // prepended as "Captured context" in the initial message
	LastOutput        string // final text response from the agent (set after Run completes)
	SkipGlobalContext bool   // set true to suppress ~/.config/tmux-ai/context.md
}

func NewRunner(ollamaHost, model string, maxIterations int, verbose bool) *Runner {
	return &Runner{
		ollamaClient:  ollama.NewClient(ollamaHost, model),
		tmux:          tmux.NewExecutor(),
		maxIterations: maxIterations,
		verbose:       verbose,
	}
}

func (r *Runner) Run(ctx context.Context, instruction string) error {
	var messages []ollama.Message
	defer func() {
		if r.ReportFile != "" && len(messages) > 0 {
			r.generateReport(messages)
		}
	}()

	if r.CheckpointFile != "" {
		if data, err := os.ReadFile(r.CheckpointFile); err == nil {
			if err := json.Unmarshal(data, &messages); err == nil {
				if r.verbose {
					fmt.Printf("Resumed %d messages from checkpoint: %s\n", len(messages), r.CheckpointFile)
				}
			}
		}
	}

	if len(messages) == 0 {
		tmuxState, err := r.getTmuxState()
		if err != nil {
			return fmt.Errorf("get tmux state: %w", err)
		}

		extra := r.ExtraContext
		if !r.SkipGlobalContext {
			if gc := cfg.LoadGlobalContext(); gc != "" {
				if extra != "" {
					extra = "Global context:\n" + gc + "\n\n" + extra
				} else {
					extra = "Global context:\n" + gc
				}
			}
		}

		messages = []ollama.Message{
			{Role: "system", Content: SystemPrompt},
			BuildInitialMessage(instruction, tmuxState, r.TargetPane, extra),
		}
	}

	for i := 0; i < r.maxIterations; i++ {
		if r.verbose {
			fmt.Printf("[Iteration %d] Calling Ollama...\n", i+1)
		}

		resp, err := r.ollamaClient.Chat(ctx, messages, ToolDefinitions)
		if err != nil {
			return fmt.Errorf("ollama chat: %w", err)
		}

		if len(resp.Message.ToolCalls) > 0 {
			if r.verbose {
				fmt.Printf("[Iteration %d] Got %d tool calls\n", i+1, len(resp.Message.ToolCalls))
			}

			messages = append(messages, resp.Message)

			for _, tc := range resp.Message.ToolCalls {
				result := r.executeTool(ctx, tc)
				messages = append(messages, ollama.Message{
					Role:      "tool",
					ToolCallID: tc.ID,
					Content:   result,
				})
			}
			r.saveCheckpoint(messages)
			continue
		}

		if resp.Message.Content != "" {
			fmt.Println(resp.Message.Content)
			r.LastOutput = resp.Message.Content
			r.saveCheckpoint(messages)
			return nil
		}
	}

	return fmt.Errorf("max iterations (%d) reached", r.maxIterations)
}

func (r *Runner) executeTool(ctx context.Context, tc ollama.ToolCall) string {
	var args map[string]interface{}
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		return fmt.Sprintf("error: failed to parse arguments: %v", err)
	}

	switch tc.Function.Name {
	case "capture_pane":
		paneID := args["pane_id"].(string)
		lines := 100
		if l, ok := args["lines"].(float64); ok {
			lines = int(l)
		}
		content, err := r.tmux.CapturePane(paneID, lines)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return content

	case "capture_pane_clean":
		paneID := args["pane_id"].(string)
		lines := 100
		if l, ok := args["lines"].(float64); ok {
			lines = int(l)
		}
		content, err := r.tmux.CapturePaneClean(paneID, lines)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return content

	case "send_keys":
		paneID := args["pane_id"].(string)
		keys := args["keys"].(string)
		if err := r.tmux.SendKeys(paneID, keys); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "send_lines":
		paneID := args["pane_id"].(string)
		content := args["content"].(string)
		if err := r.tmux.SendLines(paneID, content); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "list_panes":
		var windowID string
		if w, ok := args["window_id"].(string); ok {
			windowID = w
		}
		panes, err := r.tmux.ListPanes(windowID)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return fmt.Sprintf("%v", panes)

	case "list_windows":
		var sessionID string
		if s, ok := args["session_id"].(string); ok {
			sessionID = s
		}
		windows, err := r.tmux.ListWindows(sessionID)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return fmt.Sprintf("%v", windows)

	case "new_window":
		var name, command, workingDir string
		if n, ok := args["name"].(string); ok {
			name = n
		}
		if c, ok := args["command"].(string); ok {
			command = c
		}
		if w, ok := args["working_dir"].(string); ok {
			workingDir = w
		}
		result, err := r.tmux.NewWindow(name, command, workingDir)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return result

	case "split_pane":
		target := args["target"].(string)
		var direction, command string
		if d, ok := args["direction"].(string); ok {
			direction = d
		}
		if c, ok := args["command"].(string); ok {
			command = c
		}
		if err := r.tmux.SplitPane(target, direction, command); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "select_pane":
		paneID := args["pane_id"].(string)
		if err := r.tmux.SelectPane(paneID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "select_window":
		windowID := args["window_id"].(string)
		if err := r.tmux.SelectWindow(windowID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "kill_pane":
		paneID := args["pane_id"].(string)
		if err := r.tmux.KillPane(paneID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "kill_window":
		windowID := args["window_id"].(string)
		if err := r.tmux.KillWindow(windowID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "sleep":
		ms := int(args["ms"].(float64))
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return "ok"

	case "wait_for_pattern":
		paneID := args["pane_id"].(string)
		pattern := args["pattern"].(string)
		absent := false
		if a, ok := args["absent"].(bool); ok {
			absent = a
		}
		timeoutMs := 30000
		if t, ok := args["timeout_ms"].(float64); ok {
			timeoutMs = int(t)
		}
		re, _ := regexp.Compile(pattern)
		interval := 1000 * time.Millisecond
		elapsed := 0
		for elapsed < timeoutMs {
			content, err := r.tmux.CapturePaneClean(paneID, 100)
			if err == nil {
				found := strings.Contains(content, pattern) || (re != nil && re.MatchString(content))
				if found != absent {
					return content
				}
			}
			time.Sleep(interval)
			elapsed += int(interval / time.Millisecond)
		}
		cond := "found"
		if absent {
			cond = "gone"
		}
		return fmt.Sprintf("error: pattern '%s' not %s in pane %s within %dms", pattern, cond, paneID, timeoutMs)

	case "ask_user":
		question := args["question"].(string)
		fmt.Printf("\n[tmux-ai] QUESTION: %s\n", question)
		fmt.Print("Your response: ")
		var input string
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			input = scanner.Text()
		}
		return input

	case "run_local_command":
		command := args["command"].(string)
		if r.verbose {
			fmt.Printf("Executing local command: %s\n", command)
		}
		cmd := exec.Command("bash", "-c", command)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("error: %v\noutput: %s", err, string(output))
		}
		return string(output)

	case "send_keys_and_wait":
		paneID := args["pane_id"].(string)
		keys := args["keys"].(string)
		pattern := args["expected_pattern"].(string)
		timeoutMs := 30000
		if t, ok := args["timeout_ms"].(float64); ok {
			timeoutMs = int(t)
		}
		if err := r.tmux.SendKeys(paneID, keys); err != nil {
			return fmt.Sprintf("error: failed to send keys: %v", err)
		}
		interval := 1000 * time.Millisecond
		elapsed := 0
		for elapsed < timeoutMs {
			content, err := r.tmux.CapturePaneClean(paneID, 100)
			if err == nil {
				if strings.Contains(content, pattern) {
					return content
				}
				if re, err := regexp.Compile(pattern); err == nil && re.MatchString(content) {
					return content
				}
			}
			time.Sleep(interval)
			elapsed += int(interval / time.Millisecond)
		}
		// Send Ctrl+C (\x03) to abort
		_ = r.tmux.SendKeys(paneID, "\x03")
		return fmt.Sprintf("error: pattern '%s' not found within %dms. sent Ctrl+C to abort.", pattern, timeoutMs)

	case "abort_command":
		paneID := args["pane_id"].(string)
		if err := r.tmux.SendKeys(paneID, "\x03"); err != nil {
			return fmt.Sprintf("error: failed to send Ctrl+C: %v", err)
		}
		return "sent Ctrl+C to abort command"

	case "log_progress":
		stage := args["stage"].(string)
		message := args["message"].(string)
		level := "info"
		if l, ok := args["level"].(string); ok {
			level = l
		}
		colorCode := "\033[36m" // cyan
		if level == "success" {
			colorCode = "\033[32m" // green
		} else if level == "warning" {
			colorCode = "\033[33m" // yellow
		} else if level == "error" {
			colorCode = "\033[31m" // red
		}
		resetCode := "\033[0m"
		fmt.Printf("\n%s[%s] %s%s\n", colorCode, strings.ToUpper(stage), message, resetCode)
		return "ok"

	case "find_pane_by_process":
		processName := args["process_name"].(string)
		out, err := r.tmux.Exec("list-panes", "-a", "-F", "#{pane_id}:#{pane_current_command}")
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		var matchingPanes []string
		for _, line := range lines {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				pID := parts[0]
				pCmd := parts[1]
				if strings.Contains(pCmd, processName) {
					matchingPanes = append(matchingPanes, pID)
				}
			}
		}
		if len(matchingPanes) == 0 {
			return fmt.Sprintf("error: no pane running process containing '%s' found", processName)
		}
		return strings.Join(matchingPanes, ",")

	case "read_file":
		path := args["path"].(string)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return string(data)

	case "write_file":
		path := args["path"].(string)
		content := args["content"].(string)
		err := os.WriteFile(path, []byte(content), 0644)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "check_pane_idle":
		paneID := args["pane_id"].(string)
		cmdName, err := r.tmux.Exec("display-message", "-p", "-t", paneID, "#{pane_current_command}")
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		cmdName = strings.TrimSpace(cmdName)
		if tmuxutil.IsIdle(cmdName) {
			return "idle"
		}
		return fmt.Sprintf("busy (%s)", cmdName)

	case "search_pane_history":
		paneID := args["pane_id"].(string)
		query := args["query"].(string)
		linesBefore := 2
		if lb, ok := args["lines_before"].(float64); ok {
			linesBefore = int(lb)
		}
		linesAfter := 2
		if la, ok := args["lines_after"].(float64); ok {
			linesAfter = int(la)
		}
		out, err := r.tmux.Exec("capture-pane", "-p", "-t", paneID, "-S", "-3000")
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
		cleanOut := ansiRegex.ReplaceAllString(out, "")
		lines := strings.Split(cleanOut, "\n")
		re, err := regexp.Compile(query)
		isRegex := err == nil
		var matches []string
		for i, line := range lines {
			matched := false
			if isRegex {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(line, query)
			}
			if matched {
				start := i - linesBefore
				if start < 0 {
					start = 0
				}
				end := i + linesAfter
				if end >= len(lines) {
					end = len(lines) - 1
				}
				matchBlock := fmt.Sprintf("--- Match at line %d ---\n", i)
				for j := start; j <= end; j++ {
					prefix := "  "
					if j == i {
						prefix = "> "
					}
					matchBlock += fmt.Sprintf("%d: %s%s\n", j, prefix, lines[j])
				}
				matches = append(matches, matchBlock)
				if len(matches) >= 10 {
					matches = append(matches, "... (too many matches, showing first 10)")
					break
				}
			}
		}
		if len(matches) == 0 {
			return fmt.Sprintf("pattern '%s' not found in scrollback history", query)
		}
		return strings.Join(matches, "\n")

	case "replace_text_in_file":
		path := args["path"].(string)
		target := args["target_content"].(string)
		replacement := args["replacement_content"].(string)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		content := string(data)
		count := strings.Count(content, target)
		if count == 0 {
			return fmt.Sprintf("error: target content not found in file %s", path)
		}
		if count > 1 {
			return fmt.Sprintf("error: target content found %d times in file %s; must be unique to avoid ambiguity", count, path)
		}
		newContent := strings.Replace(content, target, replacement, 1)
		err = os.WriteFile(path, []byte(newContent), 0644)
		if err != nil {
			return fmt.Sprintf("error writing file: %v", err)
		}
		return "ok"

	case "git_status_summary":
		cmd := exec.Command("git", "status", "--porcelain")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("error running git status: %v. Output: %s", err, string(out))
		}
		statusStr := strings.TrimSpace(string(out))
		if statusStr == "" {
			return "no changes (clean working directory)"
		}
		return statusStr

	case "send_notification":
		title := args["title"].(string)
		message := args["message"].(string)
		urgency := "normal"
		if u, ok := args["urgency"].(string); ok {
			urgency = u
		}
		cmd := exec.Command("notify-send", "-u", urgency, title, message)
		err := cmd.Run()
		urgencyColor := "\033[36m"
		if urgency == "critical" {
			urgencyColor = "\033[31m"
		} else if urgency == "low" {
			urgencyColor = "\033[37m"
		}
		resetColor := "\033[0m"
		
		// Send notification escape sequences compatible with different terminal emulators
		// Wrapped with tmux passthrough if running inside tmux.
		r.sendTerminalNotification(title, message)
		
		fmt.Printf("\a\n%s[NOTIFICATION] %s: %s%s\n", urgencyColor, title, message, resetColor)
		if err != nil {
			return "notification printed to console (system notification failed or unavailable)"
		}
		return "notification sent successfully"

	case "clear_pane_history":
		paneID := args["pane_id"].(string)
		_, err := r.tmux.Exec("clear-history", "-t", paneID)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"

	case "finish":
		return "task completed"

	default:
		return fmt.Sprintf("error: unknown tool %s", tc.Function.Name)
	}
}

