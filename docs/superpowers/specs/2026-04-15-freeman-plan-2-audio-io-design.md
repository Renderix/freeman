# Freeman Plan 2 — Audio I/O

**Status:** Design
**Date:** 2026-04-15
**Authors:** Ayusman + Claude (brainstorming session)
**Parent spec:** [2026-04-15-freeman-voice-companion-design.md](2026-04-15-freeman-voice-companion-design.md)
**Predecessor plan:** [2026-04-15-freeman-plan-1-skeleton.md](../plans/2026-04-15-freeman-plan-1-skeleton.md)

## Purpose

Plan 1 delivered the Freeman call skeleton: a port-based `Session` state machine wired end-to-end with fakes — stdin-reading transcriber, stdout-printing speaker, SIGUSR1 hotkey, a `ScriptedPM`, and a Bun stub sidecar. The smoke test passes with simulated audio.

Plan 2 replaces the three audio-facing fakes (`Transcriber`, `Speaker`, `Hotkey`) with real implementations so that `freeman call` becomes a full-audio experience. The `ScriptedPM` and stub sidecar are left untouched — Plan 2 is a pure **implementation swap** against the port interfaces, not a semantic change. Plan 3 will then swap `ScriptedPM` for Claude Haiku and the stub sidecar for real `pi-coding-agent` without touching any audio code.

After Plan 2, running `freeman call`:

1. starts a `whisper-server` child process and opens the system mic + speakers;
2. prints `freeman: ready` once warmup completes;
3. lets the user press Enter to start a call;
4. speaks the canned Plan-1 intake script through Kokoro;
5. transcribes the user's real voice utterances via VAD + whisper;
6. drives the rest of the Plan-1 state machine (Intake → Dispatching → Working → Reporting → Idle);
7. returns cleanly on Ctrl-C with the terminal fully restored.

## Success criterion

A human can conduct one full hands-free call: press Enter, speak naturally, hear Kokoro respond, confirm with "yes", and hear the canned done line — all without typing. The existing Plan 1 fakes remain available behind `--fake-audio` for headless testing and CI.

## Non-goals

Explicitly deferred to future plans to keep Plan 2 focused:

- **Barge-in.** During Kokoro playback, user speech is dropped, not cut through. A TTS-echo canceler is its own can of worms (Plan 2.5).
- **Global macOS hotkey.** ⌥-space while another app is focused is out. Plan 2 uses a TTY raw-mode keypress in the foreground terminal (Plan 2.5 or 3).
- **PM swap.** `ScriptedPM` stays. Plan 3 wires Haiku.
- **Sidecar swap.** Bun stub stays. Plan 3 wires real `pi-coding-agent`.
- **whisper-server auto-respawn.** If it crashes, Freeman speaks an error and stops. Plan 3+ handles resilience.
- **Partial/streaming transcripts.** Not needed without barge-in. One utterance in, one text out.
- **Audio device hot-plug.** Unplugging headphones mid-call is undefined behavior.
- **Silero VAD, CoreAudio-native capture, CGO libwhisper.** All possible future upgrades; not for MVP.

## Architecture

```
┌──── freeman call (Go) ─────────────────────────────────────────┐
│                                                                │
│  TTY keypress ───▶ audio/hotkey ──┐                            │
│                                   ▼                            │
│  ┌──── audio/capture ────┐   ┌── Session ──┐   ┌─ audio/playback ─┐
│  │ malgo input device    │   │  machine    │   │ malgo output dev │
│  │ 16 kHz mono int16     │   │ (unchanged  │   │ 24 kHz int16     │
│  │ callback → chan       │   │  from       │   │ driven from      │
│  └─────────┬─────────────┘   │  Plan 1)    │   │ engine PCM       │
│            ▼                 └──────┬──────┘   └─────────▲───────┘
│  ┌──── audio/vad ────────┐          │                    │
│  │ webrtcvad 20 ms frames│          │                    │
│  │ silence/speech SM     │          │  Speak(ctx, text)  │
│  │ emits utterance PCM   │          │                    │
│  └─────────┬─────────────┘          │                    │
│            ▼                        │                    │
│  ┌──── audio/stt ────────┐          │            ┌───────┴──────┐
│  │ whisper-server child  │──text───▶│            │ engine.go    │
│  │ HTTP /inference       │          │            │ sherpa-onnx  │
│  │ implements Transcriber│          │            │ (existing)   │
│  └───────────────────────┘          │            └──────────────┘
│                                     ▼
│                             ScriptedPM (unchanged)
│                             Stub sidecar (unchanged)
└────────────────────────────────────────────────────────────────┘
```

