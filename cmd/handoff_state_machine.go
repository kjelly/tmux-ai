package cmd

import (
	"context"
	"fmt"
	"strings"
)

type stateMachineRoutingPreset struct {
	Mode          string                                       `yaml:"mode"`
	InitialMember string                                       `yaml:"initial_member"`
	Transitions   map[string]map[string]stateMachineTransition `yaml:"transitions"`
}

type stateMachineTransition struct {
	Action      string `yaml:"action"`
	Target      string `yaml:"target"`
	Instruction string `yaml:"instruction"`
}

func newStateMachineRouter(config stateMachineRoutingPreset, members map[string]handoffMember, stopMembers []string, defaultInstruction func(string) string) (string, handoffRouter, error) {
	if !strings.EqualFold(strings.TrimSpace(config.Mode), "state_machine") {
		return "", nil, fmt.Errorf("routing mode must be state_machine")
	}
	initial := strings.ToLower(strings.TrimSpace(config.InitialMember))
	if _, ok := members[initial]; !ok {
		return "", nil, fmt.Errorf("initial_member %q is not a member", config.InitialMember)
	}
	stopAllowed := make(map[string]bool, len(stopMembers))
	for _, member := range stopMembers {
		stopAllowed[strings.ToLower(member)] = true
	}
	normalized := make(map[string]map[string]stateMachineTransition, len(config.Transitions))
	for source, transitions := range config.Transitions {
		sourceID := strings.ToLower(strings.TrimSpace(source))
		if _, ok := members[sourceID]; !ok {
			return "", nil, fmt.Errorf("transitions reference unknown member %q", source)
		}
		if _, exists := normalized[sourceID]; exists {
			return "", nil, fmt.Errorf("duplicate transitions for member %q", source)
		}
		normalized[sourceID] = make(map[string]stateMachineTransition, len(transitions))
		for outcome, transition := range transitions {
			outcome = strings.ToLower(strings.TrimSpace(outcome))
			if outcome != "success" && outcome != "needs_followup" && outcome != "blocked" {
				return "", nil, fmt.Errorf("unsupported transition outcome %q", outcome)
			}
			if _, exists := normalized[sourceID][outcome]; exists {
				return "", nil, fmt.Errorf("duplicate transition for %s/%s", source, outcome)
			}
			transition.Action = strings.ToLower(strings.TrimSpace(transition.Action))
			transition.Target = strings.ToLower(strings.TrimSpace(transition.Target))
			switch transition.Action {
			case "send":
				if _, ok := members[transition.Target]; !ok {
					return "", nil, fmt.Errorf("transition %s/%s targets unknown member %q", source, outcome, transition.Target)
				}
				if strings.TrimSpace(transition.Instruction) == "" {
					transition.Instruction = defaultInstruction(transition.Target)
				}
				if strings.TrimSpace(transition.Instruction) == "" {
					return "", nil, fmt.Errorf("transition %s/%s has no instruction", source, outcome)
				}
			case "stop":
				if !stopAllowed[sourceID] {
					return "", nil, fmt.Errorf("member %q is not allowed to stop", source)
				}
			case "needs_human":
				if strings.TrimSpace(transition.Instruction) == "" {
					transition.Instruction = fmt.Sprintf("%s reported %s", sourceID, outcome)
				}
			default:
				return "", nil, fmt.Errorf("transition %s/%s has invalid action %q", source, outcome, transition.Action)
			}
			normalized[sourceID][outcome] = transition
		}
	}
	for memberID := range members {
		transitions, ok := normalized[memberID]
		if !ok {
			return "", nil, fmt.Errorf("member %q has no transitions", memberID)
		}
		for _, outcome := range []string{"success", "needs_followup", "blocked"} {
			if _, ok := transitions[outcome]; !ok {
				return "", nil, fmt.Errorf("member %q is missing %s transition", memberID, outcome)
			}
		}
	}
	router := func(_ context.Context, _ int, current handoffMember, result turnResult, _ bool) (handoffRoute, error) {
		if result.Status == nil {
			return handoffRoute{}, fmt.Errorf("state-machine routing requires a validated status receipt")
		}
		transition := normalized[current.ID][result.Status.Outcome]
		reason := fmt.Sprintf("state machine: %s/%s", current.ID, result.Status.Outcome)
		switch transition.Action {
		case "send":
			return handoffRoute{NextID: transition.Target, Instruction: transition.Instruction, Confidence: 1, Reason: reason}, nil
		case "stop":
			return handoffRoute{Stop: true, Reason: reason}, nil
		case "needs_human":
			return handoffRoute{NeedsHuman: true, Reason: transition.Instruction}, nil
		default:
			return handoffRoute{}, fmt.Errorf("state-machine transition %s is invalid", transition.Action)
		}
	}
	return initial, router, nil
}
