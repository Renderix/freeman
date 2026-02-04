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

## License

Apache 2.0
