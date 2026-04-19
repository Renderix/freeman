package engine

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)


// TTSEngine wrapper for sherpa-onnx Kokoro TTS.
type TTSEngine struct {
	tts *sherpa_onnx.OfflineTts
}

// Voices lists the speakers available in the bundled Kokoro voices.bin.
// The project currently ships with kokoro-en-v0_19, whose speaker IDs are
// fixed and listed here alongside short descriptions. If you swap in a
// different Kokoro build, update this map to match the new speaker order.
var Voices = map[string]string{
	"af":          "American Female - Default (Bella+Sarah mix)",
	"af_bella":    "American Female - Bella",
	"af_nicole":   "American Female - Nicole",
	"af_sarah":    "American Female - Sarah",
	"af_sky":      "American Female - Sky",
	"am_adam":     "American Male - Adam",
	"am_michael":  "American Male - Michael",
	"bf_emma":     "British Female - Emma",
	"bf_isabella": "British Female - Isabella",
	"bm_george":   "British Male - George",
	"bm_lewis":    "British Male - Lewis",
}

// voiceSpeakerIDs is the definitive voice-name → speaker-ID table for the
// bundled kokoro-en-v0_19 model. Order matters and is set by sherpa-onnx's
// voices.bin; do not sort.
var voiceSpeakerIDs = map[string]int{
	"af":          0,
	"af_bella":    1,
	"af_nicole":   2,
	"af_sarah":    3,
	"af_sky":      4,
	"am_adam":     5,
	"am_michael":  6,
	"bf_emma":     7,
	"bf_isabella": 8,
	"bm_george":   9,
	"bm_lewis":    10,
}

// speakerIDForVoice returns the speaker ID for a named voice, or 0 if
// the name is unknown (falls back to the first voice).
func speakerIDForVoice(voice string) int {
	if id, ok := voiceSpeakerIDs[voice]; ok {
		return id
	}
	return 0
}

// NewTTSEngine initializes the Sherpa-ONNX TTS engine.
func NewTTSEngine(modelPath, voicesPath, tokensPath, dataDir string) (*TTSEngine, error) {
	config := sherpa_onnx.OfflineTtsModelConfig{
		Kokoro: sherpa_onnx.OfflineTtsKokoroModelConfig{
			Model:       modelPath,
			Voices:      voicesPath,
			Tokens:      tokensPath,
			DataDir:     dataDir,
			LengthScale: 1.0,
		},
		// Single-thread sherpa-onnx so Kokoro doesn't spawn a thread
		// pool and blow past the whole-process CPU budget. Kokoro is
		// fast enough on M-series single-threaded; higher counts gave
		// <10% speedup at real-time load levels in testing.
		NumThreads: 1,
		Debug:      0,
	}

	ttsConfig := sherpa_onnx.OfflineTtsConfig{
		Model: config,
	}

	tts := sherpa_onnx.NewOfflineTts(&ttsConfig)
	if tts == nil {
		return nil, fmt.Errorf("failed to create offline tts")
	}

	return &TTSEngine{tts: tts}, nil
}

// Generate creates audio bytes for the given text and voice.
func (e *TTSEngine) Generate(text, voice string, speed float64) ([]byte, error) {
	speakerID := speakerIDForVoice(voice)

	audio := e.tts.Generate(text, speakerID, float32(speed))
	if audio.Samples == nil {
		return nil, fmt.Errorf("failed to generate audio")
	}

	return Float32ToWav(audio.Samples, audio.SampleRate), nil
}

// GeneratePCM is the hot path for local playback: runs Kokoro and returns raw
// int16 samples plus the engine's native sample rate, skipping the WAV header.
// Plan 2's playback.Speaker drives these samples straight into malgo.
//
// Semantics match Generate: the voice name is mapped to a Kokoro speaker
// ID via the alphabetical order of voices in voices.bin. Unknown voice
// names fall back to speaker ID 0.
func (e *TTSEngine) GeneratePCM(text, voice string, speed float64) ([]int16, int, error) {
	if e == nil || e.tts == nil {
		return nil, 0, fmt.Errorf("engine not initialized")
	}
	speakerID := speakerIDForVoice(voice)

	audio := e.tts.Generate(text, speakerID, float32(speed))
	if audio == nil || audio.Samples == nil || len(audio.Samples) == 0 {
		return nil, 0, fmt.Errorf("empty audio for %q", text)
	}
	pcm := float32ToPCM(audio.Samples)
	return pcm, audio.SampleRate, nil
}

// float32ToPCM converts normalized float32 audio samples to int16 PCM.
// Values outside [-1.0, 1.0] are clamped.
func float32ToPCM(samples []float32) []int16 {
	pcm := make([]int16, len(samples))
	for i, f := range samples {
		v := f * 32767.0
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		pcm[i] = int16(v)
	}
	return pcm
}

// Float32ToWav converts float32 samples to 16-bit PCM WAV bytes.
func Float32ToWav(samples []float32, sampleRate int) []byte {
	buf := new(bytes.Buffer)

	// WAV Header
	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+len(samples)*2))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(buf, binary.LittleEndian, uint16(1)) // Mono
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(buf, binary.LittleEndian, uint16(2)) // Block align
	binary.Write(buf, binary.LittleEndian, uint16(16))

	// data chunk
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(len(samples)*2))

	// Write samples
	for _, s := range samples {
		// Clamp to [-1.0, 1.0]
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		binary.Write(buf, binary.LittleEndian, int16(s*32767.0))
	}

	return buf.Bytes()
}

// GetVoices returns available voices.
func GetVoices() map[string]string {
	return Voices
}
