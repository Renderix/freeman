package ai.freeman.macos.audio

class PortAudioPlayback {
    fun play(samples: FloatArray, sampleRate: Int = 24000) {
        PortAudioJNI.playSamples(samples, sampleRate)
    }

    fun stop() = PortAudioJNI.stopPlayback()
}
