package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func stateMachineMembers() map[string]handoffMember {
	return map[string]handoffMember{
		"pane1": {ID: "pane1", Label: "reviewer", Target: "%1"},
		"pane2": {ID: "pane2", Label: "fixer", Target: "%2"},
	}
}

func TestStateMachineRouterTransitions(t *testing.T) {
	config := stateMachineRoutingPreset{
		Mode: "state_machine", InitialMember: "pane1",
		Transitions: map[string]map[string]stateMachineTransition{
			"pane1": {
				"success": {Action: "stop"}, "needs_followup": {Action: "send", Target: "pane2"}, "blocked": {Action: "needs_human"},
			},
			"pane2": {
				"success": {Action: "send", Target: "pane1"}, "needs_followup": {Action: "send", Target: "pane1"}, "blocked": {Action: "needs_human"},
			},
		},
	}
	initial, router, err := newStateMachineRouter(config, stateMachineMembers(), []string{"pane1"}, func(target string) string { return "work " + target })
	if err != nil || initial != "pane1" {
		t.Fatalf("router = %q, %v", initial, err)
	}
	for _, tc := range []struct {
		member, outcome string
		stop, human     bool
		target          string
	}{
		{"pane1", "success", true, false, ""},
		{"pane1", "needs_followup", false, false, "pane2"},
		{"pane2", "success", false, false, "pane1"},
		{"pane2", "blocked", false, true, ""},
	} {
		route, err := router(context.Background(), 1, stateMachineMembers()[tc.member], turnResult{Status: &handoffStatus{Outcome: tc.outcome}}, false)
		if err != nil || route.Stop != tc.stop || route.NeedsHuman != tc.human || route.NextID != tc.target {
			t.Fatalf("%s/%s route=%#v err=%v", tc.member, tc.outcome, route, err)
		}
		if route.NextID != "" && route.Confidence != 1 {
			t.Fatalf("%s/%s confidence=%f", tc.member, tc.outcome, route.Confidence)
		}
	}
}

func TestStateMachineRouterRejectsIncompleteAndUnauthorizedTransitions(t *testing.T) {
	config := stateMachineRoutingPreset{
		Mode: "state_machine", InitialMember: "pane1",
		Transitions: map[string]map[string]stateMachineTransition{
			"pane1": {"success": {Action: "stop"}, "needs_followup": {Action: "send", Target: "pane2"}},
			"pane2": {"success": {Action: "send", Target: "pane1"}, "needs_followup": {Action: "send", Target: "pane1"}, "blocked": {Action: "needs_human"}},
		},
	}
	if _, _, err := newStateMachineRouter(config, stateMachineMembers(), []string{"pane1"}, func(string) string { return "work" }); err == nil || !strings.Contains(err.Error(), "missing blocked") {
		t.Fatalf("incomplete error = %v", err)
	}
	config.Transitions["pane1"]["blocked"] = stateMachineTransition{Action: "needs_human"}
	config.Transitions["pane2"]["success"] = stateMachineTransition{Action: "stop"}
	if _, _, err := newStateMachineRouter(config, stateMachineMembers(), []string{"pane1"}, func(string) string { return "work" }); err == nil || !strings.Contains(err.Error(), "not allowed to stop") {
		t.Fatalf("unauthorized stop error = %v", err)
	}
}

func TestReviewFixPresetsUseValidatedStateMachine(t *testing.T) {
	for _, name := range []string{"review-fix.yaml", "implement-review-fix.yaml", "review-md-fix.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "presets", name))
		if err != nil {
			t.Fatal(err)
		}
		var preset pingPongPreset
		decoder := yaml.NewDecoder(strings.NewReader(string(data)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&preset); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if preset.Coordinator || preset.Routing.Mode != "state_machine" {
			t.Fatalf("preset %s coordinator=%t routing=%#v", name, preset.Coordinator, preset.Routing)
		}
		if name == "review-fix.yaml" && (preset.RoundTimeout == nil || *preset.RoundTimeout != 0) {
			t.Fatalf("preset %s should disable wall-clock timeout for receipt-driven review/fix rounds", name)
		}
		_, _, err = newStateMachineRouter(preset.Routing, stateMachineMembers(), preset.StopMembers, func(target string) string { return "work " + target })
		if err != nil {
			t.Fatalf("preset %s router: %v", name, err)
		}
	}
}
