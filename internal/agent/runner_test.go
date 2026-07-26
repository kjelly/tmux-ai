package agent

import (
	"context"
	"testing"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
)

// fakeRunnerOllama is a minimal fake that returns a scripted sequence of
// responses. It's lighter than ollama.FakeClient for specific test scenarios.
type fakeRunnerOllama struct {
	responses  []*ollama.ChatResponse
	repeatLast bool // if true, repeat the last response instead of returning "done"
	calls      int
}

func (f *fakeRunnerOllama) Chat(ctx context.Context, messages []ollama.Message, tools []ollama.Tool) (*ollama.ChatResponse, error) {
	idx := f.calls
	f.calls++
	if idx >= len(f.responses) {
		if f.repeatLast && len(f.responses) > 0 {
			return f.responses[len(f.responses)-1], nil
		}
		return &ollama.ChatResponse{
			Message: ollama.Message{Content: "done"},
			Done:    true,
		}, nil
	}
	return f.responses[idx], nil
}

func (f *fakeRunnerOllama) ChatWithFormat(ctx context.Context, messages []ollama.Message, tools []ollama.Tool, format any) (*ollama.ChatResponse, error) {
	return f.Chat(ctx, messages, tools)
}

func newTestRunner(client ollama.Client, tmuxExec tmux.Executor) *Runner {
	r := &Runner{
		ollamaClient:  client,
		tmux:          tmuxExec,
		maxIterations: 10,
	}
	r.tools = NewToolRegistry()
	registerAllTools(r.tools, tmuxExec, false)
	r.registerNotificationTool()
	return r
}

func TestRunnerDirectResponse(t *testing.T) {
	fake := &fakeRunnerOllama{
		responses: []*ollama.ChatResponse{
			{Message: ollama.Message{Content: "task complete"}, Done: true},
		},
	}
	r := newTestRunner(fake, tmux.NewFakeExecutor())
	if err := r.Run(context.Background(), "test instruction"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.LastOutput != "task complete" {
		t.Errorf("LastOutput: got %q, want %q", r.LastOutput, "task complete")
	}
}

func TestRunnerToolCallThenFinish(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.Captures["%5"] = "hello from pane"

	fake := &fakeRunnerOllama{
		responses: []*ollama.ChatResponse{
			// First response: tool call to capture_pane
			{Message: ollama.Message{
				ToolCalls: []ollama.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: ollama.FunctionCall{
							Name:      "capture_pane",
							Arguments: []byte(`{"pane_id": "%5"}`),
						},
					},
				},
			}},
			// Second response: content
			{Message: ollama.Message{Content: "I captured the pane"}, Done: true},
		},
	}
	r := newTestRunner(fake, fakeTmux)
	if err := r.Run(context.Background(), "capture pane %5"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.LastOutput != "I captured the pane" {
		t.Errorf("LastOutput: got %q", r.LastOutput)
	}
	if fake.calls != 2 {
		t.Errorf("expected 2 LLM calls, got %d", fake.calls)
	}
}

func TestRunnerMaxIterations(t *testing.T) {
	// Always return tool calls, never finishing
	fake := &fakeRunnerOllama{
		repeatLast: true,
		responses: []*ollama.ChatResponse{
			{Message: ollama.Message{
				ToolCalls: []ollama.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: ollama.FunctionCall{
							Name:      "sleep",
							Arguments: []byte(`{"ms": 1}`),
						},
					},
				},
			}},
		},
	}
	r := newTestRunner(fake, tmux.NewFakeExecutor())
	r.maxIterations = 3
	err := r.Run(context.Background(), "never ending")
	if err == nil {
		t.Error("expected max iterations error")
	}
}

func TestRunnerContextCancellation(t *testing.T) {
	// Return tool calls that sleep, and cancel the context mid-run
	fake := &fakeRunnerOllama{
		responses: []*ollama.ChatResponse{
			{Message: ollama.Message{
				ToolCalls: []ollama.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: ollama.FunctionCall{
							Name:      "sleep",
							Arguments: []byte(`{"ms": 10000}`),
						},
					},
				},
			}},
		},
	}
	r := newTestRunner(fake, tmux.NewFakeExecutor())
	r.maxIterations = 5

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	err := r.Run(ctx, "test")
	if err == nil {
		t.Error("expected error due to cancelled context")
	}
}
