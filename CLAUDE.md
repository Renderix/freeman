# Freeman — Kotlin Multiplatform Voice Assistant

## Project Structure

```
shared/   — common Kotlin: interfaces, VAD, wake word, conversation loop, tools
macos/    — macOS JVM target: Ollama LLM, sherpa-onnx TTS/STT, PortAudio I/O
android/  — Android target: LiteRT-LM, sherpa-onnx TTS, AudioRecord/AudioTrack
scripts/  — setup_kotlin_libs.sh (native libs), setup_models.sh (ML models)
```

## Build & Run (macOS)

```bash
# One-time: download sherpa-onnx JARs and dylibs
./scripts/setup_kotlin_libs.sh

# One-time: download Kokoro, Moonshine, Silero VAD, OpenWakeWord models
./scripts/setup_models.sh

# Build fat JAR
./gradlew :macos:macosJar

# Run (Ollama must be running)
ollama serve &
java -Djava.library.path=macos/libs \
     -jar macos/build/libs/macos-macos.jar config.yaml
```

## Testing

```bash
./gradlew :shared:macosTest       # all shared unit tests
./gradlew :macos:compileKotlinMacos   # verify macos module compiles
```

## Key Dependencies

- `com.microsoft.onnxruntime:onnxruntime` — Silero VAD + OpenWakeWord inference
- sherpa-onnx 1.13.1 (local JARs in `macos/libs/`, `android/libs/`) — Kokoro TTS + Moonshine STT
- Ktor OkHttp client — Ollama streaming REST
- `kotlinx.serialization` — config + JSON

## Architecture

- `shared/llm/LLMProvider` — `Flow<Delta>` streaming interface; Ollama on Mac, LiteRT on Android
- `shared/tts/TTS` — `suspend synthesize(text, voice?) → FloatArray`
- `shared/stt/STT` — `suspend transcribe(audio) → String`
- `shared/audio/VAD` — `isSpeech(frame) → Boolean` (Silero, stateful)
- `shared/wakeword/WakeWord` — `start(onDetected)` / `stop()`
- `shared/conv/ConversationLoop` — routes utterance → LLM → tools → TTS
- `shared/tasks/TaskManager` — parallel background agent tasks (ConcurrentHashMap)
