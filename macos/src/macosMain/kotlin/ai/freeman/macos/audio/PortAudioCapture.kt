package ai.freeman.macos.audio

import ai.freeman.audio.AudioCapture
import ai.freeman.audio.AudioFrame

class PortAudioCapture : AudioCapture {
    override fun start(onFrame: (FloatArray) -> Unit) {
        PortAudioJNI.start(object : PortAudioJNI.FrameCallback {
            override fun onFrame(samples: FloatArray) = onFrame(samples)
        }, AudioFrame.SAMPLE_RATE, AudioFrame.FRAME_SIZE)
    }

    override fun stop() = PortAudioJNI.stop()
}
