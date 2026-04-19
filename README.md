# Freeman

A voice-driven conversational coding assistant in Go with Bun/TypeScript
sidecars. Wake-word activated, low-latency speech I/O, and an
extensible Markdown-defined tool registry that lets the assistant act on
your Mac — screenshots, clipboard, web search, app control, file ops —
without any vendor lock-in.

Two modes:

- **`freeman call`** — interactive voice assistant. Mic → Whisper STT →
  Claude (Haiku by default) → Kokoro TTS → speaker. Tool-using,
  barge-in-aware, with optional background coding-agent spawn via
  `start_task`.
- **`freeman start`** — standalone WebSocket streaming TTS server
  (Kokoro-82M, <100ms latency).

Plus **`freeman logs`** — a local HTML viewer over `~/.freeman/logs/`
that turn-groups sessions, pairs tool calls with results, and surfaces
dead-air latency as a Gantt strip.

```
mic → wakeword → VAD → Whisper → Claude → Kokoro → 🔊
                                  │
                                  └─→ tools (MD registry)
                                  └─→ start_task → Sonnet/Opus
```

## Quick start

### Prerequisites (macOS)

```bash
brew install espeak-ng whisper-cpp bun
```

### Models

```bash
./scripts/setup_go_models.sh        # Kokoro TTS → ./models/
./scripts/setup_whisper_model.sh    # Whisper STT → ./models/whisper/
./scripts/setup_wakeword_models.sh  # OpenWakeWord → ./models/wakeword/
```

### Build

```bash
go build -o freeman ./cmd/freeman
cd sidecar && bun install && cd ..
```

### Run

```bash
./freeman call --config config.yaml   # voice mode (main)
./freeman start --config config.yaml  # TTS-only WebSocket server
./freeman logs                        # HTML session-log viewer
```

On first run macOS will prompt for Microphone + Screen Recording
permission — the latter is needed by the `screenshot` tool.

## Tools

The chat LLM reaches the outside world through Markdown-defined tools.
Each file in `./tools/` (and `~/.freeman/tools/` for user overrides) is
a tool spec: YAML frontmatter declares name, description, and JSON
Schema parameters; the body is a shell script.

```markdown
---
name: screenshot
description: Capture the main screen and return the PNG path.
runtime: shell
timeout_ms: 5000
parameters:
  type: object
  properties: {}
---
set -euo pipefail
OUT="${TMPDIR:-/tmp}/freeman-shot-${UUID}.png"
screencapture -x -m "$OUT"
echo "$OUT"
```

Args are passed as `ARG_<name>` environment variables (no shell
injection risk). Tools ship JSON Schema to the LLM via the standard
function-calling API, so they work identically across Anthropic,
OpenAI, or local OpenAI-compatible endpoints — swap providers without
touching tools.

Default starter pack: `screenshot`, `system_stats`, `active_window`,
`clipboard_read`/`_write`, `open_app`, `web_search` (DuckDuckGo,
keyless), `web_fetch`, `file_search` (Spotlight), `read_file`.

## Voice-call features

- **Wake words** via OpenWakeWord — `Horus`, `standby` (mute), and a
  stop word. Config and retraining scripts under `scripts/`.
- **Barge-in**: speaking during TTS cancels playback and drains pending
  audio.
- **First-sentence early-flush**: the first reply sentence streams to
  TTS on the first clause break (comma/colon/semicolon after 30 chars)
  instead of waiting for the period, cutting perceived latency.
- **Per-tool fillers**: a short acknowledgement ("one sec, lemme see")
  plays while a tool runs, masking the LLM+tool round-trip. One filler
  per turn, rotated from 3–4 variants so it doesn't feel scripted.
- **Tight VAD**: 500 ms silence threshold for end-of-speech detection.
- **Full per-session logs** under `~/.freeman/logs/YYYY-MM-DD/` with
  timestamped events for every `heard`/`speaking`/`mdtool`/wake event.

## Configuration

`config.yaml` controls everything runtime-configurable:

- `server`, `model`, `tts` — WebSocket port, Kokoro paths, default voice.
- `freeman.pm` — chat model (default: `claude-haiku-4-5`, subscription auth).
- `freeman.worker` — background task agent models.
- `freeman.audio`, `freeman.stt` — device selection, Whisper/VAD params.
- `freeman.tools.dirs` — extra dirs to scan for MD tools (defaults to
  `./tools` + `~/.freeman/tools`).
- `persona` — name, greeting, behavioural rules, wakeword models.

## Testing

```bash
go test ./...                            # all unit tests
go test -tags=smoke ./internal/tools/... # hit real tools (screencapture, curl)
cd sidecar && bun test                   # TypeScript sidecar tests
```

Files named `*_live_test.go` touch real audio hardware and are skipped
without the relevant tag.

## License

Apache 2.0
