package ai.freeman.macos.llm

import ai.freeman.config.LLMConfig
import ai.freeman.llm.Delta
import ai.freeman.llm.LLMProvider
import ai.freeman.llm.Message
import ai.freeman.llm.Role
import ai.freeman.llm.Tool
import ai.freeman.llm.ToolCall
import io.ktor.client.HttpClient
import io.ktor.client.engine.okhttp.OkHttp
import io.ktor.client.request.header
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsChannel
import io.ktor.http.ContentType
import io.ktor.http.contentType
import io.ktor.utils.io.readUTF8Line
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

private const val ANTHROPIC_API = "https://api.anthropic.com/v1/messages"
private const val ANTHROPIC_VERSION = "2023-06-01"
private const val ANTHROPIC_BETA = "prompt-caching-2024-07-31"

class ClaudeProvider(config: LLMConfig) : LLMProvider {

    private val model = config.model.ifBlank { "claude-sonnet-4-6" }
    private val apiKey = config.apiKey
        ?: System.getenv("ANTHROPIC_API_KEY")
        ?: error("Claude provider requires apiKey in config or ANTHROPIC_API_KEY env var")

    private val json = Json { ignoreUnknownKeys = true }
    private val client = HttpClient(OkHttp)

    override suspend fun chat(messages: List<Message>, tools: List<Tool>): Flow<Delta> = flow {
        val system = messages.firstOrNull { it.role == Role.system }?.content ?: ""
        val turns = messages.filter { it.role != Role.system }

        val body = buildJsonObject {
            put("model", model)
            put("max_tokens", 4096)
            put("stream", true)

            // System prompt with cache_control — cached on first call, free on subsequent turns
            put("system", buildJsonArray {
                add(buildJsonObject {
                    put("type", "text")
                    put("text", system)
                    put("cache_control", buildJsonObject { put("type", "ephemeral") })
                })
            })

            put("messages", buildJsonArray {
                turns.forEach { msg ->
                    add(buildJsonObject {
                        put("role", if (msg.role == Role.tool) "user" else msg.role.name)
                        if (msg.role == Role.tool) {
                            put("content", buildJsonArray {
                                add(buildJsonObject {
                                    put("type", "tool_result")
                                    put("tool_use_id", msg.toolCallId ?: "")
                                    put("content", msg.content)
                                })
                            })
                        } else {
                            put("content", msg.content)
                        }
                    })
                }
            })

            if (tools.isNotEmpty()) {
                put("tools", buildJsonArray {
                    tools.forEachIndexed { i, tool ->
                        add(buildJsonObject {
                            put("name", tool.name)
                            put("description", tool.description)
                            put("input_schema", tool.parameters)
                            // Cache tools on the last entry — they don't change between turns
                            if (i == tools.lastIndex) {
                                put("cache_control", buildJsonObject { put("type", "ephemeral") })
                            }
                        })
                    }
                })
            }
        }

        val response = client.post(ANTHROPIC_API) {
            contentType(ContentType.Application.Json)
            header("x-api-key", apiKey)
            header("anthropic-version", ANTHROPIC_VERSION)
            header("anthropic-beta", ANTHROPIC_BETA)
            setBody(body.toString())
        }

        // Accumulate partial tool input JSON across deltas
        val toolId = StringBuilder()
        val toolName = StringBuilder()
        val toolInput = StringBuilder()
        var inToolBlock = false

        val channel = response.bodyAsChannel()
        while (!channel.isClosedForRead) {
            val line = channel.readUTF8Line() ?: break
            when {
                line.startsWith("data: ") -> {
                    val data = line.removePrefix("data: ")
                    if (data == "[DONE]") { emit(Delta(done = true)); break }
                    runCatching {
                        val obj = json.parseToJsonElement(data).jsonObject
                        val type = obj["type"]?.jsonPrimitive?.content ?: return@runCatching

                        when (type) {
                            "content_block_start" -> {
                                val block = obj["content_block"]?.jsonObject ?: return@runCatching
                                if (block["type"]?.jsonPrimitive?.content == "tool_use") {
                                    inToolBlock = true
                                    toolId.clear(); toolName.clear(); toolInput.clear()
                                    toolId.append(block["id"]?.jsonPrimitive?.content ?: "")
                                    toolName.append(block["name"]?.jsonPrimitive?.content ?: "")
                                }
                            }
                            "content_block_delta" -> {
                                val delta = obj["delta"]?.jsonObject ?: return@runCatching
                                when (delta["type"]?.jsonPrimitive?.content) {
                                    "text_delta" ->
                                        emit(Delta(text = delta["text"]?.jsonPrimitive?.content ?: ""))
                                    "input_json_delta" ->
                                        if (inToolBlock)
                                            toolInput.append(delta["partial_json"]?.jsonPrimitive?.content ?: "")
                                }
                            }
                            "content_block_stop" -> {
                                if (inToolBlock) {
                                    emit(Delta(toolCall = ToolCall(
                                        id = toolId.toString(),
                                        name = toolName.toString(),
                                        arguments = toolInput.toString(),
                                    )))
                                    inToolBlock = false
                                }
                            }
                            "message_stop" -> emit(Delta(done = true))
                        }
                    }
                }
            }
        }
    }
}