**Key design property:** the `Transcriber`, `Speaker`, and `Hotkey` interfaces defined in `internal/call/ports.go` do not change. Plan 2 adds new implementations; the Session machine is untouched.

**Composition over wrapping.** There is no single `RealTranscriber` type that embeds capture + VAD + stt. Instead, `cmd/freeman/call.go` instantiates the three as separate values and glues them with channels. This keeps each piece independently replaceable (swap WebRTC VAD for Silero later without touching capture or stt) and keeps the wiring visible in one file.

**Threading model.** One shared `*malgo.Context` is created per process and passed to both capture and playback. Malgo's data callbacks run on audio threads — they do nothing but copy PCM into a lock-free ring (capture) or drain from a bounded channel (playback). All Session-facing code stays on Go goroutines. The audio threads never block, allocate meaningfully, or touch Session state.

**Sample-rate plumbing.** Mic at 16 kHz mono (native for whisper + webrtcvad). Kokoro/sherpa-onnx emits samples at the engine's native rate (24 kHz for Kokoro-82M). Two malgo devices at their own rates — no resampling in Go. If a device doesn't support the requested rate, malgo's internal converter handles it.

## Components

### `internal/audio/audio.go` — shared malgo context

- `New(log *slog.Logger) (*Context, error)` initializes the miniaudio backend once per process and logs the chosen backend (CoreAudio on macOS).
- `(*Context).Close() error` tears down. Safe on shutdown error paths.
- Holds no device state — just the malgo handle other sub-packages need to open devices.

### `internal/audio/capture` — microphone input

- `Open(ctx *audio.Context, cfg Config) (*Device, error)`, where `Config = {DeviceID string, SampleRate int, Channels int}`. Empty `DeviceID` = system default.
- `(*Device).Frames() <-chan []int16` yields 20 ms frames (320 samples at 16 kHz). Buffered channel of ~50 frames; when the consumer lags, drop the *oldest* frame from the ring and increment a dropped-frames counter. Latency matters more than completeness for VAD.
- `(*Device).Start()` / `Stop()` — idempotent. `Stop` closes the Frames channel.
- Malgo data callback copies into a lock-free single-producer-single-consumer ring. A Go goroutine drains the ring into the Frames channel. Keeps blocking, GC, and channel sends off the audio thread.

### `internal/audio/vad` — endpointing

- `New(cfg Config) *VAD`, where `Config = {SilenceMs, MinSpeechMs, HangoverMs, Aggressiveness int}`.
- `Run(ctx context.Context, in <-chan []int16) <-chan Utterance` — consumes 20 ms frames, classifies each with webrtcvad, and runs the endpointing state machine:

  ```
  Silent ──speech frame──▶ Speech ──silence ≥ SilenceMs──▶ Silent
                              │
                              └──duration < MinSpeechMs on end──▶ drop
  ```

- Emits `Utterance{PCM []int16, DurationMs int}` when end-of-speech fires for a speech segment longer than `MinSpeechMs`.
- Pure computation. No audio-hardware deps. Trivially testable with synthesized frame sequences.
- Uses an existing webrtcvad binding (candidate: `github.com/maxhawkins/go-webrtcvad` or similar — final pick resolved during plan writing).

### `internal/audio/stt` — whisper-server manager + HTTP client

- `Manager.Start(ctx, cfg Config) error` spawns:

  ```
  whisper-server --model <model_path> --host 127.0.0.1 --port <port> --threads <N>
  ```

  Scrapes stderr for readiness, polls `GET /` with short timeout until a 200 comes back or `startup_timeout_ms` elapses. Errors cleanly if startup fails (with the last line of stderr surfaced for diagnostics).
- `Manager.Stop() error` sends SIGTERM, waits with timeout, SIGKILL fallback.
- `Transcriber` type wraps the manager plus the output channel and **implements `call.Transcriber`**. Internally runs a goroutine that:
  1. reads `Utterance` values from the VAD channel;
  2. wraps each `[]int16` in a 44-byte WAV header (RIFF/fmt/data, PCM16 mono 16 kHz) — in-memory, no temp files;
  3. POSTs to `http://127.0.0.1:<port>/inference` as `multipart/form-data` with `file=@<wav>&response_format=json`;
  4. parses the JSON response, drops empty/whitespace-only text, pushes non-empty text onto the `Utterances()` channel.
