package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type clientImpl struct {
	host   string
	model  string
	think  bool
	client *http.Client
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Thinking   string     `json:"thinking,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	Format   any       `json:"format,omitempty"`
	Think    bool      `json:"think,omitempty"`
	Stream   bool      `json:"stream"`
}

type Tool struct {
	Type     string  `json:"type"`
	Function ToolDef `json:"function"`
}

type ToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type ChatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

func NewClient(host, model string) Client {
	return newClient(host, model, false)
}

// NewThinkingClient enables Ollama's separate thinking field for callers that
// want to display the model's reasoning trace.
func NewThinkingClient(host, model string) Client {
	return newClient(host, model, true)
}

func newClient(host, model string, think bool) Client {
	return &clientImpl{
		host:  host,
		model: model,
		think: think,
		// The caller's context owns the request deadline. Coordinator, selector,
		// and agent calls each have different configured timeouts; a fixed
		// http.Client timeout would cut off slower local models prematurely.
		client: &http.Client{},
	}
}

func (c *clientImpl) Chat(ctx context.Context, messages []Message, tools []Tool) (*ChatResponse, error) {
	return c.chat(ctx, messages, tools, nil)
}

// ChatWithFormat asks Ollama to constrain message.content to a JSON format or
// JSON schema. Callers must still validate the response locally.
func (c *clientImpl) ChatWithFormat(ctx context.Context, messages []Message, tools []Tool, format any) (*ChatResponse, error) {
	return c.chat(ctx, messages, tools, format)
}

func (c *clientImpl) chat(ctx context.Context, messages []Message, tools []Tool, format any) (*ChatResponse, error) {
	reqBody := ChatRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
		Format:   format,
		Think:    c.think,
		Stream:   false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", c.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		const maxErrorBodyBytes = 8 << 10
		errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
		detail := strings.TrimSpace(string(errorBody))
		if len(errorBody) > maxErrorBodyBytes {
			detail = strings.TrimSpace(string(errorBody[:maxErrorBodyBytes])) + "..."
		}

		if readErr != nil {
			return nil, fmt.Errorf("ollama returned %d (read error body: %w)", resp.StatusCode, readErr)
		}
		if detail == "" {
			return nil, fmt.Errorf("ollama returned %d", resp.StatusCode)
		}
		if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
			return nil, fmt.Errorf("ollama returned %d: %s (retry-after: %s)", resp.StatusCode, detail, retryAfter)
		}
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, detail)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &chatResp, nil
}
