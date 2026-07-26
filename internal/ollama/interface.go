package ollama

import "context"

// Client is the interface for communicating with an Ollama server.
// The concrete implementation in client.go satisfies this interface.
// Tests can provide a fake implementation to avoid requiring a running Ollama.
type Client interface {
	Chat(ctx context.Context, messages []Message, tools []Tool) (*ChatResponse, error)
	ChatWithFormat(ctx context.Context, messages []Message, tools []Tool, format any) (*ChatResponse, error)
}
