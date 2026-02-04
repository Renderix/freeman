package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// UserSettings represents user preferences stored in ~/.config/freeman/settings.json
type UserSettings struct {
	Voice string  `json:"voice"`
	Speed float64 `json:"speed"`
}

var DefaultUserSettings = UserSettings{
	Voice: "af_heart",
	Speed: 1.0,
}

func getSettingsPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configDir := filepath.Join(homeDir, ".config", "freeman")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(configDir, "settings.json"), nil
}

// LoadUserSettings loads settings from ~/.config/freeman/settings.json
func LoadUserSettings() UserSettings {
	path, err := getSettingsPath()
	if err != nil {
		return DefaultUserSettings
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultUserSettings
	}

	var settings UserSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return DefaultUserSettings
	}

	// Validate
	if settings.Voice == "" {
		settings.Voice = DefaultUserSettings.Voice
	}
	if settings.Speed < 0.5 || settings.Speed > 2.0 {
		settings.Speed = DefaultUserSettings.Speed
	}

	return settings
}

// SaveUserSettings saves settings to ~/.config/freeman/settings.json
func SaveUserSettings(settings UserSettings) error {
	path, err := getSettingsPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
