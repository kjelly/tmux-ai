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
}

func NewFakeClient() *FakeClient {
	return &FakeClient{}
}

func (f *FakeClient) Chat(ctx context.Context, messages []Message, tools []Tool) (*ChatResponse, error) {
	f.Calls = append(f.Calls, ChatCall{messages, tools})
	idx := f.CallIdx
	f.CallIdx++
	if idx >= len(f.Responses) {
		// Default: return empty response (task complete)
		return &ChatResponse{Message: Message{Content: "done"}, Done: true}, nil
	}
	r := f.Responses[idx]
	return &r, nil
}
