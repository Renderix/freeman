package ai.freeman.android.tts

import ai.freeman.config.TTSConfig
import ai.freeman.tts.TTS
import ai.freeman.tts.VoiceProfile
import android.content.Context

object AndroidTTSFactory {
    fun create(context: Context, config: TTSConfig): TTS {
        val modelDir = "${context.filesDir}/models/kokoro"
        return KokoroAndroidTTS(
            modelPath = "$modelDir/model.onnx",
            voicesPath = "$modelDir/voices.bin",
            tokensPath = "$modelDir/tokens.txt",
            dataDir = "$modelDir/espeak-ng-data",
            defaultSpeed = config.speed,
        )
    }
}

/** Android-side Kokoro TTS — identical logic to macos/KokoroTTS but with Android sherpa-onnx AAR. */
class KokoroAndroidTTS(
    modelPath: String,
    voicesPath: String,
    tokensPath: String,
    dataDir: String,
    private val defaultSpeed: Float = 1.0f,
) : TTS {

    private val voiceIds = mapOf(
        "af_heart" to 0, "bm_george" to 1, "af_bella" to 2, "am_adam" to 3,
        "af_nicole" to 4, "af_sarah" to 5, "am_michael" to 6, "bf_emma" to 7,
        "bm_lewis" to 8, "af_sky" to 9, "bf_isabella" to 10,
    )

    private val tts by lazy {
        com.k2fsa.sherpa.onnx.OfflineTts(
            com.k2fsa.sherpa.onnx.OfflineTtsConfig(
                model = com.k2fsa.sherpa.onnx.OfflineTtsModelConfig(
                    kokoro = com.k2fsa.sherpa.onnx.OfflineTtsKokoroModelConfig(
                        model = modelPath, voices = voicesPath,
                        tokens = tokensPath, dataDir = dataDir,
                    ),
                    numThreads = 2, debug = false,
                ),
            )
        )
    }

    override suspend fun synthesize(text: String, voice: VoiceProfile?): FloatArray {
        val speakerId = voice?.speakerId ?: voice?.name?.let { voiceIds[it] } ?: 0
        val audio = tts.generateWithCallback(
            text = text, sid = speakerId, speed = defaultSpeed, callback = { _ -> true },
        )
        return audio.samples
    }
}
