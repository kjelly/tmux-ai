package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/llmore/tmux-ai/internal/ollama"
	"github.com/llmore/tmux-ai/internal/tmux"
)

func TestPRPaneWatchReportsActivityAndAlerts(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	for _, id := range []string{"%1", "%2"} {
		ex.ExecResult["display-message -p -t "+id+" "+paneStateFormat] = "0|0||codex"
	}
	var out bytes.Buffer
	watch := prPaneWatch{executor: ex, number: 333, out: &out}
	for _, sample := range []struct {
		screen string
		want   string
		alerts int
	}{
		{"› Ask anything", "waiting for signs of activity", 0},
		{"• Working (1s • esc to interrupt)\n› Ask anything", "agent shows a running indicator", 0},
		{"• Working (2s • esc to interrupt)\n› Ask anything", "", 0},
		{"Running tests\n• Working (3s • esc to interrupt)\n› Ask anything", "visible output changed", 0},
		{"Running tests\n• Working (4s • esc to interrupt)\n│ Would you like to run the following command? │", "waiting for approval or input", 1},
		{"Running tests\n• Working (5s • esc to interrupt)\n│ Would you like to run the following command? │", "", 0},
		{"Running tests\n• Working (6s • esc to interrupt)\n› Ask anything", "agent shows a running indicator", 0},
		{"Tests finished\n› Ask anything", "agent returned to an input prompt", 1},
		{"Tests finished\n› Ask anything", "", 0},
	} {
		out.Reset()
		ex.Captures["%1"] = sample.screen
		if err := watch.poll(context.Background(), prDraft, "%1", time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); (sample.want == "" && got != "") || !strings.Contains(got, sample.want) || strings.Count(got, "\a") != sample.alerts {
			t.Fatalf("screen %q produced %q, want %q with %d alert(s)", sample.screen, got, sample.want, sample.alerts)
		}
	}

	out.Reset()
	ex.Captures["%2"] = "❯ "
	if err := watch.poll(context.Background(), prReady, "%2", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "ready pane %2: input prompt visible") || strings.Contains(got, "\a") {
		t.Fatalf("new phase inherited earlier activity: %q", got)
	}

	out.Reset()
	ex.ExecResult["display-message -p -t %2 "+paneStateFormat] = "0|0||bash"
	if err := watch.poll(context.Background(), prReady, "%2", time.Now()); err != nil {
		t.Fatalf("watcher stopped before giving GitHub time to update: %v", err)
	}
	if err := watch.poll(context.Background(), prReady, "%2", time.Now()); err == nil || !strings.Contains(err.Error(), "agent returned to a shell") {
		t.Fatalf("watcher missed repeated shell observations: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "foreground process is a shell") || strings.Count(got, "\a") != 1 {
		t.Fatalf("shell should trigger one alert: %q", got)
	}
}

func TestPRPaneWatchFollowsStablePhase(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		o := testPRLoopOptions()
		o.Observe, o.DryRun = true, dryRun
		observations := phaseObservations(prDraft, prDraft, prReady, prDraft, prReady, prReady, prReady, prMerged)
		observations[6] = prObservation{err: &prQueryError{errors.New("network unavailable")}}
		index := 0
		var sent, watched []prPhase
		err := runPRLoop(context.Background(), o,
			func(context.Context, string, int) (prPhase, error) {
				if index >= len(observations) {
					t.Fatal("polling continued after merge")
				}
				observation := observations[index]
				index++
				return observation.phase, observation.err
			},
			func(_ context.Context, phase prPhase, _ string) error { sent = append(sent, phase); return nil },
			func(_ context.Context, phase prPhase) error { watched = append(watched, phase); return nil },
			func(context.Context, time.Duration) error { return nil }, time.Now, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if dryRun {
			if len(sent) != 0 || len(watched) != 0 {
				t.Fatalf("dry run contacted panes: sent=%v watched=%v", sent, watched)
			}
		} else if !reflect.DeepEqual(sent, []prPhase{prReady}) || !reflect.DeepEqual(watched, []prPhase{prDraft, prDraft, prReady}) {
			t.Fatalf("sent=%v watched=%v", sent, watched)
		}
	}
}

func TestPRLoopAgentExitVersusMergeTrain(t *testing.T) {
	for _, enqueued := range []bool{false, true} {
		ex := tmux.NewFakeExecutor()
		ex.ExecResult["display-message -p -t %1 "+paneStateFormat] = "0|1||"
		watch := prPaneWatch{executor: ex, number: 333, out: io.Discard, quiet: true}
		o := testPRLoopOptions()
		o.StablePolls = 1
		phases := []prPhase{prReady, prReady}
		if enqueued {
			phases = []prPhase{prReady, prQueued, prQueued, prMerged}
		}
		sent := 0
		err := runPRLoop(context.Background(), o,
			func(context.Context, string, int) (prPhase, error) {
				if len(phases) == 0 {
					t.Fatal("unexpected additional PR query")
				}
				phase := phases[0]
				phases = phases[1:]
				return phase, nil
			},
			func(context.Context, prPhase, string) error { sent++; return nil },
			func(ctx context.Context, phase prPhase) error { return watch.poll(ctx, phase, "%1", time.Now()) },
			func(context.Context, time.Duration) error { return nil }, time.Now, io.Discard)
		if enqueued && err != nil || !enqueued && (err == nil || !strings.Contains(err.Error(), "agent exited")) {
			t.Fatalf("enqueued=%v: unexpected result %v", enqueued, err)
		}
		if sent != 1 || len(ex.CaptureCalls) != 0 {
			t.Fatalf("enqueued=%v: dispatches=%d, captures=%v", enqueued, sent, ex.CaptureCalls)
		}
	}
}

func TestPRPaneIdleDiagnosisIsAdvisory(t *testing.T) {
	ex := tmux.NewFakeExecutor()
	ex.ExecResult["display-message -p -t %1 "+paneStateFormat] = "0|0||codex"
	client := ollama.NewFakeClient()
	for _, content := range []string{`{"state":"needs_input","summary":"Waiting on GitHub login"}`, `{"state":"merged","summary":"Claim of success"}`} {
		client.Responses = append(client.Responses, ollama.ChatResponse{Message: ollama.Message{Content: content}})
	}
	var out bytes.Buffer
	watch := prPaneWatch{executor: ex, number: 333, out: &out, quiet: true, diagnoseAfter: time.Second,
		diagnose: func(ctx context.Context, screen string) (prPaneDiagnosis, error) {
			return diagnosePRPane(ctx, client, screen)
		}}
	start := time.Unix(100, 0)
	for second, screen := range []string{
		"Sign-in dialog\n• Working (1s • esc to interrupt)",
		"Sign-in dialog\n• Working (2s • esc to interrupt)",
		"Sign-in dialog\n• Working (3s • esc to interrupt)",
		"New output",
		"New output",
	} {
		ex.Captures["%1"] = screen
		if err := watch.poll(context.Background(), prReady, "%1", start.Add(time.Duration(second)*time.Second)); err != nil {
			t.Fatalf("diagnosis changed control flow: %v", err)
		}
	}
	if len(client.Calls) != 2 || len(client.Calls[0].Tools) != 0 ||
		!strings.Contains(out.String(), "diagnostic (needs_input): Waiting on GitHub login") ||
		!strings.Contains(out.String(), "diagnostic unavailable: invalid diagnostic state") || len(ex.SendLinesCalls) != 0 {
		t.Fatalf("unexpected diagnosis calls or output: %d, %s", len(client.Calls), out.String())
	}
}