- Implements a small `audio.Muter` interface (`Mute()` / `Unmute()`) that `Speaker` invokes around playback to suppress self-echo. While muted, VAD keeps classifying so its state machine does not desync, but utterances it emits are dropped on the floor. See Data flow step 3 for the full sequence.

### `internal/audio/playback` — speaker output + engine bridge

- `Open(ctx *audio.Context, cfg Config, eng *engine.Engine, muter audio.Muter) (*Speaker, error)`. `Speaker` implements `call.Speaker`. The `muter` dependency is wired in `cmd/freeman/call.go` and is satisfied by the `stt.Transcriber` — Speaker depends on the interface, not on stt directly, so there is no circular package import.
- Lazily opens an output device at the engine's native sample rate on first `Speak` call; keeps it open across subsequent calls to avoid re-warmup latency.
- `Speak(ctx context.Context, text string) error`:
  1. Calls `eng.SynthesizePCM(text)`, receiving `[]int16` samples + sample rate.
  2. `muter.Mute()` (suppresses self-echo).
  3. Pushes PCM in chunks into a bounded channel that the malgo output callback drains. Blocks until the channel is empty and the device has drained, or returns early on `ctx.Done()`.
  4. `muter.Unmute()` runs via `defer` so it fires on every exit path.
- On `ctx` cancel: stops writing chunks. Any chunk already handed to malgo finishes playing — no mid-chunk hard cut. This is deliberate; hard cut is a Plan 2.5 barge-in concern.
- Serializes concurrent `Speak` calls with an internal mutex. Session should not overlap speaks, but this is defensive.

### `internal/audio/hotkey` — TTY raw-mode keypress

- `Open(cfg Config) (*Hotkey, error)`:
  - If stdin is a TTY (`term.IsTerminal(int(os.Stdin.Fd()))`): puts the terminal into raw mode via `golang.org/x/term.MakeRaw`, runs a goroutine reading single bytes, matches against the configured key (default Enter: `\r` or `\n`; or space), posts to the Events channel.
  - If stdin is not a TTY (piped/redirected): returns a `stdin-line` fallback that emits on every newline. Prints a one-line notice to stderr. Preserves headless-test compatibility.
- `Close()` restores cooked mode. A SIGINT / SIGTERM handler in the command entry point *also* calls `Close()` before exit so a crash or signal doesn't leave the user's terminal wedged.
- Implements `call.Hotkey`.

### `internal/engine/engine.go` — new `SynthesizePCM` method

**Existing code change**, not just new code. Current `Synthesize(text string) ([]byte, error)` returns a full WAV-encoded blob (RIFF header + PCM) suitable for the WebSocket server. Plan 2 adds:

```go
func (e *Engine) SynthesizePCM(text string) (samples []int16, sampleRate int, err error)
```

that shares the sherpa-onnx call path and returns raw samples, skipping the WAV header entirely. The existing `Synthesize` is kept and reimplemented as a thin wrapper around `SynthesizePCM` that adds the header. All existing WebSocket callers keep working; no API break.

### `cmd/freeman/call.go` — new wiring

Replaces Plan-1 fake wiring when `--fake-audio` is not set:

```
load config
→ audio.New            (shared malgo context)
→ stt.Manager.Start    (whisper-server subprocess + readiness wait)
→ capture.Open         (mic device)
→ vad.New              (endpointing)
→ stt.NewTranscriber   (glues capture → vad → whisper-server)
→ playback.Open        (speaker device bound to engine)
→ hotkey.Open          (tty raw-mode or stdin-line fallback)
→ call.NewSession      (ScriptedPM + stub sidecar unchanged)
→ session.Run(ctx)
```

Clean shutdown is reverse order on `ctx.Done()`. Each `Close` returns an error; errors are joined via `errors.Join` and logged. `--fake-audio` preserves the Plan-1 fakes path for headless/CI and fast local iteration without paying the whisper-server startup cost.

## Data flow — one complete call

