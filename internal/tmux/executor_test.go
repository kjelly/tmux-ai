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

func TestSendTextChunksWaitsAfterEveryTenCharacters(t *testing.T) {
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
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond {
		t.Fatalf("elapsed = %s, want at least one delay per chunk", elapsed)
	}
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
	if sendTextChunkSize != 500 || sendTextChunkDelay != time.Second {
		t.Fatalf("throttle = %d chars/%s, want 500 chars/1s", sendTextChunkSize, sendTextChunkDelay)
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
