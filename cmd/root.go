package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	model          string
	ollamaHost     string
	maxIterations  int
	timeoutSeconds int
	verbose        bool
	configPath     string
)

const defaultModel = "gemma4:31b"

type userConfig struct {
	Model         string `yaml:"model"`
	Host          string `yaml:"host"`
	MaxIterations *int   `yaml:"max_iterations"`
	Timeout       *int   `yaml:"timeout"`
	Verbose       *bool  `yaml:"verbose"`
}

var rootCmd = &cobra.Command{
	Use:   "tmux-ai",
	Short: "AI-powered tmux automation via natural language",
}

func Execute() error {
	return rootCmd.Execute()
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultConfigPath() (string, error) {
	if path := strings.TrimSpace(configPath); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, "tmux-ai", "config.yaml"), nil
}

func loadUserConfig(path string) (userConfig, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return userConfig{}, false, nil
	}
	if err != nil {
		return userConfig{}, false, fmt.Errorf("read config %q: %w", path, err)
	}
	var config userConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return userConfig{}, false, appendYAMLCommandHint(fmt.Errorf("parse config %q: %w", path, err), data, "global config")
	}
	if config.MaxIterations != nil && *config.MaxIterations <= 0 {
		return userConfig{}, false, appendYAMLCommandHint(fmt.Errorf("config %q: max_iterations must be positive", path), data, "global config")
	}
	if config.Timeout != nil && *config.Timeout < 0 {
		return userConfig{}, false, appendYAMLCommandHint(fmt.Errorf("config %q: timeout must not be negative", path), data, "global config")
	}
	return config, true, nil
}

func applyUserConfig(config userConfig, changed func(string) bool, getenv func(string) string) {
	if !changed("model") && strings.TrimSpace(getenv("OLLAMA_MODEL")) == "" && strings.TrimSpace(config.Model) != "" {
		model = strings.TrimSpace(config.Model)
	}
	if !changed("host") && strings.TrimSpace(getenv("OLLAMA_HOST")) == "" && strings.TrimSpace(config.Host) != "" {
		ollamaHost = strings.TrimSpace(config.Host)
	}
	if !changed("max-iterations") && config.MaxIterations != nil {
		maxIterations = *config.MaxIterations
	}
	if !changed("timeout") && config.Timeout != nil {
		timeoutSeconds = *config.Timeout
	}
	if !changed("verbose") && config.Verbose != nil {
		verbose = *config.Verbose
	}
}

func loadAndApplyUserConfig(_ *cobra.Command, _ []string) error {
	path, err := defaultConfigPath()
	if err != nil {
		return err
	}
	config, found, err := loadUserConfig(path)
	if err != nil {
		return err
	}
	if found {
		flags := rootCmd.PersistentFlags()
		applyUserConfig(config, flags.Changed, os.Getenv)
	}

	fmt.Fprintf(os.Stderr, "🧠 LLM 設定：endpoint=%s/api/chat model=%s\n", ollamaHost, model)
	return nil
}

func init() {
	rootCmd.PersistentPreRunE = loadAndApplyUserConfig
	rootCmd.PersistentFlags().StringVar(&configPath, "config", envOrDefault("TMUX_AI_CONFIG", ""), "User config path (env: TMUX_AI_CONFIG; default: $XDG_CONFIG_HOME/tmux-ai/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&model, "model", envOrDefault("OLLAMA_MODEL", defaultModel), "Ollama model (env: OLLAMA_MODEL)")
	rootCmd.PersistentFlags().StringVar(&ollamaHost, "host", envOrDefault("OLLAMA_HOST", "http://localhost:11434"), "Ollama server URL (env: OLLAMA_HOST)")
	rootCmd.PersistentFlags().IntVar(&maxIterations, "max-iterations", 50, "Max agent loop iterations")
	rootCmd.PersistentFlags().IntVar(&timeoutSeconds, "timeout", 300, "Global timeout in seconds")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Verbose logging")
}
