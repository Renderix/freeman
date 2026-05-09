package ai.freeman.conv

import ai.freeman.config.FreemanConfig
import ai.freeman.llm.Delta
import ai.freeman.llm.LLMProvider
import ai.freeman.llm.Message
import ai.freeman.llm.Tool
import ai.freeman.tasks.TaskManager
import ai.freeman.tools.ToolRegistry
import ai.freeman.tts.TTS
import ai.freeman.tts.VoiceProfile
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class ConversationLoopTest {

    private fun fakeTts(spoken: MutableList<String>) = object : TTS {
        override suspend fun synthesize(text: String, voice: VoiceProfile?) =
            FloatArray(0).also { spoken.add(text) }
    }

    private fun fakeLlm(response: String) = object : LLMProvider {
        override suspend fun chat(messages: List<Message>, tools: List<Tool>) =
            flowOf(Delta(text = response), Delta(done = true))
    }

    @Test fun systemPromptContainsPersonaName() {
        val config = FreemanConfig()
        val prompt = SystemPrompt.build(config.persona, emptyList())
        assertTrue(prompt.contains(config.persona.name))
    }

    @Test fun sendsUserMessageToLlm() = runBlocking {
        val spoken = mutableListOf<String>()
        val loop = ConversationLoop(
            config = FreemanConfig(),
            llm = fakeLlm("Hello back!"),
            tts = fakeTts(spoken),
            taskManager = TaskManager(),
            toolRegistry = ToolRegistry(),
        )
        loop.handleUtterance("Hello!")
        assertEquals(listOf("Hello back!"), spoken)
    }
}
