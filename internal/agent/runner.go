package agent

import (
	"bufio"
	"context"
	cfg "github.com/llmore/tmux-ai/internal/config"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
)

// sanitizeLine keeps only printable ASCII, trims trailing space, and caps at 100 chars.
func sanitizeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r < 127 {
			b.WriteRune(r)
		}
	}
	result := strings.TrimRight(b.String(), " \t")
	if len(result) > 100 {
		result = result[:100]
	}
	return result
}

type Runner struct {
	ollamaClient      *ollama.Client
	tmux              *tmux.Executor
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

func (r *Runner) saveCheckpoint(messages []ollama.Message) {
	if r.CheckpointFile == "" {
		return
	}
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		if r.verbose {
			fmt.Printf("Error marshaling checkpoint: %v\n", err)
		}
		return
	}
	if err := os.WriteFile(r.CheckpointFile, data, 0644); err != nil {
		if r.verbose {
			fmt.Printf("Error writing checkpoint: %v\n", err)
		}
	}
}

func (r *Runner) getTmuxState() (string, error) {
	panes, err := r.tmux.ListPanesDetailed()
	if err != nil {
		return "", err
	}
	if len(panes) == 0 {
		return "No tmux panes found.\n", nil
	}

	type winKey struct {
		session string
		winIdx  int
	}
	type winInfo struct {
		name         string
		sessionName  string
		winIdx       int
		windowActive bool
		panes        []tmux.PaneInfo
	}
	winOrder := []winKey{}
	winMap := map[winKey]*winInfo{}

	for _, p := range panes {
		k := winKey{p.SessionName, p.WindowIndex}
		if _, ok := winMap[k]; !ok {
			winOrder = append(winOrder, k)
			winMap[k] = &winInfo{
				name:         p.WindowName,
				sessionName:  p.SessionName,
				winIdx:       p.WindowIndex,
				windowActive: p.WindowActive,
			}
		}
		winMap[k].panes = append(winMap[k].panes, p)
	}

	var sb strings.Builder
	for _, k := range winOrder {
		w := winMap[k]
		active := ""
		if w.windowActive {
			active = " (active)"
		}
		// Include the tmux target string so the agent can pass it directly to select_window
		target := fmt.Sprintf("%s:%d", w.sessionName, w.winIdx)
		sb.WriteString(fmt.Sprintf("Session %s - Window %d [target: %s]: %s%s\n", w.sessionName, w.winIdx, target, w.name, active))
		for _, p := range w.panes {
			pActive := ""
			if p.PaneActive {
				pActive = " *"
			}
			sb.WriteString(fmt.Sprintf("  Pane %s (index %d)%s - running: %s\n", p.PaneID, p.PaneIndex, pActive, p.Command))
			if content, err := r.tmux.CapturePaneClean(p.PaneID, 5); err == nil {
				lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
				shown := 0
				for i := len(lines) - 1; i >= 0 && shown < 3; i-- {
					clean := sanitizeLine(lines[i])
					if clean != "" {
						sb.WriteString(fmt.Sprintf("    | %s\n", clean))
						shown++
					}
				}
			}
		}
	}
	return sb.String(), nil
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
		shells := map[string]bool{
			"bash": true, "zsh": true, "sh": true, "fish": true, "ksh": true, "csh": true, "tmux": true,
		}
		if shells[cmdName] {
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

func (r *Runner) sendTerminalNotification(title, message string) {
	osc99 := fmt.Sprintf("\x1b]9;9;%s: %s\x07", title, message)
	osc9 := fmt.Sprintf("\x1b]9;%s: %s\x07", title, message)
	osc777 := fmt.Sprintf("\x1b]777;notify;%s;%s\x07", title, message)

	if os.Getenv("TMUX") != "" {
		osc99 = wrapTmuxPassthrough(osc99)
		osc9 = wrapTmuxPassthrough(osc9)
		osc777 = wrapTmuxPassthrough(osc777)
	}

	fmt.Print(osc99)
	fmt.Print(osc9)
	fmt.Print(osc777)
}

func wrapTmuxPassthrough(seq string) string {
	// Double the ESC (\x1b) inside the sequence to pass it through tmux correctly
	escaped := strings.ReplaceAll(seq, "\x1b", "\x1b\x1b")
	return "\x1bPtmux;" + escaped + "\x1b\\"
}

func (r *Runner) generateReport(messages []ollama.Message) {
	if r.ReportFile == "" {
		return
	}

	isMarkdown := strings.HasSuffix(r.ReportFile, ".md") || strings.HasSuffix(r.ReportFile, ".markdown")

	type TemplateData struct {
		Time     string
		Steps    int
		Success  bool
		Messages []ollama.Message
	}

	data := TemplateData{
		Time:     time.Now().Format("2006-01-02 15:04:05"),
		Steps:    len(messages),
		Success:  r.LastOutput != "",
		Messages: messages,
	}

	funcs := template.FuncMap{
		"add": func(a, b int) int {
			return a + b
		},
		"jsonToString": func(b []byte) string {
			return string(b)
		},
	}

	var tmplSrc string
	if isMarkdown {
		tmplSrc = `# tmux-ai 執行軌跡報告

* **產出時間:** {{.Time}}
* **決策步數:** {{.Steps}}
* **執行狀態:** {{if .Success}}✓ 執行成功{{else}}✗ 執行中斷 / 失敗{{end}}

---

## 決策與工具呼叫時間軸

{{range $i, $m := .Messages}}
### 步驟 #{{add $i 1}} - **{{$m.Role}}**

{{if $m.Content}}
#### 思考與內容
BKTBKTBKTtext
{{$m.Content}}
BKTBKTBKT
{{end}}

{{if $m.ToolCalls}}
#### 呼叫工具
{{range $tc := $m.ToolCalls}}
* **工具名稱:** BKT{{$tc.Function.Name}}BKT
* **參數:**
  BKTBKTBKTjson
  {{jsonToString $tc.Function.Arguments}}
  BKTBKTBKT
{{end}}
{{end}}

---
{{end}}`
		tmplSrc = strings.ReplaceAll(tmplSrc, "BKT", "`")
	} else {
		tmplSrc = `<!DOCTYPE html>
<html lang="zh-TW" class="dark">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>tmux-ai 執行軌跡報告</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script>
        tailwind.config = {
            darkMode: 'class',
            theme: {
                extend: {
                    colors: {
                        brand: {
                            50: '#f0f9ff',
                            100: '#e0f2fe',
                            500: '#0ea5e9',
                            600: '#0284c7',
                            900: '#0c4a6e',
                        }
                    }
                }
            }
        }
    </script>
    <style>
        body {
            background-color: #0f172a;
            color: #e2e8f0;
            font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
        }
    </style>
</head>
<body class="p-6 md:p-12 min-h-screen">
    <div class="max-w-5xl mx-auto space-y-8">
        <!-- Header -->
        <div class="flex flex-col md:flex-row md:items-center justify-between bg-slate-800/50 p-6 rounded-2xl border border-slate-700/50 backdrop-blur-sm gap-4">
            <div>
                <h1 class="text-3xl font-extrabold text-white tracking-tight flex items-center gap-2">
                    <span class="text-sky-400">tmux-ai</span> 執行軌跡報告
                </h1>
                <p class="text-sm text-slate-400 mt-1">產出時間: {{.Time}} | 步數: {{.Steps}}</p>
            </div>
            <div>
                {{if .Success}}
                <span class="inline-flex items-center px-4 py-2 rounded-full text-sm font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                    ✓ 執行成功
                </span>
                {{else}}
                <span class="inline-flex items-center px-4 py-2 rounded-full text-sm font-semibold bg-rose-500/10 text-rose-400 border border-rose-500/20">
                    ✗ 執行中斷 / 失敗
                </span>
                {{end}}
            </div>
        </div>

        <!-- Timeline -->
        <div class="space-y-6">
            <h2 class="text-xl font-bold text-white flex items-center gap-2">
                <span class="w-1.5 h-6 bg-sky-500 rounded-full"></span>
                決策與工具呼叫時間軸
            </h2>
            
            <div class="relative border-l border-slate-700 ml-4 space-y-8">
                {{range $i, $m := .Messages}}
                <div class="relative pl-8 group">
                    <!-- Point -->
                    <div class="absolute -left-[25px] top-1.5 w-4 h-4 rounded-full border-2 border-slate-900 bg-slate-700 group-hover:bg-sky-500 transition-colors duration-200"></div>
                    
                    <!-- Content Card -->
                    <div class="bg-slate-800/40 border border-slate-700/50 rounded-xl p-5 hover:border-slate-600 transition-colors duration-200 space-y-3">
                        <div class="flex items-center justify-between">
                            <span class="text-xs font-mono uppercase tracking-wider px-2.5 py-1 rounded bg-slate-900/60 {{if eq $m.Role "system"}}text-slate-400{{else if eq $m.Role "user"}}text-sky-400{{else if eq $m.Role "assistant"}}text-indigo-400{{else}}text-amber-400{{end}}">
                                {{$m.Role}}
                            </span>
                            <span class="text-xs text-slate-500">步驟 #{{add $i 1}}</span>
                        </div>
                        
                        <!-- Content Body -->
                        <div class="text-sm text-slate-300 leading-relaxed whitespace-pre-wrap font-mono">{{$m.Content}}</div>

                        <!-- Tool Calls -->
                        {{if $m.ToolCalls}}
                        <div class="mt-4 pt-3 border-t border-slate-700/50 space-y-2">
                            <span class="text-xs font-bold text-slate-400 uppercase tracking-wide">呼叫工具:</span>
                            {{range $tc := $m.ToolCalls}}
                            <div class="bg-slate-900/60 p-3 rounded-lg border border-slate-800 font-mono text-xs text-indigo-300 space-y-1">
                                <div class="font-bold text-indigo-400">{{$tc.Function.Name}}</div>
                                <div class="text-slate-400 whitespace-pre-wrap">{{jsonToString $tc.Function.Arguments}}</div>
                            </div>
                            {{end}}
                        </div>
                        {{end}}
                    </div>
                </div>
                {{end}}
            </div>
        </div>
    </div>
</body>
</html>`
	}

	tmpl, err := template.New("report").Funcs(funcs).Parse(tmplSrc)
	if err != nil {
		if r.verbose {
			fmt.Printf("Error parsing template: %v\n", err)
		}
		return
	}

	file, err := os.Create(r.ReportFile)
	if err != nil {
		if r.verbose {
			fmt.Printf("Error creating report file: %v\n", err)
		}
		return
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		if r.verbose {
			fmt.Printf("Error executing template: %v\n", err)
		}
	} else if r.verbose {
		fmt.Printf("Successfully generated report at: %s\n", r.ReportFile)
	}
}