package agent

import (
	"fmt"
	"strings"

	"github.com/llmore/tmux-ai/internal/ollama"
)

// baseSystemPrompt is the static portion of the system prompt — everything
// except the tool listing, which is now auto-generated from the ToolRegistry.
const baseSystemPrompt = `You are a tmux automation assistant. The user will give you a natural language instruction to accomplish a task involving tmux windows and panes.

You have access to the following tools (via function calling):`

const systemPromptFooter = `

IMPORTANT:
- Think step by step. Use capture_pane to verify results.
- After sending keys, always wait a moment and capture the pane to verify the command executed, or use send_keys_and_wait.
- If a command fails, try a different approach.
- Use wait_for_pattern or send_keys_and_wait when waiting for a command in a pane to complete rather than looping sleep+capture_pane.
- Use finish when the task is complete.
- Panes are identified by IDs like %0, %1, or session:0.0
- If a "Primary target pane" is specified, focus your work on that pane unless the task explicitly requires others.
- If "Captured context" is provided, treat it as the current state of the source pane when deciding what to do.

Current tmux state will be provided in the user message.`

// buildSystemPrompt constructs the full system prompt by combining the
// static base text with an auto-generated tool listing from the registry.
// This ensures the prompt always stays in sync with registered tools.
func buildSystemPrompt(reg *ToolRegistry) string {
	var sb strings.Builder
	sb.WriteString(baseSystemPrompt)
	sb.WriteString("\n")
	for _, desc := range reg.Descriptions() {
		sb.WriteString(desc)
		sb.WriteString("\n")
	}
	sb.WriteString(systemPromptFooter)
	return sb.String()
}

// SystemPrompt is kept for backward compatibility but now delegates to
// buildSystemPrompt with a default registry. Prefer using buildSystemPrompt
// directly when a ToolRegistry is available.
var SystemPrompt = baseSystemPrompt + systemPromptFooter

// BuildInitialMessage constructs the first user message.
// targetPane: hints to the agent which pane to focus on (can be empty).
// extraContext: prepended as "Captured context" when set (e.g. from watch/relay).
func BuildInitialMessage(instruction, tmuxState, targetPane, extraContext string) ollama.Message {
	var parts []string
	if extraContext != "" {
		parts = append(parts, fmt.Sprintf("Captured context:\n%s", extraContext))
	}
	parts = append(parts, fmt.Sprintf("Task: %s", instruction))
	if targetPane != "" {
		parts = append(parts, fmt.Sprintf("Primary target pane: %s", targetPane))
	}
	parts = append(parts, fmt.Sprintf("Current tmux state:\n%s", tmuxState))
	return ollama.Message{
		Role:    "user",
		Content: strings.Join(parts, "\n\n"),
	}
}
