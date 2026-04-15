package playback

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio"
)

// fakeSink captures PCM chunks and simulates draining.
type fakeSink struct {
	chunks [][]int16
}

func (f *fakeSink) write(samples []int16) error {
	// Copy so caller can reuse its buffer.
	cp := make([]int16, len(samples))
	copy(cp, samples)
	f.chunks = append(f.chunks, cp)
	return nil
}

func (f *fakeSink) drain(ctx context.Context) error {
	return nil
}

func (f *fakeSink) close() error { return nil }

// fakeSynth returns predetermined PCM for any text.
type fakeSynth struct {
	samples []int16
	sr      int
}

func (f *fakeSynth) GeneratePCM(text, voice string, speed float64) ([]int16, int, error) {
	return f.samples, f.sr, nil
}

// callRecorder logs mute/unmute order for assertions.
type callRecorder struct {
	events []string
	mu     atomicEventRecorder
}

type atomicEventRecorder struct {
	seq int64
}

func newRecorder() *callRecorder { return &callRecorder{} }
func (r *callRecorder) Mute() {
	r.mu.seq++
	r.events = append(r.events, "mute")
}
func (r *callRecorder) Unmute() {
	r.mu.seq++
	r.events = append(r.events, "unmute")
}

func TestSpeaker_Speak_MuteOrderAndChunks(t *testing.T) {
	samples := make([]int16, 24000) // 1 second at 24 kHz
	for i := range samples {
		samples[i] = int16(i % 100)
	}
	synth := &fakeSynth{samples: samples, sr: 24000}
	sink := &fakeSink{}
	rec := newRecorder()

	s := newSpeakerForTest(synth, rec, sink, 50 /* chunkMs */)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Speak(ctx, "hello"); err != nil {
		t.Fatalf("Speak: %v", err)
	}

	if got := len(rec.events); got != 2 {
		t.Fatalf("mute events = %d, want 2", got)
	}
	if rec.events[0] != "mute" || rec.events[1] != "unmute" {
		t.Errorf("events = %v, want [mute unmute]", rec.events)
	}

	// 1 s at 24 kHz / 50 ms chunks = 20 chunks.
	if len(sink.chunks) != 20 {
		t.Errorf("chunks = %d, want 20", len(sink.chunks))
	}

	// Every sample accounted for.
	total := 0
	for _, c := range sink.chunks {
		total += len(c)
	}
	if total != len(samples) {
		t.Errorf("total samples = %d, want %d", total, len(samples))
	}
}

func TestSpeaker_Speak_CtxCancelShortCircuits(t *testing.T) {
	samples := make([]int16, 48000) // 2 seconds
	synth := &fakeSynth{samples: samples, sr: 24000}
	sink := &blockingFakeSink{release: make(chan struct{})}
	rec := newRecorder()

	s := newSpeakerForTest(synth, rec, sink, 50)
	ctx, cancel := context.WithCancel(context.Background())

	var unmutedEarly atomic.Bool
	done := make(chan error, 1)
	go func() { done <- s.Speak(ctx, "text") }()

	time.Sleep(100 * time.Millisecond)
	cancel()
	// Release the sink so Speak can exit.
	close(sink.release)

	select {
	case err := <-done:
		if err == nil {
			t.Log("speak returned nil (sink drained before cancel took effect)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Speak did not return after cancel")
	}
	// Unmute must have run regardless of cancel path.
	if len(rec.events) < 2 || rec.events[len(rec.events)-1] != "unmute" {
		t.Errorf("last event = %v, want unmute", rec.events)
	}
	_ = &unmutedEarly
}

type blockingFakeSink struct {
	release chan struct{}
	fakeSink
}

func (b *blockingFakeSink) write(samples []int16) error {
	select {
	case <-b.release:
	case <-time.After(5 * time.Second):
	}
	return b.fakeSink.write(samples)
}

func newSpeakerForTest(synth Synthesizer, muter audio.Muter, sink pcmSink, chunkMs int) *Speaker {
	return &Speaker{
		synth:   synth,
		muter:   muter,
		sink:    sink,
		chunkMs: chunkMs,
	}
}
