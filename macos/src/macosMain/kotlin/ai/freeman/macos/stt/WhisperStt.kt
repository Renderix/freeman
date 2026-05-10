package ai.freeman.macos.stt

import ai.freeman.stt.STT
import com.k2fsa.sherpa.onnx.FeatureConfig
import com.k2fsa.sherpa.onnx.OfflineModelConfig
import com.k2fsa.sherpa.onnx.OfflineRecognizer
import com.k2fsa.sherpa.onnx.OfflineRecognizerConfig
import com.k2fsa.sherpa.onnx.OfflineWhisperModelConfig

class WhisperStt(modelDir: String) : STT {
    private val recognizer: OfflineRecognizer

    init {
        // sherpa-onnx Whisper archives use a prefix like "small.en-encoder.int8.onnx"
        val dir = java.io.File(modelDir)
        val encoder = dir.listFiles { f -> f.name.endsWith("-encoder.int8.onnx") }
            ?.firstOrNull()?.absolutePath
            ?: "$modelDir/encoder.int8.onnx"
        val decoder = encoder.replace("-encoder.int8.onnx", "-decoder.int8.onnx")
        val tokens = dir.listFiles { f -> f.name.endsWith("-tokens.txt") }
            ?.firstOrNull()?.absolutePath
            ?: "$modelDir/tokens.txt"

        val config = OfflineRecognizerConfig.builder()
            .setFeatureConfig(
                FeatureConfig.builder()
                    .setSampleRate(16000)
                    .setFeatureDim(80)
                    .build()
            )
            .setOfflineModelConfig(
                OfflineModelConfig.builder()
                    .setWhisper(
                        OfflineWhisperModelConfig.builder()
                            .setEncoder(encoder)
                            .setDecoder(decoder)
                            .setLanguage("en")
                            .setTask("transcribe")
                            .build()
                    )
                    .setTokens(tokens)
                    .setNumThreads(4)
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
