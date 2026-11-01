package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// withPingPongPresetGlobals restores the package state a preset file can
// change, so the preset parsing tests stay independent of each other.
func withPingPongPresetGlobals(t *testing.T) {
	t.Helper()
	presetPath, initial, start := ppPreset, ppInitial, ppStart
	roundTimeout, coordTimeout := ppRoundTimeout, ppCoordTimeout
	maxRetries, screenIdle := ppMaxSelectionRetries, ppScreenIdleTimeout
	settle, submitDelay := ppCompletionSettleTimeout, ppSubmitDelay
	chunkSize, chunkDelay, lines := ppPasteChunkSize, ppPasteChunkDelay, ppLines
	normalize, coordinator := ppScreenIdleNormalize, ppCoordinator
	newSession, paneStream := ppNewSession, ppPaneStream
	goal1, goal2 := ppPane1GoalMode, ppPane2GoalMode
	pane1Role, pane2Role := ppPane1Role, ppPane2Role
	toPane1, toPane2 := ppToPane1Prompt, ppToPane2Prompt
	policy, stopMembers := ppCoordinatorPolicy, ppStopMembers
	handoff, routing := ppPresetHandoff, ppRouting
	flagSet := ppHandoffFlagSet
	t.Cleanup(func() {
		ppPreset, ppInitial, ppStart = presetPath, initial, start
		ppRoundTimeout, ppCoordTimeout = roundTimeout, coordTimeout
		ppMaxSelectionRetries, ppScreenIdleTimeout = maxRetries, screenIdle
		ppCompletionSettleTimeout, ppSubmitDelay = settle, submitDelay
		ppPasteChunkSize, ppPasteChunkDelay, ppLines = chunkSize, chunkDelay, lines
		ppScreenIdleNormalize, ppCoordinator = normalize, coordinator
		ppNewSession, ppPaneStream = newSession, paneStream
		ppPane1GoalMode, ppPane2GoalMode = goal1, goal2
		ppPane1Role, ppPane2Role = pane1Role, pane2Role
		ppToPane1Prompt, ppToPane2Prompt = toPane1, toPane2
		ppCoordinatorPolicy, ppStopMembers = policy, stopMembers
		ppPresetHandoff, ppRouting = handoff, routing
		ppHandoffFlagSet = flagSet
	})
}

// writePingPongPreset writes a preset file and points the pingpong command
// at it.
func writePingPongPreset(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "preset.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ppPreset = path
	ppInitial = ""
	ppStart = "auto"
}

func TestPingPongPresetRoundTimeout(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"disabled deadline", "initial: work\nround_timeout: 0\n", 0},
		{"explicit deadline", "initial: work\nround_timeout: 1800\n", 1800},
		{"unset keeps the command default", "initial: work\n", 600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withPingPongPresetGlobals(t)
			ppRoundTimeout = 600
			ppHandoffFlagSet = nil
			writePingPongPreset(t, tc.body)
			if err := applyPingPongPreset(); err != nil {
				t.Fatal(err)
			}
			if ppRoundTimeout != tc.want {
				t.Fatalf("round timeout = %d, want %d", ppRoundTimeout, tc.want)
			}
		})
	}
}

func TestPingPongPresetRejectsNegativeRoundTimeout(t *testing.T) {
	withPingPongPresetGlobals(t)
	ppRoundTimeout = 600
	ppHandoffFlagSet = nil
	writePingPongPreset(t, "initial: work\nround_timeout: -1\n")
	err := applyPingPongPreset()
	if err == nil || !strings.Contains(err.Error(), "round_timeout must not be negative") {
		t.Fatalf("applyPingPongPreset() error = %v", err)
	}
}

func TestPingPongPresetRoundTimeoutFlagWins(t *testing.T) {
	withPingPongPresetGlobals(t)
	flags := pflag.NewFlagSet("pingpong", pflag.ContinueOnError)
	flags.Int("round-timeout", 600, "")
	if err := flags.Set("round-timeout", "900"); err != nil {
		t.Fatal(err)
	}
	ppHandoffFlagSet = flags
	ppRoundTimeout = 900
	writePingPongPreset(t, "initial: work\nround_timeout: 0\n")
	if err := applyPingPongPreset(); err != nil {
		t.Fatal(err)
	}
	if ppRoundTimeout != 900 {
		t.Fatalf("round timeout = %d, want the command line value 900", ppRoundTimeout)
	}
}
