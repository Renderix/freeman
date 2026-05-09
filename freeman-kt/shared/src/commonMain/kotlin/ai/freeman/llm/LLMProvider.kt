package ai.freeman.llm

import kotlinx.coroutines.flow.Flow

data class Delta(
    val text: String? = null,
    val toolCall: ToolCall? = null,
    val done: Boolean = false,
)

interface LLMProvider {
    val supportsAudioInput: Boolean get() = false

    suspend fun chat(
        messages: List<Message>,
        tools: List<Tool> = emptyList(),
    ): Flow<Delta>

    /** Only called when supportsAudioInput = true. audio is 16kHz mono PCM. */
    suspend fun chatWithAudio(
        audio: FloatArray,
        messages: List<Message>,
        tools: List<Tool> = emptyList(),
    ): Flow<Delta> = throw UnsupportedOperationException("audio input not supported")
}
