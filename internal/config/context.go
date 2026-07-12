package config

import (
	"os"
	"path/filepath"
	"strings"
)

// GlobalContextPath returns the path to the user's global context file.
func GlobalContextPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "context.md"), nil
}

// LoadGlobalContext reads ~/.config/tmux-ai/context.md and returns its content.
// Returns empty string if the file does not exist.
func LoadGlobalContext() string {
	path, err := GlobalContextPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
