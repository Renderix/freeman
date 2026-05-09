package ai.freeman.macos

import ai.freeman.audio.AudioFrame
import ai.freeman.audio.SileroVAD
import ai.freeman.conv.ConversationLoop
import ai.freeman.macos.audio.PortAudioCapture
import ai.freeman.macos.audio.PortAudioPlayback
import ai.freeman.macos.config.ConfigLoader
import ai.freeman.macos.llm.OllamaProvider
import ai.freeman.macos.stt.MoonshineStt
import ai.freeman.macos.tools.ProcessToolRunner
import ai.freeman.macos.tts.MacosTTSFactory
import ai.freeman.tasks.TaskManager
import ai.freeman.tools.ToolRegistry
import ai.freeman.wakeword.OnnxWakeWord
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking

fun main(args: Array<String>) {
    val configPath = args.firstOrNull() ?: "config.yaml"
    val config = ConfigLoader.load(configPath)

    println("[Freeman] Starting — persona: ${config.persona.name}")
    println("[Freeman] LLM: ${config.llm.provider}/${config.llm.model}")

    val llm = OllamaProvider(config.llm)
    val tts = MacosTTSFactory.create(config.tts)
    val stt = MoonshineStt(config.stt.modelPath)
    val vad = SileroVAD("${config.wakeword.modelsDir}/../silero/silero_vad.onnx")
    val wakeWord = OnnxWakeWord(
        melPath = "${config.wakeword.modelsDir}/melspectrogram.onnx",
        embeddingPath = "${config.wakeword.modelsDir}/embedding_model.onnx",
        keywordPath = "${config.wakeword.modelsDir}/hey_freeman.onnx",
        threshold = config.wakeword.threshold,
    )
    val playback = PortAudioPlayback()
    val toolRegistry = ToolRegistry().apply {
        config.tools.dirs.forEach { loadFromDir(it) }
    }
    val loop = ConversationLoop(
        config = config,
        llm = llm,
        tts = tts,
        taskManager = TaskManager(),
        toolRegistry = toolRegistry,
        toolRunner = ProcessToolRunner(),
    )

    runBlocking {
        println("[Freeman] ${config.persona.greeting}")
        val greetingPcm = tts.synthesize(config.persona.greeting)
        playback.play(greetingPcm)

        val capture = PortAudioCapture()
        var listening = false
        val utteranceBuffer = mutableListOf<FloatArray>()
        var silenceFrames = 0
        val silenceThreshold = 500 / AudioFrame.FRAME_MS  // ~16 frames

        wakeWord.start {
            println("[Freeman] Wake word detected — listening...")
            listening = true; utteranceBuffer.clear(); silenceFrames = 0; vad.reset()
        }

        capture.start { frame ->
            if ((wakeWord as OnnxWakeWord).processFrame(frame) && !listening) {
                listening = true; utteranceBuffer.clear(); silenceFrames = 0; vad.reset()
            }
            if (!listening) return@start

            utteranceBuffer.add(frame.copyOf())
            if (vad.isSpeech(frame)) silenceFrames = 0
            else {
                silenceFrames++
                if (silenceFrames >= silenceThreshold && utteranceBuffer.isNotEmpty()) {
                    listening = false
                    val audio = FloatArray(utteranceBuffer.sumOf { it.size }).also { out ->
                        var pos = 0
                        utteranceBuffer.forEach { f -> f.copyInto(out, pos); pos += f.size }
                    }
                    utteranceBuffer.clear()
                    launch {
                        val text = stt.transcribe(audio)
                        if (text.isBlank()) return@launch
                        println("[User] $text")
                        loop.handleUtterance(text)
                    }
                }
            }
        }

        println("[Freeman] Listening for wake word...")
        Runtime.getRuntime().addShutdownHook(Thread {
            capture.stop()
            println("\n[Freeman] ${config.persona.farewell}")
        })
        awaitCancellation()
    }
}
