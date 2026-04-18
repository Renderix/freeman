//go:build audio_live

package playback

import (
	"context"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio"
)

// staticSynth returns a 200 ms 440 Hz sine at 24 kHz.
type staticSynth struct{}

func (staticSynth) GeneratePCM(_, _ string, _ float64) ([]int16, int, error) {
	const sr = 24000
	const dur = 0.2
	n := int(sr * dur)
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		// very simple sine approximation; integer triangle is fine
		out[i] = int16((i % 100) * 300)
	}
	return out, sr, nil
}

func TestSpeaker_Live(t *testing.T) {
	actx, err := audio.New(nil)
	if err != nil {
		t.Skipf("audio context unavailable: %v", err)
	}
	defer actx.Close()

	sp, err := Open(actx, Config{Voice: "af_heart", Speed: 1.0}, staticSynth{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sp.Speak(ctx, "test"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if err := sp.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}
