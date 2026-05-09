package ai.freeman.tts

interface TTS {
    /** Synthesize text to PCM samples at 24kHz mono. */
    suspend fun synthesize(text: String, voice: VoiceProfile? = null): FloatArray
}
