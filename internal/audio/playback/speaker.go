// Package playback drives Kokoro PCM to the system speakers via malgo and
// manages self-echo suppression through the audio.Muter interface.
package playback

import (
	"context"
	"fmt"
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
	close() error
}

// Speaker implements call.Speaker.
type Speaker struct {
	actx    *audio.Context
	synth   Synthesizer
	muter   audio.Muter
	sink    pcmSink
	chunkMs int
	voice   string
	speed   float64

	mu sync.Mutex // serializes concurrent Speak calls
}

// Config selects the output device and the synthesis parameters.
type Config struct {
	DeviceID string  // empty = default
	ChunkMs  int     // default 50
	Voice    string  // e.g. "af_heart"
	Speed    float64 // e.g. 1.0
}

// Open constructs a Speaker and opens an output device bound to the given synth.
// muter is the audio.Muter that Speak will call around playback (typically the
// stt.Transcriber). Pass &audio.NoopMuter{} if no transcription is wired.
func Open(actx *audio.Context, cfg Config, synth Synthesizer, muter audio.Muter) (*Speaker, error) {
	if cfg.ChunkMs == 0 {
		cfg.ChunkMs = 50
	}
	if cfg.Voice == "" {
		cfg.Voice = "af_heart"
	}
	if cfg.Speed == 0 {
		cfg.Speed = 1.0
	}
	// Device is opened lazily inside Speak so we inherit the engine's sample rate
	// without synthesizing a probe utterance.
	return &Speaker{
		actx:    actx,
		synth:   synth,
		muter:   muter,
		sink:    nil,
		chunkMs: cfg.ChunkMs,
		voice:   cfg.Voice,
		speed:   cfg.Speed,
	}, nil
}

// Speak synthesizes and plays text. Blocks until playback drains or ctx cancels.
// Muter is called as: Mute before, Unmute deferred.
func (s *Speaker) Speak(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	s.muter.Mute()
	defer s.muter.Unmute()

	chunkSize := sr * s.chunkMs / 1000
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
	return s.sink.drain(ctx)
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

// malgoSink is the production pcmSink.
type malgoSink struct {
	dev     *malgo.Device
	ch      chan []int16
	pending []int16
	mu      sync.Mutex // protects pending
}

func newMalgoSink(actx *audio.Context, sampleRate, channels int) (*malgoSink, error) {
	m := &malgoSink{
		ch: make(chan []int16, 16),
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = uint32(channels)
	cfg.SampleRate = uint32(sampleRate)

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutput, _ []byte, frameCount uint32) {
			need := int(frameCount) * channels
			out := unsafe.Slice((*int16)(unsafe.Pointer(&pOutput[0])), need)
			filled := 0
			m.mu.Lock()
			if len(m.pending) > 0 {
				n := copy(out, m.pending)
				filled += n
				m.pending = m.pending[n:]
			}
			m.mu.Unlock()
			for filled < need {
				select {
				case chunk, ok := <-m.ch:
					if !ok {
						for i := filled; i < need; i++ {
							out[i] = 0
						}
						return
					}
					n := copy(out[filled:], chunk)
					if n < len(chunk) {
						m.mu.Lock()
						m.pending = chunk[n:]
						m.mu.Unlock()
					}
					filled += n
				default:
					for i := filled; i < need; i++ {
						out[i] = 0
					}
					return
				}
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
	cp := make([]int16, len(samples))
	copy(cp, samples)
	m.ch <- cp
	return nil
}

func (m *malgoSink) drain(ctx context.Context) error {
	// Poll until the send channel and pending tail are both empty. The audio
	// callback does not signal directly when it finishes a chunk, so a short
	// sleep between checks is the simplest correct approach.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		m.mu.Lock()
		pendingEmpty := len(m.pending) == 0
		m.mu.Unlock()
		if len(m.ch) == 0 && pendingEmpty {
			// All samples handed to hardware; wait one extra audio frame for hardware to finish.
			time.Sleep(20 * time.Millisecond)
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *malgoSink) close() error {
	if m.dev == nil {
		return nil
	}
	_ = m.dev.Stop()
	m.dev.Uninit()
	m.dev = nil
	close(m.ch)
	return nil
}
