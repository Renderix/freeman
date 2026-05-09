# Running Freeman on macOS

## Prerequisites

| Requirement | Version | Install |
|-------------|---------|---------|
| JDK 21 | 21+ | `brew install --cask temurin@21` |
| Ollama | latest | `brew install ollama` |
| PortAudio | any | `brew install portaudio` |

> **Apple Silicon (M1/M2/M3/M4):** everything works natively — no Rosetta needed.
> **Intel Mac:** supported; the setup script picks the right native JNI tarball automatically.

---

## 1. Clone the repo

```bash
git clone https://github.com/Renderix/freeman.git
cd freeman
```

## 2. Download native libraries

sherpa-onnx ships its JNI dylibs outside of Maven. Fetch them once:

```bash
./scripts/setup_kotlin_libs.sh
```

This downloads into `macos/libs/` — the run command points `java.library.path` there.

## 3. Download ML models

```bash
./scripts/setup_models.sh
```

Downloads:
- `models/kokoro/` — Kokoro-82M ONNX TTS (~150 MB)
- `models/moonshine/` — Moonshine Tiny ONNX STT (~27 MB)
- `models/silero/silero_vad.onnx` — Silero VAD (~2 MB)
- `models/wakeword/` — OpenWakeWord shared models

> **Wake word model:** the script downloads the shared OpenWakeWord models but you need
> `models/wakeword/hey_freeman.onnx` separately. You can train a custom keyword at
> [github.com/dscripka/openWakeWord](https://github.com/dscripka/openWakeWord), or skip
> wake-word detection and call `wakeWord.start {}` directly for testing.

## 4. Pull the LLM

```bash
brew services start ollama      # start Ollama as a background service
ollama pull gemma4:e4b          # ~5 GB download
```

## 5. Build

```bash
./gradlew :macos:macosJar
```

Produces `macos/build/libs/macos-macos.jar` (~113 MB fat JAR with all dependencies).

## 6. Run

```bash
java -Djava.library.path=macos/libs \
     -jar macos/build/libs/macos-macos.jar \
     config.yaml
```

Say **"hey Freeman"** to trigger the wake word, then speak your query.

---

## Configuration

Edit `config.yaml` to change persona, model, voice, or audio device:

```yaml
persona:
  name: Freeman
  greeting: "Hey, I'm Freeman. What can I do for you?"

llm:
  model: gemma4:e4b          # any model loaded in Ollama
  baseUrl: http://localhost:11434

tts:
  voice: bm_george           # af_heart | bm_george | af_bella | am_adam | …
  speed: 1.0

wakeword:
  threshold: 0.5             # lower = more sensitive
```

## Custom voice (zero-shot cloning)

Point `tts.customVoicePath` at a clean 5–10 second WAV clip of the target voice:

```yaml
tts:
  customVoicePath: /path/to/reference.wav
```

Freeman will extract a speaker embedding from the clip and use it for all TTS output. No training required.

---

## Troubleshooting

**`UnsatisfiedLinkError: no sherpa-onnx-jni`**
Run `./scripts/setup_kotlin_libs.sh` — the dylibs are missing from `macos/libs/`.

**`Error: ollama server not responding`**
Start the server: `brew services start ollama` or `ollama serve`.

**`The model requires a newer version of Ollama`**
Update: `brew upgrade ollama && brew services restart ollama`.

**No audio input / output**
Check macOS System Settings → Privacy → Microphone. Grant access to your terminal app.
