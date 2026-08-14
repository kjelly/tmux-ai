package cmd

import (
	"fmt"
	"strings"

	"github.com/llmore/tmux-ai/internal/artifact"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type handoffTransportKind string

type handoffCompletionMode string

const (
	handoffTransportText handoffTransportKind  = "text"
	handoffTransportFile handoffTransportKind  = "file"
	completionModeManual handoffCompletionMode = "manual"
	completionModeHelper handoffCompletionMode = "helper"
)

type handoffPayload struct {
	Transport handoffTransportKind
	Text      string
	Artifacts []artifact.Ref
}

func (p handoffPayload) validate() error {
	switch p.Transport {
	case handoffTransportText:
		if len(p.Artifacts) != 0 {
			return fmt.Errorf("text handoff cannot carry artifacts")
		}
	case handoffTransportFile:
		if p.Text != "" {
			return fmt.Errorf("file handoff cannot carry text payload")
		}
	default:
		return fmt.Errorf("unknown handoff transport %q", p.Transport)
	}
	return nil
}

type artifactPresetConfig struct {
	Forward         string `yaml:"forward"`
	MaxFilesPerTurn int    `yaml:"max_files_per_turn"`
	MaxFileBytes    int64  `yaml:"max_file_bytes"`
	MaxTurnBytes    int64  `yaml:"max_turn_bytes"`
	MaxRunFiles     int    `yaml:"max_run_files"`
	MaxRunBytes     int64  `yaml:"max_run_bytes"`
	Keep            bool   `yaml:"keep"`
}

type textHandoffPresetConfig struct {
	MaxPayloadBytes int `yaml:"max_payload_bytes"`
	MaxPayloadLines int `yaml:"max_payload_lines"`
}

type handoffPresetConfig struct {
	HandoffTransport string                  `yaml:"handoff_transport"`
	CompletionMode   string                  `yaml:"completion_mode"`
	Artifacts        artifactPresetConfig    `yaml:"artifacts"`
	TextHandoff      textHandoffPresetConfig `yaml:"text_handoff"`
	Compaction       compactionPresetConfig  `yaml:"compaction"`
}

type resolvedHandoffConfig struct {
	Transport      handoffTransportKind
	Source         string
	Limits         artifact.Limits
	Root           string
	Keep           bool
	TextBytes      int
	TextLines      int
	CompletionMode handoffCompletionMode
}

var (
	ppHandoffTransport     string
	ppArtifactRoot         string
	ppKeepArtifacts        bool
	ppMaxArtifactFiles     int
	ppMaxArtifactFileBytes int64
	ppMaxArtifactTurnBytes int64
	ppMaxArtifactRunFiles  int
	ppMaxArtifactRunBytes  int64
	ppMaxTextHandoffBytes  int
	ppMaxTextHandoffLines  int
	ppCompletionMode       string
)

func addHandoffFlags(command *cobra.Command) {
	flags := command.Flags()
	flags.StringVar(&ppHandoffTransport, "handoff-transport", "", "Handoff transport: text or file (overrides preset)")
	flags.StringVar(&ppArtifactRoot, "artifact-root", "", "Artifact root override for file handoff")
	flags.BoolVar(&ppKeepArtifacts, "keep-artifacts", false, "Keep file handoff artifacts after the run")
	flags.IntVar(&ppMaxArtifactFiles, "max-artifact-files-per-turn", 8, "Maximum artifacts collected per turn")
	flags.Int64Var(&ppMaxArtifactFileBytes, "max-artifact-file-bytes", 1<<20, "Maximum bytes in one artifact")
	flags.Int64Var(&ppMaxArtifactTurnBytes, "max-artifact-turn-bytes", 4<<20, "Maximum artifact bytes per turn")
	flags.IntVar(&ppMaxArtifactRunFiles, "max-artifact-run-files", 64, "Maximum artifacts per run")
	flags.Int64Var(&ppMaxArtifactRunBytes, "max-artifact-run-bytes", 32<<20, "Maximum artifact bytes per run")
	flags.IntVar(&ppMaxTextHandoffBytes, "max-text-handoff-bytes", 32*1024, "Maximum UTF-8 bytes in a text handoff")
	flags.IntVar(&ppMaxTextHandoffLines, "max-text-handoff-lines", 200, "Maximum lines in a text handoff")
	flags.StringVar(&ppCompletionMode, "completion-mode", "", "File handoff completion mode: manual or helper (overrides preset)")
	addCompactionFlags(flags)
}

var ppHandoffFlagSet *pflag.FlagSet

func resolveHandoffConfig(flags *pflag.FlagSet, preset handoffPresetConfig) (resolvedHandoffConfig, error) {
	if flags == nil {
		flags = pflag.NewFlagSet("handoff", pflag.ContinueOnError)
	}
	defaults := artifact.DefaultLimits()
	config := resolvedHandoffConfig{Limits: defaults, TextBytes: 32 * 1024, TextLines: 200, Source: "default", CompletionMode: completionModeManual}
	transport := strings.TrimSpace(preset.HandoffTransport)
	if flags.Changed("handoff-transport") {
		transport, config.Source = ppHandoffTransport, "CLI"
	} else if transport != "" {
		config.Source = "preset"
	}
	completionMode := strings.TrimSpace(preset.CompletionMode)
	if flags.Changed("completion-mode") {
		completionMode = ppCompletionMode
	}
	if completionMode != "" {
		switch handoffCompletionMode(strings.ToLower(completionMode)) {
		case completionModeManual, completionModeHelper:
			config.CompletionMode = handoffCompletionMode(strings.ToLower(completionMode))
		default:
			return resolvedHandoffConfig{}, fmt.Errorf("completion mode must be manual or helper (got %q)", completionMode)
		}
	}
	if transport == "" {
		transport = string(handoffTransportText)
	}
	switch handoffTransportKind(strings.ToLower(transport)) {
	case handoffTransportText, handoffTransportFile:
		config.Transport = handoffTransportKind(strings.ToLower(transport))
	default:
		return resolvedHandoffConfig{}, fmt.Errorf("handoff transport must be text or file (got %q)", transport)
	}
	if preset.Artifacts.Forward != "" && preset.Artifacts.Forward != "produced_or_incoming" {
		return resolvedHandoffConfig{}, fmt.Errorf("artifacts.forward must be produced_or_incoming")
	}
	applyPresetLimits := func() {
		if preset.Artifacts.MaxFilesPerTurn > 0 {
			config.Limits.MaxFilesPerTurn = preset.Artifacts.MaxFilesPerTurn
		}
		if preset.Artifacts.MaxFileBytes > 0 {
			config.Limits.MaxFileBytes = preset.Artifacts.MaxFileBytes
		}
		if preset.Artifacts.MaxTurnBytes > 0 {
			config.Limits.MaxTurnBytes = preset.Artifacts.MaxTurnBytes
		}
		if preset.Artifacts.MaxRunFiles > 0 {
			config.Limits.MaxRunFiles = preset.Artifacts.MaxRunFiles
		}
		if preset.Artifacts.MaxRunBytes > 0 {
			config.Limits.MaxRunBytes = preset.Artifacts.MaxRunBytes
		}
		config.Keep = preset.Artifacts.Keep
		if preset.TextHandoff.MaxPayloadBytes > 0 {
			config.TextBytes = preset.TextHandoff.MaxPayloadBytes
		}
		if preset.TextHandoff.MaxPayloadLines > 0 {
			config.TextLines = preset.TextHandoff.MaxPayloadLines
		}
	}
	applyPresetLimits()
	if flags.Changed("max-artifact-files-per-turn") {
		config.Limits.MaxFilesPerTurn = ppMaxArtifactFiles
	}
	if flags.Changed("max-artifact-file-bytes") {
		config.Limits.MaxFileBytes = ppMaxArtifactFileBytes
	}
	if flags.Changed("max-artifact-turn-bytes") {
		config.Limits.MaxTurnBytes = ppMaxArtifactTurnBytes
	}
	if flags.Changed("max-artifact-run-files") {
		config.Limits.MaxRunFiles = ppMaxArtifactRunFiles
	}
	if flags.Changed("max-artifact-run-bytes") {
		config.Limits.MaxRunBytes = ppMaxArtifactRunBytes
	}
	if flags.Changed("max-text-handoff-bytes") {
		config.TextBytes = ppMaxTextHandoffBytes
	}
	if flags.Changed("max-text-handoff-lines") {
		config.TextLines = ppMaxTextHandoffLines
	}
	if flags.Changed("artifact-root") {
		config.Root = ppArtifactRoot
	}
	if flags.Changed("keep-artifacts") {
		config.Keep = ppKeepArtifacts
	}
	artifactFlagSet := flags.Changed("artifact-root") || flags.Changed("keep-artifacts") || flags.Changed("max-artifact-files-per-turn") || flags.Changed("max-artifact-file-bytes") || flags.Changed("max-artifact-turn-bytes") || flags.Changed("max-artifact-run-files") || flags.Changed("max-artifact-run-bytes")
	textFlagSet := flags.Changed("max-text-handoff-bytes") || flags.Changed("max-text-handoff-lines")
	if config.Transport == handoffTransportText && artifactFlagSet {
		return resolvedHandoffConfig{}, fmt.Errorf("artifact flags require --handoff-transport file")
	}
	if config.Transport == handoffTransportText && config.CompletionMode != completionModeManual {
		return resolvedHandoffConfig{}, fmt.Errorf("completion mode helper requires --handoff-transport file")
	}
	if config.Transport == handoffTransportFile && textFlagSet {
		return resolvedHandoffConfig{}, fmt.Errorf("text handoff flags require --handoff-transport text")
	}
	if config.Transport == handoffTransportText {
		if config.TextBytes <= 0 || config.TextLines <= 0 {
			return resolvedHandoffConfig{}, fmt.Errorf("text handoff limits must be greater than zero")
		}
		if ppLines < config.TextLines+8 {
			return resolvedHandoffConfig{}, fmt.Errorf("--lines=%d is too small for --max-text-handoff-lines=%d", ppLines, config.TextLines)
		}
		if config.TextBytes > ppMaxRelayBytes || config.TextBytes > ppMaxTotalRelayBytes {
			return resolvedHandoffConfig{}, fmt.Errorf("text handoff byte limit exceeds relay budget")
		}
	} else if config.Limits.MaxFilesPerTurn <= 0 || config.Limits.MaxFileBytes < 0 || config.Limits.MaxTurnBytes < 0 || config.Limits.MaxRunFiles <= 0 || config.Limits.MaxRunBytes < 0 {
		return resolvedHandoffConfig{}, fmt.Errorf("artifact limits are invalid")
	}
	return config, nil
}
