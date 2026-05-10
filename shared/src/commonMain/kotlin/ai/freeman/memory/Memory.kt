package ai.freeman.memory

data class Memory(
    val id: Long = 0,
    val role: String,      // "user" | "assistant"
    val content: String,
    val sessionId: String,
    val createdAt: Long = System.currentTimeMillis(),
)
