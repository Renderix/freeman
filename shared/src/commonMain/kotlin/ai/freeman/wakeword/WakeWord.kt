package ai.freeman.wakeword

interface WakeWord {
    fun start(onDetected: () -> Unit)
    fun stop()
}
