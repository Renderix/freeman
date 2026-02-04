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

// Voices maps voice IDs to descriptive names.
var Voices = map[string]string{
	"af_heart":    "American Female - Heart",
	"af_alloy":    "American Female - Alloy",
	"af_aoede":    "American Female - Aoede",
	"af_bella":    "American Female - Bella",
	"af_jessica":  "American Female - Jessica",
	"af_kore":     "American Female - Kore",
	"af_nicole":   "American Female - Nicole",
	"af_nova":     "American Female - Nova",
	"af_river":    "American Female - River",
	"af_sarah":    "American Female - Sarah",
	"af_sky":      "American Female - Sky",
	"am_adam":     "American Male - Adam",
	"am_echo":     "American Male - Echo",
	"am_eric":     "American Male - Eric",
	"am_fenrir":   "American Male - Fenrir",
	"am_liam":     "American Male - Liam",
	"am_michael":  "American Male - Michael",
	"am_onyx":     "American Male - Onyx",
	"am_puck":     "American Male - Puck",
	"bf_alice":    "British Female - Alice",
	"bf_emma":     "British Female - Emma",
	"bf_isabella": "British Female - Isabella",
	"bf_lily":     "British Female - Lily",
	"bm_daniel":   "British Male - Daniel",
	"bm_fable":    "British Male - Fable",
	"bm_george":   "British Male - George",
	"bm_lewis":    "British Male - Lewis",
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
		NumThreads: 4,
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
	// Find speaker ID for the voice
	// Note: Sherpa-ONNX Kokoro uses speaker IDs.
	// The mapping depends on how the model was exported.
	// For now we'll assume the user provides a speaker ID or we map it.
	// In the Python version, voices are filenames.

	// TODO: Map string voice to speaker ID if needed.
	// Most sherpa-onnx Kokoro models use speaker IDs 0-N.
	// We'll use 0 as default for now if it's a simple setup.
	speakerID := 0

	audio := e.tts.Generate(text, speakerID, float32(speed))
	if audio.Samples == nil {
		return nil, fmt.Errorf("failed to generate audio")
	}

	return Float32ToWav(audio.Samples, audio.SampleRate), nil
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
