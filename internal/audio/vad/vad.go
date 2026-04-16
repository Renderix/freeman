package vad

import (
	"context"
	"sync"
)

// Utterance is a completed user speech segment with end-of-speech already detected.
type Utterance struct {
	PCM        []int16
	DurationMs int
}

// Config tunes the endpointing state machine.
type Config struct {
	SilenceMs      int // end-of-speech trigger; default 800
	MinSpeechMs    int // drop segments shorter than this; default 300
	HangoverMs     int // keep classifying as speech for this long after last speech frame; default 500
	Aggressiveness int // webrtcvad 0-3; default 2
	SampleRate     int // e.g. 16000
	FrameMs        int // e.g. 20
}

func (c Config) framesFor(ms int) int {
	if c.FrameMs == 0 {
		return 0
	}
	return ms / c.FrameMs
}

// VAD owns the detector and the endpointing SM.
type VAD struct {
	cfg    Config
	det    Detector
	onsets chan struct{}

	mu    sync.Mutex
	muted bool
}

// New returns a VAD backed by the WebRTC detector.
func New(cfg Config) (*VAD, error) {
	d, err := NewWebRTCDetector(cfg.Aggressiveness)
	if err != nil {
		return nil, err
	}
	return NewWithDetector(cfg, d), nil
}

// NewWithDetector lets tests inject a fake classifier.
func NewWithDetector(cfg Config, d Detector) *VAD {
	return &VAD{cfg: cfg, det: d, onsets: make(chan struct{}, 1)}
}

// SpeechOnsets returns a channel that receives a value on every
// stateSilent → stateSpeech transition. The channel has capacity 1;
// extra onsets are dropped rather than blocking the VAD goroutine.
func (v *VAD) SpeechOnsets() <-chan struct{} { return v.onsets }

// Mute implements audio.Muter. While muted, incoming frames are
// dropped, any in-flight speech buffer is discarded, and no onsets
// fire. Used by the Speaker to suppress self-echo during playback.
func (v *VAD) Mute() {
	v.mu.Lock()
	v.muted = true
	v.mu.Unlock()
}

// Unmute implements audio.Muter.
func (v *VAD) Unmute() {
	v.mu.Lock()
	v.muted = false
	v.mu.Unlock()
}

func (v *VAD) isMuted() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.muted
}

// Run consumes 20 ms frames from `in` and emits completed utterances to the
// returned channel. The channel closes when `in` closes or ctx is canceled.
func (v *VAD) Run(ctx context.Context, in <-chan []int16) <-chan Utterance {
	out := make(chan Utterance, 4)
	go func() {
		defer close(out)

		state := stateSilent
		silenceFrames := 0
		var buf []int16

		silenceLimit := v.cfg.framesFor(v.cfg.SilenceMs)
		minSpeech := v.cfg.framesFor(v.cfg.MinSpeechMs)
		bufFrames := 0

		flush := func() {
			if bufFrames >= minSpeech && len(buf) > 0 {
				out <- Utterance{
					PCM:        buf,
					DurationMs: bufFrames * v.cfg.FrameMs,
				}
			}
			buf = nil
			bufFrames = 0
			silenceFrames = 0
			state = stateSilent
		}

		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-in:
				if !ok {
					flush()
					return
				}
				// While muted, drop frames and discard any in-flight speech
				// buffer so playback self-echo never leaks into the pipeline.
				if v.isMuted() {
					if state == stateSpeech || bufFrames > 0 {
						buf = nil
						bufFrames = 0
						silenceFrames = 0
						state = stateSilent
					}
					continue
				}
				isSpeech, err := v.det.IsSpeech(frame, v.cfg.SampleRate)
				if err != nil {
					continue
				}
				switch state {
				case stateSilent:
					if isSpeech {
						state = stateSpeech
						// Notify listeners of speech onset; non-blocking to never stall the goroutine.
						select {
						case v.onsets <- struct{}{}:
						default:
						}
						buf = append(buf, frame...)
						bufFrames++
						silenceFrames = 0
					}
				case stateSpeech:
					if isSpeech {
						buf = append(buf, frame...)
						bufFrames++
						silenceFrames = 0
					} else {
						silenceFrames++
						if silenceFrames >= silenceLimit {
							// End of speech: emit only the speech frames (no trailing silence).
							flush()
						}
					}
				}
			}
		}
	}()
	return out
}

type state int

const (
	stateSilent state = iota
	stateSpeech
)
