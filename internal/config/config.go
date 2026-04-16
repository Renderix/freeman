package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`

	Model struct {
		Dir        string `yaml:"dir"`
		ModelFile  string `yaml:"model_file"`
		VoicesFile string `yaml:"voices_file"`
		TokensFile string `yaml:"tokens_file"`
		DataDir    string `yaml:"data_dir"`
	} `yaml:"model"`

	TTS struct {
		DefaultVoice              string  `yaml:"default_voice"`
		DefaultSpeed              float64 `yaml:"default_speed"`
		MaxSentenceChars          int     `yaml:"max_sentence_chars"`
		PartialSentenceTimeoutSec float64 `yaml:"partial_sentence_timeout_sec"`
	} `yaml:"tts"`

	Freeman FreemanConfig `yaml:"freeman"`
	Persona PersonaConfig `yaml:"persona"`
}

// AudioConfig selects capture and playback devices.
type AudioConfig struct {
	InputDevice  string `yaml:"input_device"`
	OutputDevice string `yaml:"output_device"`
}

type FreemanConfig struct {
	PM      PMConfig      `yaml:"pm"`
	Worker  WorkerConfig  `yaml:"worker"`
	Audio   AudioConfig   `yaml:"audio"`
	STT     STTConfig     `yaml:"stt"`
	Logging LoggingConfig `yaml:"logging"`
}

type PMConfig struct {
	Model               string  `yaml:"model"`
	ConfidenceThreshold float64 `yaml:"confidence_threshold"`
	APIKeyEnv           string  `yaml:"api_key_env"`
}

type WorkerConfig struct {
	DefaultModel string `yaml:"default_model"`
	OpusModel    string `yaml:"opus_model"`
	Auth         string `yaml:"auth"`
}

type STTConfig struct {
	Model            string    `yaml:"model"`
	ModelPath        string    `yaml:"model_path"`
	ServerPath       string    `yaml:"server_path"`
	ServerPort       int       `yaml:"server_port"`
	StartupTimeoutMS int       `yaml:"startup_timeout_ms"`
	VAD              VADConfig `yaml:"vad"`
}

type VADConfig struct {
	SilenceMS      int `yaml:"silence_ms"`
	MinSpeechMS    int `yaml:"min_speech_ms"`
	HangoverMS     int `yaml:"hangover_ms"`
	Aggressiveness int `yaml:"aggressiveness"`
}

type LoggingConfig struct {
	TranscriptDir string `yaml:"transcript_dir"`
}

type WakewordKeywordConfig struct {
	Model     string  `yaml:"model"`
	Threshold float32 `yaml:"threshold"`
}

type WakewordKeywordsConfig struct {
	Wake WakewordKeywordConfig `yaml:"wake"`
	Mute WakewordKeywordConfig `yaml:"mute"`
	Stop WakewordKeywordConfig `yaml:"stop"`
}

type WakewordConfig struct {
	ModelsDir      string                 `yaml:"models_dir"`
	Melspectrogram string                 `yaml:"melspectrogram"`
	Embedding      string                 `yaml:"embedding"`
	Keywords       WakewordKeywordsConfig `yaml:"keywords"`
}

type PersonaConfig struct {
	Name     string         `yaml:"name"`
	Greeting string         `yaml:"greeting"`
	Traits   []string       `yaml:"traits"`
	Rules    []string       `yaml:"rules"`
	Wakeword WakewordConfig `yaml:"wakeword"`
}

var DefaultConfig = Config{
	Server: struct {
		Port int `yaml:"port"`
	}{
		Port: 17000,
	},
	Model: struct {
		Dir        string `yaml:"dir"`
		ModelFile  string `yaml:"model_file"`
		VoicesFile string `yaml:"voices_file"`
		TokensFile string `yaml:"tokens_file"`
		DataDir    string `yaml:"data_dir"`
	}{
		Dir:        "./models",
		ModelFile:  "model.onnx",
		VoicesFile: "voices.bin",
		TokensFile: "tokens.txt",
		DataDir:    "espeak-ng-data",
	},
	TTS: struct {
		DefaultVoice              string  `yaml:"default_voice"`
		DefaultSpeed              float64 `yaml:"default_speed"`
		MaxSentenceChars          int     `yaml:"max_sentence_chars"`
		PartialSentenceTimeoutSec float64 `yaml:"partial_sentence_timeout_sec"`
	}{
		DefaultVoice:              "af_heart",
		DefaultSpeed:              1.0,
		MaxSentenceChars:          150,
		PartialSentenceTimeoutSec: 2.0,
	},
	Freeman: FreemanConfig{
		PM: PMConfig{
			Model:               "claude-haiku-4-5",
			ConfidenceThreshold: 0.8,
			APIKeyEnv:           "ANTHROPIC_API_KEY",
		},
		Worker: WorkerConfig{
			DefaultModel: "claude-sonnet-4-6",
			OpusModel:    "claude-opus-4-6",
			Auth:         "subscription",
		},
		Audio: AudioConfig{
			InputDevice:  "",
			OutputDevice: "",
		},
		STT: STTConfig{
			Model:            "whisper-large-v3-turbo",
			ModelPath:        "./models/whisper/ggml-large-v3-turbo.bin",
			StartupTimeoutMS: 10000,
			VAD: VADConfig{
				SilenceMS:      800,
				MinSpeechMS:    300,
				HangoverMS:     500,
				Aggressiveness: 2,
			},
		},
		Logging: LoggingConfig{
			TranscriptDir: "./.freeman/transcripts",
		},
	},
}

// LoadConfig loads configuration from config.yaml or returns defaults.
func LoadConfig(configPath string) Config {
	if configPath == "" {
		configPath = "config.yaml"
	}

	file, err := os.ReadFile(configPath)
	if err != nil {
		return DefaultConfig
	}

	conf := DefaultConfig
	if err := yaml.Unmarshal(file, &conf); err != nil {
		return DefaultConfig
	}

	return conf
}
