# Freeman

A local voice assistant for macOS and Android. Speak to it, it thinks, it talks back.

- **macOS** — Claude or Ollama LLM · Kokoro TTS · Whisper STT · Silero VAD · OpenWakeWord
- **Android** — Gemma 4 E4B via LiteRT · Kokoro TTS · Moonshine STT · Silero VAD

---

## macOS — Quick Start

### 1. Download

Grab the latest `freeman-vX.X.X-macos.zip` from the [Releases](https://github.com/Renderix/freeman/releases) page and unzip it:

```bash
unzip freeman-*-macos.zip
cd freeman-*-macos
```

### 2. Set up

```bash
./setup.sh
```

The script will ask for your LLM provider and persona settings, download the required ML models (~350 MB), and write `config.yaml`.

### 3. Run

```bash
./run.sh
```

Say **"hey Freeman"** (if wake word is enabled) or speak directly after the app starts.

---

## LLM Options

| Provider | Requires | Notes |
|---|---|---|
| `claude` | `ANTHROPIC_API_KEY` env var | Best quality, cloud-based |
| `ollama` | [Ollama](https://ollama.com) running locally | Fully local, no API key |

For Ollama: `brew install ollama && ollama pull gemma4:e4b`

---

## macOS — Build from Source

```bash
git clone https://github.com/Renderix/freeman.git
cd freeman

./scripts/setup_kotlin_libs.sh   # download sherpa-onnx JARs + dylibs
./scripts/setup_models.sh        # download ML models
./setup.sh                       # configure
./gradlew :macos:macosJar        # build
./run.sh                         # run
```

---

## Platform Guides

- [macOS — detailed guide](docs/macos.md)
- [Android — build and sideload](docs/android.md)
