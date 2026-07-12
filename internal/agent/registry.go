package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/llmore/tmux-ai/internal/ollama"
)

// ToolHandler executes a single tool call and returns the result string.
type ToolHandler func(ctx context.Context, args map[string]interface{}) string

// ToolEntry pairs an LLM-facing tool definition with its execution handler.
type ToolEntry struct {
	Definition ollama.Tool
	Handler    ToolHandler
}

// ToolRegistry maintains a set of named tools with their definitions and
// handlers. It replaces the previous pattern of a separate ToolDefinitions
// slice (tools.go) and a 430-line executeTool switch/case (runner.go).
//
// Adding a new tool requires only a single Register call — the definition,
// handler, and SystemPrompt tool listing are all handled automatically.
type ToolRegistry struct {
	tools map[string]ToolEntry
	order []string // preserves registration order for stable output
}

// NewToolRegistry creates an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]ToolEntry),
	}
}

// Register adds a tool to the registry. Panics if a tool with the same
// name is already registered.
func (r *ToolRegistry) Register(name string, def ollama.Tool, handler ToolHandler) {
	if _, exists := r.tools[name]; exists {
		panic(fmt.Sprintf("tool %q already registered", name))
	}
	r.tools[name] = ToolEntry{Definition: def, Handler: handler}
	r.order = append(r.order, name)
}

// Definitions returns the tool definitions in registration order, suitable
// for passing to ollama.Client.Chat.
func (r *ToolRegistry) Definitions() []ollama.Tool {
	defs := make([]ollama.Tool, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, r.tools[name].Definition)
	}
	return defs
}

// Execute dispatches a tool call to the registered handler.
func (r *ToolRegistry) Execute(ctx context.Context, tc ollama.ToolCall) string {
	entry, ok := r.tools[tc.Function.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %s", tc.Function.Name)
	}

	var args map[string]interface{}
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		return fmt.Sprintf("error: failed to parse arguments: %v", err)
	}

	return entry.Handler(ctx, args)
}

// Names returns the names of all registered tools in registration order.
func (r *ToolRegistry) Names() []string {
	return r.order
}

// Descriptions returns "name: description" for each tool, useful for
// auto-generating the SystemPrompt tool listing.
func (r *ToolRegistry) Descriptions() []string {
	descs := make([]string, 0, len(r.order))
	for _, name := range r.order {
		descs = append(descs, fmt.Sprintf("- %s: %s", name, r.tools[name].Definition.Function.Description))
	}
	return descs
}
