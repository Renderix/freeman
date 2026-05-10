package ai.freeman.macos

import ai.freeman.audio.AudioFrame
import ai.freeman.audio.SileroVAD
import ai.freeman.conv.ConversationLoop
import ai.freeman.macos.audio.AVFoundationCapture
import ai.freeman.macos.audio.AVFoundationPlayback
import ai.freeman.macos.config.ConfigLoader
import ai.freeman.macos.llm.ClaudeProvider
import ai.freeman.macos.llm.OllamaProvider
import ai.freeman.macos.memory.SqliteMemoryStore
import ai.freeman.macos.stt.MoonshineStt
import ai.freeman.macos.stt.WhisperStt
import ai.freeman.macos.tools.ProcessToolRunner
import ai.freeman.macos.tts.MacosTTSFactory
import ai.freeman.tasks.TaskManager
import ai.freeman.tools.ToolRegistry
import ai.freeman.wakeword.OnnxWakeWord
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlin.math.abs

private fun ts(): String {
    val t = java.time.LocalTime.now()
    return "%02d:%02d:%02d.%03d".format(t.hour, t.minute, t.second, t.nano / 1_000_000)
}

fun main(args: Array<String>) {
    val configPath = args.firstOrNull() ?: "config.yaml"
    val config = ConfigLoader.load(configPath)

    println("[Freeman] Starting — persona: ${config.persona.name}")
    println("[Freeman] LLM: ${config.llm.provider}/${config.llm.model}")

    val llm = when (config.llm.provider) {
        "claude" -> ClaudeProvider(config.llm)
        else     -> OllamaProvider(config.llm)
    }
    val tts = MacosTTSFactory.create(config.tts)
    val stt = when (config.stt.provider) {
        "whisper" -> WhisperStt(config.stt.modelPath)
        else      -> MoonshineStt(config.stt.modelPath)
    }

    val vad: SileroVAD? = if (config.wakeword.enabled)
        SileroVAD("${config.wakeword.modelsDir}/../silero/silero_vad.onnx") else null
    val wakeWord: OnnxWakeWord? = if (config.wakeword.enabled)
        OnnxWakeWord(
            melPath = "${config.wakeword.modelsDir}/melspectrogram.onnx",
            embeddingPath = "${config.wakeword.modelsDir}/embedding_model.onnx",
            keywordPath = "${config.wakeword.modelsDir}/hey_freeman.onnx",
            threshold = config.wakeword.threshold,
        ) else null

    val playback = AVFoundationPlayback()
    val toolRegistry = ToolRegistry().apply {
        config.tools.dirs.forEach { loadFromDir(it) }
    }

    val memoryStore = if (config.memory.enabled) {
        val dbPath = config.memory.dbPath.replace("~", System.getProperty("user.home"))
        java.io.File(dbPath).parentFile?.mkdirs()
        SqliteMemoryStore(dbPath = dbPath)
    } else null

    val loop = ConversationLoop(
        config = config,
        llm = llm,
        tts = tts,
        taskManager = TaskManager(),
        toolRegistry = toolRegistry,
        toolRunner = ProcessToolRunner(),
        memoryStore = memoryStore,
        onSpeak = { audio -> playback.play(audio) },
    )

    runBlocking {
        // ── Pipeline channels ────────────────────────────────────────────────
        // mic frames (512 samples @ 16 kHz = 32 ms each; 128 frames ≈ 4 s buffer)
        val micChannel       = Channel<FloatArray>(128)
        // complete utterances ready for STT
        val utteranceChannel = Channel<FloatArray>(4)
        // transcribed text ready for LLM
        val textChannel      = Channel<String>(Channel.UNLIMITED)

        val capture = AVFoundationCapture()

        // ── Stage 1: mic → micChannel ────────────────────────────────────────
        // AVFoundation callback drops frames rather than blocking the audio thread.
        capture.start { frame -> micChannel.trySend(frame) }

        // ── Stage 2: micChannel → VAD → utteranceChannel ─────────────────────
        launch {
            // Two-tier end-of-utterance detection:
            //   short silence (480 ms) only fires after substantial speech (≥1500 ms)
            //   long  silence (1000 ms) always fires — catches short commands too
            val shortSilence = 480  / AudioFrame.FRAME_MS  // ~15 frames
            val longSilence  = 1000 / AudioFrame.FRAME_MS  // ~31 frames
            val substantialSpeechMs = 1500
            var listening = !config.wakeword.enabled
            val utteranceBuffer = mutableListOf<FloatArray>()
            var silenceFrames = 0
            var voiceActive = false
            var voiceStartTs = 0L

            // Wake-word callback runs on its own thread; use a conflated channel
            // to safely signal the VAD coroutine without blocking.
            val wakeChannel = Channel<Unit>(Channel.CONFLATED)
            wakeWord?.start {
                println("[Freeman] Wake word detected — listening...")
                wakeChannel.trySend(Unit)
            }

            for (frame in micChannel) {
                // Drain any pending wake events
                if (wakeChannel.tryReceive().isSuccess && !listening) {
                    listening = true; utteranceBuffer.clear(); silenceFrames = 0; vad?.reset()
                }

                if (wakeWord != null && wakeWord.processFrame(frame) && !listening) {
                    listening = true; utteranceBuffer.clear(); silenceFrames = 0; vad?.reset()
                }
                if (!listening) continue

                val isSpeech = vad?.isSpeech(frame)
                    ?: (frame.maxOrNull()?.let { abs(it) > 0.04f } == true)

                if (isSpeech) {
                    if (!voiceActive) {
                        voiceActive = true
                        voiceStartTs = System.currentTimeMillis()
                        println("[Freeman] ${ts()} voice start")
                    }
                    silenceFrames = 0
                    utteranceBuffer.add(frame.copyOf())
                } else if (voiceActive) {
                    utteranceBuffer.add(frame.copyOf())
                    silenceFrames++
                    val spokenMs = System.currentTimeMillis() - voiceStartTs
                    val threshold = if (spokenMs >= substantialSpeechMs) shortSilence else longSilence
                    if (silenceFrames >= threshold) {
                        val durationMs = System.currentTimeMillis() - voiceStartTs
                        println("[Freeman] ${ts()} voice end (${durationMs}ms) → STT")
                        voiceActive = false
                        listening = !config.wakeword.enabled
                        val audio = FloatArray(utteranceBuffer.sumOf { it.size }).also { out ->
                            var pos = 0
                            utteranceBuffer.forEach { f -> f.copyInto(out, pos); pos += f.size }
                        }
                        utteranceBuffer.clear()
                        silenceFrames = 0
                        utteranceChannel.send(audio)
                    }
                }
            }
        }

        // ── Stage 3: utteranceChannel → STT → textChannel ───────────────────
        launch(Dispatchers.Default) {
            for (audio in utteranceChannel) {
                val text = stt.transcribe(audio)
                if (text.isBlank()) {
                    println("[Freeman] ${ts()} → listening (blank STT)")
                } else {
                    println("[User]    ${ts()} $text")
                    textChannel.send(text)
                }
            }
        }

        // ── Stage 4: textChannel → LLM (sequential, one turn at a time) ─────
        launch {
            for (text in textChannel) {
                loop.handleUtterance(text)
                println("[Freeman] ${ts()} → listening")
            }
        }

        // Play greeting after capture.start() so AVAudioEngine is initialized
        println("[Freeman] ${config.persona.greeting}")
        val greetingPcm = tts.synthesize(config.persona.greeting)
        playback.play(greetingPcm)

        if (config.wakeword.enabled)
            println("[Freeman] Listening for wake word...")
        else
            println("[Freeman] Listening...")

        Runtime.getRuntime().addShutdownHook(Thread {
            capture.stop()
            memoryStore?.close()
            println("\n[Freeman] ${config.persona.farewell}")
        })
        awaitCancellation()
    }
}
