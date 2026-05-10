package ai.freeman.macos.audio

import ai.freeman.audio.AudioCapture
import ai.freeman.audio.AudioFrame

class AVFoundationCapture : AudioCapture {
    override fun start(onFrame: (FloatArray) -> Unit) {
        AVFoundationAudioJNI.startCapture(object : AVFoundationAudioJNI.FrameCallback {
            override fun onFrame(samples: FloatArray) = onFrame(samples)
        }, AudioFrame.SAMPLE_RATE, AudioFrame.FRAME_SIZE)
    }

    override fun stop() = AVFoundationAudioJNI.stopCapture()
}
