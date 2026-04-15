// Package audio owns the miniaudio (malgo) context that capture and playback
// sub-packages share, and defines the cross-cutting Muter interface Speaker
// uses to suppress self-echo during TTS playback.
package audio

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/gen2brain/malgo"
)

// Context wraps the malgo AllocatedContext. One instance per process.
type Context struct {
	raw *malgo.AllocatedContext
	log *slog.Logger
}

// New initializes the backend. Pass nil for log to get a discard logger.
func New(log *slog.Logger) (*Context, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(msg string) {
		log.Debug("malgo", "msg", msg)
	})
	if err != nil {
		return nil, fmt.Errorf("malgo init: %w", err)
	}
	log.Info("audio: context ready")
	return &Context{raw: ctx, log: log}, nil
}

// Raw returns the underlying malgo context for sub-packages that need to open
// devices. Callers must not free or reinitialize it.
func (c *Context) Raw() *malgo.AllocatedContext {
	return c.raw
}

// Log returns the slog logger, for sub-packages that want to emit structured
// audio events without importing slog directly.
func (c *Context) Log() *slog.Logger {
	return c.log
}

// Close tears down the context. Safe to call multiple times.
func (c *Context) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}
	err := c.raw.Uninit()
	c.raw.Free()
	c.raw = nil
	if err != nil {
		return fmt.Errorf("malgo uninit: %w", err)
	}
	return nil
}

// Muter is implemented by components that can temporarily drop transcribed
// audio. Speaker invokes Mute() before TTS playback and Unmute() after, to
// prevent Kokoro's own voice from echoing back through the mic and turning
// into a spurious user utterance.
//
// Mute/Unmute must be safe to call concurrently and must be idempotent: two
// Mutes without an intervening Unmute leave the muter muted; two Unmutes with
// no intervening Mute leave it unmuted.
type Muter interface {
	Mute()
	Unmute()
}

// NoopMuter is a test helper and a safe default when no transcriber is wired.
type NoopMuter struct {
	mu    sync.Mutex
	muted bool
}

func (n *NoopMuter) Mute() {
	n.mu.Lock()
	n.muted = true
	n.mu.Unlock()
}

func (n *NoopMuter) Unmute() {
	n.mu.Lock()
	n.muted = false
	n.mu.Unlock()
}

// IsMuted is test-only inspection.
func (n *NoopMuter) IsMuted() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.muted
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
