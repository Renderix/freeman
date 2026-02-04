import io
import warnings
from typing import Dict, Optional

import numpy as np
import soundfile as sf
import torch
from kokoro import KPipeline

# Filter warnings
warnings.filterwarnings("ignore")


class TTSEngine:
    """
    TTS Engine using Kokoro-82M model.
    Optimized for MPS (Metal Performance Shaders) on M1/M2/M3 Macs.
    """

    VOICES = {
        # American English
        "af_heart": "American Female - Heart", "af_alloy": "American Female - Alloy",
        "af_aoede": "American Female - Aoede", "af_bella": "American Female - Bella",
        "af_jessica": "American Female - Jessica", "af_kore": "American Female - Kore",
        "af_nicole": "American Female - Nicole", "af_nova": "American Female - Nova",
        "af_river": "American Female - River", "af_sarah": "American Female - Sarah",
        "af_sky": "American Female - Sky",
        "am_adam": "American Male - Adam", "am_echo": "American Male - Echo",
        "am_eric": "American Male - Eric", "am_fenrir": "American Male - Fenrir",
        "am_liam": "American Male - Liam", "am_michael": "American Male - Michael",
        "am_onyx": "American Male - Onyx", "am_puck": "American Male - Puck",
        # British English
        "bf_alice": "British Female - Alice", "bf_emma": "British Female - Emma",
        "bf_isabella": "British Female - Isabella", "bf_lily": "British Female - Lily",
        "bm_daniel": "British Male - Daniel", "bm_fable": "British Male - Fable",
        "bm_george": "British Male - George", "bm_lewis": "British Male - Lewis",
    }

    def __init__(self, lang_code: str = 'a'):
        self.lang_code = lang_code
        self.device = self._get_device()
        self.pipeline = KPipeline(lang_code=lang_code, device=self.device)
        self._warmup()

    def _get_device(self) -> torch.device:
        if torch.backends.mps.is_available():
            return torch.device("mps")
        return torch.device("cpu")

    def _warmup(self):
        """Warm up the model with a tiny inference."""
        list(self.pipeline("Warmup", voice='af_heart'))

    def generate(self, text: str, voice: str, speed: float = 1.0) -> Optional[bytes]:
        """Synchronous generation to be run in executor. Returns None on failure."""
        try:
            generator = self.pipeline(text, voice=voice, speed=speed)
            audio_chunks = []
            for _, _, audio in generator:
                audio_chunks.append(audio)

            if not audio_chunks:
                return None

            combined = np.concatenate(audio_chunks)
            return self._to_wav_bytes(combined)
        except Exception:
            return None

    def _to_wav_bytes(self, audio: np.ndarray, sample_rate: int = 24000) -> bytes:
        buffer = io.BytesIO()
        sf.write(buffer, audio, sample_rate, format='WAV', subtype='PCM_16')
        buffer.seek(0)
        return buffer.read()

    @classmethod
    def get_voices(cls) -> Dict[str, str]:
        return cls.VOICES
