package ai.freeman.macos.audio

import ai.freeman.audio.AudioCapture
import ai.freeman.audio.AudioFrame

class AVFoundationCapture : AudioCapture {
    override fun start(onFrame: (FloatArray) -> Unit) {
        val result = AVFoundationAudioJNI.startCapture(object : AVFoundationAudioJNI.FrameCallback {
            override fun onFrame(samples: FloatArray) = onFrame(samples)
        }, AudioFrame.SAMPLE_RATE, AudioFrame.FRAME_SIZE)
        if (result != 0) println("[Freeman] startCapture failed: $result")
    }

    override fun stop() = AVFoundationAudioJNI.stopCapture()
}
