package ai.freeman.macos.audio

object PortAudioJNI {
    init {
        System.load("${System.getProperty("user.dir")}/macos/native/libportaudio_jni.dylib")
    }

    @JvmStatic external fun start(callback: FrameCallback, sampleRate: Int, framesPerBuffer: Int): Int
    @JvmStatic external fun stop()
    @JvmStatic external fun playSamples(samples: FloatArray, sampleRate: Int): Int
    @JvmStatic external fun stopPlayback()

    interface FrameCallback {
        fun onFrame(samples: FloatArray)
    }
}
