package ai.freeman.conv

import ai.freeman.config.FreemanConfig
import ai.freeman.llm.Delta
import ai.freeman.llm.LLMProvider
import ai.freeman.llm.Message
import ai.freeman.llm.Role
import ai.freeman.llm.Tool
import ai.freeman.llm.ToolCall
import ai.freeman.memory.Memory
import ai.freeman.memory.MemoryStore
import ai.freeman.tasks.TaskManager
import ai.freeman.tools.ToolRegistry
import ai.freeman.tools.ToolRunner
import ai.freeman.tts.TTS
import kotlinx.coroutines.flow.collect
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.util.UUID

class ConversationLoop(
    private val config: FreemanConfig,
    private val llm: LLMProvider,
    private val tts: TTS,
    private val taskManager: TaskManager,
    private val toolRegistry: ToolRegistry,
    private val toolRunner: ToolRunner? = null,
    private val memoryStore: MemoryStore? = null,
) {
    private val history = mutableListOf<Message>()
    private val sessionId = UUID.randomUUID().toString()

    suspend fun handleUtterance(text: String) {
        val taskContext = taskManager.promptSummary()
        val userText = if (taskContext != "no background tasks")
            "[background tasks:\n$taskContext]\n\n$text" else text

        history.add(Message(role = Role.user, content = userText))
        memoryStore?.save(Memory(role = "user", content = text, sessionId = sessionId))

        val recalled = memoryStore?.search(text, limit = config.memory.recallLimit) ?: emptyList()
        val systemPrompt = SystemPrompt.build(config.persona, toolRegistry.tools(), recalled)

        val messages = listOf(Message(role = Role.system, content = systemPrompt)) + history
        val tools = toolRegistry.tools() + builtinTaskTools()

        val sentenceBuffer = SentenceBuffer { sentence ->
            // Caller owns playback — sentence chunks emitted here
        }

        val assistantText = StringBuilder()
        val toolCalls = mutableListOf<ToolCall>()

        llm.chat(messages, tools).collect { delta ->
            delta.text?.let { assistantText.append(it); sentenceBuffer.append(it) }
            delta.toolCall?.let { toolCalls.add(it) }
        }
        sentenceBuffer.flush()

        val reply = assistantText.toString()
        if (reply.isNotBlank()) {
            history.add(Message(role = Role.assistant, content = reply))
            memoryStore?.save(Memory(role = "assistant", content = reply, sessionId = sessionId))
            tts.synthesize(reply)
        }

        // Trim in-session history to the sliding window
        val window = config.memory.historyWindow
        if (history.size > window) history.subList(0, history.size - window).clear()

        for (tc in toolCalls) {
            val args = parseArgs(tc.arguments)
            val result = when (tc.name) {
                "start_task"  -> { taskManager.start(args["goal"] ?: ""); "task started" }
                "cancel_task" -> { taskManager.cancel(args["id"] ?: ""); "task cancelled" }
                "task_status" -> taskManager.promptSummary()
                else -> {
                    val mdTool = toolRegistry.tools().find { it.name == tc.name }
                    if (mdTool != null && toolRunner != null)
                        toolRegistry.dispatch(tc.name, args, toolRunner)
                    else """{"error":"unknown tool ${tc.name}"}"""
                }
            }
            history.add(Message(role = Role.tool, content = result, toolCallId = tc.id))
        }
    }

    private fun builtinTaskTools(): List<Tool> = listOf(
        Tool(name = "start_task", description = "Start a background agent task.",
             parameters = buildJsonObject { put("type", JsonPrimitive("object")) }),
        Tool(name = "cancel_task", description = "Cancel a running task.",
             parameters = buildJsonObject { put("type", JsonPrimitive("object")) }),
        Tool(name = "task_status", description = "Get status of all background tasks.",
             parameters = buildJsonObject { put("type", JsonPrimitive("object")) }),
    )

    private fun parseArgs(json: String): Map<String, String> = try {
        Json.parseToJsonElement(json).jsonObject
            .mapValues { it.value.jsonPrimitive.contentOrNull ?: it.value.toString() }
    } catch (e: Exception) { emptyMap() }
}
