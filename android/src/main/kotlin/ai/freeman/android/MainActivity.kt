package ai.freeman.android

import ai.freeman.audio.AudioFrame
import ai.freeman.audio.SileroVAD
import ai.freeman.android.audio.AndroidAudioCapture
import ai.freeman.android.audio.AudioTrackPlayback
import ai.freeman.android.llm.LiteRtProvider
import ai.freeman.android.stt.NoOpStt
import ai.freeman.android.tools.AndroidToolRunner
import ai.freeman.android.tts.AndroidTTSFactory
import ai.freeman.config.FreemanConfig
import ai.freeman.conv.ConversationLoop
import ai.freeman.tasks.TaskManager
import ai.freeman.tools.ToolRegistry
import ai.freeman.wakeword.OnnxWakeWord
import android.Manifest
import android.app.Activity
import android.content.pm.PackageManager
import android.os.Bundle
import android.widget.TextView
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

class MainActivity : Activity() {
    private val scope = CoroutineScope(Dispatchers.Main + SupervisorJob())
    private lateinit var statusLabel: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        statusLabel = TextView(this).apply { text = "Freeman starting..." }
        setContentView(statusLabel)

        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.RECORD_AUDIO), 1)
            return
        }
        startFreeman()
    }

    override fun onRequestPermissionsResult(code: Int, perms: Array<String>, results: IntArray) {
        if (results.firstOrNull() == PackageManager.PERMISSION_GRANTED) startFreeman()
    }

    private fun startFreeman() {
        val config = FreemanConfig()
        val modelBase = filesDir.absolutePath

        val llm = LiteRtProvider(modelPath = "$modelBase/models/gemma4-e4b.task", context = this)
        val tts = AndroidTTSFactory.create(this, config.tts)
        val vad = SileroVAD("$modelBase/models/silero/silero_vad.onnx")
        val wakeWord = OnnxWakeWord(
            melPath = "$modelBase/models/wakeword/melspectrogram.onnx",
            embeddingPath = "$modelBase/models/wakeword/embedding_model.onnx",
            keywordPath = "$modelBase/models/wakeword/hey_freeman.onnx",
            threshold = config.wakeword.threshold,
        )
        val playback = AudioTrackPlayback()
        val loop = ConversationLoop(
            config = config, llm = llm, tts = tts,
            taskManager = TaskManager(), toolRegistry = ToolRegistry(),
            toolRunner = AndroidToolRunner(),
        )

        statusLabel.text = config.persona.greeting
        scope.launch { playback.play(tts.synthesize(config.persona.greeting)) }

        var listening = false
        val utteranceBuffer = mutableListOf<FloatArray>()
        var silenceFrames = 0
        val silenceThreshold = 500 / AudioFrame.FRAME_MS

        wakeWord.start {
            runOnUiThread { statusLabel.text = "Listening..." }
            listening = true; utteranceBuffer.clear(); silenceFrames = 0; vad.reset()
        }

        val capture = AndroidAudioCapture()
        capture.start { frame ->
            if ((wakeWord as OnnxWakeWord).processFrame(frame) && !listening) {
                listening = true; utteranceBuffer.clear(); silenceFrames = 0; vad.reset()
                runOnUiThread { statusLabel.text = "Listening..." }
            }
            if (!listening) return@start
            utteranceBuffer.add(frame.copyOf())
            if (vad.isSpeech(frame)) silenceFrames = 0
            else {
                silenceFrames++
                if (silenceFrames >= silenceThreshold && utteranceBuffer.isNotEmpty()) {
                    listening = false
                    val audio = FloatArray(utteranceBuffer.sumOf { it.size }).also { out ->
                        var pos = 0; utteranceBuffer.forEach { f -> f.copyInto(out, pos); pos += f.size }
                    }
                    utteranceBuffer.clear()
                    scope.launch {
                        runOnUiThread { statusLabel.text = "Thinking..." }
                        loop.handleUtterance("[audio input]")
                        runOnUiThread { statusLabel.text = "Say 'hey freeman'..." }
                    }
                }
            }
        }
        statusLabel.text = "Say 'hey freeman'..."
    }

    override fun onDestroy() { super.onDestroy(); scope.cancel() }
}
