# Wake/Kill Word Detection + Persona System

## Overview

Replace the Enter-key hotkey with Porcupine wake word detection, enabling hands-free voice activation and deactivation. Introduce a persona system so the wake/kill words, greeting, personality traits, and response rules are configurable per-persona in YAML.

Persona: **Horus** (first persona on the Freeman platform).

## Keywords

| Phrase | Action | Behavior |
|--------|--------|----------|
| "Horus" | Wake | Unmute VAD+STT, enter conversation mode, TTS greeting |
| "Mute" | Mute | Silence TTS, mute VAD+STT, return to idle listening |
| "Horus stop" | Shutdown | Kill TTS, kill background tasks, teardown, exit process |

All three keywords are detected via Porcupine `.ppn` model files created through the Picovoice Console. Free tier supports exactly 3 custom keywords.

Porcupine is **never muted** — it processes raw capture frames at all times, including during TTS playback. This ensures "Mute" and "Horus stop" respond instantly even while the assistant is speaking.

## New Package: `internal/audio/wakeword/`

Wraps the Porcupine Go SDK (`github.com/Picovoice/porcupine/binding/go`).

### Types

```go
type KeywordKind int
const (
    KeywordWake KeywordKind = iota  // "Horus"
    KeywordMute                      // "Mute"
    KeywordStop                      // "Horus stop"
)

type Config struct {
    AccessKey     string
    KeywordPaths  []string    // 3 .ppn file paths [wake, mute, stop]
    Sensitivities []float32   // per-keyword sensitivity [0,1]
}
```

### Interface

```go
func NewDetector(cfg Config) (*Detector, error)  // Init Porcupine engine
func (d *Detector) Run(frames <-chan []int16)     // Goroutine: process frames, emit events
func (d *Detector) Events() <-chan KeywordKind    // Buffered channel (capacity 4)
func (d *Detector) Stop()                         // Cleanup, Porcupine.Delete()
```

### Frame Handling

- Porcupine requires 16-bit mono PCM at its own `SampleRate` (16kHz) and `FrameLength`
- Our capture produces 320-sample frames at 16kHz — if Porcupine's `FrameLength` differs, the detector handles reframing internally by accumulating samples

### Keyword Model Files

