package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Freeman_Defaults(t *testing.T) {
	conf := LoadConfig("/nonexistent/path.yaml")
	if conf.Freeman.PM.Model != "claude-haiku-4-5" {
		t.Errorf("default PM model = %q, want claude-haiku-4-5", conf.Freeman.PM.Model)
	}
	if conf.Freeman.PM.ConfidenceThreshold != 0.8 {
		t.Errorf("default confidence = %v, want 0.8", conf.Freeman.PM.ConfidenceThreshold)
	}
	if conf.Freeman.Worker.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("default worker model = %q", conf.Freeman.Worker.DefaultModel)
	}
	if conf.Freeman.Hotkey != "option+space" {
		t.Errorf("default hotkey = %q", conf.Freeman.Hotkey)
	}
}

func TestLoadConfig_Freeman_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
freeman:
  pm:
    model: custom-pm
    confidence_threshold: 0.5
    api_key_env: MY_KEY
  worker:
    default_model: custom-sonnet
    opus_model: custom-opus
    auth: api_key
  stt:
    model: whisper-tiny
    model_path: ./m.bin
    vad:
      silence_ms: 500
      min_speech_ms: 200
  hotkey: ctrl+space
  logging:
    transcript_dir: ./t
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := LoadConfig(path)
	if conf.Freeman.PM.Model != "custom-pm" {
		t.Errorf("pm.model = %q", conf.Freeman.PM.Model)
	}
	if conf.Freeman.Worker.OpusModel != "custom-opus" {
		t.Errorf("worker.opus = %q", conf.Freeman.Worker.OpusModel)
	}
	if conf.Freeman.STT.VAD.SilenceMS != 500 {
		t.Errorf("vad.silence = %d", conf.Freeman.STT.VAD.SilenceMS)
	}
	if conf.Freeman.Hotkey != "ctrl+space" {
		t.Errorf("hotkey = %q", conf.Freeman.Hotkey)
	}
}