1. `freeman call` starts. Loads config, initializes the shared audio context, spawns `whisper-server` as a child process (blocking until healthy — stderr prints `freeman: warming up whisper…` and the user waits ~3-5 s the first time). Opens capture and playback devices. Arms TTY raw-mode hotkey. Session in `Idle`.

2. User presses Enter. `Hotkey.Events()` fires. Session → `Intake`. ScriptedPM emits its canned greeting. `Speaker.Speak(ctx, "hi. what are we building?")` calls `engine.SynthesizePCM`, pushes PCM chunks to the playback device, blocks until drained.

3. Capture has been running the whole time. 20 ms mic frames stream into VAD continuously. **During Kokoro playback, `Transcriber` is muted** (`Mute()` called by `Speaker.Speak` on entry, `Unmute()` deferred on exit): VAD keeps classifying so its internal state machine doesn't desync on partial frames, but any utterance it emits while muted is dropped on the floor. This is the crude TTS-echo protection we get for free by deferring barge-in. A code comment notes Plan 2.5 will replace it with a real echo canceler.

4. After Kokoro finishes, Transcriber resumes. User speaks. VAD detects speech start → buffers frames → detects 800 ms silence → emits `Utterance{PCM, DurationMs}`.

5. STT Transcriber wraps the PCM as WAV in memory, POSTs to `http://127.0.0.1:<port>/inference`, parses the JSON response, pushes non-empty text onto the `call.Transcriber.Utterances()` channel.

6. Session reads the utterance and forwards it to ScriptedPM. The rest of the Plan-1 machine (Intake → Dispatching → Working → Reporting → Idle) runs identically to today.

7. Hotkey during `Idle` → new call. Hotkey during `Working` → cancel, exactly as Plan 1 already handles. Ctrl-C → graceful shutdown: stop session, close hotkey (restore terminal), close playback, close capture, stop whisper-server, close audio context.

## Error handling

| Failure | Behavior |
|---|---|
| malgo context init fails | Exit 1 with `could not initialize audio system: <err>`. No partial state left behind. |
| Mic permission denied (OS refuses device open) | Exit 1 with `microphone permission required — grant Freeman in System Settings → Privacy & Security → Microphone`. |
| Speaker device open fails | Exit 1 with device name and underlying error. |
| `whisper-server` binary not found | Exit 1 with `whisper-server not found in PATH; set freeman.stt.server_path in config or install whisper.cpp`. |
| `whisper-server` model file missing | Exit 1 with resolved model path and a hint to run `scripts/setup_whisper_model.sh`. |
| `whisper-server` startup timeout | Kill child, exit 1 with stderr tail for diagnostics. |
| `whisper-server` crashes mid-session | Speaker says `transcriber crashed, please restart.` Session → Idle. Process exits on next hotkey press. Respawn is Plan 3+. |
| VAD emits zero-length utterance | Dropped silently; never reaches whisper. |
| whisper returns empty or whitespace-only transcript | Speaker says `sorry, didn't catch that.` Session stays in current state. |
| whisper HTTP error (non-200, timeout, malformed JSON) | Speaker says `transcriber error, try again.` Full error logged. Session stays in current state. |
| Terminal is not a TTY (piped, redirected) | Auto-fall-back to line-buffered stdin hotkey mode. One-line notice to stderr. |
| TTY raw-mode restore fails on exit | Log to stderr, continue exit — never hang the shell. SIGINT / SIGTERM handlers restore before exit. |
| `Speak()` called while another Speak is in flight | Serialized by internal mutex. Defensive; Session shouldn't overlap speaks. |
| Playback device underrun (buffer starvation) | Malgo logs; we continue. Audible glitch but recoverable. Not worth complicating the write loop. |
| Capture drop (consumer too slow) | Drop oldest frame in ring, increment dropped-frames counter, log once per 5 s if non-zero. |

**Explicit non-handling:** zero auto-respawn for `whisper-server`; zero auto-recovery for audio device disconnects mid-call. Both are Plan 3+.

## Configuration

`config.yaml` gains new keys under the existing `freeman:` block. Every key has a default in `internal/config/config.go`; an empty `freeman:` block still works.

