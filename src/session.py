import asyncio
import time
from typing import Optional, Callable, Any, List
from .sentence_buffer import SentenceBuffer


# Error codes per spec §3.3
class ErrorCode:
    INVALID_VOICE = "INVALID_VOICE"
    TTS_ERROR = "TTS_ERROR"
    BUFFER_OVERFLOW = "BUFFER_OVERFLOW"
    TIMEOUT = "TIMEOUT"
    FATAL = "FATAL"


class Session:
    """
    Manages an individual TTS streaming session.
    Orchestrates text buffering, TTS generation, and audio delivery.
    """

    # Audio specs: 24kHz, 16-bit mono = ~48 bytes per ms
    BYTES_PER_MS = 48

    def __init__(
        self,
        session_id: str,
        voice: str,
        speed: float,
        tts_engine: Any,
        send_audio: Callable[[bytes], Any],
        send_json: Callable[[dict], Any]
    ):
        self.id = session_id
        self.voice = voice
        self.speed = speed
        self.tts_engine = tts_engine
        self.send_audio = send_audio
        self.send_json = send_json

        self.buffer = SentenceBuffer()
        self.sentence_count = 0
        self.is_active = True

        # Performance tracking
        self.start_time = time.time()
        self.total_duration_ms = 0

        # Queue tracking for spec-compliant messages
        self.pending_sentences: List[str] = []
        self.sentences_completed = 0

    async def handle_text(self, chunk: str, is_final: bool = False) -> int:
        """Handle incoming text chunk. Returns number of pending sentences."""
        sentences = self.buffer.add_chunk(chunk)

        if is_final:
            final_sentence = self.buffer.flush()
            if final_sentence:
                sentences.append(final_sentence)

        for sentence in sentences:
            await self._process_sentence(sentence)

        return len(self.pending_sentences)

    def _estimate_duration_ms(self, text: str) -> int:
        """Estimate audio duration in ms based on text length and speed."""
        # Rough estimate: ~80ms per character at normal speed
        base_duration = len(text) * 80
        return int(base_duration / self.speed)

    def _calculate_duration_ms(self, audio_bytes: bytes) -> int:
        """Calculate actual audio duration from WAV bytes."""
        # WAV header is 44 bytes, rest is audio data
        # 24kHz, 16-bit mono = 48000 bytes per second
        if len(audio_bytes) <= 44:
            return 0
        audio_data_bytes = len(audio_bytes) - 44
        return int(audio_data_bytes / self.BYTES_PER_MS)

    async def _process_sentence(self, text: str):
        """Generate and send audio for a sentence."""
        self.sentence_count += 1
        s_id = self.sentence_count

        # Add to pending queue
        self.pending_sentences.append(text)
        queue_position = len(self.pending_sentences) - 1

        estimated_duration = self._estimate_duration_ms(text)

        await self.send_json({
            "type": "sentence_start",
            "id": s_id,
            "text": text,
            "estimated_duration_sec": estimated_duration / 1000.0,
            "queue_position": queue_position
        })

        start_gen = time.time()

        # Run TTS in executor
        loop = asyncio.get_event_loop()
        audio_bytes = await loop.run_in_executor(
            None,
            lambda: self.tts_engine.generate(text, self.voice, self.speed)
        )

        # Remove from pending queue
        if text in self.pending_sentences:
            self.pending_sentences.remove(text)

        if audio_bytes:
            await self.send_audio(audio_bytes)

            gen_time_ms = int((time.time() - start_gen) * 1000)
            duration_ms = self._calculate_duration_ms(audio_bytes)
            self.total_duration_ms += duration_ms
            self.sentences_completed += 1

            await self.send_json({
                "type": "sentence_complete",
                "id": s_id,
                "duration_ms": duration_ms,
                "processing_ms": gen_time_ms,
                "queue_remaining": len(self.pending_sentences)
            })
        else:
            await self.send_json({
                "type": "error",
                "sentence_id": s_id,
                "message": "TTS generation failed",
                "recoverable": True,
                "code": ErrorCode.TTS_ERROR
            })

    async def flush(self):
        """Force flush remaining buffer."""
        sentence = self.buffer.flush()
        if sentence:
            await self._process_sentence(sentence)

    async def check_timeout(self):
        """Check for partial sentence timeout."""
        sentence = self.buffer.check_timeout()
        if sentence:
            await self._process_sentence(sentence)

    async def send_stream_end(self, reason: str = "client_end"):
        """Send stream_end message per spec §3.3."""
        await self.send_json({
            "type": "stream_end",
            "total_sentences": self.sentences_completed,
            "total_duration_ms": self.total_duration_ms,
            "reason": reason
        })
