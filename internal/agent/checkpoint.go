package agent

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/llmore/tmux-ai/internal/ollama"
)

// saveCheckpoint writes the current message history to the checkpoint file
// (if one is configured).
func (r *Runner) saveCheckpoint(messages []ollama.Message) {
	if r.CheckpointFile == "" {
		return
	}
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		if r.verbose {
			fmt.Printf("Error marshaling checkpoint: %v\n", err)
		}
		return
	}
	if err := os.WriteFile(r.CheckpointFile, data, 0644); err != nil {
		if r.verbose {
			fmt.Printf("Error writing checkpoint: %v\n", err)
		}
	}
}

// loadCheckpoint reads a previously saved checkpoint file and returns the
// message history. Returns (nil, false) if the file doesn't exist or
// can't be parsed.
func (r *Runner) loadCheckpoint() ([]ollama.Message, bool) {
	if r.CheckpointFile == "" {
		return nil, false
	}
	data, err := os.ReadFile(r.CheckpointFile)
	if err != nil {
		return nil, false
	}
	var messages []ollama.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, false
	}
	if r.verbose {
		fmt.Printf("Resumed %d messages from checkpoint: %s\n", len(messages), r.CheckpointFile)
	}
	return messages, true
}
