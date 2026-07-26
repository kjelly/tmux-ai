package ollama

import "context"

// FakeClient is a test double for the Client interface.
// It returns a configurable sequence of responses.
type FakeClient struct {
	Responses []ChatResponse
	CallIdx   int
	Calls     []ChatCall
}

type ChatCall struct {
	Messages []Message
	Tools    []Tool
	Format   any
}

func NewFakeClient() *FakeClient {
	return &FakeClient{}
}

func (f *FakeClient) Chat(ctx context.Context, messages []Message, tools []Tool) (*ChatResponse, error) {
	return f.chat(messages, tools, nil)
}

func (f *FakeClient) ChatWithFormat(ctx context.Context, messages []Message, tools []Tool, format any) (*ChatResponse, error) {
	return f.chat(messages, tools, format)
}

func (f *FakeClient) chat(messages []Message, tools []Tool, format any) (*ChatResponse, error) {
	f.Calls = append(f.Calls, ChatCall{Messages: messages, Tools: tools, Format: format})
	idx := f.CallIdx
	f.CallIdx++
	if idx >= len(f.Responses) {
		// Default: return empty response (task complete)
		return &ChatResponse{Message: Message{Content: "done"}, Done: true}, nil
	}
	r := f.Responses[idx]
	return &r, nil
}
