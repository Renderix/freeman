package ai.freeman.audio

object AudioFrame {
    const val SAMPLE_RATE = 16000
    const val FRAME_SIZE = 512
    const val FRAME_MS = FRAME_SIZE * 1000 / SAMPLE_RATE   // = 32ms
}
