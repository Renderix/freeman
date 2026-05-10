package ai.freeman.macos.audio

object AVFoundationAudioJNI {
    init {
        System.loadLibrary("avfoundation_audio_jni")
    }

    @JvmStatic external fun startCapture(callback: FrameCallback, sampleRate: Int, framesPerBuffer: Int): Int
    @JvmStatic external fun stopCapture()
    @JvmStatic external fun playSamples(samples: FloatArray, sampleRate: Int): Int
    @JvmStatic external fun stopPlayback()

    interface FrameCallback {
        fun onFrame(samples: FloatArray)
    }
}
