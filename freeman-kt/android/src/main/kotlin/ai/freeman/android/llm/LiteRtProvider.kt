package ai.freeman.android.llm

import ai.freeman.llm.*
import android.content.Context
import com.google.mediapipe.tasks.genai.llminference.LlmInference
import com.google.mediapipe.tasks.genai.llminference.LlmInference.LlmInferenceOptions
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow

class LiteRtProvider(
    private val modelPath: String,
    private val context: Context?,
) : LLMProvider {

    private val inference: LlmInference? by lazy {
        context ?: return@lazy null
        val options = LlmInferenceOptions.builder()
            .setModelPath(modelPath)
            .setMaxTokens(2048)
            .build()
        LlmInference.createFromOptions(context, options)
    }

    override suspend fun chat(
        messages: List<Message>,
        tools: List<Tool>,
    ): Flow<Delta> = callbackFlow {
        val llm = inference ?: run {
            trySend(Delta(text = "[LiteRT not initialised]", done = true))
            close()
            return@callbackFlow
        }

        val prompt = buildGemmaPrompt(messages)
        llm.generateResponseAsync(prompt) { partial, done ->
            if (partial != null) trySend(Delta(text = partial))
            if (done) { trySend(Delta(done = true)); close() }
        }

        awaitClose()
    }

    private fun buildGemmaPrompt(messages: List<Message>): String =
        messages.joinToString("\n") { msg ->
            when (msg.role) {
                Role.system    -> "<start_of_turn>system\n${msg.content}<end_of_turn>"
                Role.user      -> "<start_of_turn>user\n${msg.content}<end_of_turn>"
                Role.assistant -> "<start_of_turn>model\n${msg.content}<end_of_turn>"
                Role.tool      -> "<start_of_turn>tool\n${msg.content}<end_of_turn>"
            }
        } + "\n<start_of_turn>model\n"
}
