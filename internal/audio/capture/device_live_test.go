//go:build audio_live

package capture

import (
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio"
)

func TestDevice_Live(t *testing.T) {
	actx, err := audio.New(nil)
	if err != nil {
		t.Skipf("audio context unavailable: %v", err)
	}
	defer actx.Close()

	dev, err := Open(actx, Config{SampleRate: 16000, Channels: 1, FrameMs: 20})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dev.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer dev.Stop()

	timeout := time.After(2 * time.Second)
	count := 0
	for {
		select {
		case <-dev.Frames():
			count++
			if count >= 10 {
				t.Logf("got %d frames", count)
				return
			}
		case <-timeout:
			t.Fatalf("only got %d frames in 2 seconds", count)
		}
	}
}
