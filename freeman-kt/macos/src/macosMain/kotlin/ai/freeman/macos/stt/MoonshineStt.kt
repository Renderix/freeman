package ai.freeman.macos.stt

import ai.freeman.stt.STT
import com.k2fsa.sherpa.onnx.OfflineRecognizer
import com.k2fsa.sherpa.onnx.OfflineRecognizerConfig
import com.k2fsa.sherpa.onnx.FeatureConfig
import com.k2fsa.sherpa.onnx.OfflineModelConfig
import com.k2fsa.sherpa.onnx.OfflineMoonshineModelConfig

class MoonshineStt(modelDir: String) : STT {
    private val recognizer: OfflineRecognizer

    init {
        val config = OfflineRecognizerConfig(
            featConfig = FeatureConfig(sampleRate = 16000, featureDim = 80),
            modelConfig = OfflineModelConfig(
                moonshine = OfflineMoonshineModelConfig(
                    preprocessor = "$modelDir/preprocess.onnx",
                    encoder = "$modelDir/encode.int8.onnx",
                    uncachedDecoder = "$modelDir/uncached_decode.int8.onnx",
                    cachedDecoder = "$modelDir/cached_decode.int8.onnx",
                ),
                numThreads = 2,
                debug = false,
            ),
        )
        recognizer = OfflineRecognizer(config)
    }

    override suspend fun transcribe(audio: FloatArray): String {
        val stream = recognizer.createStream()
        stream.acceptWaveform(audio, sampleRate = 16000)
        recognizer.decode(stream)
        val result = recognizer.getResult(stream).text.trim()
        stream.release()
        return result
    }
}
