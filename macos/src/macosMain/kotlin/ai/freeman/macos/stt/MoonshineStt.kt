package ai.freeman.macos.stt

import ai.freeman.stt.STT
import com.k2fsa.sherpa.onnx.FeatureConfig
import com.k2fsa.sherpa.onnx.OfflineModelConfig
import com.k2fsa.sherpa.onnx.OfflineMoonshineModelConfig
import com.k2fsa.sherpa.onnx.OfflineRecognizer
import com.k2fsa.sherpa.onnx.OfflineRecognizerConfig

class MoonshineStt(modelDir: String) : STT {
    private val recognizer: OfflineRecognizer

    init {
        val config = OfflineRecognizerConfig.builder()
            .setFeatureConfig(
                FeatureConfig.builder()
                    .setSampleRate(16000)
                    .setFeatureDim(80)
                    .build()
            )
            .setOfflineModelConfig(
                OfflineModelConfig.builder()
                    .setMoonshine(
                        OfflineMoonshineModelConfig.builder()
                            .setPreprocessor("$modelDir/preprocess.onnx")
                            .setEncoder("$modelDir/encode.int8.onnx")
                            .setUncachedDecoder("$modelDir/uncached_decode.int8.onnx")
                            .setCachedDecoder("$modelDir/cached_decode.int8.onnx")
                            .build()
                    )
                    .setTokens("$modelDir/tokens.txt")
                    .setNumThreads(2)
                    .setDebug(false)
                    .build()
            )
            .build()
        recognizer = OfflineRecognizer(config)
    }

    override suspend fun transcribe(audio: FloatArray): String {
        val stream = recognizer.createStream()
        stream.acceptWaveform(audio, 16000)
        recognizer.decode(stream)
        val result = recognizer.getResult(stream).text.trim()
        stream.release()
        return result
    }
}
