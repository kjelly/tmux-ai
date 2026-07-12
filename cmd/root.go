package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	model          string
	ollamaHost     string
	maxIterations  int
	timeoutSeconds int
	verbose        bool
)

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

func init() {
	rootCmd.PersistentFlags().StringVar(&model, "model", envOrDefault("OLLAMA_MODEL", "llama3.2"), "Ollama model (env: OLLAMA_MODEL)")
	rootCmd.PersistentFlags().StringVar(&ollamaHost, "host", envOrDefault("OLLAMA_HOST", "http://localhost:11434"), "Ollama server URL (env: OLLAMA_HOST)")
	rootCmd.PersistentFlags().IntVar(&maxIterations, "max-iterations", 50, "Max agent loop iterations")
	rootCmd.PersistentFlags().IntVar(&timeoutSeconds, "timeout", 300, "Global timeout in seconds")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Verbose logging")
}
