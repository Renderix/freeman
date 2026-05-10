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
import ai.freeman.tools.StoredTool
import ai.freeman.tools.ToolRegistry
import ai.freeman.tools.ToolRunner
import ai.freeman.skills.SkillStore
import ai.freeman.tools.ToolStore
import ai.freeman.tts.TTS
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.util.UUID

private fun ts(): String {
    val t = java.time.LocalTime.now()
    return "%02d:%02d:%02d.%03d".format(t.hour, t.minute, t.second, t.nano / 1_000_000)
}

class ConversationLoop(
    private val config: FreemanConfig,
    private val llm: LLMProvider,
    private val tts: TTS,
    private val taskManager: TaskManager,
    private val toolRegistry: ToolRegistry,
    private val toolRunner: ToolRunner? = null,
    private val toolStore: ToolStore? = null,
    private val skillStore: SkillStore? = null,
    private val memoryStore: MemoryStore? = null,
    private val onSpeak: suspend (FloatArray) -> Unit = {},
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
        val skills   = skillStore?.search(text, limit = 3) ?: emptyList()
        val systemPrompt = SystemPrompt.build(config.persona, toolRegistry.tools(), recalled, skills, override = config.systemPrompt)

        val messages = listOf(Message(role = Role.system, content = systemPrompt)) + history
        val tools = toolRegistry.tools() + builtinTaskTools() + builtinDefineToolIfEnabled()

        val sentenceChannel = Channel<String>(Channel.UNLIMITED)
        val sentenceBuffer = SentenceBuffer { sentence -> sentenceChannel.trySend(sentence) }

        val assistantText = StringBuilder()
        val toolCalls = mutableListOf<ToolCall>()

        println("[Freeman] ${ts()} → LLM")
        coroutineScope {
            val audioChannel = Channel<FloatArray>(2)

            val synthesisJob = launch(kotlinx.coroutines.Dispatchers.Default) {
                for (sentence in sentenceChannel) {
                    println("[Freeman] ${ts()} synth: \"${sentence.take(60)}\"")
                    val audio = tts.synthesize(sentence)
                    println("[Freeman] ${ts()} play")
                    audioChannel.send(audio)
                }
                audioChannel.close()
            }
            val playbackJob = launch(kotlinx.coroutines.Dispatchers.IO) {
                for (audio in audioChannel) {
                    onSpeak(audio)
                    println("[Freeman] ${ts()} done speaking")
                }
            }
            var firstToken = true
            llm.chat(messages, tools).collect { delta ->
                if (firstToken && delta.text != null) {
                    println("[Freeman] ${ts()} ← LLM first token")
                    firstToken = false
                }
                delta.text?.let { assistantText.append(it); sentenceBuffer.append(it) }
                delta.toolCall?.let { toolCalls.add(it) }
            }
            sentenceBuffer.flush()
            sentenceChannel.close()
            synthesisJob.join()
            playbackJob.join()
        }

        val reply = assistantText.toString()
        if (reply.isNotBlank()) {
            println("[Freeman] ${ts()} ← LLM done: \"${reply.take(80)}\"")
            history.add(Message(role = Role.assistant, content = reply))
            memoryStore?.save(Memory(role = "assistant", content = reply, sessionId = sessionId))
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
                "define_tool" -> handleDefineTool(args)
                else -> {
                    if (toolRunner != null)
                        toolRegistry.dispatch(tc.name, args, toolRunner)
                    else """{"error":"unknown tool ${tc.name}"}"""
                }
            }
            history.add(Message(role = Role.tool, content = result, toolCallId = tc.id))
        }
    }

    private fun handleDefineTool(args: Map<String, String>): String {
        val name        = args["name"]?.trim()        ?: return """{"error":"name required"}"""
        val description = args["description"]?.trim() ?: return """{"error":"description required"}"""
        val body        = args["body"]?.trim()        ?: return """{"error":"body required"}"""
        val paramsJson  = args["params_json"]?.trim() ?: """{"type":"object"}"""
        val tool = StoredTool(name = name, description = description, paramsJson = paramsJson, body = body)
        toolStore?.upsert(tool)
        toolRegistry.register(tool)
        println("[Freeman] defined tool: $name")
        return "Tool '$name' saved and available."
    }

    private fun builtinDefineToolIfEnabled(): List<Tool> {
        if (toolStore == null) return emptyList()
        return listOf(Tool(
            name = "define_tool",
            description = "Save a new tool the user just described. Write a bash script body; read args from env vars named ARG_<PARAM_UPPERCASE>.",
            parameters = buildJsonObject {
                put("type", JsonPrimitive("object"))
                put("properties", buildJsonObject {
                    put("name",        buildJsonObject { put("type", JsonPrimitive("string")) })
                    put("description", buildJsonObject { put("type", JsonPrimitive("string")) })
                    put("body",        buildJsonObject { put("type", JsonPrimitive("string")) })
                    put("params_json", buildJsonObject { put("type", JsonPrimitive("string")); put("description", JsonPrimitive("JSON schema for parameters, optional")) })
                })
                put("required", kotlinx.serialization.json.buildJsonArray {
                    add(JsonPrimitive("name")); add(JsonPrimitive("description")); add(JsonPrimitive("body"))
                })
            },
        ))
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
