package cmd

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
	"github.com/spf13/pflag"
)

func TestResolveCompactionThresholdScreenMeter(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	addCompactionFlags(flags)
	config, err := resolveCompactionConfig(flags, compactionPresetConfig{
		Enabled:          true,
		Mode:             "threshold",
		MinRoundsBetween: 2,
		AtPercent:        70,
		Members:          []string{"Pane1"},
		ContextMeter: contextMeterPresetConfig{
			Mode:    "screen_regex",
			Pattern: `(?i)context:\s*(\d+)%`,
		},
	})
	if err != nil {
		t.Fatalf("resolveCompactionConfig() error = %v", err)
	}
	if !config.Enabled || config.Mode != compactionThreshold || config.MinRoundsBetween != 2 || config.AtPercent != 70 || !config.Members["pane1"] || config.Meter.Mode != contextMeterScreenRegex {
		t.Fatalf("resolved config = %#v", config)
	}
}

func TestResolveCompactionLLMMeterDefaults(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	addCompactionFlags(flags)
	config, err := resolveCompactionConfig(flags, compactionPresetConfig{
		Enabled: true,
		Mode:    "threshold",
		ContextMeter: contextMeterPresetConfig{
			Mode: "llm",
		},
	})
	if err != nil {
		t.Fatalf("resolveCompactionConfig() error = %v", err)
	}
	if config.Meter.Mode != contextMeterLLM || config.Meter.BottomLines != 16 || config.Meter.LLMMinConfidence != 0.85 {
		t.Fatalf("resolved LLM config = %#v", config.Meter)
	}
}

