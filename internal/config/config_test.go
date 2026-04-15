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
	if conf.Freeman.Hotkey.Mode != "tty" {
		t.Errorf("default hotkey mode = %q, want tty", conf.Freeman.Hotkey.Mode)
	}
	if conf.Freeman.Hotkey.Key != "enter" {
		t.Errorf("default hotkey key = %q, want enter", conf.Freeman.Hotkey.Key)
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
  hotkey:
    mode: global
    key: ctrl+space
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
	if conf.Freeman.Hotkey.Mode != "global" {
		t.Errorf("hotkey.mode = %q", conf.Freeman.Hotkey.Mode)
	}
	if conf.Freeman.Hotkey.Key != "ctrl+space" {
		t.Errorf("hotkey.key = %q", conf.Freeman.Hotkey.Key)
	}
}

func TestLoadConfig_Freeman_Plan2Defaults(t *testing.T) {
	conf := LoadConfig("/nonexistent/path.yaml")

	if conf.Freeman.Audio.InputDevice != "" {
		t.Errorf("default Audio.InputDevice = %q, want empty", conf.Freeman.Audio.InputDevice)
	}
	if conf.Freeman.Audio.OutputDevice != "" {
		t.Errorf("default Audio.OutputDevice = %q, want empty", conf.Freeman.Audio.OutputDevice)
	}
	if conf.Freeman.STT.ServerPath != "" {
		t.Errorf("default STT.ServerPath = %q, want empty", conf.Freeman.STT.ServerPath)
	}
	if conf.Freeman.STT.ServerPort != 0 {
		t.Errorf("default STT.ServerPort = %d, want 0", conf.Freeman.STT.ServerPort)
	}
	if conf.Freeman.STT.ModelPath != "./models/whisper/ggml-large-v3-turbo.bin" {
		t.Errorf("default STT.ModelPath = %q", conf.Freeman.STT.ModelPath)
	}
	if conf.Freeman.STT.StartupTimeoutMS != 10000 {
		t.Errorf("default STT.StartupTimeoutMS = %d, want 10000", conf.Freeman.STT.StartupTimeoutMS)
	}
	if conf.Freeman.STT.VAD.SilenceMS != 800 {
		t.Errorf("default VAD.SilenceMS = %d, want 800", conf.Freeman.STT.VAD.SilenceMS)
	}
	if conf.Freeman.STT.VAD.MinSpeechMS != 300 {
		t.Errorf("default VAD.MinSpeechMS = %d, want 300", conf.Freeman.STT.VAD.MinSpeechMS)
	}
	if conf.Freeman.STT.VAD.HangoverMS != 500 {
		t.Errorf("default VAD.HangoverMS = %d, want 500", conf.Freeman.STT.VAD.HangoverMS)
	}
	if conf.Freeman.STT.VAD.Aggressiveness != 2 {
		t.Errorf("default VAD.Aggressiveness = %d, want 2", conf.Freeman.STT.VAD.Aggressiveness)
	}
	if conf.Freeman.Hotkey.Mode != "tty" {
		t.Errorf("default Hotkey.Mode = %q, want tty", conf.Freeman.Hotkey.Mode)
	}
	if conf.Freeman.Hotkey.Key != "enter" {
		t.Errorf("default Hotkey.Key = %q, want enter", conf.Freeman.Hotkey.Key)
	}
}

func TestLoadConfig_Freeman_Plan2YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
freeman:
  audio:
    input_device: "MacBook Microphone"
    output_device: "External Speakers"
  stt:
    server_path: /opt/whisper/whisper-server
    server_port: 17100
    model_path: /models/ggml.bin
    startup_timeout_ms: 20000
    vad:
      silence_ms: 500
      min_speech_ms: 250
      hangover_ms: 300
      aggressiveness: 3
  hotkey:
    mode: stdin-line
    key: space
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	conf := LoadConfig(path)
	if conf.Freeman.Audio.InputDevice != "MacBook Microphone" {
		t.Errorf("Audio.InputDevice = %q", conf.Freeman.Audio.InputDevice)
	}
	if conf.Freeman.STT.ServerPort != 17100 {
		t.Errorf("STT.ServerPort = %d", conf.Freeman.STT.ServerPort)
	}
	if conf.Freeman.STT.VAD.Aggressiveness != 3 {
		t.Errorf("VAD.Aggressiveness = %d", conf.Freeman.STT.VAD.Aggressiveness)
	}
	if conf.Freeman.Hotkey.Mode != "stdin-line" {
		t.Errorf("Hotkey.Mode = %q", conf.Freeman.Hotkey.Mode)
	}
	if conf.Freeman.Hotkey.Key != "space" {
		t.Errorf("Hotkey.Key = %q", conf.Freeman.Hotkey.Key)
	}
	if conf.Freeman.Audio.OutputDevice != "External Speakers" {
		t.Errorf("Audio.OutputDevice = %q", conf.Freeman.Audio.OutputDevice)
	}
	if conf.Freeman.STT.ServerPath != "/opt/whisper/whisper-server" {
		t.Errorf("STT.ServerPath = %q", conf.Freeman.STT.ServerPath)
	}
	if conf.Freeman.STT.ModelPath != "/models/ggml.bin" {
		t.Errorf("STT.ModelPath = %q", conf.Freeman.STT.ModelPath)
	}
	if conf.Freeman.STT.StartupTimeoutMS != 20000 {
		t.Errorf("STT.StartupTimeoutMS = %d", conf.Freeman.STT.StartupTimeoutMS)
	}
	if conf.Freeman.STT.VAD.SilenceMS != 500 {
		t.Errorf("VAD.SilenceMS = %d", conf.Freeman.STT.VAD.SilenceMS)
	}
	if conf.Freeman.STT.VAD.MinSpeechMS != 250 {
		t.Errorf("VAD.MinSpeechMS = %d", conf.Freeman.STT.VAD.MinSpeechMS)
	}
	if conf.Freeman.STT.VAD.HangoverMS != 300 {
		t.Errorf("VAD.HangoverMS = %d", conf.Freeman.STT.VAD.HangoverMS)
	}
}
