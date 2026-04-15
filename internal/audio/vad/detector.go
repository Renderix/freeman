package vad

import (
	"fmt"

	webrtcvad "github.com/maxhawkins/go-webrtcvad"
)

// Detector classifies a single PCM frame as speech or non-speech.
// Extracted to an interface so vad.Run is unit-testable without the CGO binding.
type Detector interface {
	IsSpeech(frame []int16, sampleRate int) (bool, error)
}

// webrtcDetector wraps maxhawkins/go-webrtcvad.
type webrtcDetector struct {
	v *webrtcvad.VAD
}

// NewWebRTCDetector returns a Detector backed by the WebRTC VAD, with the
// given aggressiveness (0 least, 3 most).
func NewWebRTCDetector(aggressiveness int) (Detector, error) {
	v, err := webrtcvad.New()
	if err != nil {
		return nil, fmt.Errorf("webrtcvad new: %w", err)
	}
	if err := v.SetMode(aggressiveness); err != nil {
		return nil, fmt.Errorf("webrtcvad set mode %d: %w", aggressiveness, err)
	}
	return &webrtcDetector{v: v}, nil
}

func (d *webrtcDetector) IsSpeech(frame []int16, sampleRate int) (bool, error) {
	// go-webrtcvad's Process takes []byte little-endian PCM16.
	b := make([]byte, len(frame)*2)
	for i, s := range frame {
		u := uint16(s)
		b[i*2] = byte(u)
		b[i*2+1] = byte(u >> 8)
	}
	return d.v.Process(sampleRate, b)
}
