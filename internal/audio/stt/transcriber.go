package stt

import (
	"context"
	"strings"
	"sync"

	"github.com/Renderix/freeman/internal/audio/vad"
)

// Transcriber consumes utterance PCM from a VAD channel, POSTs each to a
// whisper-server via Client, and emits non-empty text on Utterances(). It also
// implements audio.Muter: while muted, results are dropped silently.
type Transcriber struct {
	client     *Client
	in         <-chan vad.Utterance
	out        chan string
	sampleRate int

	mu    sync.Mutex
	muted bool
}

func NewTranscriber(c *Client, in <-chan vad.Utterance, sampleRate int) *Transcriber {
	return &Transcriber{
		client:     c,
		in:         in,
		out:        make(chan string, 4),
		sampleRate: sampleRate,
	}
}

// Run starts the background goroutine that drives transcription until ctx ends
// or the input channel closes.
func (t *Transcriber) Run(ctx context.Context) {
	go func() {
		defer close(t.out)
		for {
			select {
			case <-ctx.Done():
				return
			case u, ok := <-t.in:
				if !ok {
					return
				}
				wav := EncodeWAV(u.PCM, t.sampleRate)
				text, err := t.client.Transcribe(ctx, wav)
				if err != nil {
					// Per the spec, whisper errors are logged and the Session
					// simply never sees an utterance for this VAD segment. It
					// is Plan 3's job to surface a spoken "transcriber error"
					// message via a diagnostics channel.
					continue
				}
				text = strings.TrimSpace(text)
				if text == "" {
					continue
				}
				if t.isMuted() {
					continue
				}
				select {
				case t.out <- text:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

// Utterances implements call.Transcriber.
func (t *Transcriber) Utterances() <-chan string { return t.out }

// Stop is a no-op — the Run goroutine exits when ctx is canceled or in closes.
// Kept to satisfy call.Transcriber.
func (t *Transcriber) Stop() {}

// Mute implements audio.Muter.
func (t *Transcriber) Mute() {
	t.mu.Lock()
	t.muted = true
	t.mu.Unlock()
}

// Unmute implements audio.Muter.
func (t *Transcriber) Unmute() {
	t.mu.Lock()
	t.muted = false
	t.mu.Unlock()
}

func (t *Transcriber) isMuted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.muted
}
