package agent

import (
	"context"
	"testing"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
)

func TestToolRegistryRegisterAndExecute(t *testing.T) {
	reg := NewToolRegistry()

	called := false
	reg.Register("test_tool", ollama.Tool{
		Type: "function",
		Function: ollama.ToolDef{
			Name:        "test_tool",
			Description: "A test tool",
			Parameters:  mustSchema(`{"type": "object", "properties": {"msg": {"type": "string"}}}`),
		},
	}, func(ctx context.Context, args map[string]interface{}) string {
		called = true
		return strArg(args, "msg")
	})

	// Test Definitions
	defs := reg.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].Function.Name != "test_tool" {
		t.Errorf("expected test_tool, got %s", defs[0].Function.Name)
	}

	// Test Names
	names := reg.Names()
	if len(names) != 1 || names[0] != "test_tool" {
		t.Errorf("expected [test_tool], got %v", names)
	}

	// Test Execute
	result := reg.Execute(context.Background(), ollama.ToolCall{
		Function: ollama.FunctionCall{
			Name:      "test_tool",
			Arguments: []byte(`{"msg": "hello"}`),
		},
	})
	if !called {
		t.Error("handler was not called")
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestToolRegistryUnknownTool(t *testing.T) {
	reg := NewToolRegistry()
	result := reg.Execute(context.Background(), ollama.ToolCall{
		Function: ollama.FunctionCall{
			Name:      "nonexistent",
			Arguments: []byte(`{}`),
		},
	})
	if result == "" || result[0:5] != "error" {
		t.Errorf("expected error message, got %q", result)
	}
}

func TestRegisterAllTools(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	reg := NewToolRegistry()
	registerAllTools(reg, fake, false)

	// Verify all expected tools are registered
	expected := []string{
		"capture_pane", "capture_pane_clean", "send_keys", "send_lines",
		"list_panes", "list_windows", "new_window", "split_pane",
		"select_pane", "select_window", "kill_pane", "kill_window",
		"sleep", "wait_for_pattern", "ask_user", "run_local_command",
		"send_keys_and_wait", "abort_command", "log_progress",
		"find_pane_by_process", "read_file", "write_file",
		"check_pane_idle", "search_pane_history", "replace_text_in_file",
		"git_status_summary", "clear_pane_history", "finish",
	}
	for _, name := range expected {
		found := false
		for _, n := range reg.Names() {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool %q not registered", name)
		}
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	reg := NewToolRegistry()
	registerAllTools(reg, fake, false)

	prompt := buildSystemPrompt(reg)
	if prompt == "" {
		t.Error("system prompt should not be empty")
	}
	// Should contain at least one tool name
	if !contains(prompt, "capture_pane") {
		t.Error("system prompt should list capture_pane")
	}
	if !contains(prompt, "finish") {
		t.Error("system prompt should list finish")
	}
	// Should contain the IMPORTANT footer
	if !contains(prompt, "IMPORTANT") {
		t.Error("system prompt should contain IMPORTANT section")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