- Stored in `models/wakeword/` (e.g., `horus.ppn`, `mute.ppn`, `horus-stop.ppn`)
- Created via Picovoice Console (https://console.picovoice.ai/)
- Not checked into git — downloaded or generated during setup

## Capture Device: Multi-Consumer Fan-Out

`capture.Device` currently exposes a single frame channel. Both VAD and the wakeword detector need independent frame streams.

### Changes to `capture/device.go`

```go
func (d *Device) Subscribe() <-chan []int16
func (d *Device) Unsubscribe(ch <-chan []int16)
```

- The drain goroutine pushes each frame to all subscribers (non-blocking, drop if consumer lags)
- VAD subscribes for speech endpointing (mutable as before — muting happens inside VAD)
- Wakeword detector subscribes separately (never muted, always receives frames)
- Replaces the current single `Frames()` channel

## Session State Machine

Three modes replace the current hotkey-gated flow.

```
[Idle] ──("Horus" detected)──> [Awake]
   ^                              |
   |                              |-- VAD + STT unmuted, conversation active
   |                              |-- normal turn flow (utterance -> LLM -> TTS)
   |                              |
   └──("Mute" detected)──────────┘
                                  |
                           ("Horus stop" detected)
                                  |
                                  v
                              [Shutdown]
                         (teardown everything, exit)
```

### Changes to `conv/session.go`

- Remove the `hotkeys` case from the select loop
- Add `wakewordEvents` case with switch on `KeywordKind`:
  - `KeywordWake`: if not already awake, unmute VAD+STT, call `greet()`, set awake
  - `KeywordMute`: cancel in-flight TTS, mute VAD+STT, set idle
  - `KeywordStop`: call `shutdown()` — cancel TTS, cancel background task, close chat agent, close audio, exit
- `KeywordMute` and `KeywordStop` are handled even when `convBusy=true` (they interrupt mid-conversation)
- On startup, session begins in idle mode (VAD+STT start muted, waiting for "Horus")

### Session Deps Change

`conv.Deps` struct: remove `Hotkeys <-chan struct{}`, add `WakewordEvents <-chan wakeword.KeywordKind`.

## Call Command Wiring (`cmd/freeman/call.go`)

### Initialization Sequence

1. Audio context + Kokoro engine + Whisper — unchanged
2. Capture device starts — unchanged, now uses `Subscribe()` for consumers
3. **Wakeword detector created** — subscribes to capture frames, initialized with `.ppn` paths from config
4. VAD created — subscribes to capture frames (separate subscription), **starts muted**
5. STT transcriber — **starts muted**
6. Speaker — MultiMuter still gates VAD+STT (unchanged)
7. **No hotkey** — removed entirely
8. Session created with `wakeword.Detector` instead of `hotkey.Hotkey` in deps

### Startup Behavior

1. All audio initializes, VAD + STT start muted
2. Wakeword detector starts consuming frames immediately
3. Print to stderr: `Horus listening... say "Horus" to begin`
4. No greeting, no TTS — silent listening until wake word
5. On "Horus": unmute VAD+STT, TTS plays greeting (e.g., "I'm here"), enter conversation
6. On "Mute": mute VAD+STT, print `Muted... say "Horus" to resume`, no TTS
7. On "Horus stop": print `Shutting down...`, teardown, exit

## Removals

- **Delete `internal/audio/hotkey/`** — entire package
- **Remove hotkey from session deps and call.go** — no TTY raw mode, no terminal restore
- **Drop `golang.org/x/term` dependency** — only used by hotkey

## Persona Configuration

Top-level `persona:` key in `config.yaml`. Contains all persona-specific settings including identity, behavior rules, and wake word config.

```yaml
persona:
  name: "Horus"
  greeting: "I'm here"
  traits:
    - "concise"
    - "technical"
  rules:
    - "No markdown in responses"
    - "No bullet points"
    - "No code fences"
    - "Keep responses under 3 sentences"
  access_key_env: PICOVOICE_ACCESS_KEY
  keyword_paths:
    wake: "./models/wakeword/horus.ppn"
    mute: "./models/wakeword/mute.ppn"
    stop: "./models/wakeword/horus-stop.ppn"
  sensitivities:
    wake: 0.5
    mute: 0.5
    stop: 0.7
```

### System Prompt Assembly

The system prompt sent to conv-sidecar's `init` message is assembled at runtime from persona fields:

- Name injected as identity: "You are {name}, a voice assistant..."
- Traits appended as personality descriptors
- Rules appended as behavioral constraints
- Greeting used for the TTS confirmation on wake word detection

This replaces the current hardcoded system prompt string in `session.go`.

### Future Personas

To add a new persona (e.g., "Jarvis"), create new `.ppn` files and swap the `persona:` block in config. No code changes needed.

## Dependencies

### Added
- `github.com/Picovoice/porcupine/binding/go` — Porcupine Go SDK (CGO, bundles native lib)

### Removed
- `golang.org/x/term` — no longer needed without hotkey

## Key Design Decisions

1. **Porcupine never muted**: Ensures kill/mute words work during TTS playback. Reads raw capture frames independently of the muter system.
2. **Capture fan-out via Subscribe()**: Clean multi-consumer pattern. Each consumer gets its own channel, no shared state.
3. **VAD+STT start muted**: No audio processing until wake word. Saves CPU in idle state.
4. **"Horus stop" = full shutdown**: Not a soft stop. Kills everything and exits the process. Higher sensitivity (0.7) to reduce accidental triggers.
5. **Persona in config, not code**: All persona-specific state is YAML-configurable. System prompt assembled from structured fields rather than a raw string.
6. **Hotkey fully replaced**: No fallback. Wake word is the only activation method.
