# Freeman TTS (Go Edition)

High-performance, real-time text-to-speech streaming server using Kokoro-82M and ONNX Runtime.

```
LLM Text Stream  →  Freeman (Go)  →  Audio Stream
     "Hello"            ↓           🔊 <100ms
```

## Features

- **Blazing Fast**: <100ms latency from sentence to audio.
- **Ultra-Lean**: Single **20MB** binary (down from 1GB in Python).
- **Go Powered**: High-concurrency WebSocket support using Goroutines.
- **Apple Silicon Optimized**: Native performance on M1/M2/M3 Macs.

## Quick Start

### 1. Prerequisites (macOS)

```bash
brew install espeak-ng
```

### 2. Setup Models

The Go version uses ONNX models. Run the helper script to download them:

```bash
chmod +x scripts/setup_go_models.sh
./scripts/setup_go_models.sh
```

### 3. Build & Run

```bash
# Build binary
go build -o freeman ./cmd/freeman

# Start server (configure models path and port in config.yaml)
./freeman start --config config.yaml
```

## API Usage

### WebSocket
Connect to `ws://localhost:17000/ws/stream`.

**Payloads:**
- `{"type": "init", "voice": "af_heart", "speed": 1.0}`
- `{"type": "text", "chunk": "Hello world.", "is_final": true}`

## Plan 2 — Voice I/O prerequisites

`freeman call` (without `--fake-audio`) requires:

1. **whisper.cpp `whisper-server`** — install via Homebrew or build from source:
   ```
   brew install whisper-cpp          # provides whisper-server in PATH
   # or
   git clone https://github.com/ggerganov/whisper.cpp
   cd whisper.cpp && make server
   # copy the resulting server binary onto your PATH or set
   # freeman.stt.server_path in config.yaml
   ```

2. **Whisper model file** — download with the provided script:
   ```
   ./scripts/setup_whisper_model.sh
   ```

3. **Microphone permission** — on first run, macOS will prompt to grant Freeman
   access. If you decline, Freeman exits with a message pointing at
   `System Settings → Privacy & Security → Microphone`.

After that, `./freeman call`, wait for `freeman: ready`, and press Enter.

## License

Apache 2.0
