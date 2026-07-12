package workflow

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWorkflowCycleDetection(t *testing.T) {
	// This tests the cycle detection logic in cmd/workflow.go's validateWorkflow.
	// We replicate the core logic here since validateWorkflow is in cmd package.
	wf := &Workflow{
		Name: "test",
		Steps: []Step{
			{Name: "a", Depends: []string{"b"}},
			{Name: "b", Depends: []string{"a"}},
		},
	}
	if hasCycle(wf) {
		// expected: cycle detected
	} else {
		t.Error("should detect cycle a→b→a")
	}
}

func TestWorkflowNoCycle(t *testing.T) {
	wf := &Workflow{
		Name: "test",
		Steps: []Step{
			{Name: "a"},
			{Name: "b", Depends: []string{"a"}},
			{Name: "c", Depends: []string{"a", "b"}},
		},
	}
	if hasCycle(wf) {
		t.Error("should not detect cycle in valid DAG")
	}
}

func TestWorkflowSelfCycle(t *testing.T) {
	wf := &Workflow{
		Name: "test",
		Steps: []Step{
			{Name: "a", Depends: []string{"a"}},
		},
	}
	if !hasCycle(wf) {
		t.Error("should detect self-cycle")
	}
}

// hasCycle implements the same DFS-based cycle detection used in
// cmd/workflow.go validateWorkflow. We replicate it here for testing
// since validateWorkflow lives in the cmd package.
func hasCycle(wf *Workflow) bool {
	visited := map[string]int{} // 0=unvisited, 1=in-progress, 2=done
	stepByName := map[string]Step{}
	for _, s := range wf.Steps {
		stepByName[s.Name] = s
	}
	var dfs func(name string) bool
	dfs = func(name string) bool {
		if visited[name] == 1 {
			return true
		}
		if visited[name] == 2 {
			return false
		}
		visited[name] = 1
		s := stepByName[name]
		for _, dep := range s.Depends {
			if dfs(dep) {
				return true
			}
		}
		visited[name] = 2
		return false
	}
	for _, s := range wf.Steps {
		if dfs(s.Name) {
			return true
		}
	}
	return false
}

func TestWorkflowYAMLParsing(t *testing.T) {
	yamlStr := `
name: test
params:
  key: value
steps:
  - name: step1
    type: run
    instruction: "do something"
  - name: step2
    type: shell
    command: "echo hello"
    depends: [step1]
`
	var w Workflow
	if err := yaml.Unmarshal([]byte(yamlStr), &w); err != nil {
		t.Fatalf("yaml parse error: %v", err)
	}
	if w.Name != "test" {
		t.Errorf("name: got %q", w.Name)
	}
	if len(w.Steps) != 2 {
		t.Errorf("steps: got %d", len(w.Steps))
	}
	if w.Steps[1].Depends[0] != "step1" {
		t.Errorf("dep: got %q", w.Steps[1].Depends[0])
	}
	if w.Params["key"] != "value" {
		t.Errorf("param: got %q", w.Params["key"])
	}
}
