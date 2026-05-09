package ai.freeman.macos.tts

import ai.freeman.tts.TTS
import ai.freeman.tts.VoiceProfile
import com.k2fsa.sherpa.onnx.OfflineTts
import com.k2fsa.sherpa.onnx.OfflineTtsConfig
import com.k2fsa.sherpa.onnx.OfflineTtsModelConfig
import com.k2fsa.sherpa.onnx.OfflineTtsKokoroModelConfig
import com.k2fsa.sherpa.onnx.SpeakerEmbeddingExtractor
import com.k2fsa.sherpa.onnx.SpeakerEmbeddingExtractorConfig

class KokoroTTS(
    modelPath: String,
    voicesPath: String,
    tokensPath: String,
    dataDir: String,
    private val defaultSpeed: Float = 1.0f,
) : TTS {

    private val tts: OfflineTts
    private var customEmbedding: FloatArray? = null

    private val voiceIds = mapOf(
        "af_heart" to 0, "bm_george" to 1, "af_bella" to 2, "am_adam" to 3,
        "af_nicole" to 4, "af_sarah" to 5, "am_michael" to 6, "bf_emma" to 7,
        "bm_lewis" to 8, "af_sky" to 9, "bf_isabella" to 10,
    )

    init {
        val config = OfflineTtsConfig(
            model = OfflineTtsModelConfig(
                kokoro = OfflineTtsKokoroModelConfig(
                    model = modelPath,
                    voices = voicesPath,
                    tokens = tokensPath,
                    dataDir = dataDir,
                ),
                numThreads = 2,
                debug = false,
            ),
        )
        tts = OfflineTts(config)
    }

    fun loadCustomVoice(referenceWavPath: String, encoderPath: String, voicesPath: String) {
        val extractor = SpeakerEmbeddingExtractor(
            SpeakerEmbeddingExtractorConfig(model = encoderPath)
        )
        val stream = extractor.createStream()
        val (samples, sampleRate) = readWav(referenceWavPath)
        stream.acceptWaveform(samples, sampleRate = sampleRate)
        stream.inputFinished()
        customEmbedding = extractor.compute(stream)
        stream.release()
    }

    override suspend fun synthesize(text: String, voice: VoiceProfile?): FloatArray {
        val speakerId = voice?.speakerId ?: voice?.name?.let { voiceIds[it] } ?: 0
        val audio = tts.generateWithCallback(
            text = text,
            sid = speakerId,
            speed = defaultSpeed,
            callback = { _ -> true },
        )
        return audio.samples
    }

    fun close() = tts.release()

    private fun readWav(path: String): Pair<FloatArray, Int> {
        val bytes = java.io.File(path).readBytes()
        val sampleRate = bytes.getInt(24)
        val shorts = (44 until bytes.size step 2).map { i ->
            ((bytes[i + 1].toInt() shl 8) or (bytes[i].toInt() and 0xFF)).toShort()
        }
        return FloatArray(shorts.size) { shorts[it] / 32768f } to sampleRate
    }

    private fun ByteArray.getInt(offset: Int): Int =
        (this[offset].toInt() and 0xFF) or
        ((this[offset + 1].toInt() and 0xFF) shl 8) or
        ((this[offset + 2].toInt() and 0xFF) shl 16) or
        ((this[offset + 3].toInt() and 0xFF) shl 24)
}
