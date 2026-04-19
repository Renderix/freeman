# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Freeman is a voice-driven conversational assistant built in Go with Bun/TypeScript sidecars. It has three modes:

1. **Voice Call** (`freeman call`): Main mode. Interactive voice assistant — mic capture → wake-word gate → Whisper STT → conversational LLM → Kokoro TTS → speaker playback. The LLM reaches the Mac through a Markdown-defined tool registry (screenshot, clipboard, web search, file ops, app control), and can spawn a background coding task via `start_task`.
2. **TTS Server** (`freeman start`): WebSocket streaming text-to-speech using Kokoro-82M via ONNX Runtime.
3. **Log Viewer** (`freeman logs`): Local HTTP UI over `~/.freeman/logs/` that turn-groups session events and surfaces tool I/O + dead-air latency.

## Build & Run

```bash
# Build
go build -o freeman ./cmd/freeman

# Dependencies
go mod download && go mod tidy
cd sidecar && bun install        # TypeScript sidecar deps

# Model setup (required before first run)
./scripts/setup_go_models.sh     # Kokoro ONNX → ./models/
./scripts/setup_whisper_model.sh # Whisper    → ./models/whisper/
./scripts/setup_wakeword_models.sh  # OpenWakeWord shared + keyword ONNX → ./models/wakeword/

# Run voice call (main mode)
./freeman call --config config.yaml

# Run TTS-only server
./freeman start --config config.yaml

# Run log viewer (opens browser on 127.0.0.1:17001)
./freeman logs

# Health check
curl http://localhost:17000/health
```

## Testing

```bash
go test ./...                           # all unit tests
go test ./internal/tools/...            # single package
go test -run TestSentenceBuffer ./internal/buffer/...  # single test
go test -tags=smoke ./internal/tools/...  # hit real OS tools (screencapture, curl, pbpaste)
go test -tags=smoke ./internal/logs/...   # parse a real session log
cd sidecar && bun test                    # TypeScript sidecar tests
```

Live tests (`*_live_test.go`) require real audio hardware and are skipped in CI. Smoke tests (`//go:build smoke`) are opt-in via the `smoke` build tag and hit real filesystems/networks.

## Architecture

Four-layer stack.

### Layer 1: Audio I/O (`internal/audio/`)
- `capture/`: Microphone input via malgo + ring buffer, multi-consumer fan-out via `Subscribe()`
- `playback/`: Speaker output + Kokoro TTS engine integration
- `vad/`: WebRTC VAD (speech boundary detection; `silence_ms: 500` is the end-of-speech debounce)
- `stt/`: Whisper STT subprocess manager + HTTP client
- `wakeword/`: OpenWakeWord ONNX detector (always-on, never muted) — detects wake / mute / stop keywords
- Self-echo prevention: VAD + transcriber are muted during TTS playback via `Muter` interface

### Layer 2: Conversation (`internal/conv/`)
- `session.go`: Long-lived event loop routing between wakeword detector, transcriber, speaker, task manager, MD tool registry, and conv-sidecar. Key building blocks:
  - `assistantBuffer`: splits streamed LLM text into spoken sentences on newlines; **early-flushes the first sentence on a clause break** (`,` `;` `:` after 30 chars) to cut first-sentence latency.
  - `runTool`: dispatches `start_task`/`reply_to_task`/`cancel_task`/`task_status` to the TaskManager, falls through to `tools.Registry` for everything else (MD tools).
  - `toolFillerPhrase` + `claimFillerSlot`: speaks a short casual acknowledgement ("one sec, lemme see") on the first MD tool call of each turn to mask tool latency. One filler per turn, random pick from a small variant pool.
  - `mdtool call` / `mdtool result` slog events record every tool invocation with args, duration, ok/error, and output preview for auditability.
- Hosts `sidecar/conv-sidecar.ts` subprocess (pi-coding-agent LLM session)
- Per turn: user speech → Whisper text → pack with task state → send to conv-sidecar → stream reply → sentence-chunk → TTS
- Barge-in: if user speaks during TTS, playback cancels and pending audio is drained
- `projectctx.go`: Extracts project context (README + manifests + dir listing, ~4KB cap) once at boot

### Layer 3: MD Tool Registry (`internal/tools/`)
- Provider-agnostic tools for the chat LLM, defined as single Markdown files with YAML frontmatter (name, description, JSON Schema parameters, runtime, timeout). The body is a shell script.
- Loader scans `./tools/` and `~/.freeman/tools/` (configurable via `freeman.tools.dirs`); later dirs override earlier ones by tool name.
- Runner executes the shell body with args as `ARG_<name>` env vars — never string-interpolated, so no shell-injection risk. Per-tool timeouts, stdout = result body.
- JSON Schema passes through unchanged to the provider's function-calling API, so tools work across Anthropic, OpenAI, local OpenAI-compatible endpoints.
- Starter pack in `./tools/`: `screenshot`, `system_stats`, `active_window`, `clipboard_read`/`_write`, `open_app`, `web_search` (keyless DuckDuckGo), `web_fetch`, `file_search` (Spotlight), `read_file`.

