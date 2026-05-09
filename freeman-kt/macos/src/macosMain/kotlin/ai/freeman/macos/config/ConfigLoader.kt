package ai.freeman.macos.config

import ai.freeman.config.*

object ConfigLoader {
    fun load(path: String): FreemanConfig {
        val file = java.io.File(path)
        if (!file.exists()) return FreemanConfig()
        return parseYaml(file.readText())
    }

    private fun parseYaml(yaml: String): FreemanConfig {
        fun scalar(key: String): String? =
            Regex("""^\s*${Regex.escape(key)}:\s*"?([^"\n]+)"?\s*$""", RegexOption.MULTILINE)
                .find(yaml)?.groupValues?.get(1)?.trim()

        return FreemanConfig(
            llm = LLMConfig(
                provider = scalar("provider") ?: "ollama",
                model = scalar("model") ?: "gemma4:e4b",
                baseUrl = scalar("baseUrl") ?: "http://localhost:11434",
            ),
            tts = TTSConfig(
                modelPath = scalar("modelPath") ?: "./models/kokoro",
                voice = scalar("voice") ?: "bm_george",
                speed = scalar("speed")?.toFloatOrNull() ?: 1.0f,
            ),
            stt = STTConfig(
                enabled = scalar("enabled")?.toBooleanStrictOrNull() ?: true,
                modelPath = scalar("modelPath") ?: "./models/moonshine",
            ),
            wakeword = WakeWordConfig(
                modelsDir = scalar("modelsDir") ?: "./models/wakeword",
                threshold = scalar("threshold")?.toFloatOrNull() ?: 0.5f,
            ),
        )
    }
}
