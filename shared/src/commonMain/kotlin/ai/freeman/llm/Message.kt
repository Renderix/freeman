package ai.freeman.llm

import kotlinx.serialization.Serializable

@Serializable
enum class Role { system, user, assistant, tool }

@Serializable
data class Message(
    val role: Role,
    val content: String,
    val toolCallId: String? = null,
    val toolCalls: List<ToolCall>? = null,
)

@Serializable
data class ToolCall(
    val id: String,
    val name: String,
    val arguments: String,   // JSON string
)
