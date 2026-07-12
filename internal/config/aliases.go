package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Aliases map[string]string

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tmux-ai"), nil
}

func aliasFile() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aliases.json"), nil
}

func LoadAliases() (Aliases, error) {
	file, err := aliasFile()
	if err != nil {
		return Aliases{}, err
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return Aliases{}, nil
	}
	if err != nil {
		return nil, err
	}
	var a Aliases
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return a, nil
}

func (a Aliases) Save() error {
	file, err := aliasFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0644)
}

// Resolve returns the pane ID for a name or pane ID.
// Strings starting with % are already IDs. Numeric strings (e.g. "0") are treated as pane indices.
// Otherwise look up in aliases.
func (a Aliases) Resolve(s string) string {
	if s == "" || strings.HasPrefix(s, "%") {
		return s
	}
	if id, ok := a[s]; ok {
		return id
	}
	return s
}
