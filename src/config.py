import os
import json
from pathlib import Path

DEFAULT_CONFIG = {
    "voice": "af_heart",
    "speed": 1.0,
    "max_sentence_duration_sec": 10.0,
    "partial_sentence_timeout_sec": 2.0,
    "sample_rate": 24000
}

class Config:
    def __init__(self):
        self.config_dir = Path.home() / ".config" / "freeman"
        self.config_path = self.config_dir / "config.json"
        self.settings = DEFAULT_CONFIG.copy()
        self.load()

    def load(self):
        if self.config_path.exists():
            try:
                with open(self.config_path, "r") as f:
                    user_settings = json.load(f)
                    self.settings.update(user_settings)
            except Exception as e:
                print(f"Error loading config: {e}")

    def save(self):
        self.config_dir.mkdir(parents=True, exist_ok=True)
        try:
            with open(self.config_path, "w") as f:
                json.dump(self.settings, f, indent=2)
        except Exception as e:
            print(f"Error saving config: {e}")

    def get(self, key, default=None):
        return self.settings.get(key, default)

    def set(self, key, value):
        self.settings[key] = value
        self.save()

config = Config()
