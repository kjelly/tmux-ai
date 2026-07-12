package workflow

import (
	"testing"
)

func TestExpandParams(t *testing.T) {
	e := &Executor{
		params: map[string]string{"model": "claude", "port": "8080"},
	}
	got := e.expand("use {{params.model}} on port {{params.port}}")
	if got != "use claude on port 8080" {
		t.Errorf("got %q", got)
	}
}

func TestExpandStepOutput(t *testing.T) {
	e := &Executor{
		params:  map[string]string{},
		results: map[string]*StepResult{"build": {Output: "Build succeeded"}},
	}
	got := e.expand("result: {{steps.build.output}}")
	if got != "result: Build succeeded" {
		t.Errorf("got %q", got)
	}
}

func TestExpandNoMatch(t *testing.T) {
	e := &Executor{
		params: map[string]string{"x": "1"},
	}
	got := e.expand("no substitution here")
	if got != "no substitution here" {
		t.Errorf("got %q", got)
	}
	// Unresolved params remain literal
	got2 := e.expand("{{params.y}}")
	if got2 != "{{params.y}}" {
		t.Errorf("unresolved param: got %q", got2)
	}
}

func TestEvalDepsNoDeps(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{},
	}
	s := &Step{Name: "s1", When: ""}
	ready, skip := e.evalDeps(s)
	if !ready || skip {
		t.Error("step with no deps should be ready and not skipped")
	}
}

func TestEvalDepsDepNotDone(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{},
	}
	s := &Step{Name: "s2", Depends: []string{"s1"}}
	ready, skip := e.evalDeps(s)
	if ready || skip {
		t.Error("dep not done: should not be ready and not skipped")
	}
}

func TestEvalDepsDepSuccess(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{"s1": {Status: "success"}},
	}
	s := &Step{Name: "s2", Depends: []string{"s1"}, When: ""}
	ready, skip := e.evalDeps(s)
	if !ready || skip {
		t.Error("dep succeeded: should be ready")
	}
}

func TestEvalDepsDepFailedOnSuccess(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{"s1": {Status: "failed"}},
	}
	s := &Step{Name: "s2", Depends: []string{"s1"}, When: ""}
	ready, skip := e.evalDeps(s)
	if ready || !skip {
		t.Error("dep failed with on_success: should be skipped")
	}
}

func TestEvalDepsDepFailedOnFailure(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{"s1": {Status: "failed"}},
	}
	s := &Step{Name: "s2", Depends: []string{"s1"}, When: "on_failure"}
	ready, skip := e.evalDeps(s)
	if !ready || skip {
		t.Error("dep failed with on_failure: should be ready")
	}
}

func TestEvalDepsAlways(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{"s1": {Status: "failed"}},
	}
	s := &Step{Name: "s2", Depends: []string{"s1"}, When: "always"}
	ready, skip := e.evalDeps(s)
	if !ready || skip {
		t.Error("always: should be ready even if dep failed")
	}
}

func TestEvalExprContains(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{"s1": {Output: "error: build failed"}},
	}
	handled, result := e.evalExpr(`contains(steps.s1.output, "error:")`)
	if !handled || !result {
		t.Error("should match contains")
	}
}

func TestEvalExprNotContains(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{"s1": {Output: "all good"}},
	}
	handled, result := e.evalExpr(`not_contains(steps.s1.output, "error:")`)
	if !handled || !result {
		t.Error("not_contains should match when text absent")
	}
}

func TestEvalExprNotHandled(t *testing.T) {
	e := &Executor{}
	handled, _ := e.evalExpr("some random expr")
	if handled {
		t.Error("unknown expr should not be handled")
	}
}

func TestBuildExtraContextSingle(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{"s1": {Output: "output1"}},
	}
	s := &Step{ContextFrom: "s1"}
	got := e.buildExtraContext(s)
	if got == "" {
		t.Error("should have context")
	}
}

func TestBuildExtraContextMultiple(t *testing.T) {
	e := &Executor{
		results: map[string]*StepResult{
			"s1": {Output: "output1"},
			"s2": {Output: "output2"},
		},
	}
	s := &Step{ContextFrom: []interface{}{"s1", "s2"}}
	got := e.buildExtraContext(s)
	if got == "" {
		t.Error("should have context")
	}
}

func TestBuildExtraContextNone(t *testing.T) {
	e := &Executor{}
	s := &Step{ContextFrom: nil}
	got := e.buildExtraContext(s)
	if got != "" {
		t.Error("nil ContextFrom should return empty")
	}
}

func TestFindStep(t *testing.T) {
	e := &Executor{
		wf: &Workflow{
			Steps: []Step{{Name: "a"}, {Name: "b"}},
		},
	}
	if e.findStep("a") == nil {
		t.Error("should find step a")
	}
	if e.findStep("c") != nil {
		t.Error("should not find step c")
	}
}
