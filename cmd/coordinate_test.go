package cmd

import (
	"strings"
	"testing"
)

func testCoordinateMembers() []coordinateMember {
	return []coordinateMember{
		{Name: "architect", Target: "team:architecture", Role: "plan and review"},
		{Name: "implementer", Target: "team:implementation", Role: "edit and test"},
		{Name: "tester", Target: "team:test", Role: "verify independently"},
	}
}

func TestValidateCoordinateMembers(t *testing.T) {
	if err := validateCoordinateMembers(testCoordinateMembers()); err != nil {
		t.Fatalf("validateCoordinateMembers() error = %v", err)
	}

	tests := []struct {
		name    string
		members []coordinateMember
		want    string
	}{
		{name: "too few", members: testCoordinateMembers()[:1], want: "at least two"},
		{name: "missing role", members: []coordinateMember{{Name: "one", Target: "%1"}, {Name: "two", Target: "%2", Role: "test"}}, want: "name, target, and role"},
		{name: "duplicate name", members: []coordinateMember{{Name: "worker", Target: "%1", Role: "one"}, {Name: "WORKER", Target: "%2", Role: "two"}}, want: "duplicate member name"},
		{name: "duplicate target", members: []coordinateMember{{Name: "one", Target: "%1", Role: "one"}, {Name: "two", Target: "%1", Role: "two"}}, want: "duplicate member target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCoordinateMembers(tt.members)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseCoordinateDecision(t *testing.T) {
	members := testCoordinateMembers()
	got, err := parseCoordinateDecision("```json\n{\"action\":\"send\",\"target\":\"TESTER\",\"instruction\":\"run the race tests\",\"reason\":\"implementation finished\",\"confidence\":0.91}\n```", members)
	if err != nil {
		t.Fatalf("parseCoordinateDecision() error = %v", err)
	}
	if got.Target != "tester" || got.Instruction != "run the race tests" || got.Confidence != 0.91 {
		t.Fatalf("parseCoordinateDecision() = %#v", got)
	}
}

func TestParseCoordinateDecisionRejectsUnknownMember(t *testing.T) {
	_, err := parseCoordinateDecision(`{"action":"send","target":"deployer","instruction":"deploy","confidence":0.9}`, testCoordinateMembers())
	if err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("error = %v, want unknown member", err)
	}
}