func TestResolveCompactionLLMMeterRequiresThresholdMode(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	addCompactionFlags(flags)
	_, err := resolveCompactionConfig(flags, compactionPresetConfig{
		Enabled: true,
		ContextMeter: contextMeterPresetConfig{
			Mode: "llm",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires compaction.mode=threshold") {
		t.Fatalf("resolveCompactionConfig() error = %v, want threshold-mode validation", err)
	}
}

func TestCompactorReadsScreenAndEstimateMeters(t *testing.T) {
	member := handoffMember{ID: "pane1", Label: "reviewer", Target: "%42"}
	ex := tmux.NewFakeExecutor()
	ex.Captures[member.Target] = "Context: 153600/200000 tokens"
	screen := newCompactor(resolvedCompactionConfig{Meter: resolvedContextMeterConfig{
		Mode:    contextMeterScreenRegex,
		Pattern: regexpMustCompile(t, `Context:\s*(\d+)/(\d+)\s*tokens`),
	}}, nil)
	reading, err := screen.readContext(context.Background(), ex, member)
	if err != nil || !reading.Known || reading.Percent != 77 || reading.Confidence != "reported" {
		t.Fatalf("screen reading = %#v, %v", reading, err)
	}
	estimate := newCompactor(resolvedCompactionConfig{Meter: resolvedContextMeterConfig{Mode: contextMeterEstimate, WindowTokens: 100}}, nil)
	estimate.recordPrompt(member, strings.Repeat("x", 200))
	reading, err = estimate.readContext(context.Background(), ex, member)
	if err != nil || !reading.Known || reading.Percent != 50 || reading.Confidence != "lower bound" {
		t.Fatalf("estimate reading = %#v, %v", reading, err)
	}
}

func TestCompactorOnlySendsCompactToEligibleCodexPane(t *testing.T) {
	oldDelay, oldConfirm := ppSubmitDelay, ppSubmitConfirmTimeout
	ppSubmitDelay, ppSubmitConfirmTimeout = 0, 0
	defer func() { ppSubmitDelay, ppSubmitConfirmTimeout = oldDelay, oldConfirm }()

	member := handoffMember{ID: "pane1", Label: "reviewer", Target: "%42"}
	ex := tmux.NewFakeExecutor()
	ex.ExecResult["display-message -p -t %42 "+paneStateFormat] = "0|0||codex"
	compactor := newCompactor(resolvedCompactionConfig{Enabled: true, Mode: compactionBeforeHandoff, MinRoundsBetween: 1}, nil)
	if err := compactor.maybeCompact(context.Background(), ex, member, 1); err != nil {
		t.Fatalf("maybeCompact() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].PaneID != "%42" || ex.SendLinesCalls[0].Content != "/compact" {
		t.Fatalf("compact sends = %#v", ex.SendLinesCalls)
	}

	ex.SendLinesCalls = nil
	ex.ExecResult["display-message -p -t %42 "+paneStateFormat] = "0|0||agy"
	if err := compactor.maybeCompact(context.Background(), ex, member, 2); err != nil {
		t.Fatalf("non-Codex maybeCompact() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("non-Codex pane received /compact: %#v", ex.SendLinesCalls)
	}
}

func TestCompactorUsesLLMDecisionFromBottomLines(t *testing.T) {
	oldDelay, oldConfirm := ppSubmitDelay, ppSubmitConfirmTimeout
	ppSubmitDelay, ppSubmitConfirmTimeout = 0, 0
	defer func() { ppSubmitDelay, ppSubmitConfirmTimeout = oldDelay, oldConfirm }()

	member := handoffMember{ID: "pane1", Label: "reviewer", Target: "%42"}
	ex := tmux.NewFakeExecutor()
	ex.ExecResult["display-message -p -t %42 "+paneStateFormat] = "0|0||codex"
	ex.Captures[member.Target] = "old line that must not be classified\nordinary output\nContext window: 188000 / 200000 tokens\nPress Ctrl-C to interrupt"
	fake := ollama.NewFakeClient()
	fake.Responses = []ollama.ChatResponse{{Message: ollama.Message{Content: `{"compact":true,"confidence":0.91,"reason":"context window is nearly full"}`}, Done: true}}
	compactor := newCompactor(resolvedCompactionConfig{
		Enabled: true, Mode: compactionThreshold, MinRoundsBetween: 1,
		Meter: resolvedContextMeterConfig{Mode: contextMeterLLM, BottomLines: 3, LLMMinConfidence: 0.85},
	}, newHandoffGuard())
	compactor.client = fake

	if err := compactor.maybeCompact(context.Background(), ex, member, 1); err != nil {
		t.Fatalf("maybeCompact() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 1 || ex.SendLinesCalls[0].Content != "/compact" {
		t.Fatalf("compact sends = %#v", ex.SendLinesCalls)
	}
	if len(fake.Calls) != 1 || fake.Calls[0].Format == nil {
		t.Fatalf("LLM calls = %#v", fake.Calls)
	}
	prompt := fake.Calls[0].Messages[1].Content
	if strings.Contains(prompt, "old line that must not be classified") || !strings.Contains(prompt, "188000 / 200000") {
		t.Fatalf("LLM prompt did not contain only the requested bottom lines: %q", prompt)
	}
}

func TestCompactorLLMLowConfidenceDoesNotCompact(t *testing.T) {
	oldDelay, oldConfirm := ppSubmitDelay, ppSubmitConfirmTimeout
	ppSubmitDelay, ppSubmitConfirmTimeout = 0, 0
	defer func() { ppSubmitDelay, ppSubmitConfirmTimeout = oldDelay, oldConfirm }()

	member := handoffMember{ID: "pane1", Label: "reviewer", Target: "%42"}
	ex := tmux.NewFakeExecutor()
	ex.ExecResult["display-message -p -t %42 "+paneStateFormat] = "0|0||codex"
	ex.Captures[member.Target] = "Context window: 188000 / 200000 tokens"
	fake := ollama.NewFakeClient()
	fake.Responses = []ollama.ChatResponse{{Message: ollama.Message{Content: `{"compact":true,"confidence":0.50,"reason":"uncertain"}`}, Done: true}}
	compactor := newCompactor(resolvedCompactionConfig{
		Enabled: true, Mode: compactionThreshold, MinRoundsBetween: 1,
		Meter: resolvedContextMeterConfig{Mode: contextMeterLLM, BottomLines: 3, LLMMinConfidence: 0.85},
	}, newHandoffGuard())
	compactor.client = fake

	if err := compactor.maybeCompact(context.Background(), ex, member, 1); err != nil {
		t.Fatalf("maybeCompact() error = %v", err)
	}
	if len(ex.SendLinesCalls) != 0 {
		t.Fatalf("low-confidence LLM decision sent /compact: %#v", ex.SendLinesCalls)
	}
}

func TestParseLLMCompactionDecisionRequiresExactSchema(t *testing.T) {
	if _, err := parseLLMCompactionDecision(`{"compact":false,"confidence":0.9,"reason":"fine","extra":true}`); err == nil {
		t.Fatal("parseLLMCompactionDecision accepted extra field")
	}
	if _, err := parseLLMCompactionDecision("result: {\"compact\":false,\"confidence\":0.9,\"reason\":\"fine\"}"); err == nil {
		t.Fatal("parseLLMCompactionDecision accepted non-JSON output")
	}
}

func regexpMustCompile(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return re
}
