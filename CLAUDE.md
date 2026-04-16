# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Freeman is a voice-driven conversational coding assistant built in Go with Bun/TypeScript sidecars. It has two modes:

1. **TTS Server** (`freeman start`): WebSocket streaming text-to-speech using Kokoro-82M via ONNX Runtime
2. **Voice Call** (`freeman call`): Interactive voice assistant — mic capture → Whisper STT → conversational LLM → Kokoro TTS → speaker playback, with the ability to spawn background coding tasks

## Build & Run

```bash
# Build
go build -o freeman ./cmd/freeman

# Dependencies
go mod download && go mod tidy
cd sidecar && bun install        # TypeScript sidecar deps

# Model setup (required before first run)
./scripts/setup_go_models.sh     # Kokoro ONNX models → ./models/
./scripts/setup_whisper_model.sh # Whisper model → ./models/whisper/

# Run TTS server
./freeman start --config config.yaml

# Run voice call
./freeman call --config config.yaml

# Health check
curl http://localhost:17000/health
```

## Testing

```bash
go test ./...                        # all tests
go test ./internal/buffer/...        # single package
go test -run TestSentenceBuffer ./internal/buffer/...  # single test
cd sidecar && bun test               # TypeScript sidecar tests
```

Live tests (`*_live_test.go`) require real audio hardware and are skipped in CI.

## Architecture

Three-layer stack:

### Layer 1: Audio I/O (`internal/audio/`)
- `capture/`: Microphone input via malgo + ring buffer
- `playback/`: Speaker output + Kokoro TTS engine integration
- `vad/`: WebRTC Voice Activity Detection (speech boundary detection)
- `stt/`: Whisper STT subprocess manager + HTTP client
- `hotkey/`: TTY-mode hotkey detection (Enter key triggers recording)
- Self-echo prevention: VAD + transcriber are muted during TTS playback via `Muter` interface

### Layer 2: Conversation (`internal/conv/`)
- `session.go`: Long-lived event loop routing between hotkey, transcriber, speaker, task manager, and conv-sidecar
- Hosts `sidecar/conv-sidecar.ts` subprocess (pi-coding-agent LLM session)
- Per turn: user speech → Whisper text → pack with task state → send to conv-sidecar → stream reply → sentence-chunk → TTS
- Barge-in: if user speaks during TTS, playback cancels and pending audio is drained
- `projectctx.go`: Extracts project context (README + manifests + dir listing, ~4KB cap) once at boot

### Layer 3: Background Tasks (`internal/conv/taskmgr.go`)
- Single-task supervisor: spawns `sidecar/sidecar.ts` per coding task
- States: `none → running → {needs_input | done | failed} → none`
- Task state injected as `[background task: ...]` prefix in next user_say prompt — LLM sees updates naturally
- Conv-sidecar has tools: `start_task`, `reply_to_task`, `cancel_task`, `task_status`

### Supporting Packages
- `internal/engine/`: Kokoro TTS wrapper — `Generate()` returns WAV bytes (API), `GeneratePCM()` returns int16 samples (local playback)
- `internal/buffer/`: SentenceBuffer — accumulates streaming text, detects sentence boundaries, handles abbreviations, long-sentence splitting (>150 chars), timeout flush (2s)
- `internal/session/`: Per-WebSocket-connection state for the TTS API mode
- `internal/sidecar/`: JSONL protocol types + subprocess client for Go↔TypeScript communication
- `internal/agent/picoding/`: Adapter layer mapping ChatAgent/TaskAgent interfaces to pi-coding-agent sidecars; includes model resolution (`sonnet`/`opus` hints → full model IDs)
- `internal/config/`: YAML config loading with defaults

### TypeScript Sidecars (`sidecar/`)
- `conv-sidecar.ts`: Long-lived conversation LLM (one per call). JSONL protocol: `init`, `user_say`, `tool_result`, `task_update`, `shutdown` → `ready`, `assistant_say`, `tool_call`, `turn_end`, `error`
- `sidecar.ts`: Per-task coding agent (one-shot). JSONL protocol: `start`, `ask_user_reply`, `cancel` → `tool_activity`, `ask_user`, `done`, `error`
- Both use `@mariozechner/pi-coding-agent` as the underlying LLM agent

## Data Flow

**TTS Server:** WebSocket → Session → SentenceBuffer → Engine (Kokoro) → WAV audio response

**Voice Call:** Mic → VAD → Whisper STT → ConvSession → conv-sidecar (LLM) → SentenceBuffer → Engine (Kokoro PCM) → Speaker

## WebSocket Protocol (TTS Server)

Connect: `ws://localhost:17000/ws/stream`

| Message Type | Purpose |
|-------------|---------|
| `init` | Start session with voice/speed |
| `text` | Send text chunk |
| `flush` | Force buffer flush |
| `end` | Close stream |

## Configuration

`config.yaml` controls: server port, model paths, TTS defaults, conversation model settings, audio devices, STT/VAD parameters, hotkey mode, transcript logging.

Key config sections: `server`, `model` (Kokoro paths), `tts` (voice/speed/buffer), `freeman.pm` (conversation model), `freeman.worker` (task agent models), `freeman.audio`, `freeman.stt` (Whisper + VAD), `freeman.hotkey`.

## Key Dependencies

- `github.com/k2-fsa/sherpa-onnx-go` — ONNX TTS engine (Kokoro-82M)
- `github.com/gen2brain/malgo` — Cross-platform audio I/O (miniaudio)
- `github.com/maxhawkins/go-webrtcvad` — Voice Activity Detection
- `github.com/gin-gonic/gin` — HTTP/WebSocket server
- `github.com/spf13/cobra` — CLI framework
- `@mariozechner/pi-coding-agent` — TypeScript LLM agent (sidecar dependency)