```yaml
freeman:
  audio:
    input_device: ""        # empty = system default
    output_device: ""       # empty = system default
  stt:
    server_path: ""         # empty = auto-discover whisper-server in PATH
    server_port: 0          # 0 = pick ephemeral
    model_path: ./models/whisper/ggml-large-v3-turbo.bin
    startup_timeout_ms: 10000
    vad:
      silence_ms: 800
      min_speech_ms: 300
      hangover_ms: 500
      aggressiveness: 2     # webrtcvad 0-3
  hotkey:
    mode: tty               # "tty" | "stdin-line"
    key: enter              # "enter" | "space"
```

`hotkey.mode: tty` automatically falls back to `stdin-line` when stdin isn't a TTY, so the config default works in both interactive and piped contexts.

**Breaking change from Plan 1.** Plan 1 shipped `freeman.hotkey` as a plain string (`option+space`). Plan 2 replaces it with the nested object above. The implementation plan must update the `FreemanConfig.Hotkey` field type, migrate the default in `config.yaml`, and update any Plan 1 tests that set the old string form. No backward-compat shim — Plan 1 is one week old and the field was only read at wiring time.

## Testing

### Unit tests (pure Go, no audio hardware)

- **`internal/audio/vad`**: table-driven. Synthesized frame sequences — pure silence, pure speech, speech-silence-speech, short noise under `MinSpeechMs`, long silence then speech. Assert utterance boundaries and output frame counts.
- **`internal/audio/stt`**: mock HTTP server returning canned JSON (success, empty text, HTTP 500, slow response past timeout). Assert `Transcriber` produces expected strings on its channel and handles each error per the table.
- **`internal/audio/hotkey`**: pty-based test. Open a pty pair, point `Hotkey` at the slave side, write keypresses to the master, assert `Events` fires. Skip cleanly if pty creation fails (portability).
- **`internal/engine`**: new test for `SynthesizePCM` against a known short input, asserting non-empty `[]int16` output at the expected sample rate and that the wrapped `Synthesize` still produces a valid WAV.

### Integration tests with fakes (unchanged from Plan 1)

- Plan 1's `internal/call/session_test.go` happy-path test continues to run exactly as-is. The Session machine hasn't changed. **This is a deliberate design property: Plan 2 must not regress any Plan 1 test.**
- The `--fake-audio` code path is covered by re-running the Plan 1 smoke test procedure against the Plan 2 binary.

### Live-audio integration test (tagged, manual)

- New `internal/audio/audio_live_test.go` under `//go:build audio_live`. Opens real devices via malgo, records ~2 seconds, runs a pre-recorded "hello world" WAV through whisper-server, asserts the response contains "hello". Plays a short Kokoro utterance through playback. Not run in CI. Invocation: `go test -tags audio_live ./internal/audio/...`.

### Manual smoke test (gate for plan completion)

```
1.  go build -o freeman ./cmd/freeman
2.  ./freeman call
3.  Wait for "freeman: ready"
4.  Press Enter (hotkey)
5.  Hear Kokoro greeting
6.  Speak: "build a feature flag"
7.  Hear Kokoro voice asking for constraints
8.  Speak: "off, ten percent"
9.  Hear Kokoro speak the scripted summary
10. Speak: "yes"
11. Hear "starting now" then the canned done line
12. Press Enter (hangup) or speak again for another turn
13. Ctrl-C exits cleanly; the terminal is restored to cooked mode
```

## Packaging

- Add `scripts/setup_whisper_model.sh` to fetch `ggml-large-v3-turbo.bin` (~1.5 GB) into `models/whisper/`.
- Update `README.md` with a Plan 2 prerequisites section: `whisper-server` binary (`brew install whisper-cpp` or manual build of `whisper.cpp`) and the model download step.
- Update `scripts/setup_go_models.sh` to print a pointer to the whisper setup script.
- `go.mod` gains `github.com/gen2brain/malgo` (or equivalent), a webrtcvad binding, and `golang.org/x/term`.

## Open questions (for the implementation plan to resolve)

- Exact webrtcvad binding (`maxhawkins/go-webrtcvad` vs. alternatives — pick based on maintenance + CGO footprint).
- Whether to bundle `whisper-server` binary download in `setup_whisper_model.sh` or require the user to install it separately (leaning "separately" — less surface area, standard macOS path).
- Chunk size for playback PCM feed (small = lower barge-in latency later, large = simpler buffer management). Start at ~50 ms chunks and tune empirically.
- How to source webrtcvad in a way that doesn't break `go build` on a fresh macOS (may need a build-tag fallback to energy-threshold VAD if the CGO binding proves fragile).
