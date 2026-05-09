package ai.freeman.macos.llm

import ai.freeman.config.LLMConfig
import ai.freeman.llm.*
import io.ktor.client.*
import io.ktor.client.engine.okhttp.*
import io.ktor.client.plugins.contentnegotiation.*
import io.ktor.client.request.*
import io.ktor.client.statement.*
import io.ktor.http.*
import io.ktor.serialization.kotlinx.json.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.*

class OllamaProvider(private val config: LLMConfig) : LLMProvider {

    private val json = Json { ignoreUnknownKeys = true }
    private val client = HttpClient(OkHttp) {
        install(ContentNegotiation) { json(json) }
    }

    override suspend fun chat(
        messages: List<Message>,
        tools: List<Tool>,
    ): Flow<Delta> = flow {
        val body = buildRequestBody(messages, tools)

        val response = client.post("${config.baseUrl}/api/chat") {
            contentType(ContentType.Application.Json)
            setBody(body)
        }

        val buf = StringBuilder()
        response.bodyAsChannel().let { channel ->
            while (!channel.isClosedForRead) {
                val line = channel.readUTF8Line() ?: break
                if (line.isBlank()) continue
                buf.append(line)
                runCatching {
                    val chunk = json.decodeFromString<OllamaChunk>(buf.toString())
                    buf.clear()
                    val toolCalls = chunk.message?.tool_calls
                    val content = chunk.message?.content
                    when {
                        toolCalls != null && toolCalls.isNotEmpty() -> toolCalls.forEach { tc ->
                            emit(Delta(toolCall = ToolCall(
                                id = tc.id ?: java.util.UUID.randomUUID().toString(),
                                name = tc.function.name,
                                arguments = json.encodeToString(JsonObject.serializer(), tc.function.arguments),
                            )))
                        }
                        content != null -> emit(Delta(text = content))
                    }
                    if (chunk.done) emit(Delta(done = true))
                }
            }
        }
    }

    private fun buildRequestBody(messages: List<Message>, tools: List<Tool>): JsonObject =
        buildJsonObject {
            put("model", config.model)
            put("stream", true)
            put("messages", buildJsonArray {
                messages.forEach { msg ->
                    add(buildJsonObject {
                        put("role", msg.role.name)
                        put("content", msg.content)
                        msg.toolCallId?.let { put("tool_call_id", it) }
                    })
                }
            })
            if (tools.isNotEmpty()) {
                put("tools", buildJsonArray {
                    tools.forEach { tool ->
                        add(buildJsonObject {
                            put("type", "function")
                            put("function", buildJsonObject {
                                put("name", tool.name)
                                put("description", tool.description)
                                put("parameters", tool.parameters)
                            })
                        })
                    }
                })
            }
        }
}

@Serializable
private data class OllamaChunk(
    val model: String = "",
    val message: OllamaMessage? = null,
    val done: Boolean = false,
)

@Serializable
private data class OllamaMessage(
    val role: String = "",
    val content: String? = null,
    val tool_calls: List<OllamaToolCall>? = null,
)

@Serializable
private data class OllamaToolCall(
    val id: String? = null,
    val function: OllamaFunction,
)

@Serializable
private data class OllamaFunction(
    val name: String,
    val arguments: JsonObject = JsonObject(emptyMap()),
)
