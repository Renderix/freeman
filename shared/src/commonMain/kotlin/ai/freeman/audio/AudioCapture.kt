package ai.freeman.audio

interface AudioCapture {
    fun start(onFrame: (FloatArray) -> Unit)
    fun stop()
}
