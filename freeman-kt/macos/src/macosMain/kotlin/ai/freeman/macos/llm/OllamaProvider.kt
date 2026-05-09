package ai.freeman.macos.llm

import ai.freeman.config.LLMConfig
import ai.freeman.llm.Delta
import ai.freeman.llm.LLMProvider
import ai.freeman.llm.Message
import ai.freeman.llm.Tool
import ai.freeman.llm.ToolCall
import io.ktor.client.HttpClient
import io.ktor.client.engine.okhttp.OkHttp
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.ContentType
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import io.ktor.utils.io.readUTF8Line
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

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
