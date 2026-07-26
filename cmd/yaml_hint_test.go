package cmd

import (
	"strings"
	"testing"
)

func TestYAMLCommandHint(t *testing.T) {
	tests := []struct {
		name, data, current, want string
	}{
		{name: "workflow for pingpong", data: "name: build\nsteps: []\n", current: "pingpong preset", want: "tmux-ai workflow run"},
		{name: "coordinate for pingpong", data: "members: []\n", current: "pingpong preset", want: "tmux-ai coordinate --preset"},
		{name: "pingpong for coordinate", data: "pane1: a\npane2: b\n", current: "coordinate preset", want: "tmux-ai pingpong --preset"},
		{name: "config for workflow", data: "model: test\ntimeout: 10\n", current: "workflow", want: "tmux-ai --config"},
		{name: "same kind has no hint", data: "steps: []\n", current: "workflow", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := yamlCommandHint([]byte(tt.data), tt.current)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("yamlCommandHint() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestAppendYAMLCommandHint(t *testing.T) {
	err := appendYAMLCommandHint(assertionError("invalid"), []byte("members: []\n"), "pingpong preset")
	if !strings.Contains(err.Error(), "this YAML looks like a coordinate preset") {
		t.Fatalf("error = %v, want coordinate hint", err)
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
