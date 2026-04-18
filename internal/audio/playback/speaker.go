// Package playback drives Kokoro PCM to the system speakers via malgo.
// Speakers expose three operations to the conv layer: Speak queues a
// sentence's samples into a long-lived sink without blocking on playback,
// Flush waits for the sink to drain, and Cancel clears it immediately for
// barge-in. Self-echo suppression (Mute/Unmute of VAD/STT) is owned by the
// caller so mute state can span a whole multi-sentence assistant turn —
// otherwise silence between sentences (Kokoro synth latency) surfaces as
// audible breaks in the voice.
package playback

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/Renderix/freeman/internal/audio"
	"github.com/gen2brain/malgo"
)

// Synthesizer is the subset of engine.TTSEngine this package needs.
// Voice and speed are passed through on every call so the caller (Speaker)
// can hold them as configuration.
type Synthesizer interface {
	GeneratePCM(text, voice string, speed float64) ([]int16, int, error)
}

// pcmSink abstracts the malgo device so tests can run without hardware.
type pcmSink interface {
	write(samples []int16) error
	drain(ctx context.Context) error
	clear()
	close() error
}

// Speaker implements conv.Speaker.
type Speaker struct {
	actx    *audio.Context
	synth   Synthesizer
	sink    pcmSink
	chunkMs int
	voice   string
	speed   float64

	mu sync.Mutex // serializes concurrent Speak calls (Kokoro is not reentrant)
}

// Config selects the output device and the synthesis parameters.
type Config struct {
	DeviceID string  // empty = default
	ChunkMs  int     // default 50
	Voice    string  // e.g. "af_heart"
	Speed    float64 // e.g. 1.0
}

// Open constructs a Speaker bound to the given synth. The output device is
// opened lazily on the first Speak call so it inherits the engine's sample
// rate without synthesizing a probe utterance.
func Open(actx *audio.Context, cfg Config, synth Synthesizer) (*Speaker, error) {
	if cfg.ChunkMs == 0 {
		cfg.ChunkMs = 50
	}
	if cfg.Voice == "" {
		cfg.Voice = "af_heart"
	}
	if cfg.Speed == 0 {
		cfg.Speed = 1.0
	}
	return &Speaker{
		actx:    actx,
		synth:   synth,
		sink:    nil,
		chunkMs: cfg.ChunkMs,
		voice:   cfg.Voice,
		speed:   cfg.Speed,
	}, nil
}

// Speak synthesizes text and writes samples into the sink. It does NOT wait
// for the audio to play out — the sink keeps draining on its own while the
// caller queues the next sentence. This is the core of gapless playback:
// synth for sentence N+1 runs while N is still audible, so the audio
// callback never sees an empty buffer mid-turn.
//
// ctx cancels synthesis only. Already-queued samples keep playing; use
// Cancel to interrupt playback itself.
func (s *Speaker) Speak(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	samples, sr, err := s.synth.GeneratePCM(text, s.voice, s.speed)
	if err != nil {
		return fmt.Errorf("synth: %w", err)
	}
	if len(samples) == 0 {
		return nil
	}

	if s.sink == nil {
		sink, err := newMalgoSink(s.actx, sr, 1)
		if err != nil {
			return fmt.Errorf("open playback device: %w", err)
		}
		s.sink = sink
	}

	chunkSize := sr * s.chunkMs / 1000
	if chunkSize <= 0 {
		chunkSize = len(samples)
	}
	for off := 0; off < len(samples); off += chunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		end := off + chunkSize
		if end > len(samples) {
			end = len(samples)
		}
		if err := s.sink.write(samples[off:end]); err != nil {
			return err
		}
	}
	return nil
}

