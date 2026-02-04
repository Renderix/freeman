import os
import json
from pathlib import Path
from kokoro import KPipeline

def get_config_voice():
    config_path = Path.home() / ".config" / "freeman" / "config.json"
    if config_path.exists():
        try:
            with open(config_path, "r") as f:
                return json.load(f).get("voice", "af_heart")
        except:
            pass
    return "af_heart"

def download_voices(extra_voices=None):
    """
    Pre-downloads the configured voice and optional extras.
    """
    default_voice = get_config_voice()
    voices_to_download = {default_voice}
    
    if extra_voices:
        voices_to_download.update(extra_voices)
        
    print(f"🔄 Preparing to bundle {len(voices_to_download)} voices...")
    print(f"🎯 Primary (Config) Voice: {default_voice}")
    
    pipeline = KPipeline(lang_code='a')
    
    for voice in sorted(list(voices_to_download)):
        print(f"📥 Caching voice: {voice}")
        # Trigger download/cache
        list(pipeline("warmup", voice=voice))
        
    print("✅ Selected voices cached successfully.")

if __name__ == "__main__":
    # By default, just bundle the config voice to keep the binary slim
    download_voices()
