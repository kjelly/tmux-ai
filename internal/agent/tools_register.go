package agent

import (
	"bufio"
	"context"
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

// registerAllTools registers every tool with the given registry.
// This is the single source of truth for tool definitions AND handlers.
// The Runner calls this once during initialization.
func registerAllTools(reg *ToolRegistry, tmuxExec tmux.Executor, verbose bool) {

	// ── capture_pane ──
	reg.Register("capture_pane", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "capture_pane",
			Description: "Capture the visible content of a tmux pane. Use this to see what's currently displayed in a pane.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID (e.g., %0, %1, or session:0.0)"},
					"lines": {"type": "integer", "description": "Number of lines to capture (default 100)"}
				},
				"required": ["pane_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		lines := intArg(args, "lines", 100)
		content, err := tmuxExec.CapturePane(paneID, lines)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return content
	})

	// ── capture_pane_clean ──
	reg.Register("capture_pane_clean", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "capture_pane_clean",
			Description: "Capture the visible content of a tmux pane with ANSI escape codes stripped. Use this for getting clean text output for parsing.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID (e.g., %0, %1, or session:0.0)"},
					"lines": {"type": "integer", "description": "Number of lines to capture (default 100)"}
				},
				"required": ["pane_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		lines := intArg(args, "lines", 100)
		content, err := tmuxExec.CapturePaneClean(paneID, lines)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return content
	})

	// ── send_keys ──
	reg.Register("send_keys", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_keys",
			Description: "Send keystrokes to a tmux pane. The keys will be typed as if the user pressed them. End commands with Enter.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The target pane ID"},
					"keys": {"type": "string", "description": "The keys to send"}
				},
				"required": ["pane_id", "keys"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		keys := strArg(args, "keys")
		if err := tmuxExec.SendKeys(paneID, keys); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── send_lines ──
	reg.Register("send_lines", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_lines",
			Description: "Send multi-line content to a tmux pane. Useful for pasting blocks of text.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The target pane ID"},
					"content": {"type": "string", "description": "The multi-line content to send"}
				},
				"required": ["pane_id", "content"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		content := strArg(args, "content")
		if err := tmuxExec.SendLines(paneID, content); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── list_panes ──
	reg.Register("list_panes", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "list_panes",
			Description: "List all tmux panes, optionally in a specific window.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"window_id": {"type": "string", "description": "Optional: list panes only in this window"}
				}
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		windowID := strArg(args, "window_id")
		panes, err := tmuxExec.ListPanes(windowID)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return fmt.Sprintf("%v", panes)
	})

	// ── list_windows ──
	reg.Register("list_windows", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "list_windows",
			Description: "List all tmux windows, optionally in a specific session.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"session_id": {"type": "string", "description": "Optional: list windows only in this session"}
				}
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		sessionID := strArg(args, "session_id")
		windows, err := tmuxExec.ListWindows(sessionID)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return fmt.Sprintf("%v", windows)
	})

	// ── new_window ──
	reg.Register("new_window", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "new_window",
			Description: "Create a new tmux window with an optional name, command, and working directory.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Window name"},
					"command": {"type": "string", "description": "Command to run in the new window"},
					"working_dir": {"type": "string", "description": "Working directory for the new window"}
				}
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		name := strArg(args, "name")
		command := strArg(args, "command")
		workingDir := strArg(args, "working_dir")
		result, err := tmuxExec.NewWindow(name, command, workingDir)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return result
	})

	// ── split_pane ──
	reg.Register("split_pane", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "split_pane",
			Description: "Split a tmux pane horizontally or vertically.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"target": {"type": "string", "description": "The target pane to split"},
					"direction": {"type": "string", "description": "horizontal or vertical"},
					"command": {"type": "string", "description": "Command to run in the new pane"}
				},
				"required": ["target"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		target := strArg(args, "target")
		direction := strArg(args, "direction")
		command := strArg(args, "command")
		if err := tmuxExec.SplitPane(target, direction, command); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── select_pane ──
	reg.Register("select_pane", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "select_pane",
			Description: "Select/focus a tmux pane.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID to select"}
				},
				"required": ["pane_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		if err := tmuxExec.SelectPane(paneID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── select_window ──
	reg.Register("select_window", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "select_window",
			Description: "Select/focus a tmux window.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"window_id": {"type": "string", "description": "The window ID to select"}
				},
				"required": ["window_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		windowID := strArg(args, "window_id")
		if err := tmuxExec.SelectWindow(windowID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── kill_pane ──
	reg.Register("kill_pane", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "kill_pane",
			Description: "Kill/close a tmux pane.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID to kill"}
				},
				"required": ["pane_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		if err := tmuxExec.KillPane(paneID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── kill_window ──
	reg.Register("kill_window", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "kill_window",
			Description: "Kill/close a tmux window.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"window_id": {"type": "string", "description": "The window ID to kill"}
				},
				"required": ["window_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		windowID := strArg(args, "window_id")
		if err := tmuxExec.KillWindow(windowID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── sleep ──
	reg.Register("sleep", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "sleep",
			Description: "Wait for a specified number of milliseconds.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"ms": {"type": "integer", "description": "Milliseconds to sleep"}
				},
				"required": ["ms"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		ms := intArg(args, "ms", 0)
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return "ok"
	})

	// ── wait_for_pattern ──
	reg.Register("wait_for_pattern", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "wait_for_pattern",
			Description: "Efficiently monitor/poll a pane until a specific pattern/regex is found (or disappears with absent=true).",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID to monitor"},
					"pattern": {"type": "string", "description": "Pattern to wait for (substring or regex)"},
					"absent": {"type": "boolean", "description": "If true, wait for pattern to disappear"},
					"timeout_ms": {"type": "integer", "description": "Timeout in milliseconds (default 30000)"}
				},
				"required": ["pane_id", "pattern"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		pattern := strArg(args, "pattern")
		absent := boolArg(args, "absent", false)
		timeoutMs := intArg(args, "timeout_ms", 30000)
		re, _ := regexp.Compile(pattern)
		interval := 1000 * time.Millisecond
		elapsed := 0
		for elapsed < timeoutMs {
			content, err := tmuxExec.CapturePaneClean(paneID, 100)
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
	})

	// ── ask_user ──
	reg.Register("ask_user", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "ask_user",
			Description: "Ask the human user for decision input, clarification, or guidance when stuck.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"question": {"type": "string", "description": "The question to ask the user"}
				},
				"required": ["question"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		question := strArg(args, "question")
		fmt.Printf("\n[tmux-ai] QUESTION: %s\n", question)
		fmt.Print("Your response: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return scanner.Text()
		}
		return ""
	})

	// ── run_local_command ──
	reg.Register("run_local_command", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "run_local_command",
			Description: "Run a local shell command in the workspace outside of tmux (e.g. to run tests or local checks).",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "The shell command to run"}
				},
				"required": ["command"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		command := strArg(args, "command")
		if verbose {
			fmt.Printf("Executing local command: %s\n", command)
		}
		cmd := exec.Command("bash", "-c", command)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("error: %v\noutput: %s", err, string(output))
		}
		return string(output)
	})

	// ── send_keys_and_wait ──
	reg.Register("send_keys_and_wait", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_keys_and_wait",
			Description: "Send keys to a pane and block-wait until an expected pattern appears (aborts with Ctrl+C on timeout).",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The target pane ID"},
					"keys": {"type": "string", "description": "The keys to send"},
					"expected_pattern": {"type": "string", "description": "Pattern to wait for"},
					"timeout_ms": {"type": "integer", "description": "Timeout in milliseconds (default 30000)"}
				},
				"required": ["pane_id", "keys", "expected_pattern"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		keys := strArg(args, "keys")
		pattern := strArg(args, "expected_pattern")
		timeoutMs := intArg(args, "timeout_ms", 30000)
		if err := tmuxExec.SendKeys(paneID, keys); err != nil {
			return fmt.Sprintf("error: failed to send keys: %v", err)
		}
		interval := 1000 * time.Millisecond
		elapsed := 0
		for elapsed < timeoutMs {
			content, err := tmuxExec.CapturePaneClean(paneID, 100)
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
		_ = tmuxExec.SendKeys(paneID, "\x03")
		return fmt.Sprintf("error: pattern '%s' not found within %dms. sent Ctrl+C to abort.", pattern, timeoutMs)
	})

	// ── abort_command ──
	reg.Register("abort_command", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "abort_command",
			Description: "Send Ctrl+C (\\x03) to a pane to abort a running command.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The target pane ID"}
				},
				"required": ["pane_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		if err := tmuxExec.SendKeys(paneID, "\x03"); err != nil {
			return fmt.Sprintf("error: failed to send Ctrl+C: %v", err)
		}
		return "sent Ctrl+C to abort command"
	})

	// ── log_progress ──
	reg.Register("log_progress", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "log_progress",
			Description: "Print structured execution status to the console to inform the user of progress.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"stage": {"type": "string", "description": "Stage name"},
					"message": {"type": "string", "description": "Progress message"},
					"level": {"type": "string", "description": "info, success, warning, or error"}
				},
				"required": ["stage", "message"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		stage := strArg(args, "stage")
		message := strArg(args, "message")
		level := strArg(args, "level")
		if level == "" {
			level = "info"
		}
		colorCode := "\033[36m" // cyan
		if level == "success" {
			colorCode = "\033[32m"
		} else if level == "warning" {
			colorCode = "\033[33m"
		} else if level == "error" {
			colorCode = "\033[31m"
		}
		fmt.Printf("\n%s[%s] %s\033[0m\n", colorCode, strings.ToUpper(stage), message)
		return "ok"
	})

	// ── find_pane_by_process ──
	reg.Register("find_pane_by_process", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "find_pane_by_process",
			Description: "Dynamically locate a pane ID by searching for running processes (e.g. 'claude', 'python').",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"process_name": {"type": "string", "description": "Process name to search for"}
				},
				"required": ["process_name"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		processName := strArg(args, "process_name")
		out, err := tmuxExec.Exec("list-panes", "-a", "-F", "#{pane_id}:#{pane_current_command}")
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
	})

	// ── read_file ──
	reg.Register("read_file", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "read_file",
			Description: "Read file contents in the local workspace directly.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to read"}
				},
				"required": ["path"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		path := strArg(args, "path")
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return string(data)
	})

	// ── write_file ──
	reg.Register("write_file", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "write_file",
			Description: "Write or modify file content in the local workspace directly.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to write"},
					"content": {"type": "string", "description": "Content to write"}
				},
				"required": ["path", "content"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		path := strArg(args, "path")
		content := strArg(args, "content")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── check_pane_idle ──
	reg.Register("check_pane_idle", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "check_pane_idle",
			Description: "Check if a tmux pane is currently idle (waiting at a shell prompt) or busy.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID to check"}
				},
				"required": ["pane_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		cmdName, err := tmuxExec.Exec("display-message", "-p", "-t", paneID, "#{pane_current_command}")
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		cmdName = strings.TrimSpace(cmdName)
		if tmuxutil.IsIdle(cmdName) {
			return "idle"
		}
		return fmt.Sprintf("busy (%s)", cmdName)
	})

	// ── search_pane_history ──
	reg.Register("search_pane_history", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "search_pane_history",
			Description: "Search the entire scrollback history of a tmux pane for a pattern, returning matching lines with context.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID to search"},
					"query": {"type": "string", "description": "Search query (substring or regex)"},
					"lines_before": {"type": "integer", "description": "Context lines before match (default 2)"},
					"lines_after": {"type": "integer", "description": "Context lines after match (default 2)"}
				},
				"required": ["pane_id", "query"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		query := strArg(args, "query")
		linesBefore := intArg(args, "lines_before", 2)
		linesAfter := intArg(args, "lines_after", 2)
		out, err := tmuxExec.Exec("capture-pane", "-p", "-t", paneID, "-S", "-3000")
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
	})

	// ── replace_text_in_file ──
	reg.Register("replace_text_in_file", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "replace_text_in_file",
			Description: "Replace a specific unique target text block in a file with a new replacement text block (recommended over reading/writing the whole file to save tokens).",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path"},
					"target_content": {"type": "string", "description": "The exact text to find"},
					"replacement_content": {"type": "string", "description": "The replacement text"}
				},
				"required": ["path", "target_content", "replacement_content"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		path := strArg(args, "path")
		target := strArg(args, "target_content")
		replacement := strArg(args, "replacement_content")
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
		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			return fmt.Sprintf("error writing file: %v", err)
		}
		return "ok"
	})

	// ── git_status_summary ──
	reg.Register("git_status_summary", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "git_status_summary",
			Description: "Get a quick summary of modified, added, and deleted files in the git workspace.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {}
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
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
	})

	// ── send_notification ──
	// This tool needs access to the Runner's sendTerminalNotification method,
	// so it's registered separately in runner.go where it has access to *Runner.

	// ── clear_pane_history ──
	reg.Register("clear_pane_history", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "clear_pane_history",
			Description: "Clear the scrollback history buffer of a tmux pane to prevent matching older process logs.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"pane_id": {"type": "string", "description": "The pane ID to clear history for"}
				},
				"required": ["pane_id"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		paneID := strArg(args, "pane_id")
		_, err := tmuxExec.Exec("clear-history", "-t", paneID)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// ── finish ──
	reg.Register("finish", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "finish",
			Description: "Mark the task as complete. Call this when you've accomplished the user's request.",
			Parameters: mustSchema(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Summary of what was accomplished"}
				},
				"required": ["message"]
			}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		return "task completed"
	})
}

// mustSchema parses a JSON schema string. Panics on error (schemas are static).
func mustSchema(jsonStr string) map[string]interface{} {
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &schema); err != nil {
		panic(fmt.Sprintf("invalid tool schema: %v", err))
	}
	return schema
}
