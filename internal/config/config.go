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
}

// LoadConfig loads configuration from config.yaml or returns defaults.
func LoadConfig(configPath string) Config {
	if configPath == "" {
		configPath = "config.yaml"
	}

	file, err := os.ReadFile(configPath)
	if err != nil {
		// Fallback to searching in standard locations if needed,
		// but here we just return defaults if local file is missing.
		return DefaultConfig
	}

	conf := DefaultConfig
	if err := yaml.Unmarshal(file, &conf); err != nil {
		return DefaultConfig
	}

	return conf
}
