package ai.freeman.llm

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject

@Serializable
data class Tool(
    val name: String,
    val description: String,
    val parameters: JsonObject,   // raw JSON Schema, passed through to provider
)
