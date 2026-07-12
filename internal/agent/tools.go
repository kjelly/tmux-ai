package agent

import "github.com/llmore/tmux-ai/internal/ollama"

var ToolDefinitions = []ollama.Tool{
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "capture_pane",
			Description: "Capture the visible content of a tmux pane. Use this to see what's currently displayed in a pane.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID (e.g., %0, %1, or session:0.0)",
					},
					"lines": map[string]interface{}{
						"type":        "integer",
						"description": "Number of lines to capture (default 100)",
					},
				},
				"required": []string{"pane_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "capture_pane_clean",
			Description: "Capture the visible content of a tmux pane with ANSI escape codes stripped. Use this for getting clean text output for parsing.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID (e.g., %0, %1, or session:0.0)",
					},
					"lines": map[string]interface{}{
						"type":        "integer",
						"description": "Number of lines to capture (default 100)",
					},
				},
				"required": []string{"pane_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_keys",
			Description: "Send keystrokes to a tmux pane. The keys will be typed as if the user pressed them. End commands with Enter.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The target pane ID",
					},
					"keys": map[string]interface{}{
						"type":        "string",
						"description": "The keys to send (e.g., 'make build\\n' or 'hello world')",
					},
				},
				"required": []string{"pane_id", "keys"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_lines",
			Description: "Send multi-line content to a tmux pane. Use this for sending code, long prompts, or content with newlines. Does NOT add Enter at the end.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The target pane ID",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The multi-line content to send (can include newlines, code snippets, etc.)",
					},
				},
				"required": []string{"pane_id", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "list_panes",
			Description: "List all tmux panes. Optionally filter by window.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"window_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional window ID to list panes within",
					},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "list_windows",
			Description: "List all tmux windows. Optionally filter by session.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional session ID to list windows within",
					},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "new_window",
			Description: "Create a new tmux window.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Optional window name",
					},
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Optional command to run in the window",
					},
					"working_dir": map[string]interface{}{
						"type":        "string",
						"description": "Optional working directory for the new window",
					},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "split_pane",
			Description: "Split a tmux pane horizontally or vertically.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Target pane to split",
					},
					"direction": map[string]interface{}{
						"type":        "string",
						"description": "horizontal or vertical",
					},
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Optional command to run in new pane",
					},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "select_pane",
			Description: "Navigate the user's tmux client to a specific pane. Use the pane_id (%0, %1, etc.) as shown in the tmux state.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID to navigate to (e.g. %27)",
					},
				},
				"required": []string{"pane_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "select_window",
			Description: "Navigate the user's tmux client to a specific window, including switching sessions if needed. Use the 'target' value shown in the tmux state (format: session:window_index, e.g. 'sport:1' or 'github:3').",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"window_id": map[string]interface{}{
						"type":        "string",
						"description": "The window target in 'session:window_index' format (e.g. 'sport:1'). Always use this format — never pass just a window name or number alone.",
					},
				},
				"required": []string{"window_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "kill_pane",
			Description: "Kill a tmux pane.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID to kill",
					},
				},
				"required": []string{"pane_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "kill_window",
			Description: "Kill a tmux window.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"window_id": map[string]interface{}{
						"type":        "string",
						"description": "The window ID to kill",
					},
				},
				"required": []string{"window_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "sleep",
			Description: "Wait for a specified duration in milliseconds.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"ms": map[string]interface{}{
						"type":        "integer",
						"description": "Milliseconds to wait",
					},
				},
				"required": []string{"ms"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "wait_for_pattern",
			Description: "Wait/poll a tmux pane until its visible content contains (or no longer contains) a pattern. Returns clean pane content once the condition is met.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID to monitor",
					},
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "The pattern (substring or regex) to search for in the pane output",
					},
					"absent": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, wait until the pattern is NO LONGER present (wait for text to disappear). Default false.",
					},
					"timeout_ms": map[string]interface{}{
						"type":        "integer",
						"description": "Optional timeout in milliseconds (default 30000)",
					},
				},
				"required": []string{"pane_id", "pattern"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "ask_user",
			Description: "Ask the human user a question when you need clarification, decision input, or guidance when stuck.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"question": map[string]interface{}{
						"type":        "string",
						"description": "The question to ask the user",
					},
				},
				"required": []string{"question"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "run_local_command",
			Description: "Execute a shell command locally in the workspace (outside of tmux). Returns stdout and stderr.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to run",
					},
				},
				"required": []string{"command"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_keys_and_wait",
			Description: "Send keystrokes to a tmux pane and block-wait until a pattern appears. If the pattern is not found within the timeout, it sends Ctrl+C to abort and returns.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The target pane ID",
					},
					"keys": map[string]interface{}{
						"type":        "string",
						"description": "The keys to send (e.g. 'make test\\n')",
					},
					"expected_pattern": map[string]interface{}{
						"type":        "string",
						"description": "The pattern (substring or regex) to wait for",
					},
					"timeout_ms": map[string]interface{}{
						"type":        "integer",
						"description": "Optional timeout in milliseconds (default 30000)",
					},
				},
				"required": []string{"pane_id", "keys", "expected_pattern"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "abort_command",
			Description: "Send Ctrl+C (\\x03) to a tmux pane to abort the currently running command.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The target pane ID",
					},
				},
				"required": []string{"pane_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "log_progress",
			Description: "Log structured progress of the task to the host console (stdout) to keep the user informed.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"stage": map[string]interface{}{
						"type":        "string",
						"description": "The current stage of execution (e.g. 'Initialization', 'Reviewing Code')",
					},
					"message": map[string]interface{}{
						"type":        "string",
						"description": "Descriptive progress update message",
					},
					"level": map[string]interface{}{
						"type":        "string",
						"description": "Log level: info, success, warning, or error",
					},
				},
				"required": []string{"stage", "message"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "find_pane_by_process",
			Description: "Find pane IDs by matching the active process command name (e.g., 'claude', 'node', 'python').",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"process_name": map[string]interface{}{
						"type":        "string",
						"description": "The command or process name to find",
					},
				},
				"required": []string{"process_name"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "read_file",
			Description: "Read the contents of a local file in the workspace.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "The absolute or relative path to the file",
					},
				},
				"required": []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "write_file",
			Description: "Write content to a local file in the workspace, overwriting existing content.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "The path to the file",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The text content to write to the file",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "check_pane_idle",
			Description: "Check if a tmux pane is currently idle (waiting at shell prompt) or busy running a command.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID to check",
					},
				},
				"required": []string{"pane_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "search_pane_history",
			Description: "Search the scrollback history buffer of a tmux pane for a pattern (substring or regex), returning matching lines with context.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID to search",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The pattern (substring or regex) to search for",
					},
					"lines_before": map[string]interface{}{
						"type":        "integer",
						"description": "Optional lines of context before the match (default 2)",
					},
					"lines_after": map[string]interface{}{
						"type":        "integer",
						"description": "Optional lines of context after the match (default 2)",
					},
				},
				"required": []string{"pane_id", "query"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "replace_text_in_file",
			Description: "Replace a unique, specific target text block in a file with a new replacement text block.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "The path to the file to modify",
					},
					"target_content": map[string]interface{}{
						"type":        "string",
						"description": "The exact unique substring inside the file that you want to replace",
					},
					"replacement_content": map[string]interface{}{
						"type":        "string",
						"description": "The new replacement text to insert",
					},
				},
				"required": []string{"path", "target_content", "replacement_content"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "git_status_summary",
			Description: "Get a summary of modified, untracked, and deleted files in the git workspace directory.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "send_notification",
			Description: "Send a system desktop notification or terminal alert to notify the user of an important event, progress milestone, or required action.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "The title of the notification",
					},
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The content/body of the notification",
					},
					"urgency": map[string]interface{}{
						"type":        "string",
						"description": "Optional urgency level: low, normal, or critical",
					},
				},
				"required": []string{"title", "message"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "clear_pane_history",
			Description: "Clear the scrollback history buffer of a tmux pane to prevent matching older process logs.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pane_id": map[string]interface{}{
						"type":        "string",
						"description": "The pane ID to clear history for",
					},
				},
				"required": []string{"pane_id"},
			},
		},
	},
	{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "finish",
			Description: "Mark the task as complete. Call this when you've accomplished the user's request.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "Summary of what was accomplished",
					},
				},
				"required": []string{"message"},
			},
		},
	},
}