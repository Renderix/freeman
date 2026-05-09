package ai.freeman.macos.tts

import ai.freeman.config.TTSConfig
import ai.freeman.tts.TTS

object MacosTTSFactory {
    fun create(config: TTSConfig): TTS {
        val tts = KokoroTTS(
            modelPath = "${config.modelPath}/model.onnx",
            voicesPath = "${config.modelPath}/voices.bin",
            tokensPath = "${config.modelPath}/tokens.txt",
            dataDir = "${config.modelPath}/espeak-ng-data",
            defaultSpeed = config.speed,
        )
        config.customVoicePath?.let { wavPath ->
            tts.loadCustomVoice(
                referenceWavPath = wavPath,
                encoderPath = "${config.modelPath}/model.onnx",
            )
        }
        return tts
    }
}
