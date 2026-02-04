# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Freeman is a high-performance, real-time text-to-speech (TTS) streaming server using Kokoro-82M and ONNX Runtime. Go-based WebSocket server that converts text streams to audio with <100ms latency.

## Build Commands

```bash
# Build from source
go build -o freeman ./cmd/freeman

# Download/verify dependencies
go mod download
go mod tidy

# Setup models (required before first run)
./scripts/setup_go_models.sh
```

## Running the Server

```bash
# Start server (default port 17000)
./freeman start --config config.yaml

# With explicit flags
./freeman start --models ./models --port 17000

# Health check
curl http://localhost:17000/health
```

## Testing

No test infrastructure exists yet. Tests should be added as `*_test.go` files alongside implementation.

```bash
# When tests exist:
go test ./...
go test ./internal/buffer/...  # single package
```

## Architecture

```
cmd/freeman/main.go          # Entry point, Cobra CLI (start, version commands)
internal/
  api/server.go              # Gin HTTP server, WebSocket handler at /ws/stream
  config/config.go           # YAML config loading, defaults
  engine/engine.go           # Sherpa-ONNX Kokoro TTS wrapper, WAV encoding
  buffer/buffer.go           # Sentence accumulation, boundary detection, timeout flush
  session/session.go         # Per-connection state, audio processing pipeline
models/                      # ONNX model, voices.bin, tokens.txt, espeak-ng-data
```

**Data Flow:** WebSocket → Session → Buffer (sentence accumulation) → Engine (TTS) → WAV audio response

## WebSocket Protocol

Connect: `ws://localhost:17000/ws/stream`

| Message Type | Purpose |
|-------------|---------|
| `init` | Start session with voice/speed |
| `text` | Send text chunk |
| `flush` | Force buffer flush |
| `end` | Close stream |

## Key Dependencies

- `github.com/k2-fsa/sherpa-onnx-go` - ONNX TTS engine (Kokoro)
- `github.com/gin-gonic/gin` - HTTP framework
- `github.com/gorilla/websocket` - WebSocket support
- `github.com/spf13/cobra` - CLI framework

## Configuration

`config.yaml` controls: server port, model paths, default voice (`af_heart`), speed, sentence limits, buffer timeout (2.0s).

24 voices available: American female (af_*), American male (am_*), British female (bf_*), British male (bm_*).
