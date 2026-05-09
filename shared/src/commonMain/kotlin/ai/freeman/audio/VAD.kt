package ai.freeman.audio

interface VAD {
    /** Returns true if the frame contains speech. Frame must be 512 samples at 16kHz. */
    fun isSpeech(frame: FloatArray): Boolean
    fun reset()
}
