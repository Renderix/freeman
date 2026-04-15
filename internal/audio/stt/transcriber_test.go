package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio/vad"
)

func TestTranscriber_EmitsAndMutes(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	defer srv.Close()

	utts := make(chan vad.Utterance, 4)
	c := NewClient(srv.URL, time.Second)
	tr := NewTranscriber(c, utts, 16000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Run(ctx)

	utts <- vad.Utterance{PCM: []int16{0, 1, 2, 3}, DurationMs: 320}
	select {
	case got := <-tr.Utterances():
		if got != "hello" {
			t.Errorf("got %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no utterance")
	}

	// Mute, then send another — should NOT appear on Utterances().
	tr.Mute()
	utts <- vad.Utterance{PCM: []int16{0, 1, 2, 3}, DurationMs: 320}
	select {
	case got := <-tr.Utterances():
		t.Fatalf("muted but got %q", got)
	case <-time.After(200 * time.Millisecond):
		// good
	}
	tr.Unmute()

	// After unmute, next utterance goes through.
	utts <- vad.Utterance{PCM: []int16{0, 1, 2, 3}, DurationMs: 320}
	select {
	case got := <-tr.Utterances():
		if got != "hello" {
			t.Errorf("got %q after unmute", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no utterance after unmute")
	}

	if calls < 2 {
		t.Errorf("whisper calls = %d, want >= 2 (muted utterance should still POST but result is dropped)", calls)
	}
}

func TestTranscriber_DropsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"   "}`))
	}))
	defer srv.Close()

	utts := make(chan vad.Utterance, 1)
	c := NewClient(srv.URL, time.Second)
	tr := NewTranscriber(c, utts, 16000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Run(ctx)

	utts <- vad.Utterance{PCM: []int16{0, 1}, DurationMs: 40}
	select {
	case got := <-tr.Utterances():
		t.Errorf("whitespace passed through: %q", got)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}
