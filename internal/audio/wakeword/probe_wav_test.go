//go:build wakeword_probe

package wakeword

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
)

// TestProbeWav feeds a WAV file through the detector one 1280-sample chunk
// at a time, logging every per-keyword score. Guarded by the
// wakeword_probe build tag so it only runs when you want to compare
// against the Python reference on the same audio. Usage:
//
//	WAV=/abs/path/to.wav KW=0 go test -tags wakeword_probe -run TestProbeWav ./internal/audio/wakeword/ -v
//
// KW selects which keyword to score (0=wake, 1=mute, 2=stop). Expects the
// model files to live at models/wakeword/{melspectrogram,embedding_model,horus,standby,disengage}.onnx
// relative to the repo root. The test resolves paths via the TEST_CWD env
// var (set it to the repo root) or falls back to the current working dir.
func TestProbeWav(t *testing.T) {
	wavPath := os.Getenv("WAV")
	if wavPath == "" {
		t.Skip("set WAV=/path/to.wav")
	}
	kwIdx := os.Getenv("KW")
	if kwIdx == "" {
		kwIdx = "0"
	}
	repoRoot := os.Getenv("TEST_CWD")
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}

	samples, err := readWav16kMono(wavPath)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	t.Logf("wav samples=%d duration=%.2fs", len(samples), float64(len(samples))/16000)

	d, err := NewDetector(Config{
		ModelsDir:    repoRoot + "/models/wakeword",
		MelModelFile: "melspectrogram.onnx",
		EmbModelFile: "embedding_model.onnx",
		Keywords: [3]KeywordConfig{
			{ModelPath: "horus.onnx", Threshold: 0.5},
			{ModelPath: "standby.onnx", Threshold: 0.5},
			{ModelPath: "disengage.onnx", Threshold: 0.5},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	defer d.Stop()

	// Feed the wav one 1280-sample chunk at a time, exactly as Python's
	// reference does, and capture per-chunk scores by instrumenting the
	// detector state directly (we can't use Events() because the channel
	// only receives when a threshold is crossed).
	for i := 0; i+chunkSize <= len(samples); i += chunkSize {
		d.processChunk(int16ToFloat32(samples[i : i+chunkSize]))
		// Run every keyword's classifier manually so we see scores below
		// the detection threshold, just like Python's predict() output.
		if d.embBuffer.len()/embSize >= kwEmbCount {
			embData := d.embBuffer.lastN(kwEmbCount * embSize)
			for k := 0; k < 3; k++ {
				copy(d.kwInputTensors[k].GetData(), embData)
				if err := d.kwSessions[k].Run(); err != nil {
					t.Fatalf("kw run: %v", err)
				}
				score := d.kwOutputTensors[k].GetData()[0]
				if score > 0.05 {
					t.Logf("chunk %3d t=%.2fs keyword=%s score=%.4f",
						i/chunkSize, float64(i)/16000.0, KeywordKind(k).String(), score)
				}
			}
		}
	}
}

// readWav16kMono is a minimal WAV loader: PCM int16, 16 kHz, mono. Panics
// on anything else so mismatches show up loudly in the probe.
func readWav16kMono(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var header [44]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, err
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}
	channels := binary.LittleEndian.Uint16(header[22:24])
	sampleRate := binary.LittleEndian.Uint32(header[24:28])
	bitsPerSample := binary.LittleEndian.Uint16(header[34:36])
	if channels != 1 || sampleRate != 16000 || bitsPerSample != 16 {
		return nil, fmt.Errorf("need 16kHz/mono/16-bit, got %dHz/%dch/%dbit",
			sampleRate, channels, bitsPerSample)
	}

	rest, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	out := make([]int16, len(rest)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(rest[2*i:]))
	}
	return out, nil
}
