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
		watch.poll(prDraft, "%1")
		if got := out.String(); (sample.want == "" && got != "") || !strings.Contains(got, sample.want) || strings.Count(got, "\a") != sample.alerts {
			t.Fatalf("screen %q produced %q, want %q with %d alert(s)", sample.screen, got, sample.want, sample.alerts)
		}
	}

	out.Reset()
	ex.Captures["%2"] = "❯ "
	watch.poll(prReady, "%2")
	if got := out.String(); !strings.Contains(got, "ready pane %2: input prompt visible") || strings.Contains(got, "\a") {
		t.Fatalf("new phase inherited earlier activity: %q", got)
	}

	out.Reset()
	ex.ExecResult["display-message -p -t %2 "+paneStateFormat] = "0|0||bash"
	watch.poll(prReady, "%2")
	watch.poll(prReady, "%2")
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
			func(phase prPhase) { watched = append(watched, phase) },
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