### Layer 4: Background Tasks (`internal/conv/taskmgr.go`)
- Single-task supervisor: spawns `sidecar/sidecar.ts` per coding task
- States: `none → running → {needs_input | done | failed} → none`
- Task state injected as `[background task: ...]` prefix in next user_say prompt — LLM sees updates naturally
- Conv-sidecar exposes hardcoded task tools: `start_task`, `reply_to_task`, `cancel_task`, `task_status`. MD tools from the registry are registered dynamically on top.

### Supporting Packages
- `internal/engine/`: Kokoro TTS wrapper — `Generate()` returns WAV bytes (API), `GeneratePCM()` returns int16 samples (local playback). Voice names → sherpa-onnx speaker IDs via an explicit table tied to the bundled `kokoro-en-v0_19` model (11 speakers).
- `internal/buffer/`: `SentenceBuffer` used by the WebSocket TTS server path (NOT the voice call, which uses `assistantBuffer` in `internal/conv/session.go`).
- `internal/session/`: Per-WebSocket-connection state for the TTS API mode.
- `internal/sidecar/`: JSONL protocol types + subprocess client for Go↔TypeScript communication.
- `internal/agent/`: `ChatAgent` / `TaskAgent` interfaces + `ToolSpec` type (provider-agnostic).
- `internal/agent/picoding/`: Adapter layer mapping those interfaces to pi-coding-agent sidecars; includes model resolution (`sonnet`/`opus` hints → full model IDs).
- `internal/config/`: YAML config loading with defaults.
- `internal/logs/`: slog-text parser + turn grouper + embedded HTTP server for `freeman logs`.

### TypeScript Sidecars (`sidecar/`)
- `conv-sidecar.ts`: Long-lived conversation LLM (one per call). JSONL protocol: `init` (carries dynamic tool specs), `user_say`, `tool_result`, `task_update`, `shutdown` → `ready`, `assistant_say`, `tool_call`, `turn_end`, `error`.
- `sidecar.ts`: Per-task coding agent (one-shot). JSONL protocol: `start`, `ask_user_reply`, `cancel` → `tool_activity`, `ask_user`, `done`, `error`.
- Both use `@mariozechner/pi-coding-agent` as the underlying LLM agent.

## Data Flow

**Voice Call:** Mic → OpenWakeWord (wake word) → VAD → Whisper STT → ConvSession → conv-sidecar (LLM) → [optional MD tool or `start_task`] → assistantBuffer (early-flush) → Engine (Kokoro PCM) → Speaker

**TTS Server:** WebSocket → Session → SentenceBuffer → Engine (Kokoro) → WAV audio response

**Logs Viewer:** `freeman logs` → internal/logs parser → HTTP server → embedded HTML UI (transcript + Gantt + tool details)

## WebSocket Protocol (TTS Server)

Connect: `ws://localhost:17000/ws/stream`

| Message Type | Purpose |
|-------------|---------|
| `init` | Start session with voice/speed |
| `text` | Send text chunk |
| `flush` | Force buffer flush |
| `end` | Close stream |

## Configuration

`config.yaml` controls: server port, model paths, TTS defaults, conversation model settings, audio devices, STT/VAD parameters, persona settings, transcript logging, and tool directories.

Key config sections:
- `server`, `model`, `tts` — WebSocket port, Kokoro paths, default voice (currently `bm_george`).
- `freeman.pm` — chat model + auth.
- `freeman.worker` — background task agent models.
- `freeman.audio`, `freeman.stt` — device selection, Whisper + VAD (`silence_ms: 500`).
- `freeman.tools.dirs` — extra dirs for MD tools; empty = default (`./tools`, `~/.freeman/tools`).
- `persona` — name, greeting, farewell, behavioural `rules` appended to the system prompt, wakeword model paths and thresholds.

## Key Dependencies

- `github.com/k2-fsa/sherpa-onnx-go` — ONNX TTS engine (Kokoro-82M)
- `github.com/gen2brain/malgo` — Cross-platform audio I/O (miniaudio)
- `github.com/maxhawkins/go-webrtcvad` — Voice Activity Detection
- `github.com/yalue/onnxruntime_go` — ONNX Runtime for OpenWakeWord
- `github.com/gin-gonic/gin` — HTTP/WebSocket server
- `github.com/spf13/cobra` — CLI framework
- `gopkg.in/yaml.v3` — YAML config + tool frontmatter parsing
- `@mariozechner/pi-coding-agent` — TypeScript LLM agent (sidecar dependency)
