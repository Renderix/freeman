package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// loadEngineForTest builds a TTSEngine against the repo's models/ directory.
// Skips if models are missing — CI without models should not fail.
func loadEngineForTest(t *testing.T) *TTSEngine {
	t.Helper()
	root := "../.."
	modelDir := filepath.Join(root, "models")
	modelFile := filepath.Join(modelDir, "model.onnx")
	if _, err := os.Stat(modelFile); os.IsNotExist(err) {
		t.Skipf("model file missing at %s; run scripts/setup_go_models.sh", modelFile)
	}
	eng, err := NewTTSEngine(
		filepath.Join(modelDir, "model.onnx"),
		filepath.Join(modelDir, "voices.bin"),
		filepath.Join(modelDir, "tokens.txt"),
		filepath.Join(modelDir, "espeak-ng-data"),
	)
	if err != nil {
		t.Fatalf("NewTTSEngine: %v", err)
	}
	return eng
}

func TestFloat32ToPCM_Clamping(t *testing.T) {
	tests := []struct {
		name  string
		input float32
		want  int16
	}{
		{"zero", 0.0, 0},
		{"positive full-scale", 1.0, 32767},
		{"negative full-scale", -1.0, -32767},
		{"above max", 2.0, 32767},
		{"below min", -2.0, -32768},
		{"half", 0.5, 16383},
		{"negative half", -0.5, -16383},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := float32ToPCM([]float32{tc.input})
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			if got[0] != tc.want {
				t.Errorf("float32ToPCM(%v) = %d, want %d", tc.input, got[0], tc.want)
			}
		})
	}
}

func TestGeneratePCM_NonEmpty(t *testing.T) {
	eng := loadEngineForTest(t)
	samples, sr, err := eng.GeneratePCM("hello", "af_heart", 1.0)
	if err != nil {
		t.Fatalf("GeneratePCM: %v", err)
	}
	if sr <= 0 {
		t.Errorf("sample rate = %d, want > 0", sr)
	}
	if len(samples) == 0 {
		t.Fatal("samples empty")
	}
	// Very loose lower bound — "hello" at any sane sample rate is at least 0.1 s.
	if len(samples) < sr/20 {
		t.Errorf("samples = %d at sr=%d, want at least %d", len(samples), sr, sr/20)
	}
}

func TestGenerate_WAVStillValid(t *testing.T) {
	eng := loadEngineForTest(t)
	wav, err := eng.Generate("test", "af_heart", 1.0)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(wav) < 44 {
		t.Fatalf("wav = %d bytes, want >= 44 (header)", len(wav))
	}
	if !bytes.Equal(wav[0:4], []byte("RIFF")) {
		t.Errorf("missing RIFF header")
	}
	if !bytes.Equal(wav[8:12], []byte("WAVE")) {
		t.Errorf("missing WAVE marker")
	}
}
