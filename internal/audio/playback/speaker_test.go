package playback

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeSink captures PCM chunks and simulates drain/clear semantics.
type fakeSink struct {
	mu       sync.Mutex
	chunks   [][]int16
	drained  int
	cleared  int
	drainBuf bool
}

func (f *fakeSink) write(samples []int16) error {
	cp := make([]int16, len(samples))
	copy(cp, samples)
	f.mu.Lock()
	f.chunks = append(f.chunks, cp)
	f.mu.Unlock()
	return nil
}

func (f *fakeSink) drain(ctx context.Context) error {
	f.mu.Lock()
	f.drained++
	f.mu.Unlock()
	return nil
}

func (f *fakeSink) clear() {
	f.mu.Lock()
	f.cleared++
	f.chunks = nil
	f.mu.Unlock()
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

func TestSpeaker_Speak_ChunksSamplesNoDrain(t *testing.T) {
	samples := make([]int16, 24000) // 1 second at 24 kHz
	for i := range samples {
		samples[i] = int16(i % 100)
	}
	synth := &fakeSynth{samples: samples, sr: 24000}
	sink := &fakeSink{}

	s := newSpeakerForTest(synth, sink, 50 /* chunkMs */)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Speak(ctx, "hello"); err != nil {
		t.Fatalf("Speak: %v", err)
	}

	// Speak must NOT drain — that's Flush's job. Draining between sentences
	// would reintroduce the inter-sentence silence this test guards against.
	if sink.drained != 0 {
		t.Errorf("drain called %d times during Speak, want 0", sink.drained)
	}

	// 1s at 24 kHz with 50 ms chunks = 20 chunks.
	if len(sink.chunks) != 20 {
		t.Errorf("chunks = %d, want 20", len(sink.chunks))
	}

	total := 0
	for _, c := range sink.chunks {
		total += len(c)
	}
	if total != len(samples) {
		t.Errorf("total samples = %d, want %d", total, len(samples))
	}
}

func TestSpeaker_Flush_DelegatesToSink(t *testing.T) {
	synth := &fakeSynth{samples: []int16{1, 2, 3}, sr: 24000}
	sink := &fakeSink{}
	s := newSpeakerForTest(synth, sink, 50)
	ctx := context.Background()

	if err := s.Speak(ctx, "hi"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if sink.drained != 1 {
		t.Errorf("drain called %d times, want 1", sink.drained)
	}
}

func TestSpeaker_Cancel_ClearsSink(t *testing.T) {
	synth := &fakeSynth{samples: []int16{1, 2, 3}, sr: 24000}
	sink := &fakeSink{}
	s := newSpeakerForTest(synth, sink, 50)

	if err := s.Speak(context.Background(), "hi"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	s.Cancel()
	if sink.cleared != 1 {
		t.Errorf("clear called %d times, want 1", sink.cleared)
	}
}

func TestSpeaker_Flush_NoSinkIsNoop(t *testing.T) {
	// Flush must tolerate being called before any Speak (no device opened).
	s := &Speaker{}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush on empty speaker: %v", err)
	}
}

func newSpeakerForTest(synth Synthesizer, sink pcmSink, chunkMs int) *Speaker {
	return &Speaker{
		synth:   synth,
		sink:    sink,
		chunkMs: chunkMs,
	}
}
