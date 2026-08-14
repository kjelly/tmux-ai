package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSplitTextChunksUsesUnicodeCharacters(t *testing.T) {
	input := "甲乙丙丁戊己庚辛壬癸ABCDEFGHIJK"
	chunks := splitTextChunks(input, 10)
	want := []string{"甲乙丙丁戊己庚辛壬癸", "ABCDEFGHIJ", "K"}
	if len(chunks) != len(want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	for i := range want {
		if chunks[i] != want[i] {
			t.Fatalf("chunk %d = %q, want %q", i, chunks[i], want[i])
		}
	}
	if strings.Join(chunks, "") != input {
		t.Fatal("chunks do not reconstruct input")
	}
}

func TestSendTextChunksWaitsOnlyBetweenChunks(t *testing.T) {
	var got []string
	started := time.Now()
	err := sendTextChunksWith(context.Background(), "1234567890abcdefghij", 10, 5*time.Millisecond, func(chunk string) error {
		got = append(got, chunk)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "1234567890" || got[1] != "abcdefghij" {
		t.Fatalf("sent chunks = %#v", got)
	}
	if elapsed := time.Since(started); elapsed < 5*time.Millisecond || elapsed >= 10*time.Millisecond {
		t.Fatalf("elapsed = %s, want one inter-chunk delay", elapsed)
	}
}

func TestSendTextChunksDoesNotDelayAfterSingleChunk(t *testing.T) {
	started := time.Now()
	err := sendTextChunksWith(context.Background(), "123", 10, 50*time.Millisecond, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 40*time.Millisecond {
		t.Fatalf("elapsed = %s, single chunk should not wait", elapsed)
	}
}

func TestPasteTextChunkUsesUniqueNamedBuffer(t *testing.T) {
	var calls [][]string
	e := &executorImpl{command: func(args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		return "", nil
	}}
	if err := e.pasteTextChunk("%42", "first"); err != nil {
		t.Fatal(err)
	}
	if err := e.pasteTextChunk("%42", "second"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("calls = %#v", calls)
	}
	first, second := calls[0], calls[1]
	if first[0] != "load-buffer" || first[1] != "-b" || strings.HasPrefix(first[2], "tmux-ai-") == false {
		t.Fatalf("load call = %#v", first)
	}
	if strings.Contains(strings.Join(first, " "), "-t %42") {
		t.Fatalf("load-buffer targeted pane: %#v", first)
	}
	if want := []string{"paste-buffer", "-b", first[2], "-d", "-p", "-r", "-t", "%42"}; !equalStrings(second, want) {
		t.Fatalf("paste call = %#v, want %#v", second, want)
	}
	if calls[2][2] == first[2] {
		t.Fatalf("buffer name reused: %#v", calls)
	}
}

func TestPasteTextChunkCleansUpNamedBufferOnPasteFailure(t *testing.T) {
	var calls [][]string
	e := &executorImpl{command: func(args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "paste-buffer" {
			return "", errors.New("paste failed")
		}
		return "", nil
	}}
	if err := e.pasteTextChunk("%42", "body"); err == nil {
		t.Fatal("pasteTextChunk() error = nil")
	}
	if len(calls) != 3 || calls[2][0] != "delete-buffer" || calls[2][1] != "-b" || calls[2][2] != calls[0][2] {
		t.Fatalf("calls = %#v", calls)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSendTextChunksStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var got []string
	err := sendTextChunksWith(ctx, "1234567890abcdefghij", 10, time.Second, func(chunk string) error {
		got = append(got, chunk)
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(got) != 1 || got[0] != "1234567890" {
		t.Fatalf("sent chunks after cancellation = %#v", got)
	}
}

func TestTextThrottleDefaults(t *testing.T) {
	defaults := DefaultOptions()
	if defaults.PasteChunkRunes != 500 || defaults.PasteChunkDelay != time.Second {
		t.Fatalf("throttle = %d runes/%s, want 500 runes/1s", defaults.PasteChunkRunes, defaults.PasteChunkDelay)
	}
}

func TestPasteOptionsFallBackToDefaults(t *testing.T) {
	normalized := Options{PasteChunkRunes: -1, PasteChunkDelay: -time.Second}.normalized()
	if normalized != DefaultOptions() {
		t.Fatalf("normalized = %#v, want the defaults", normalized)
	}
	// Zero delay is a legitimate choice for an agent that handles one paste.
	fast := Options{PasteChunkRunes: 8000, PasteChunkDelay: 0}.normalized()
	if fast.PasteChunkRunes != 8000 || fast.PasteChunkDelay != 0 {
		t.Fatalf("normalized = %#v, want the caller's values", fast)
	}
}

func TestMixedTextAndControlPreservesOrder(t *testing.T) {
	var got []string
	err := sendMixedTextWith(
		context.Background(), "前文12345678\x03後文", 10, 0,
		func(text string) error {
			got = append(got, "text:"+text)
			return nil
		},
		func(control rune) error {
			got = append(got, "control:"+string(control))
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"text:前文12345678", "control:\x03", "text:後文"}
	if len(got) != len(want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTrimLineEndsRestoresCapturePaneTrimming(t *testing.T) {
	// capture-pane -J keeps the padding tmux normally strips, which would make
	// every screen comparison and relayed line carry pane-width whitespace.
	got := trimLineEnds("marker  \n  indented\t\nplain")
	if want := "marker\n  indented\nplain"; got != want {
		t.Fatalf("trimLineEnds() = %q, want %q", got, want)
	}
}
