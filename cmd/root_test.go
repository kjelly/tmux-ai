package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestLoadUserConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("model: test-model\nmax_iterations: 12\nverbose: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, found, err := loadUserConfig(path)
	if err != nil || !found {
		t.Fatalf("loadUserConfig() = %#v, found=%t, err=%v", config, found, err)
	}
	if config.Model != "test-model" || config.MaxIterations == nil || *config.MaxIterations != 12 || config.Verbose == nil || !*config.Verbose {
		t.Fatalf("unexpected config: %#v", config)
	}

	_, found, err = loadUserConfig(filepath.Join(dir, "missing.yaml"))
	if err != nil || found {
		t.Fatalf("missing config found=%t, err=%v", found, err)
	}
}

func TestApplyUserConfigRespectsFlagAndEnvironmentPrecedence(t *testing.T) {
	oldModel, oldHost, oldIterations, oldTimeout, oldVerbose := model, ollamaHost, maxIterations, timeoutSeconds, verbose
	defer func() {
		model, ollamaHost, maxIterations, timeoutSeconds, verbose = oldModel, oldHost, oldIterations, oldTimeout, oldVerbose
	}()
	iterations, timeout := 12, 45
	configVerbose := true
	config := userConfig{Model: "config-model", Host: "http://config", MaxIterations: &iterations, Timeout: &timeout, Verbose: &configVerbose}

	applyUserConfig(config, func(string) bool { return false }, func(string) string { return "" })
	if model != "config-model" || ollamaHost != "http://config" || maxIterations != 12 || timeoutSeconds != 45 || !verbose {
		t.Fatalf("config was not applied: model=%q host=%q iterations=%d timeout=%d verbose=%t", model, ollamaHost, maxIterations, timeoutSeconds, verbose)
	}

	model, ollamaHost = "flag-model", "http://env"
	applyUserConfig(config, func(name string) bool { return name == "model" }, func(name string) string {
		if name == "OLLAMA_HOST" {
			return "http://env"
		}
		return ""
	})
	if model != "flag-model" || ollamaHost != "http://env" {
		t.Fatalf("flag/environment precedence was overwritten: model=%q host=%q", model, ollamaHost)
	}
}

func TestPRLoopDoesNotAdvertiseUnusedLLMThroughRoot(t *testing.T) {
	dir := fakePRGH(t)
	t.Setenv("TMUXAI_TEST_PR_JSON", `{"number":333,"state":"OPEN","isDraft":false}`)
	oldPath := configPath
	configPath = filepath.Join(dir, "missing-config.yaml")
	t.Cleanup(func() { configPath = oldPath })

	root := &cobra.Command{Use: "tmux-ai", PersistentPreRunE: loadAndApplyUserConfig}
	root.AddCommand(newPRLoopCommand())
	root.SetArgs([]string{"pr-loop", "333", "--repo", "owner/repo", "--draft-pane", "1", "--ready-pane", "2", "--dry-run", "--stable-polls", "1"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String()+stderr.String(), "LLM 設定") {
		t.Fatalf("pr-loop advertised an unused LLM: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "$pr-review-merge 333") {
		t.Fatalf("native command decision was lost: %s", stdout.String())
	}
}

func TestRootStillReportsLLMForUnmarkedCommands(t *testing.T) {
	oldPath := configPath
	configPath = filepath.Join(t.TempDir(), "missing-config.yaml")
	t.Cleanup(func() { configPath = oldPath })
	root := &cobra.Command{Use: "tmux-ai", PersistentPreRunE: loadAndApplyUserConfig}
	root.AddCommand(&cobra.Command{Use: "llm-command", Run: func(*cobra.Command, []string) {}})
	root.SetArgs([]string{"llm-command"})
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "LLM 設定：endpoint=") {
		t.Fatalf("LLM configuration log was lost: %s", stderr.String())
	}
}