// Flush blocks until the sink has drained (all queued samples played).
// Callers use this at end-of-turn, before unmuting the mic, so the tail
// of the last sentence isn't cut off and so self-echo can't leak in.
// Returns ctx.Err() if canceled — leaves pending samples in place.
func (s *Speaker) Flush(ctx context.Context) error {
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	if sink == nil {
		return nil
	}
	err := sink.drain(ctx)
	// Report underruns accumulated across this speaking batch so we can
	// tell whether synth kept up with playback. Non-zero means the user
	// heard one audible gap per count.
	if ms, ok := sink.(*malgoSink); ok {
		if n := ms.underruns(); n > 0 {
			fmt.Fprintf(os.Stderr, "playback underruns this turn: %d (gaps in audio)\n", n)
		}
	}
	return err
}

// Cancel clears all pending samples from the sink immediately. Used for
// barge-in: stop mid-utterance so the user's interruption isn't talked
// over. Safe to call when nothing is playing.
func (s *Speaker) Cancel() {
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	if sink != nil {
		sink.clear()
	}
}

// Close tears down the output device. Safe on shutdown paths.
func (s *Speaker) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sink == nil {
		return nil
	}
	err := s.sink.close()
	s.sink = nil
	return err
}

// malgoSink is the production pcmSink. Samples accumulate in a single
// buffer under a mutex; the audio callback copies from the front and
// shrinks the buffer. Silence is only emitted when the buffer is
// genuinely empty (end-of-utterance or idle), never on producer jitter.
type malgoSink struct {
	dev *malgo.Device

	mu               sync.Mutex
	buf              []int16 // pending samples, consumed from the front by the callback
	hasWritten       bool    // true once any samples have ever been written
	underrunsSinceFlush int  // write() calls that found buf empty after the first — each is an audible gap
}

func newMalgoSink(actx *audio.Context, sampleRate, channels int) (*malgoSink, error) {
	m := &malgoSink{}
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = uint32(channels)
	cfg.SampleRate = uint32(sampleRate)

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutput, _ []byte, frameCount uint32) {
			need := int(frameCount) * channels
			out := unsafe.Slice((*int16)(unsafe.Pointer(&pOutput[0])), need)
			m.mu.Lock()
			n := copy(out, m.buf)
			m.buf = m.buf[n:]
			m.mu.Unlock()
			for i := n; i < need; i++ {
				out[i] = 0
			}
		},
	}

	dev, err := malgo.InitDevice(actx.Raw().Context, cfg, callbacks)
	if err != nil {
		return nil, err
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return nil, err
	}
	m.dev = dev
	return m, nil
}

func (m *malgoSink) write(samples []int16) error {
	m.mu.Lock()
	// If this write finds the buffer empty AND we've written before,
	// the audio callback must have drained everything and been padding
	// with silence until we got here — i.e. the listener just heard a
	// gap. Count it so Flush can report how choppy the utterance was.
	if m.hasWritten && len(m.buf) == 0 {
		m.underrunsSinceFlush++
	}
	m.buf = append(m.buf, samples...)
	m.hasWritten = true
	m.mu.Unlock()
	return nil
}

// underruns returns and resets the count of write() calls that landed
// in an already-empty buffer since the last call. Each one corresponds
// to an audible gap in the preceding playback.
func (m *malgoSink) underruns() int {
	m.mu.Lock()
	n := m.underrunsSinceFlush
	m.underrunsSinceFlush = 0
	m.hasWritten = false
	m.mu.Unlock()
	return n
}

func (m *malgoSink) drain(ctx context.Context) error {
	// Poll until the buffer is empty. Audio hardware consumes at real-time,
	// so a short sleep between checks keeps the goroutine cheap without
	// meaningfully affecting end-of-playback latency.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		m.mu.Lock()
		empty := len(m.buf) == 0
		m.mu.Unlock()
		if empty {
			// Wait one extra audio period for hardware to finish the tail.
			time.Sleep(20 * time.Millisecond)
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *malgoSink) clear() {
	m.mu.Lock()
	m.buf = nil
	m.mu.Unlock()
}

func (m *malgoSink) close() error {
	if m.dev == nil {
		return nil
	}
	_ = m.dev.Stop()
	m.dev.Uninit()
	m.dev = nil
	return nil
}
