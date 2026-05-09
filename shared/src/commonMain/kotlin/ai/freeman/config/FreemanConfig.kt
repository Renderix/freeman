package ai.freeman.config

import kotlinx.serialization.Serializable

@Serializable
data class FreemanConfig(
    val persona: PersonaConfig = PersonaConfig(),
    val llm: LLMConfig = LLMConfig(),
    val tts: TTSConfig = TTSConfig(),
    val stt: STTConfig = STTConfig(),
    val wakeword: WakeWordConfig = WakeWordConfig(),
    val tools: ToolsConfig = ToolsConfig(),
)

@Serializable
data class PersonaConfig(
    val name: String = "Freeman",
    val greeting: String = "Hi, how can I help?",
    val farewell: String = "Goodbye.",
    val rules: List<String> = emptyList(),
)

@Serializable
data class LLMConfig(
    val provider: String = "ollama",          // "ollama" | "claude" | "litert"
    val model: String = "gemma4:e4b",
    val baseUrl: String = "http://localhost:11434",
    val apiKey: String? = null,               // Claude: key or set ANTHROPIC_API_KEY env var
    val numCtx: Int = 8192,                   // Ollama: context window (tokens)
    val keepAlive: String = "-1",             // Ollama: keep model loaded forever
)

@Serializable
data class TTSConfig(
    val modelPath: String = "./models/kokoro",
    val voice: String = "bm_george",
    val speed: Float = 1.0f,
    val customVoicePath: String? = null,
)

@Serializable
data class STTConfig(
    val enabled: Boolean = true,
    val modelPath: String = "./models/moonshine",
)

@Serializable
data class WakeWordConfig(
    val modelsDir: String = "./models/wakeword",
    val threshold: Float = 0.5f,
)

@Serializable
data class ToolsConfig(
    val dirs: List<String> = listOf("./tools"),
)
