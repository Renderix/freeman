package ai.freeman.macos.audio

class AVFoundationPlayback {
    fun play(samples: FloatArray, sampleRate: Int = 24000) {
        println("[Freeman] audio: ${samples.size} frames @ ${sampleRate}Hz")
        val result = AVFoundationAudioJNI.playSamples(samples, sampleRate)
        if (result != 0) println("[Freeman] playSamples failed: $result")
        else println("[Freeman] audio: done")
    }

    fun stop() = AVFoundationAudioJNI.stopPlayback()
}
