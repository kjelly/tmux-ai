package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Templates maps template names to instruction strings.
type Templates map[string]string

func templatesFile() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "templates.json"), nil
}

func LoadTemplates() (Templates, error) {
	file, err := templatesFile()
	if err != nil {
		return Templates{}, err
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return Templates{}, nil
	}
	if err != nil {
		return nil, err
	}
	var t Templates
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return t, nil
}

func (t Templates) Save() error {
	file, err := templatesFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0644)
}
