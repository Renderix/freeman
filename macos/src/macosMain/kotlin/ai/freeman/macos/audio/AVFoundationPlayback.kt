package ai.freeman.macos.audio

class AVFoundationPlayback {
    fun play(samples: FloatArray, sampleRate: Int = 24000) {
        AVFoundationAudioJNI.playSamples(samples, sampleRate)
    }

    fun stop() = AVFoundationAudioJNI.stopPlayback()
}
