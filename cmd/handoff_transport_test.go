package cmd

import (
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/artifact"
	"github.com/spf13/cobra"
)

func TestHandoffPayloadValidateKeepsPlanesSeparate(t *testing.T) {
	if err := (handoffPayload{Transport: handoffTransportText, Text: "payload"}).validate(); err != nil {
		t.Fatalf("text payload error = %v", err)
	}
	if err := (handoffPayload{Transport: handoffTransportFile}).validate(); err != nil {
		t.Fatalf("file payload error = %v", err)
	}
	if err := (handoffPayload{Transport: handoffTransportText, Text: "payload", Artifacts: []artifact.Ref{{ID: "a-r0001-01-deadbeef"}}}).validate(); err == nil || !strings.Contains(err.Error(), "cannot carry artifacts") {
		t.Fatalf("text payload with artifacts error = %v", err)
	}
	if err := (handoffPayload{Transport: handoffTransportFile, Text: "payload"}).validate(); err == nil || !strings.Contains(err.Error(), "cannot carry text payload") {
		t.Fatalf("file payload with text error = %v", err)
	}
	if err := (handoffPayload{Transport: "carrier-pigeon"}).validate(); err == nil || !strings.Contains(err.Error(), "unknown handoff transport") {
		t.Fatalf("unknown transport error = %v", err)
	}
}

func TestResolveHandoffConfigAppliesPresetLimits(t *testing.T) {
	oldLines, oldMax, oldTotal := ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes
	defer func() { ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = oldLines, oldMax, oldTotal }()
	ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = 256, 65536, 131072
	command := &cobra.Command{Use: "test"}
	addHandoffFlags(command)
	preset := handoffPresetConfig{
		HandoffTransport: "file",
		Artifacts: artifactPresetConfig{
			MaxFilesPerTurn: 5,
			MaxFileBytes:    4096,
			MaxTurnBytes:    8192,
			MaxRunFiles:     20,
			MaxRunBytes:     65536,
		},
	}
	config, err := resolveHandoffConfig(command.Flags(), preset)
	if err != nil {
		t.Fatal(err)
	}
	if config.Limits.MaxFilesPerTurn != 5 || config.Limits.MaxFileBytes != 4096 || config.Limits.MaxTurnBytes != 8192 || config.Limits.MaxRunFiles != 20 || config.Limits.MaxRunBytes != 65536 {
		t.Fatalf("limits = %#v", config.Limits)
	}
}

func TestResolveHandoffConfigArtifactLimitFlagsOverridePreset(t *testing.T) {
	oldLines, oldMax, oldTotal := ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes
	defer func() { ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = oldLines, oldMax, oldTotal }()
	ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = 256, 65536, 131072
	command := &cobra.Command{Use: "test"}
	addHandoffFlags(command)
	if err := command.Flags().Set("max-artifact-files-per-turn", "3"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("max-artifact-run-bytes", "1024"); err != nil {
		t.Fatal(err)
	}
	preset := handoffPresetConfig{
		HandoffTransport: "file",
		Artifacts:        artifactPresetConfig{MaxFilesPerTurn: 5, MaxRunFiles: 20, MaxRunBytes: 65536},
	}
	config, err := resolveHandoffConfig(command.Flags(), preset)
	if err != nil {
		t.Fatal(err)
	}
	if config.Limits.MaxFilesPerTurn != 3 {
		t.Fatalf("max files per turn = %d, want the command line value 3", config.Limits.MaxFilesPerTurn)
	}
	if config.Limits.MaxRunBytes != 1024 {
		t.Fatalf("max run bytes = %d, want the command line value 1024", config.Limits.MaxRunBytes)
	}
	if config.Limits.MaxRunFiles != 20 {
		t.Fatalf("max run files = %d, want the preset value 20", config.Limits.MaxRunFiles)
	}
}

func TestResolveHandoffConfigRejectsIncompatibleFlags(t *testing.T) {
	oldLines, oldMax, oldTotal := ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes
	defer func() { ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = oldLines, oldMax, oldTotal }()
	ppLines, ppMaxRelayBytes, ppMaxTotalRelayBytes = 256, 65536, 131072

	command := &cobra.Command{Use: "test"}
	addHandoffFlags(command)
	if err := command.Flags().Set("max-text-handoff-bytes", "1024"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveHandoffConfig(command.Flags(), handoffPresetConfig{HandoffTransport: "file"}); err == nil || !strings.Contains(err.Error(), "text handoff flags require") {
		t.Fatalf("text flag with file transport error = %v", err)
	}

	command = &cobra.Command{Use: "test"}
	addHandoffFlags(command)
	if _, err := resolveHandoffConfig(command.Flags(), handoffPresetConfig{HandoffTransport: "file", CompletionMode: "sometimes"}); err == nil || !strings.Contains(err.Error(), "completion mode must be manual or helper") {
		t.Fatalf("invalid completion mode error = %v", err)
	}

	command = &cobra.Command{Use: "test"}
	addHandoffFlags(command)
	if _, err := resolveHandoffConfig(command.Flags(), handoffPresetConfig{HandoffTransport: "carrier-pigeon"}); err == nil || !strings.Contains(err.Error(), "handoff transport must be text or file") {
		t.Fatalf("invalid transport error = %v", err)
	}
}
