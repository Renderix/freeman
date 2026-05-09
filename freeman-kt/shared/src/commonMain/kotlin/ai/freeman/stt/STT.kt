package ai.freeman.stt

interface STT {
    /** Transcribe a complete utterance (already VAD-gated). Returns empty string if inaudible. */
    suspend fun transcribe(audio: FloatArray): String
}
