# tmux-ai Design Specification

## Overview

A Go CLI program that accepts natural language instructions and uses Ollama (LLM) with function calling to interact with tmux. The program acts as an agent that captures tmux state, sends it to the LLM, and executes tool calls returned by the LLM in a loop until the task is complete.

## Changelog

| Date | Change |
|------|--------|
| 2026-06-10 | Initial design |
| 2026-06-11 | Added `launch` workflow step type; `new_window` gains `working_dir`; `{{steps.NAME.output}}` interpolation in workflow executor |

## Architecture

```
User Input (natural language)
         │
         ▼
  ┌──────────────┐     tool calls      ┌──────────────┐
  │  Agent Loop   │ ◄────────────────► │  Ollama API  │
  │  (Runner)    │   tool results      │  /api/chat   │
  └──────┬───────┘                     └──────────────┘
         │ execute tools
         ▼
  ┌──────────────┐
  │ Tmux Executor│ ── wraps tmux CLI commands
  └──────────────┘
```

## Interaction Flow

1. User provides a natural language instruction via CLI
2. Program captures current tmux environment (sessions, windows, panes, pane contents)
3. Agent sends instruction + tmux context to Ollama with tool definitions
4. Ollama returns tool calls (function calls) based on the instruction
5. Program executes each tool call and captures results
6. Results are sent back to Ollama for the next decision
7. Loop continues until Ollama calls the `finish` tool or returns a plain text response
8. Final response is printed to stdout

## Components

### CLI Entry Point (`cmd/`)
- `root.go` - Cobra root command with global flags
- `run.go` - `run` subcommand that executes the agent

### Ollama Client (`internal/ollama/`)
- `client.go` - HTTP client for Ollama `/api/chat` endpoint
- Handles tool calling response parsing
- Manages message history

### Agent Runner (`internal/agent/`)
- `runner.go` - Main agent loop logic
- `messages.go` - Message construction (system prompt, user message)
- `tools.go` - Tool definitions sent to LLM

### Tmux Executor (`internal/tmux/`)
- `executor.go` - Executes tmux CLI commands
- `capture.go` - `capture-pane` functionality
- `keys.go` - `send-keys` functionality
- `window.go` - Window/pane management (new, split, kill, select)
- `list.go` - List sessions, windows, panes

## Tool Definitions (Functions)

These tools are sent to the LLM via Ollama's function calling API.

| Tool Name | Parameters | Description |
|-----------|------------|-------------|
| `capture_pane` | `pane_id: string, lines?: number` | Capture pane content (last N lines, default 100) |
| `send_keys` | `pane_id: string, keys: string` | Send keys to a pane |
| `list_panes` | `window_id?: string` | List all panes, optionally in a specific window |
| `list_windows` | `session_id?: string` | List all windows, optionally in a specific session |
| `new_window` | `name?: string, command?: string, working_dir?: string` | Create a new tmux window |
| `split_pane` | `target?: string, direction?: "horizontal"\|"vertical", command?: string` | Split a pane |
| `select_pane` | `pane_id: string` | Select/focus a pane |
| `select_window` | `window_id: string` | Select/focus a window |
| `kill_pane` | `pane_id: string` | Kill a pane |
| `kill_window` | `window_id: string` | Kill a window |
| `sleep` | `ms: number` | Wait for specified milliseconds |
| `finish` | `message: string` | Mark task as complete with summary message |

## Tmux Pane Identification

Panes can be identified by:
- Index: `%0`, `%1`, etc. (tmux pane IDs)
- The program normalizes pane references for the LLM

## Agent Loop Algorithm

```
1. capture tmux environment state
2. build messages: [system_prompt, user_instruction + tmux_context]
3. iteration = 0
4. loop while iteration < max_iterations:
   a. response = ollama.chat(messages, tools=tool_defs)
   b. if response.tool_calls:
        for each tool_call:
          result = execute_tool(tool_call)
          messages.append(tool_result_message)
        iteration++
        continue
   c. if response.content:
        print response.content
        break
5. if iteration >= max_iterations:
     print error "max iterations reached"
```

## Configuration

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--model` | `OLLAMA_MODEL` | `llama3.2` | Ollama model name |
| `--host` | `OLLAMA_HOST` | `http://localhost:11434` | Ollama server URL |
| `--max-iterations` | - | `50` | Max agent loop iterations |
| `--timeout` | - | `300s` | Global execution timeout |
| `--verbose` | - | `false` | Log LLM interactions |

## System Prompt

The system prompt instructs the LLM to:
- Act as a tmux automation assistant
- Use the provided tools to accomplish the user's request
- Think step by step
- Use `finish` tool when the task is complete
- Prefer `capture_pane` to verify results after `send_keys`

## Error Handling

- Invalid tool calls: return error message to LLM
- tmux command failure: return error with stderr
- Max iterations reached: exit with error code
- Ollama connection failure: exit with error

## Edge Cases

- No active tmux session: return error before starting
- Pane doesn't exist: return error to LLM so it can retry/adapt
- Empty instruction: return error
- LLM returns no response: return error

## Project Structure

```
tmux-ai/
├── cmd/
│   ├── root.go
│   └── run.go
├── internal/
│   ├── agent/
│   │   ├── runner.go
│   │   ├── messages.go
│   │   └── tools.go
│   ├── ollama/
│   │   └── client.go
│   └── tmux/
│       ├── executor.go
│       ├── capture.go
│       ├── keys.go
│       ├── window.go
│       └── list.go
├── docs/
│   └── specs/
│       └── 2026-06-10-tmux-ai-design.md
├── go.mod
├── go.sum
└── main.go
```

## Workflow Step Types

The `workflow` subcommand supports a DAG of steps. Each step has a `type`:

| type | Engine | Description |
|------|--------|-------------|
| `run` | LLM agent | Execute a natural language instruction in a target pane |
| `watch` | Polling | Wait for a pattern in a pane; output is the captured content |
| `shell` | `bash -c` | Run a shell command directly (no LLM) |
| `launch` | Native tmux | Open a new window, start a program, wait for ready, send input |
| `relay` | LLM agent | Capture from-pane content → agent operates on to-pane |
| `broadcast` | LLM agent (parallel) | Same instruction sent to multiple panes concurrently |

### `launch` Step

Designed for workflows that need to open an interactive program (e.g. `claude`) and send an initial command without relying on the LLM agent for timing.

```yaml
- name: open-claude-review
  type: launch
  window: pr-review        # new tmux window name
  cwd: /path/to/repo       # working directory (-c flag)
  command: claude          # program to type into the shell
  pattern: ">"             # poll until this appears (program ready)
  timeout: 30              # seconds before giving up
  input: "/review <url>"  # sent after pattern is matched
```

Execution sequence (all native, no LLM):
1. `tmux new-window -n <window> -c <cwd>` → get window ID
2. `tmux list-panes -t <windowID>` → get pane ID
3. `send_keys <paneID> "<command>\n"`
4. Poll `capture-pane` until output contains `<pattern>` or timeout
5. `send_keys <paneID> "<input>\n"`

### Template Interpolation

The workflow executor expands two placeholders in all string fields:

| Placeholder | Resolved to |
|-------------|-------------|
| `{{params.KEY}}` | Value from `params:` block or `--param KEY=value` override |
| `{{steps.NAME.output}}` | `output` field of a completed step (available after `depends:` is satisfied) |

## Dependencies

- `github.com/spf13/cobra` - CLI framework
- `net/http` - HTTP client (standard library)
- `encoding/json` - JSON handling (standard library)