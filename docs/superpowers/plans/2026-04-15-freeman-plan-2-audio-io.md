# Freeman Plan 2 — Audio I/O Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Plan-1 audio-facing fakes with real implementations — malgo mic capture, WebRTC VAD endpointing, whisper-server subprocess + HTTP client, malgo Kokoro playback, and a TTY raw-mode keypress hotkey — so `freeman call` becomes a full-audio experience without touching the Session state machine, ScriptedPM, or stub sidecar.

**Architecture:** Seven focused sub-packages under `internal/audio/` (`capture`, `vad`, `stt`, `playback`, `hotkey`, plus a shared `Context`/`Muter` at the root), glued together in `cmd/freeman/call.go`. Each sub-package has a pure computational core that unit-tests without hardware, and a thin hardware-touching shell that lives behind `//go:build audio_live`. Transcriber pauses itself (via an `audio.Muter` interface) while Speaker plays back, to suppress self-echo in the absence of real barge-in.

**Tech Stack:** Go 1.25.6, `github.com/gen2brain/malgo` (miniaudio CGO binding for capture + playback), `github.com/maxhawkins/go-webrtcvad` (WebRTC VAD CGO binding), `golang.org/x/term` (TTY raw mode), `whisper.cpp` / `whisper-server` (subprocess) for STT, existing `sherpa-onnx-go` Kokoro engine for TTS.

**Reference spec:** `docs/superpowers/specs/2026-04-15-freeman-plan-2-audio-io-design.md`

---

## File Structure

**New package tree under `internal/audio/`:**

- `internal/audio/audio.go` — `Context` wrapping `*malgo.AllocatedContext`, `Muter` interface, `NoopMuter` test helper.
- `internal/audio/audio_test.go` — `NoopMuter` behavior, `Context` construction/close (skippable on init failure).
- `internal/audio/vad/vad.go` — endpointing state machine + WebRTC VAD wrapper, `Run(ctx, in) <-chan Utterance`.
- `internal/audio/vad/vad_test.go` — table-driven SM tests with synthetic frame sequences, no CGO call in tests.
- `internal/audio/vad/detector.go` — `Detector` interface abstraction over the CGO binding so tests can inject a fake classifier.
- `internal/audio/capture/ring.go` — pure SPSC `Ring[int16]` with drop-oldest semantics.
- `internal/audio/capture/ring_test.go` — producer/consumer correctness, wrap-around, drop counter.
- `internal/audio/capture/device.go` — malgo capture device binding the ring to a `Frames()` channel.
- `internal/audio/capture/device_live_test.go` — `//go:build audio_live`, opens a real mic.
- `internal/audio/stt/wav.go` — in-memory PCM16 mono WAV encoder.
- `internal/audio/stt/wav_test.go` — round-trip decode vs. a reference file.
- `internal/audio/stt/client.go` — HTTP client for `POST /inference`.
- `internal/audio/stt/client_test.go` — mock `httptest` server covering success/empty/500/timeout.
- `internal/audio/stt/manager.go` — `whisper-server` subprocess lifecycle (start, readiness, stop).
- `internal/audio/stt/manager_test.go` — arg construction + readiness polling against a mock `sleep` child.
- `internal/audio/stt/transcriber.go` — `Transcriber` type implementing both `call.Transcriber` and `audio.Muter`; glues VAD utterances → WAV encode → HTTP client → utterance channel.
- `internal/audio/stt/transcriber_test.go` — muted utterances are dropped, non-muted are forwarded; error-to-error-string mapping for Session.
- `internal/audio/playback/speaker.go` — `Speaker` type implementing `call.Speaker`; chunks PCM to a bounded channel drained by a malgo output device.
- `internal/audio/playback/speaker_test.go` — fake `pcmWriter` asserts chunking, `ctx.Done()` short-circuit, muter order.
- `internal/audio/playback/speaker_live_test.go` — `//go:build audio_live`, plays a short Kokoro utterance through real hardware.
- `internal/audio/hotkey/hotkey.go` — `Open` with TTY / stdin-line branches, `Events()` channel, `Close()` restoring cooked mode.
- `internal/audio/hotkey/hotkey_test.go` — pty-based keypress test + stdin-line fallback test, SIGINT restore.

**New scripts / docs:**

- `scripts/setup_whisper_model.sh` — download `ggml-large-v3-turbo.bin` into `models/whisper/`.
- `README.md` — new "Plan 2 prerequisites" section.

**Existing files modified:**

- `internal/config/config.go` — new `AudioConfig`, `STTConfig`, `VADConfig`, `HotkeyConfig` substructs; **breaking change**: `FreemanConfig.Hotkey` goes from `string` to `HotkeyConfig`.
- `internal/config/config_test.go` — new defaults + breaking-change coverage.
- `config.yaml` — migrate `hotkey: option+space` to the new nested form.
- `internal/engine/engine.go` — add `SynthesizePCM` method, refactor `Synthesize` to wrap it.
- `internal/engine/engine_test.go` — new file, exercises `SynthesizePCM` against a real Kokoro run.
- `cmd/freeman/call.go` — real audio wiring, `--fake-audio` fallback flag, shutdown ordering.
- `go.mod` / `go.sum` — `gen2brain/malgo`, `maxhawkins/go-webrtcvad`, `golang.org/x/term` (if not already present).

**Out of scope for Plan 2** (explicit non-goals from the spec): barge-in, global macOS hotkey, `ScriptedPM` swap, stub sidecar swap, whisper-server auto-respawn, partial transcripts, device hot-plug.

---

## Task 1: Extend config with audio / stt / hotkey sections (breaking change)

**Background from the existing code** (verified before writing this plan, do not re-investigate):

- `internal/config/config.go` already declares `FreemanConfig`, `STTConfig{Model, ModelPath, VAD}`, `VADConfig{SilenceMS, MinSpeechMS}` (note the **uppercase `MS`** casing — keep it), `PMConfig`, `WorkerConfig`, `LoggingConfig`.
- Defaults are set in a package-level `var DefaultConfig = Config{...}` literal (not a function). `LoadConfig` returns this var on missing/invalid files.
- `FreemanConfig.Hotkey` is currently a plain `string` with default `"option+space"`. This is the one field that needs a breaking type change.

Plan 2 adds new fields, does **not** rename `SilenceMS`/`MinSpeechMS`, and converts `Hotkey string` to `Hotkey HotkeyConfig`.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `config.yaml`
- Modify: `cmd/freeman/call.go` (the `channelHotkey` wiring does not read `conf.Freeman.Hotkey`, so this file may not need any change — verify in Step 4)

- [ ] **Step 1: Write the failing test for the new config shape**

Append to `internal/config/config_test.go`:

```go
func TestLoadConfig_Freeman_Plan2Defaults(t *testing.T) {
	conf := LoadConfig("/nonexistent/path.yaml")

	if conf.Freeman.Audio.InputDevice != "" {
		t.Errorf("default Audio.InputDevice = %q, want empty", conf.Freeman.Audio.InputDevice)
	}
	if conf.Freeman.Audio.OutputDevice != "" {
		t.Errorf("default Audio.OutputDevice = %q, want empty", conf.Freeman.Audio.OutputDevice)
	}
	if conf.Freeman.STT.ServerPath != "" {
		t.Errorf("default STT.ServerPath = %q, want empty", conf.Freeman.STT.ServerPath)
	}
	if conf.Freeman.STT.ServerPort != 0 {
		t.Errorf("default STT.ServerPort = %d, want 0", conf.Freeman.STT.ServerPort)
	}
	if conf.Freeman.STT.ModelPath != "./models/whisper/ggml-large-v3-turbo.bin" {
		t.Errorf("default STT.ModelPath = %q", conf.Freeman.STT.ModelPath)
	}
	if conf.Freeman.STT.StartupTimeoutMS != 10000 {
		t.Errorf("default STT.StartupTimeoutMS = %d, want 10000", conf.Freeman.STT.StartupTimeoutMS)
	}
	if conf.Freeman.STT.VAD.SilenceMS != 800 {
		t.Errorf("default VAD.SilenceMS = %d, want 800", conf.Freeman.STT.VAD.SilenceMS)
	}
	if conf.Freeman.STT.VAD.MinSpeechMS != 300 {
		t.Errorf("default VAD.MinSpeechMS = %d, want 300", conf.Freeman.STT.VAD.MinSpeechMS)
	}
	if conf.Freeman.STT.VAD.HangoverMS != 500 {
		t.Errorf("default VAD.HangoverMS = %d, want 500", conf.Freeman.STT.VAD.HangoverMS)
	}
	if conf.Freeman.STT.VAD.Aggressiveness != 2 {
		t.Errorf("default VAD.Aggressiveness = %d, want 2", conf.Freeman.STT.VAD.Aggressiveness)
	}
	if conf.Freeman.Hotkey.Mode != "tty" {
		t.Errorf("default Hotkey.Mode = %q, want tty", conf.Freeman.Hotkey.Mode)
	}
	if conf.Freeman.Hotkey.Key != "enter" {
		t.Errorf("default Hotkey.Key = %q, want enter", conf.Freeman.Hotkey.Key)
	}
}

func TestLoadConfig_Freeman_Plan2YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
freeman:
  audio:
    input_device: "MacBook Microphone"
    output_device: "External Speakers"
  stt:
    server_path: /opt/whisper/whisper-server
    server_port: 17100
    model_path: /models/ggml.bin
    startup_timeout_ms: 20000
    vad:
      silence_ms: 500
      min_speech_ms: 250
      hangover_ms: 300
      aggressiveness: 3
  hotkey:
    mode: stdin-line
    key: space
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	conf := LoadConfig(path)
	if conf.Freeman.Audio.InputDevice != "MacBook Microphone" {
		t.Errorf("Audio.InputDevice = %q", conf.Freeman.Audio.InputDevice)
	}
	if conf.Freeman.STT.ServerPort != 17100 {
		t.Errorf("STT.ServerPort = %d", conf.Freeman.STT.ServerPort)
	}
	if conf.Freeman.STT.VAD.Aggressiveness != 3 {
		t.Errorf("VAD.Aggressiveness = %d", conf.Freeman.STT.VAD.Aggressiveness)
	}
	if conf.Freeman.Hotkey.Mode != "stdin-line" {
		t.Errorf("Hotkey.Mode = %q", conf.Freeman.Hotkey.Mode)
	}
	if conf.Freeman.Hotkey.Key != "space" {
		t.Errorf("Hotkey.Key = %q", conf.Freeman.Hotkey.Key)
	}
}
```

If `filepath` isn't yet imported in `config_test.go`, add it to the imports.

- [ ] **Step 2: Run tests, verify failure**

Run: `go test ./internal/config/... -run Plan2`
Expected: FAIL — `conf.Freeman.Audio` undefined, `conf.Freeman.Hotkey.Mode` undefined (the existing `Hotkey` is a string).

- [ ] **Step 3: Update `FreemanConfig`, extend `STTConfig` and `VADConfig`, add new substructs**

Edit `internal/config/config.go`.

**Add two new substruct types** (above `FreemanConfig`):

```go
// AudioConfig selects capture and playback devices.
type AudioConfig struct {
	InputDevice  string `yaml:"input_device"`
	OutputDevice string `yaml:"output_device"`
}

// HotkeyConfig selects the Plan 2 hotkey implementation.
type HotkeyConfig struct {
	Mode string `yaml:"mode"` // "tty" | "stdin-line"
	Key  string `yaml:"key"`  // "enter" | "space"
}
```

**Extend `STTConfig`** (it already exists) by adding three fields and re-ordering for readability:

```go
type STTConfig struct {
	Model            string    `yaml:"model"`
	ModelPath        string    `yaml:"model_path"`
	ServerPath       string    `yaml:"server_path"`
	ServerPort       int       `yaml:"server_port"`
	StartupTimeoutMS int       `yaml:"startup_timeout_ms"`
	VAD              VADConfig `yaml:"vad"`
}
```

**Extend `VADConfig`** by adding two fields. Keep the existing `SilenceMS` / `MinSpeechMS` casing:

```go
type VADConfig struct {
	SilenceMS      int `yaml:"silence_ms"`
	MinSpeechMS    int `yaml:"min_speech_ms"`
	HangoverMS     int `yaml:"hangover_ms"`
	Aggressiveness int `yaml:"aggressiveness"`
}
```

**Modify `FreemanConfig`** — insert `Audio` and change `Hotkey` from string to struct:

```go
type FreemanConfig struct {
	PM      PMConfig      `yaml:"pm"`
	Worker  WorkerConfig  `yaml:"worker"`
	Audio   AudioConfig   `yaml:"audio"`
	STT     STTConfig     `yaml:"stt"`
	Hotkey  HotkeyConfig  `yaml:"hotkey"`
	Logging LoggingConfig `yaml:"logging"`
}
```

**Update the package-level `var DefaultConfig`** to fill the new fields. Find the existing `Freeman: FreemanConfig{...}` literal and:

1. Add a new `Audio: AudioConfig{InputDevice: "", OutputDevice: ""}` line after `Worker`.
2. Inside `STT: STTConfig{...}`, add `StartupTimeoutMS: 10000`, and keep the existing `Model`/`ModelPath` lines.
3. Inside `STT.VAD`, add `HangoverMS: 500` and `Aggressiveness: 2` alongside the existing `SilenceMS: 800` / `MinSpeechMS: 300`.
4. Replace `Hotkey: "option+space",` with `Hotkey: HotkeyConfig{Mode: "tty", Key: "enter"},`.

Preserve every existing PM / Worker / Logging default unchanged.

- [ ] **Step 4: Verify `cmd/freeman/call.go` still compiles**

Run: `grep -rn 'Freeman\.Hotkey' cmd/ internal/`
Expected: likely zero matches — Plan 1's `call.go` wires a local `channelHotkey` struct on SIGUSR1 and never reads `conf.Freeman.Hotkey`. If any match exists, replace `conf.Freeman.Hotkey` (string) with `conf.Freeman.Hotkey.Key` or `fmt.Sprintf("%+v", conf.Freeman.Hotkey)` as appropriate — none of the real call sites should actually use the string value.

- [ ] **Step 5: Migrate `config.yaml` to the nested form**

In `config.yaml`, find the existing `freeman:` block. Replace its `hotkey: option+space` line with:

```yaml
freeman:
  # ... existing pm, worker sections preserved ...
  audio:
    input_device: ""
    output_device: ""
  stt:
    server_path: ""
    server_port: 0
    model_path: ./models/whisper/ggml-large-v3-turbo.bin
    startup_timeout_ms: 10000
    vad:
      silence_ms: 800
      min_speech_ms: 300
      hangover_ms: 500
      aggressiveness: 2
  hotkey:
    mode: tty
    key: enter
```

Leave all non-`freeman:` top-level sections (`server:`, `models:`, `voice:`) untouched.

- [ ] **Step 6: Run the full config test suite**

Run: `go test ./internal/config/... -v`
Expected: all tests PASS, including the new `Plan2Defaults` and `Plan2YAML` tests and any pre-existing Plan 1 tests.

- [ ] **Step 7: Run the full project build + vet**

Run: `go build ./... && go vet ./...`
Expected: both succeed with no output.

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go config.yaml cmd/freeman/call.go
git commit -m "config: extend freeman section with audio/stt/hotkey substructs"
```

---

## Task 2: Add `engine.TTSEngine.GeneratePCM`

**Background** (verified in the existing repo — do not re-investigate):

- The engine type is `TTSEngine`, not `Engine`.
- Constructor: `NewTTSEngine(modelPath, voicesPath, tokensPath, dataDir string) (*TTSEngine, error)`.
- Existing method: `func (e *TTSEngine) Generate(text, voice string, speed float64) ([]byte, error)` — returns a complete WAV blob by calling `e.tts.Generate(...)` (which returns a sherpa-onnx struct with `Samples []float32` and `SampleRate int`) and then `Float32ToWav(audio.Samples, audio.SampleRate)`.
- `Float32ToWav(samples []float32, sampleRate int) []byte` is already exported in the same file.
- There is no existing `engine_test.go`.

Task 2 adds a sibling method `GeneratePCM` that shares the sherpa-onnx call path and returns raw int16 samples + sample rate, without touching `Generate` or `Float32ToWav`.

**Files:**
- Modify: `internal/engine/engine.go`
- Create: `internal/engine/engine_test.go`

- [ ] **Step 1: Read the existing `Generate` implementation**

Run: `sed -n '70,95p' internal/engine/engine.go`
Note exactly how `speakerID` is resolved from `voice` and how the sherpa-onnx `Generate` is called. The new `GeneratePCM` must copy this logic exactly.

- [ ] **Step 2: Write the failing test**

Create `internal/engine/engine_test.go`:

```go
package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// loadEngineForTest builds a TTSEngine against the repo's models/ directory.
// Skips if models are missing — CI without models should not fail.
func loadEngineForTest(t *testing.T) *TTSEngine {
	t.Helper()
	root := "../.."
	modelDir := filepath.Join(root, "models")
	if _, err := os.Stat(modelDir); os.IsNotExist(err) {
		t.Skipf("models dir missing at %s; run scripts/setup_go_models.sh", modelDir)
	}
	eng, err := NewTTSEngine(
		filepath.Join(modelDir, "model.onnx"),
		filepath.Join(modelDir, "voices.bin"),
		filepath.Join(modelDir, "tokens.txt"),
		filepath.Join(modelDir, "espeak-ng-data"),
	)
	if err != nil {
		t.Fatalf("NewTTSEngine: %v", err)
	}
	return eng
}

func TestGeneratePCM_NonEmpty(t *testing.T) {
	eng := loadEngineForTest(t)
	samples, sr, err := eng.GeneratePCM("hello", "af_heart", 1.0)
	if err != nil {
		t.Fatalf("GeneratePCM: %v", err)
	}
	if sr <= 0 {
		t.Errorf("sample rate = %d, want > 0", sr)
	}
	if len(samples) == 0 {
		t.Fatal("samples empty")
	}
	// Very loose lower bound — "hello" at any sane sample rate is at least 0.1 s.
	if len(samples) < sr/20 {
		t.Errorf("samples = %d at sr=%d, want at least %d", len(samples), sr, sr/20)
	}
}

func TestGenerate_WAVStillValid(t *testing.T) {
	eng := loadEngineForTest(t)
	wav, err := eng.Generate("test", "af_heart", 1.0)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(wav) < 44 {
		t.Fatalf("wav = %d bytes, want >= 44 (header)", len(wav))
	}
	if !bytes.Equal(wav[0:4], []byte("RIFF")) {
		t.Errorf("missing RIFF header")
	}
	if !bytes.Equal(wav[8:12], []byte("WAVE")) {
		t.Errorf("missing WAVE marker")
	}
}
```

- [ ] **Step 3: Run the test to see it fail**

Run: `go test ./internal/engine/ -run GeneratePCM -v`
Expected: FAIL — `eng.GeneratePCM undefined`.

- [ ] **Step 4: Add `GeneratePCM` to `engine.go`**

Append to `internal/engine/engine.go`, directly below the existing `Generate` method. Note that Plan-1's existing `Generate` hardcodes `speakerID := 0` and ignores the `voice` argument — `GeneratePCM` intentionally matches that behavior so both methods produce the same audio:

```go
// GeneratePCM is the hot path for local playback: runs Kokoro and returns raw
// int16 samples plus the engine's native sample rate, skipping the WAV header.
// Plan 2's playback.Speaker drives these samples straight into malgo.
//
// Semantics match Generate: the voice argument is reserved for future use but
// currently ignored — both methods use speaker ID 0.
func (e *TTSEngine) GeneratePCM(text, voice string, speed float64) ([]int16, int, error) {
	if e == nil || e.tts == nil {
		return nil, 0, fmt.Errorf("engine not initialized")
	}
	speakerID := 0
	_ = voice // parity with Generate; wire voice→speakerID mapping when Generate grows one

	audio := e.tts.Generate(text, speakerID, float32(speed))
	if audio == nil || audio.Samples == nil || len(audio.Samples) == 0 {
		return nil, 0, fmt.Errorf("empty audio for %q", text)
	}
	pcm := make([]int16, len(audio.Samples))
	for i, f := range audio.Samples {
		v := f * 32767.0
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		pcm[i] = int16(v)
	}
	return pcm, audio.SampleRate, nil
}
```

`fmt` is already imported at the top of the file.

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/engine/ -v`
Expected: both tests PASS, or both SKIP if `models/` is absent. Mixed outcomes indicate a real failure.

- [ ] **Step 6: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean. Every existing caller of `Generate` keeps working — `GeneratePCM` is purely additive.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/engine.go internal/engine/engine_test.go
git commit -m "engine: add TTSEngine.GeneratePCM for local playback"
```

---

## Task 3: Create `internal/audio` root package with `Context` and `Muter`

**Files:**
- Create: `internal/audio/audio.go`
- Create: `internal/audio/audio_test.go`
- Modify: `go.mod` (add `gen2brain/malgo`)

- [ ] **Step 1: Add the malgo dependency**

Run:
```
cd ~/charlotte/freeman
go get github.com/gen2brain/malgo@latest
go mod tidy
```
Expected: `go.mod` now lists `github.com/gen2brain/malgo`, `go.sum` is updated.

- [ ] **Step 2: Write the failing test**

Create `internal/audio/audio_test.go`:

```go
package audio

import "testing"

func TestNoopMuter(t *testing.T) {
	var m Muter = &NoopMuter{}
	// Should be safe to call repeatedly and in any order.
	m.Mute()
	m.Mute()
	m.Unmute()
	m.Unmute()
	m.Mute()
	m.Unmute()
}

func TestContext_NewCloseSkippable(t *testing.T) {
	ctx, err := New(nil)
	if err != nil {
		t.Skipf("audio context unavailable in this environment: %v", err)
	}
	if ctx == nil {
		t.Fatal("New returned nil context with nil error")
	}
	if err := ctx.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/audio/ -run NoopMuter -v`
Expected: FAIL — `Muter undefined`, `NoopMuter undefined`, `New undefined`.

- [ ] **Step 4: Implement `audio.go`**

Create `internal/audio/audio.go`:

```go
// Package audio owns the miniaudio (malgo) context that capture and playback
// sub-packages share, and defines the cross-cutting Muter interface Speaker
// uses to suppress self-echo during TTS playback.
package audio

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/gen2brain/malgo"
)

// Context wraps the malgo AllocatedContext. One instance per process.
type Context struct {
	raw *malgo.AllocatedContext
	log *slog.Logger
}

// New initializes the backend. Pass nil for log to get a discard logger.
func New(log *slog.Logger) (*Context, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(msg string) {
		log.Debug("malgo", "msg", msg)
	})
	if err != nil {
		return nil, fmt.Errorf("malgo init: %w", err)
	}
	log.Info("audio: context ready", "backend", ctx.Context.Backend())
	return &Context{raw: ctx, log: log}, nil
}

// Raw returns the underlying malgo context for sub-packages that need to open
// devices. Callers must not free or reinitialize it.
func (c *Context) Raw() *malgo.AllocatedContext {
	return c.raw
}

// Log returns the slog logger, for sub-packages that want to emit structured
// audio events without importing slog directly.
func (c *Context) Log() *slog.Logger {
	return c.log
}

// Close tears down the context. Safe to call multiple times.
func (c *Context) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}
	if err := c.raw.Uninit(); err != nil {
		return fmt.Errorf("malgo uninit: %w", err)
	}
	c.raw.Free()
	c.raw = nil
	return nil
}

// Muter is implemented by components that can temporarily drop transcribed
// audio. Speaker invokes Mute() before TTS playback and Unmute() after, to
// prevent Kokoro's own voice from echoing back through the mic and turning
// into a spurious user utterance.
//
// Mute/Unmute must be safe to call concurrently and must be idempotent: two
// Mutes without an intervening Unmute leave the muter muted; two Unmutes with
// no intervening Mute leave it unmuted.
type Muter interface {
	Mute()
	Unmute()
}

// NoopMuter is a test helper and a safe default when no transcriber is wired.
type NoopMuter struct {
	mu    sync.Mutex
	muted bool
}

func (n *NoopMuter) Mute() {
	n.mu.Lock()
	n.muted = true
	n.mu.Unlock()
}

func (n *NoopMuter) Unmute() {
	n.mu.Lock()
	n.muted = false
	n.mu.Unlock()
}

// IsMuted is test-only inspection.
func (n *NoopMuter) IsMuted() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.muted
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/audio/ -v`
Expected: `TestNoopMuter` PASS. `TestContext_NewCloseSkippable` either PASS (if CI host has audio) or SKIP (if it can't init a backend). Both outcomes are acceptable.

- [ ] **Step 6: Commit**

```bash
git add internal/audio/audio.go internal/audio/audio_test.go go.mod go.sum
git commit -m "audio: add shared malgo context and Muter interface"
```

---

## Task 4: `internal/audio/vad` — endpointing state machine + WebRTC VAD wrapper

**Files:**
- Create: `internal/audio/vad/detector.go`
- Create: `internal/audio/vad/vad.go`
- Create: `internal/audio/vad/vad_test.go`
- Modify: `go.mod` (add `maxhawkins/go-webrtcvad`)

- [ ] **Step 1: Add the webrtcvad dependency**

Run:
```
go get github.com/maxhawkins/go-webrtcvad@latest
go mod tidy
```

If this package does not exist or fails to build on the current toolchain, fall back to `github.com/baabaaox/go-webrtcvad` (same API surface, alternate mirror). Record whichever one resolves in `go.mod`, and use its import path throughout the rest of this task. The rest of the plan assumes `maxhawkins`; substitute globally if needed.

- [ ] **Step 2: Write the failing test for the pure state machine**

Create `internal/audio/vad/vad_test.go`:

```go
package vad

import (
	"context"
	"testing"
	"time"
)

// fakeDetector flips "is speech" according to a scripted slice, one value per call.
type fakeDetector struct {
	script []bool
	idx    int
}

func (f *fakeDetector) IsSpeech(frame []int16, sampleRate int) (bool, error) {
	if f.idx >= len(f.script) {
		return false, nil
	}
	v := f.script[f.idx]
	f.idx++
	return v, nil
}

func frame() []int16 { return make([]int16, 320) } // 20 ms at 16 kHz

// Script: 5 silence frames, 20 speech frames (400 ms), 40 silence frames (800 ms).
// Speech segment is 400 ms, above the 300 ms min, so one utterance should fire.
func TestVAD_SingleUtterance(t *testing.T) {
	script := make([]bool, 0, 65)
	for i := 0; i < 5; i++ {
		script = append(script, false)
	}
	for i := 0; i < 20; i++ {
		script = append(script, true)
	}
	for i := 0; i < 40; i++ {
		script = append(script, false)
	}
	fd := &fakeDetector{script: script}
	v := NewWithDetector(Config{
		SilenceMs:      800,
		MinSpeechMs:    300,
		HangoverMs:     0,
		Aggressiveness: 2,
		SampleRate:     16000,
		FrameMs:        20,
	}, fd)

	in := make(chan []int16, len(script))
	for range script {
		in <- frame()
	}
	close(in)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := v.Run(ctx, in)

	var got []Utterance
	for u := range out {
		got = append(got, u)
	}
	if len(got) != 1 {
		t.Fatalf("utterances = %d, want 1", len(got))
	}
	if got[0].DurationMs != 400 {
		t.Errorf("duration = %d, want 400", got[0].DurationMs)
	}
	expectedSamples := 20 * 320
	if len(got[0].PCM) != expectedSamples {
		t.Errorf("pcm len = %d, want %d", len(got[0].PCM), expectedSamples)
	}
}

// Script: speech burst under MinSpeechMs followed by silence — drop.
func TestVAD_DropsShortSpeech(t *testing.T) {
	script := make([]bool, 0, 40)
	for i := 0; i < 5; i++ {
		script = append(script, true) // 100 ms, under 300 ms
	}
	for i := 0; i < 45; i++ {
		script = append(script, false) // 900 ms silence
	}
	fd := &fakeDetector{script: script}
	v := NewWithDetector(Config{
		SilenceMs:   800,
		MinSpeechMs: 300,
		SampleRate:  16000,
		FrameMs:     20,
	}, fd)

	in := make(chan []int16, len(script))
	for range script {
		in <- frame()
	}
	close(in)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := v.Run(ctx, in)

	var got []Utterance
	for u := range out {
		got = append(got, u)
	}
	if len(got) != 0 {
		t.Errorf("utterances = %d, want 0", len(got))
	}
}

// Script: two separated speech bursts, both above threshold — two utterances.
func TestVAD_TwoUtterances(t *testing.T) {
	script := make([]bool, 0)
	script = append(script, boolN(3, false)...)   // 60 ms pre
	script = append(script, boolN(20, true)...)   // 400 ms speech
	script = append(script, boolN(45, false)...)  // 900 ms silence
	script = append(script, boolN(20, true)...)   // 400 ms speech
	script = append(script, boolN(45, false)...)  // 900 ms silence
	fd := &fakeDetector{script: script}
	v := NewWithDetector(Config{
		SilenceMs:   800,
		MinSpeechMs: 300,
		SampleRate:  16000,
		FrameMs:     20,
	}, fd)

	in := make(chan []int16, len(script))
	for range script {
		in <- frame()
	}
	close(in)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := v.Run(ctx, in)

	var got []Utterance
	for u := range out {
		got = append(got, u)
	}
	if len(got) != 2 {
		t.Fatalf("utterances = %d, want 2", len(got))
	}
}

func boolN(n int, v bool) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = v
	}
	return out
}
```

- [ ] **Step 3: Run the test to see it fail**

Run: `go test ./internal/audio/vad/ -v`
Expected: FAIL — everything undefined.

- [ ] **Step 4: Create `detector.go`**

Create `internal/audio/vad/detector.go`:

```go
package vad

import (
	"fmt"

	webrtcvad "github.com/maxhawkins/go-webrtcvad"
)

// Detector classifies a single PCM frame as speech or non-speech.
// Extracted to an interface so vad.Run is unit-testable without the CGO binding.
type Detector interface {
	IsSpeech(frame []int16, sampleRate int) (bool, error)
}

// webrtcDetector wraps maxhawkins/go-webrtcvad.
type webrtcDetector struct {
	v *webrtcvad.VAD
}

// NewWebRTCDetector returns a Detector backed by the WebRTC VAD, with the
// given aggressiveness (0 least, 3 most).
func NewWebRTCDetector(aggressiveness int) (Detector, error) {
	v, err := webrtcvad.New()
	if err != nil {
		return nil, fmt.Errorf("webrtcvad new: %w", err)
	}
	if err := v.SetMode(aggressiveness); err != nil {
		return nil, fmt.Errorf("webrtcvad set mode %d: %w", aggressiveness, err)
	}
	return &webrtcDetector{v: v}, nil
}

func (d *webrtcDetector) IsSpeech(frame []int16, sampleRate int) (bool, error) {
	// go-webrtcvad's Process takes []byte little-endian PCM16.
	b := make([]byte, len(frame)*2)
	for i, s := range frame {
		u := uint16(s)
		b[i*2] = byte(u)
		b[i*2+1] = byte(u >> 8)
	}
	return d.v.Process(sampleRate, b)
}
```

If the chosen binding exposes a different `Process` signature, adjust this wrapper only. The rest of the package should not care.

- [ ] **Step 5: Create `vad.go` with the endpointing state machine**

Create `internal/audio/vad/vad.go`:

```go
package vad

import "context"

// Utterance is a completed user speech segment with end-of-speech already detected.
type Utterance struct {
	PCM        []int16
	DurationMs int
}

// Config tunes the endpointing state machine.
type Config struct {
	SilenceMs      int // end-of-speech trigger; default 800
	MinSpeechMs    int // drop segments shorter than this; default 300
	HangoverMs     int // keep classifying as speech for this long after last speech frame; default 500
	Aggressiveness int // webrtcvad 0-3; default 2
	SampleRate     int // e.g. 16000
	FrameMs        int // e.g. 20
}

func (c Config) framesFor(ms int) int {
	if c.FrameMs == 0 {
		return 0
	}
	return ms / c.FrameMs
}

// VAD owns the detector and the endpointing SM.
type VAD struct {
	cfg Config
	det Detector
}

// New returns a VAD backed by the WebRTC detector.
func New(cfg Config) (*VAD, error) {
	d, err := NewWebRTCDetector(cfg.Aggressiveness)
	if err != nil {
		return nil, err
	}
	return NewWithDetector(cfg, d), nil
}

// NewWithDetector lets tests inject a fake classifier.
func NewWithDetector(cfg Config, d Detector) *VAD {
	return &VAD{cfg: cfg, det: d}
}

// Run consumes 20 ms frames from `in` and emits completed utterances to the
// returned channel. The channel closes when `in` closes or ctx is canceled.
func (v *VAD) Run(ctx context.Context, in <-chan []int16) <-chan Utterance {
	out := make(chan Utterance, 4)
	go func() {
		defer close(out)

		state := stateSilent
		silenceFrames := 0
		var buf []int16

		silenceLimit := v.cfg.framesFor(v.cfg.SilenceMs)
		minSpeech := v.cfg.framesFor(v.cfg.MinSpeechMs)
		bufFrames := 0

		flush := func() {
			if bufFrames >= minSpeech && len(buf) > 0 {
				out <- Utterance{
					PCM:        buf,
					DurationMs: bufFrames * v.cfg.FrameMs,
				}
			}
			buf = nil
			bufFrames = 0
			silenceFrames = 0
			state = stateSilent
		}

		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-in:
				if !ok {
					flush()
					return
				}
				isSpeech, err := v.det.IsSpeech(frame, v.cfg.SampleRate)
				if err != nil {
					continue
				}
				switch state {
				case stateSilent:
					if isSpeech {
						state = stateSpeech
						buf = append(buf, frame...)
						bufFrames++
						silenceFrames = 0
					}
				case stateSpeech:
					buf = append(buf, frame...)
					bufFrames++
					if isSpeech {
						silenceFrames = 0
					} else {
						silenceFrames++
						if silenceFrames >= silenceLimit {
							// End of speech: trim trailing silence tail from duration
							// (but keep the PCM — whisper handles it fine).
							flush()
						}
					}
				}
			}
		}
	}()
	return out
}

type state int

const (
	stateSilent state = iota
	stateSpeech
)
```

- [ ] **Step 6: Run the tests to see them pass**

Run: `go test ./internal/audio/vad/ -v`
Expected: `TestVAD_SingleUtterance`, `TestVAD_DropsShortSpeech`, `TestVAD_TwoUtterances` all PASS.

- [ ] **Step 7: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/audio/vad go.mod go.sum
git commit -m "audio/vad: add WebRTC VAD wrapper and endpointing state machine"
```

---

## Task 5: `internal/audio/capture` — ring buffer and malgo capture device

**Files:**
- Create: `internal/audio/capture/ring.go`
- Create: `internal/audio/capture/ring_test.go`
- Create: `internal/audio/capture/device.go`
- Create: `internal/audio/capture/device_live_test.go`

- [ ] **Step 1: Write the failing ring buffer test**

Create `internal/audio/capture/ring_test.go`:

```go
package capture

import "testing"

func TestRing_PushPop(t *testing.T) {
	r := NewRing(4)
	if got := r.Push([]int16{1, 2, 3}); got != 0 {
		t.Errorf("dropped on first push = %d, want 0", got)
	}
	if got := r.Push([]int16{4, 5}); got != 1 {
		t.Errorf("dropped on overflow = %d, want 1", got)
	}
	// Capacity 4, we pushed 5 elements. Oldest one dropped.
	out := r.PopAll()
	// Expected contents: [2, 3, 4, 5] — oldest (1) dropped.
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4", len(out))
	}
	if out[0] != 2 || out[3] != 5 {
		t.Errorf("out = %v, want [2 3 4 5]", out)
	}
}

func TestRing_DroppedCounter(t *testing.T) {
	r := NewRing(2)
	r.Push([]int16{1, 2})
	r.Push([]int16{3, 4, 5}) // drops 1, 2, 3
	if r.Dropped() != 3 {
		t.Errorf("dropped = %d, want 3", r.Dropped())
	}
}

func TestRing_WrapAround(t *testing.T) {
	r := NewRing(8)
	r.Push([]int16{1, 2, 3, 4})
	if got := r.PopAll(); len(got) != 4 {
		t.Errorf("first pop = %v", got)
	}
	r.Push([]int16{5, 6, 7, 8, 9})
	got := r.PopAll()
	if len(got) != 5 {
		t.Fatalf("wrap pop len = %d", len(got))
	}
	if got[0] != 5 || got[4] != 9 {
		t.Errorf("wrap pop = %v", got)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/audio/capture/ -v`
Expected: FAIL — `NewRing undefined`.

- [ ] **Step 3: Implement the ring**

Create `internal/audio/capture/ring.go`:

```go
package capture

import "sync"

// Ring is a bounded int16 buffer with drop-oldest overwrite semantics, intended
// for handing mic samples from the malgo audio thread to the Go consumer
// goroutine. Single-producer single-consumer assumptions hold in practice, but
// the API is guarded with a mutex so stray races are safe.
type Ring struct {
	mu       sync.Mutex
	buf      []int16
	head     int // next write position
	size     int // number of valid samples
	capacity int
	dropped  int64
}

func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{
		buf:      make([]int16, capacity),
		capacity: capacity,
	}
}

// Push writes samples, overwriting the oldest on overflow. Returns the number
// of samples that were dropped by this call.
func (r *Ring) Push(samples []int16) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := 0
	for _, s := range samples {
		r.buf[r.head] = s
		r.head = (r.head + 1) % r.capacity
		if r.size < r.capacity {
			r.size++
		} else {
			dropped++
		}
	}
	r.dropped += int64(dropped)
	return dropped
}

// PopAll returns every sample currently in the ring, oldest-first, and clears
// the ring.
func (r *Ring) PopAll() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return nil
	}
	out := make([]int16, r.size)
	start := (r.head - r.size + r.capacity) % r.capacity
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(start+i)%r.capacity]
	}
	r.size = 0
	return out
}

// Dropped returns the cumulative count of overwritten samples since creation.
func (r *Ring) Dropped() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}
```

- [ ] **Step 4: Run the ring tests to see them pass**

Run: `go test ./internal/audio/capture/ -v`
Expected: all three ring tests PASS.

- [ ] **Step 5: Create the malgo device wrapper**

Create `internal/audio/capture/device.go`:

```go
// Package capture drives the microphone through malgo and exposes a clean Go
// channel of fixed-size PCM frames for downstream VAD/STT.
package capture

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/Renderix/freeman/internal/audio"
	"github.com/gen2brain/malgo"
)

// Config selects the capture device and format.
type Config struct {
	DeviceID   string // empty = default
	SampleRate int    // 16000 for Plan 2
	Channels   int    // 1
	FrameMs    int    // 20
}

// Device runs a malgo capture device and hands fixed-size frames to Frames().
type Device struct {
	cfg       Config
	dev       *malgo.Device
	ring      *Ring
	frames    chan []int16
	stopOnce  sync.Once
	stopCh    chan struct{}
	frameSize int // samples per frame
	droppedT  time.Time
	log       func(msg string, kv ...any)
}

// Open initializes a capture device. It does not start it — call Start.
func Open(actx *audio.Context, cfg Config) (*Device, error) {
	if actx == nil || actx.Raw() == nil {
		return nil, fmt.Errorf("audio context not initialized")
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Channels == 0 {
		cfg.Channels = 1
	}
	if cfg.FrameMs == 0 {
		cfg.FrameMs = 20
	}
	frameSize := cfg.SampleRate * cfg.FrameMs / 1000

	d := &Device{
		cfg:       cfg,
		ring:      NewRing(frameSize * 200), // ~4 s of mic audio
		frames:    make(chan []int16, 50),
		stopCh:    make(chan struct{}),
		frameSize: frameSize,
		log: func(msg string, kv ...any) {
			actx.Log().Debug(msg, kv...)
		},
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(cfg.Channels)
	deviceConfig.SampleRate = uint32(cfg.SampleRate)
	deviceConfig.Alsa.NoMMap = 1

	callbacks := malgo.DeviceCallbacks{
		Data: func(_, pInput []byte, framecount uint32) {
			// pInput is PCM16 little-endian interleaved. Channels=1, so samples=framecount.
			n := int(framecount) * cfg.Channels
			if n == 0 {
				return
			}
			samples := unsafe.Slice((*int16)(unsafe.Pointer(&pInput[0])), n)
			// Copy out of the C buffer before Push — Push stores into our ring's Go slice.
			copied := make([]int16, n)
			copy(copied, samples)
			d.ring.Push(copied)
		},
	}

	dev, err := malgo.InitDevice(actx.Raw().Context, deviceConfig, callbacks)
	if err != nil {
		return nil, fmt.Errorf("init capture device: %w", err)
	}
	d.dev = dev
	return d, nil
}

// Start begins capturing and kicks off the drain goroutine that converts the
// ring into fixed-size frames on the Frames() channel.
func (d *Device) Start() error {
	if err := d.dev.Start(); err != nil {
		return fmt.Errorf("start capture: %w", err)
	}
	go d.drain()
	return nil
}

// Frames returns the frame channel. Consumers must drain it; the drain
// goroutine applies drop-oldest on the ring when the channel is full.
func (d *Device) Frames() <-chan []int16 {
	return d.frames
}

// Stop halts capture and closes the Frames channel. Idempotent.
func (d *Device) Stop() {
	d.stopOnce.Do(func() {
		close(d.stopCh)
		if d.dev != nil {
			_ = d.dev.Stop()
			d.dev.Uninit()
		}
	})
}

// drain converts the ring into fixed-size frames. Runs until stopCh closes.
func (d *Device) drain() {
	defer close(d.frames)
	tick := time.NewTicker(time.Duration(d.cfg.FrameMs) * time.Millisecond / 2)
	defer tick.Stop()
	var pending []int16
	for {
		select {
		case <-d.stopCh:
			return
		case <-tick.C:
		}
		pending = append(pending, d.ring.PopAll()...)
		for len(pending) >= d.frameSize {
			frame := make([]int16, d.frameSize)
			copy(frame, pending[:d.frameSize])
			pending = pending[d.frameSize:]
			select {
			case d.frames <- frame:
			default:
				// consumer is lagging — ring already applied drop-oldest,
				// so just drop this frame and log once in a while.
				d.logLaggingDrop()
			}
		}
		if now := time.Now(); now.Sub(d.droppedT) > 5*time.Second {
			dropped := d.ring.Dropped()
			if dropped > 0 {
				d.log("capture: dropped samples in last interval", "count", dropped)
				d.droppedT = now
			}
		}
	}
}

func (d *Device) logLaggingDrop() {
	d.log("capture: frame dropped, consumer lagging")
}
```

- [ ] **Step 6: Create the live-audio test (skipped in normal runs)**

Create `internal/audio/capture/device_live_test.go`:

```go
//go:build audio_live

package capture

import (
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio"
)

func TestDevice_Live(t *testing.T) {
	actx, err := audio.New(nil)
	if err != nil {
		t.Skipf("audio context unavailable: %v", err)
	}
	defer actx.Close()

	dev, err := Open(actx, Config{SampleRate: 16000, Channels: 1, FrameMs: 20})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dev.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer dev.Stop()

	timeout := time.After(2 * time.Second)
	count := 0
	for {
		select {
		case <-dev.Frames():
			count++
			if count >= 10 {
				t.Logf("got %d frames", count)
				return
			}
		case <-timeout:
			t.Fatalf("only got %d frames in 2 seconds", count)
		}
	}
}
```

- [ ] **Step 7: Run the non-live tests**

Run: `go test ./internal/audio/capture/ -v`
Expected: ring tests PASS. Live test is not compiled (tag off).

- [ ] **Step 8: Build with the live tag to verify it compiles**

Run: `go test -tags audio_live -run none ./internal/audio/capture/`
Expected: compiles without error (no tests actually executed because of `-run none`).

- [ ] **Step 9: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 10: Commit**

```bash
git add internal/audio/capture
git commit -m "audio/capture: add SPSC ring and malgo capture device"
```

---

## Task 6: `internal/audio/stt` — WAV encoder, manager, client, Transcriber

**Files:**
- Create: `internal/audio/stt/wav.go`
- Create: `internal/audio/stt/wav_test.go`
- Create: `internal/audio/stt/client.go`
- Create: `internal/audio/stt/client_test.go`
- Create: `internal/audio/stt/manager.go`
- Create: `internal/audio/stt/manager_test.go`
- Create: `internal/audio/stt/transcriber.go`
- Create: `internal/audio/stt/transcriber_test.go`

- [ ] **Step 1: Write the failing WAV test**

Create `internal/audio/stt/wav_test.go`:

```go
package stt

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodeWAV_Header(t *testing.T) {
	samples := []int16{0, 100, -100, 200, -200, 300}
	buf := EncodeWAV(samples, 16000)
	if len(buf) != 44+len(samples)*2 {
		t.Fatalf("len = %d, want %d", len(buf), 44+len(samples)*2)
	}
	if !bytes.Equal(buf[0:4], []byte("RIFF")) {
		t.Errorf("missing RIFF")
	}
	if !bytes.Equal(buf[8:12], []byte("WAVE")) {
		t.Errorf("missing WAVE")
	}
	var sr uint32
	_ = binary.Read(bytes.NewReader(buf[24:28]), binary.LittleEndian, &sr)
	if sr != 16000 {
		t.Errorf("sr = %d", sr)
	}
	// Data bytes start at 44, little-endian int16.
	var first int16
	_ = binary.Read(bytes.NewReader(buf[44:46]), binary.LittleEndian, &first)
	if first != 0 {
		t.Errorf("first sample = %d, want 0", first)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/audio/stt/ -run EncodeWAV -v`
Expected: FAIL — `EncodeWAV undefined`.

- [ ] **Step 3: Implement `wav.go`**

Create `internal/audio/stt/wav.go`:

```go
package stt

import "encoding/binary"

// EncodeWAV wraps PCM16 mono samples in a RIFF/WAVE header in memory.
func EncodeWAV(samples []int16, sampleRate int) []byte {
	const numChannels = 1
	const bitsPerSample = 16
	dataSize := len(samples) * 2
	buf := make([]byte, 44+dataSize)

	copy(buf[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], []byte("WAVE"))
	copy(buf[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], numChannels)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*numChannels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[32:34], numChannels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(buf[34:36], bitsPerSample)
	copy(buf[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[44+i*2:46+i*2], uint16(s))
	}
	return buf
}
```

- [ ] **Step 4: WAV tests should now pass**

Run: `go test ./internal/audio/stt/ -run EncodeWAV -v`
Expected: PASS.

- [ ] **Step 5: Write the failing client test**

Create `internal/audio/stt/client_test.go`:

```go
package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_Transcribe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":" hello world "}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	text, err := c.Transcribe(context.Background(), EncodeWAV([]int16{0, 1, 2, 3}, 16000))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if strings.TrimSpace(text) != "hello world" {
		t.Errorf("text = %q, want hello world", text)
	}
}

func TestClient_Transcribe_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	_, err := c.Transcribe(context.Background(), EncodeWAV([]int16{0}, 16000))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_Transcribe_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 2*time.Second)
	text, err := c.Transcribe(context.Background(), EncodeWAV([]int16{0}, 16000))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}
```

- [ ] **Step 6: Run the client tests to see them fail**

Run: `go test ./internal/audio/stt/ -run Client_ -v`
Expected: FAIL — `NewClient undefined`.

- [ ] **Step 7: Implement `client.go`**

Create `internal/audio/stt/client.go`:

```go
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

// Client POSTs WAV audio to a whisper-server /inference endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

type inferenceResponse struct {
	Text string `json:"text"`
}

// Transcribe POSTs the given WAV bytes to /inference and returns the decoded text.
func (c *Client) Transcribe(ctx context.Context, wav []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="utt.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, bytes.NewReader(wav)); err != nil {
		return "", err
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		tail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("whisper http %d: %s", resp.StatusCode, bytes.TrimSpace(tail))
	}

	var out inferenceResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("whisper json decode: %w", err)
	}
	return out.Text, nil
}
```

- [ ] **Step 8: Run all stt tests**

Run: `go test ./internal/audio/stt/ -v`
Expected: all three client tests PASS, WAV tests continue PASS.

- [ ] **Step 9: Write the failing manager test**

Create `internal/audio/stt/manager_test.go`:

```go
package stt

import (
	"os/exec"
	"testing"
)

func TestManager_BuildArgs(t *testing.T) {
	cfg := ManagerConfig{
		ServerPath: "/bin/whisper-server",
		Host:       "127.0.0.1",
		Port:       17101,
		ModelPath:  "/models/ggml.bin",
		Threads:    4,
	}
	args := buildArgs(cfg)
	want := []string{
		"--model", "/models/ggml.bin",
		"--host", "127.0.0.1",
		"--port", "17101",
		"--threads", "4",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range args {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestManager_ResolveServerPath_Empty(t *testing.T) {
	// When ServerPath is empty, resolveServerPath should use exec.LookPath.
	got, err := resolveServerPath("")
	if err == nil && got == "" {
		t.Errorf("resolveServerPath returned empty path with nil error")
	}
	// Either the binary is in PATH (got != "") or an error is returned. Both fine.
	_ = exec.ErrNotFound
}
```

- [ ] **Step 10: Run it to see it fail**

Run: `go test ./internal/audio/stt/ -run Manager_ -v`
Expected: FAIL — `buildArgs`, `resolveServerPath`, `ManagerConfig` undefined.

- [ ] **Step 11: Implement `manager.go`**

Create `internal/audio/stt/manager.go`:

```go
package stt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ManagerConfig configures the whisper-server subprocess.
type ManagerConfig struct {
	ServerPath       string // empty = look up whisper-server in PATH
	Host             string // default 127.0.0.1
	Port             int    // 0 = pick ephemeral
	ModelPath        string
	Threads          int
	StartupTimeoutMs int
}

// Manager owns the whisper-server child process lifecycle.
type Manager struct {
	cfg       ManagerConfig
	cmd       *exec.Cmd
	baseURL   string
	stderrBuf *lineBuffer
	mu        sync.Mutex
	stopped   bool
}

// Start spawns whisper-server and blocks until it answers GET / or the timeout
// elapses. On failure it kills the child and returns an error with the last
// stderr lines attached for diagnostics.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bin, err := resolveServerPath(m.cfg.ServerPath)
	if err != nil {
		return err
	}
	if m.cfg.Host == "" {
		m.cfg.Host = "127.0.0.1"
	}
	if m.cfg.Port == 0 {
		p, err := pickEphemeralPort()
		if err != nil {
			return err
		}
		m.cfg.Port = p
	}
	if m.cfg.Threads == 0 {
		m.cfg.Threads = 4
	}
	if m.cfg.StartupTimeoutMs == 0 {
		m.cfg.StartupTimeoutMs = 10000
	}

	args := buildArgs(m.cfg)
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	m.stderrBuf = &lineBuffer{cap: 40}
	go m.stderrBuf.consume(stderrPipe)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start whisper-server: %w", err)
	}
	m.cmd = cmd
	m.baseURL = fmt.Sprintf("http://%s:%d", m.cfg.Host, m.cfg.Port)

	if err := m.waitReady(ctx); err != nil {
		_ = m.killLocked()
		return fmt.Errorf("%w\n--- whisper-server stderr ---\n%s", err, m.stderrBuf.String())
	}
	return nil
}

// BaseURL returns the http://host:port for use by Client.
func (m *Manager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.baseURL
}

// Stop terminates the subprocess with SIGTERM, then SIGKILL after a grace period.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.killLocked()
}

func (m *Manager) killLocked() error {
	if m.cmd == nil || m.cmd.Process == nil || m.stopped {
		return nil
	}
	m.stopped = true
	_ = m.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- m.cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		_ = m.cmd.Process.Kill()
		<-done
		return nil
	}
}

func (m *Manager) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(time.Duration(m.cfg.StartupTimeoutMs) * time.Millisecond)
	hc := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := hc.Get(m.baseURL + "/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("whisper-server readiness timed out")
}

func resolveServerPath(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("whisper-server not at %q: %w", configured, err)
		}
		return configured, nil
	}
	p, err := exec.LookPath("whisper-server")
	if err != nil {
		return "", fmt.Errorf("whisper-server not found in PATH; set freeman.stt.server_path in config")
	}
	return p, nil
}

func buildArgs(cfg ManagerConfig) []string {
	return []string{
		"--model", cfg.ModelPath,
		"--host", cfg.Host,
		"--port", strconv.Itoa(cfg.Port),
		"--threads", strconv.Itoa(cfg.Threads),
	}
}

func pickEphemeralPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// lineBuffer accumulates the last N lines of stderr for diagnostics.
type lineBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

func (b *lineBuffer) consume(r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		b.mu.Lock()
		b.lines = append(b.lines, s.Text())
		if len(b.lines) > b.cap {
			b.lines = b.lines[len(b.lines)-b.cap:]
		}
		b.mu.Unlock()
	}
}

func (b *lineBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := ""
	for _, l := range b.lines {
		out += l + "\n"
	}
	return out
}

// NewManager constructs a Manager; Start must be called before use.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{cfg: cfg}
}
```

- [ ] **Step 12: Run manager tests**

Run: `go test ./internal/audio/stt/ -run Manager_ -v`
Expected: both PASS.

- [ ] **Step 13: Write the failing Transcriber test**

Create `internal/audio/stt/transcriber_test.go`:

```go
package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio/vad"
)

func TestTranscriber_EmitsAndMutes(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	defer srv.Close()

	utts := make(chan vad.Utterance, 4)
	c := NewClient(srv.URL, time.Second)
	tr := NewTranscriber(c, utts, 16000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Run(ctx)

	utts <- vad.Utterance{PCM: []int16{0, 1, 2, 3}, DurationMs: 320}
	select {
	case got := <-tr.Utterances():
		if got != "hello" {
			t.Errorf("got %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no utterance")
	}

	// Mute, then send another — should NOT appear on Utterances().
	tr.Mute()
	utts <- vad.Utterance{PCM: []int16{0, 1, 2, 3}, DurationMs: 320}
	select {
	case got := <-tr.Utterances():
		t.Fatalf("muted but got %q", got)
	case <-time.After(200 * time.Millisecond):
		// good
	}
	tr.Unmute()

	// After unmute, next utterance goes through.
	utts <- vad.Utterance{PCM: []int16{0, 1, 2, 3}, DurationMs: 320}
	select {
	case got := <-tr.Utterances():
		if got != "hello" {
			t.Errorf("got %q after unmute", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no utterance after unmute")
	}

	if calls < 2 {
		t.Errorf("whisper calls = %d, want >= 2 (muted utterance should still POST but result is dropped)", calls)
	}
}

func TestTranscriber_DropsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"   "}`))
	}))
	defer srv.Close()

	utts := make(chan vad.Utterance, 1)
	c := NewClient(srv.URL, time.Second)
	tr := NewTranscriber(c, utts, 16000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Run(ctx)

	utts <- vad.Utterance{PCM: []int16{0, 1}, DurationMs: 40}
	select {
	case got := <-tr.Utterances():
		t.Errorf("whitespace passed through: %q", got)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}
```

Note the test's assertion "muted utterance should still POST": this is a deliberate choice — we run whisper unconditionally and drop the *result* when muted, rather than gating the POST. Keeping a single code path is simpler than racing the mute flag against the HTTP call.

- [ ] **Step 14: Run it to see it fail**

Run: `go test ./internal/audio/stt/ -run Transcriber -v`
Expected: FAIL — `NewTranscriber undefined`.

- [ ] **Step 15: Implement `transcriber.go`**

Create `internal/audio/stt/transcriber.go`:

```go
package stt

import (
	"context"
	"strings"
	"sync"

	"github.com/Renderix/freeman/internal/audio/vad"
)

// Transcriber consumes utterance PCM from a VAD channel, POSTs each to a
// whisper-server via Client, and emits non-empty text on Utterances(). It also
// implements audio.Muter: while muted, results are dropped silently.
type Transcriber struct {
	client     *Client
	in         <-chan vad.Utterance
	out        chan string
	sampleRate int

	mu    sync.Mutex
	muted bool
}

func NewTranscriber(c *Client, in <-chan vad.Utterance, sampleRate int) *Transcriber {
	return &Transcriber{
		client:     c,
		in:         in,
		out:        make(chan string, 4),
		sampleRate: sampleRate,
	}
}

// Run starts the background goroutine that drives transcription until ctx ends
// or the input channel closes.
func (t *Transcriber) Run(ctx context.Context) {
	go func() {
		defer close(t.out)
		for {
			select {
			case <-ctx.Done():
				return
			case u, ok := <-t.in:
				if !ok {
					return
				}
				wav := EncodeWAV(u.PCM, t.sampleRate)
				text, err := t.client.Transcribe(ctx, wav)
				if err != nil {
					// Per the spec, whisper errors are logged and the Session
					// simply never sees an utterance for this VAD segment. It
					// is Plan 3's job to surface a spoken "transcriber error"
					// message via a diagnostics channel.
					continue
				}
				text = strings.TrimSpace(text)
				if text == "" {
					continue
				}
				if t.isMuted() {
					continue
				}
				select {
				case t.out <- text:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

// Utterances implements call.Transcriber.
func (t *Transcriber) Utterances() <-chan string { return t.out }

// Stop is a no-op — the Run goroutine exits when ctx is canceled or in closes.
// Kept to satisfy call.Transcriber.
func (t *Transcriber) Stop() {}

// Mute implements audio.Muter.
func (t *Transcriber) Mute() {
	t.mu.Lock()
	t.muted = true
	t.mu.Unlock()
}

// Unmute implements audio.Muter.
func (t *Transcriber) Unmute() {
	t.mu.Lock()
	t.muted = false
	t.mu.Unlock()
}

func (t *Transcriber) isMuted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.muted
}
```

- [ ] **Step 16: Run all stt tests**

Run: `go test ./internal/audio/stt/ -v`
Expected: all tests in the package PASS.

- [ ] **Step 17: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 18: Commit**

```bash
git add internal/audio/stt
git commit -m "audio/stt: add WAV encoder, HTTP client, server manager, and Transcriber"
```

---

## Task 7: `internal/audio/playback` — Speaker with muter integration

**Files:**
- Create: `internal/audio/playback/speaker.go`
- Create: `internal/audio/playback/speaker_test.go`
- Create: `internal/audio/playback/speaker_live_test.go`

- [ ] **Step 1: Write the failing Speaker test**

Create `internal/audio/playback/speaker_test.go`:

```go
package playback

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio"
)

// fakeSink captures PCM chunks and simulates draining.
type fakeSink struct {
	chunks [][]int16
}

func (f *fakeSink) write(samples []int16) error {
	// Copy so caller can reuse its buffer.
	cp := make([]int16, len(samples))
	copy(cp, samples)
	f.chunks = append(f.chunks, cp)
	return nil
}

func (f *fakeSink) drain(ctx context.Context) error {
	return nil
}

func (f *fakeSink) close() error { return nil }

// fakeSynth returns predetermined PCM for any text.
type fakeSynth struct {
	samples []int16
	sr      int
}

func (f *fakeSynth) GeneratePCM(text, voice string, speed float64) ([]int16, int, error) {
	return f.samples, f.sr, nil
}

// callRecorder logs mute/unmute order for assertions.
type callRecorder struct {
	events []string
	mu     atomicEventRecorder
}

type atomicEventRecorder struct {
	seq int64
}

func newRecorder() *callRecorder { return &callRecorder{} }
func (r *callRecorder) Mute() {
	r.mu.seq++
	r.events = append(r.events, "mute")
}
func (r *callRecorder) Unmute() {
	r.mu.seq++
	r.events = append(r.events, "unmute")
}

func TestSpeaker_Speak_MuteOrderAndChunks(t *testing.T) {
	samples := make([]int16, 24000) // 1 second at 24 kHz
	for i := range samples {
		samples[i] = int16(i % 100)
	}
	synth := &fakeSynth{samples: samples, sr: 24000}
	sink := &fakeSink{}
	rec := newRecorder()

	s := newSpeakerForTest(synth, rec, sink, 50 /* chunkMs */)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Speak(ctx, "hello"); err != nil {
		t.Fatalf("Speak: %v", err)
	}

	if got := len(rec.events); got != 2 {
		t.Fatalf("mute events = %d, want 2", got)
	}
	if rec.events[0] != "mute" || rec.events[1] != "unmute" {
		t.Errorf("events = %v, want [mute unmute]", rec.events)
	}

	// 1 s at 24 kHz / 50 ms chunks = 20 chunks.
	if len(sink.chunks) != 20 {
		t.Errorf("chunks = %d, want 20", len(sink.chunks))
	}

	// Every sample accounted for.
	total := 0
	for _, c := range sink.chunks {
		total += len(c)
	}
	if total != len(samples) {
		t.Errorf("total samples = %d, want %d", total, len(samples))
	}
}

func TestSpeaker_Speak_CtxCancelShortCircuits(t *testing.T) {
	samples := make([]int16, 48000) // 2 seconds
	synth := &fakeSynth{samples: samples, sr: 24000}
	sink := &blockingFakeSink{release: make(chan struct{})}
	rec := newRecorder()

	s := newSpeakerForTest(synth, rec, sink, 50)
	ctx, cancel := context.WithCancel(context.Background())

	var unmutedEarly atomic.Bool
	done := make(chan error, 1)
	go func() { done <- s.Speak(ctx, "text") }()

	time.Sleep(100 * time.Millisecond)
	cancel()
	// Release the sink so Speak can exit.
	close(sink.release)

	select {
	case err := <-done:
		if err == nil {
			t.Log("speak returned nil (sink drained before cancel took effect)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Speak did not return after cancel")
	}
	// Unmute must have run regardless of cancel path.
	if len(rec.events) < 2 || rec.events[len(rec.events)-1] != "unmute" {
		t.Errorf("last event = %v, want unmute", rec.events)
	}
	_ = unmutedEarly
}

type blockingFakeSink struct {
	release chan struct{}
	fakeSink
}

func (b *blockingFakeSink) write(samples []int16) error {
	select {
	case <-b.release:
	case <-time.After(5 * time.Second):
	}
	return b.fakeSink.write(samples)
}

func newSpeakerForTest(synth Synthesizer, muter audio.Muter, sink pcmSink, chunkMs int) *Speaker {
	return &Speaker{
		synth:   synth,
		muter:   muter,
		sink:    sink,
		chunkMs: chunkMs,
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/audio/playback/ -v`
Expected: FAIL — `Speaker`, `Synthesizer`, `pcmSink`, `newSpeakerForTest` undefined.

- [ ] **Step 3: Implement `speaker.go`**

Create `internal/audio/playback/speaker.go`:

```go
// Package playback drives Kokoro PCM to the system speakers via malgo and
// manages self-echo suppression through the audio.Muter interface.
package playback

import (
	"context"
	"fmt"
	"sync"
	"unsafe"

	"github.com/Renderix/freeman/internal/audio"
	"github.com/gen2brain/malgo"
)

// Synthesizer is the subset of engine.TTSEngine this package needs.
// Voice and speed are passed through on every call so the caller (Speaker)
// can hold them as configuration.
type Synthesizer interface {
	GeneratePCM(text, voice string, speed float64) ([]int16, int, error)
}

// pcmSink abstracts the malgo device so tests can run without hardware.
type pcmSink interface {
	write(samples []int16) error
	drain(ctx context.Context) error
	close() error
}

// Speaker implements call.Speaker.
type Speaker struct {
	actx    *audio.Context
	synth   Synthesizer
	muter   audio.Muter
	sink    pcmSink
	chunkMs int
	voice   string
	speed   float64

	mu sync.Mutex // serializes concurrent Speak calls
}

// Config selects the output device and the synthesis parameters.
type Config struct {
	DeviceID string  // empty = default
	ChunkMs  int     // default 50
	Voice    string  // e.g. "af_heart"
	Speed    float64 // e.g. 1.0
}

// Open constructs a Speaker and opens an output device bound to the given synth.
// muter is the audio.Muter that Speak will call around playback (typically the
// stt.Transcriber). Pass audio.NoopMuter{} if no transcription is wired.
func Open(actx *audio.Context, cfg Config, synth Synthesizer, muter audio.Muter) (*Speaker, error) {
	if cfg.ChunkMs == 0 {
		cfg.ChunkMs = 50
	}
	if cfg.Voice == "" {
		cfg.Voice = "af_heart"
	}
	if cfg.Speed == 0 {
		cfg.Speed = 1.0
	}
	// Device is opened lazily inside Speak so we inherit the engine's sample rate
	// without synthesizing a probe utterance.
	return &Speaker{
		actx:    actx,
		synth:   synth,
		muter:   muter,
		sink:    nil,
		chunkMs: cfg.ChunkMs,
		voice:   cfg.Voice,
		speed:   cfg.Speed,
	}, nil
}

// Speak synthesizes and plays text. Blocks until playback drains or ctx cancels.
// Muter is called as: Mute before, Unmute deferred.
func (s *Speaker) Speak(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	samples, sr, err := s.synth.GeneratePCM(text, s.voice, s.speed)
	if err != nil {
		return fmt.Errorf("synth: %w", err)
	}
	if len(samples) == 0 {
		return nil
	}

	if s.sink == nil {
		sink, err := newMalgoSink(s.actx, sr, 1)
		if err != nil {
			return fmt.Errorf("open playback device: %w", err)
		}
		s.sink = sink
	}

	s.muter.Mute()
	defer s.muter.Unmute()

	chunkSize := sr * s.chunkMs / 1000
	for off := 0; off < len(samples); off += chunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		end := off + chunkSize
		if end > len(samples) {
			end = len(samples)
		}
		if err := s.sink.write(samples[off:end]); err != nil {
			return err
		}
	}
	return s.sink.drain(ctx)
}

// Close tears down the output device. Safe on shutdown paths.
func (s *Speaker) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sink == nil {
		return nil
	}
	err := s.sink.close()
	s.sink = nil
	return err
}

// malgoSink is the production pcmSink.
type malgoSink struct {
	dev   *malgo.Device
	ch    chan []int16
	pending []int16
}

func newMalgoSink(actx *audio.Context, sampleRate, channels int) (*malgoSink, error) {
	m := &malgoSink{
		ch: make(chan []int16, 16),
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = uint32(channels)
	cfg.SampleRate = uint32(sampleRate)

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutput, _ []byte, frameCount uint32) {
			need := int(frameCount) * channels
			out := unsafe.Slice((*int16)(unsafe.Pointer(&pOutput[0])), need)
			filled := 0
			// Drain pending first.
			if len(m.pending) > 0 {
				n := copy(out, m.pending)
				filled += n
				m.pending = m.pending[n:]
			}
			for filled < need {
				select {
				case chunk, ok := <-m.ch:
					if !ok {
						for i := filled; i < need; i++ {
							out[i] = 0
						}
						return
					}
					n := copy(out[filled:], chunk)
					if n < len(chunk) {
						m.pending = chunk[n:]
					}
					filled += n
				default:
					for i := filled; i < need; i++ {
						out[i] = 0
					}
					return
				}
			}
		},
	}

	dev, err := malgo.InitDevice(actx.Raw().Context, cfg, callbacks)
	if err != nil {
		return nil, err
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return nil, err
	}
	m.dev = dev
	return m, nil
}

func (m *malgoSink) write(samples []int16) error {
	cp := make([]int16, len(samples))
	copy(cp, samples)
	m.ch <- cp
	return nil
}

func (m *malgoSink) drain(ctx context.Context) error {
	// Poll until the send channel and pending tail are both empty. The audio
	// callback does not signal directly when it finishes a chunk, so a short
	// sleep between checks is the simplest correct approach.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if len(m.ch) == 0 && len(m.pending) == 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *malgoSink) close() error {
	if m.dev == nil {
		return nil
	}
	_ = m.dev.Stop()
	m.dev.Uninit()
	m.dev = nil
	close(m.ch)
	return nil
}
```

Make sure `"time"` is included in the file's import block — `drain` uses `time.Sleep`.

- [ ] **Step 4: Run speaker tests**

Run: `go test ./internal/audio/playback/ -v`
Expected: both non-live tests PASS.

- [ ] **Step 5: Add the live audio test**

Create `internal/audio/playback/speaker_live_test.go`:

```go
//go:build audio_live

package playback

import (
	"context"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/audio"
)

// staticSynth returns a 200 ms 440 Hz sine at 24 kHz.
type staticSynth struct{}

func (staticSynth) GeneratePCM(_, _ string, _ float64) ([]int16, int, error) {
	const sr = 24000
	const dur = 0.2
	n := int(sr * dur)
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		// very simple sine approximation; integer triangle is fine
		out[i] = int16((i % 100) * 300)
	}
	return out, sr, nil
}

func TestSpeaker_Live(t *testing.T) {
	actx, err := audio.New(nil)
	if err != nil {
		t.Skipf("audio context unavailable: %v", err)
	}
	defer actx.Close()

	sp, err := Open(actx, Config{Voice: "af_heart", Speed: 1.0}, staticSynth{}, &audio.NoopMuter{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sp.Speak(ctx, "test"); err != nil {
		t.Fatalf("Speak: %v", err)
	}
}
```

- [ ] **Step 6: Verify the live tag still compiles**

Run: `go test -tags audio_live -run none ./internal/audio/playback/`
Expected: compiles, no tests run.

- [ ] **Step 7: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/audio/playback
git commit -m "audio/playback: add Speaker with muter-wrapped malgo output"
```

---

## Task 8: `internal/audio/hotkey` — TTY raw-mode keypress

**Files:**
- Create: `internal/audio/hotkey/hotkey.go`
- Create: `internal/audio/hotkey/hotkey_test.go`
- Modify: `go.mod` (add `golang.org/x/term` if absent)

- [ ] **Step 1: Ensure the term dependency is present**

Run:
```
go get golang.org/x/term@latest
go mod tidy
```
(No-op if already pulled in transitively.)

- [ ] **Step 2: Write the failing tests**

Create `internal/audio/hotkey/hotkey_test.go`:

```go
package hotkey

import (
	"bytes"
	"testing"
	"time"
)

func TestStdinLine_EmitsOnNewline(t *testing.T) {
	r := bytes.NewBufferString("hello\nworld\n")
	h := newStdinLineHotkey(r)
	h.run()

	got := 0
	timeout := time.After(time.Second)
loop:
	for {
		select {
		case <-h.Events():
			got++
		case <-timeout:
			break loop
		}
		if got >= 2 {
			break
		}
	}
	if got != 2 {
		t.Errorf("events = %d, want 2", got)
	}
}

func TestTTYKeyMatch_Enter(t *testing.T) {
	if !matchKey("enter", '\r') {
		t.Errorf("enter should match \\r")
	}
	if !matchKey("enter", '\n') {
		t.Errorf("enter should match \\n")
	}
	if matchKey("enter", ' ') {
		t.Errorf("enter should not match space")
	}
}

func TestTTYKeyMatch_Space(t *testing.T) {
	if !matchKey("space", ' ') {
		t.Errorf("space should match ' '")
	}
	if matchKey("space", '\r') {
		t.Errorf("space should not match \\r")
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/audio/hotkey/ -v`
Expected: FAIL — `newStdinLineHotkey`, `matchKey` undefined.

- [ ] **Step 4: Implement `hotkey.go`**

Create `internal/audio/hotkey/hotkey.go`:

```go
// Package hotkey provides a terminal-based hotkey that posts an event whenever
// the user presses a configured key. In TTY mode it puts the terminal into raw
// mode and reads single bytes; in stdin-line mode (fallback for non-TTY stdin)
// it posts on every newline.
package hotkey

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// Config selects the mode and the target key.
type Config struct {
	Mode string // "tty" | "stdin-line"
	Key  string // "enter" | "space"
}

// Hotkey is the public type returned by Open.
type Hotkey struct {
	events    chan struct{}
	stopOnce  sync.Once
	stopCh    chan struct{}
	restoreFn func()
}

func (h *Hotkey) Events() <-chan struct{} { return h.events }

func (h *Hotkey) Stop() {
	h.stopOnce.Do(func() {
		close(h.stopCh)
		if h.restoreFn != nil {
			h.restoreFn()
		}
	})
}

// Open constructs a Hotkey based on cfg. If cfg.Mode is "tty" but stdin is not
// a TTY, it falls back to stdin-line mode and prints a notice to stderr.
func Open(cfg Config) (*Hotkey, error) {
	if cfg.Key == "" {
		cfg.Key = "enter"
	}
	if cfg.Mode == "" {
		cfg.Mode = "tty"
	}
	h := &Hotkey{
		events: make(chan struct{}, 4),
		stopCh: make(chan struct{}),
	}
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if cfg.Mode == "tty" && !isTTY {
		fmt.Fprintln(os.Stderr, "freeman: stdin is not a TTY; hotkey falls back to line mode")
		cfg.Mode = "stdin-line"
	}
	switch cfg.Mode {
	case "tty":
		return h, h.startTTY(cfg.Key)
	case "stdin-line":
		h.restoreFn = func() {}
		go runStdinLine(os.Stdin, h.events, h.stopCh)
		return h, nil
	default:
		return nil, fmt.Errorf("unknown hotkey mode %q", cfg.Mode)
	}
}

func (h *Hotkey) startTTY(key string) error {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("tty raw mode: %w", err)
	}
	h.restoreFn = func() { _ = term.Restore(fd, oldState) }

	// Also restore on SIGINT / SIGTERM so a crash doesn't wedge the shell.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		h.restoreFn()
	}()

	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-h.stopCh:
				return
			default:
			}
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			if n == 0 {
				continue
			}
			if matchKey(key, rune(buf[0])) {
				select {
				case h.events <- struct{}{}:
				default:
				}
			}
			if buf[0] == 0x03 { // Ctrl-C in raw mode
				h.restoreFn()
				_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
				return
			}
		}
	}()
	return nil
}

// matchKey maps the config key name to a byte match.
func matchKey(name string, r rune) bool {
	switch strings.ToLower(name) {
	case "enter":
		return r == '\r' || r == '\n'
	case "space":
		return r == ' '
	default:
		return false
	}
}

// runStdinLine drives the stdin-line fallback: one event per newline from r.
// Runs until r hits EOF or stopCh closes.
func runStdinLine(r io.Reader, events chan<- struct{}, stopCh <-chan struct{}) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-stopCh:
			return
		default:
		}
		select {
		case events <- struct{}{}:
		default:
		}
	}
}

// stdinLineHotkey is a test-only wrapper that exposes the runStdinLine
// goroutine over an owned channel, so unit tests can feed it a bytes.Buffer
// without touching os.Stdin or TTY plumbing.
type stdinLineHotkey struct {
	events chan struct{}
	stopCh chan struct{}
	reader io.Reader
}

func newStdinLineHotkey(r io.Reader) *stdinLineHotkey {
	return &stdinLineHotkey{
		events: make(chan struct{}, 4),
		stopCh: make(chan struct{}),
		reader: r,
	}
}

func (s *stdinLineHotkey) Events() <-chan struct{} { return s.events }

func (s *stdinLineHotkey) run() {
	go runStdinLine(s.reader, s.events, s.stopCh)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/audio/hotkey/ -v`
Expected: `TestStdinLine_EmitsOnNewline`, `TestTTYKeyMatch_Enter`, `TestTTYKeyMatch_Space` all PASS.

- [ ] **Step 6: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/audio/hotkey go.mod go.sum
git commit -m "audio/hotkey: add TTY raw-mode and stdin-line hotkey"
```

---

## Task 9: Wire real audio into `cmd/freeman/call.go`

**Background** (from the Plan-1 `cmd/freeman/call.go`, verified before writing this plan):

- `configFile` is the package-level flag var (set in `cmd/freeman/main.go`) — not `configPath`.
- The fakes names are: `fakes.NewLineReaderTranscriber(os.Stdin)`, `fakes.NewStdoutSpeaker(os.Stdout)`, `fakes.NewScriptedPM()`. There is no `NewStdinHotkey` — Plan 1 uses a local `channelHotkey` struct fed by SIGUSR1.
- The sidecar is spawned via `sidecar.Spawn(ctx, "bun", "run", filepath.Join(repoRoot, "sidecar", "stub.ts"))`, where `repoRoot` is found by walking up for `go.mod` via `findRepoRoot()`.
- `call.NewSession` takes a `call.SessionDeps{Transcriber, Speaker, PM, Hotkey, Sidecar}` struct literal. No `Log` field — do not add one.
- The current Plan-1 `runCall` is a single function (~50 lines). Plan 2 splits it into two helpers plus a dispatcher.

**Files:**
- Modify: `cmd/freeman/call.go`

- [ ] **Step 1: Re-read the current `call.go`**

Run: `cat cmd/freeman/call.go`
Orient yourself before editing. Notice the Plan-1 `channelHotkey` type at the bottom and the `findRepoRoot` helper — both are kept and reused.

- [ ] **Step 2: Add the `--fake-audio` flag**

Near the top of `cmd/freeman/call.go`, add a package-level bool and register it in an `init()` on the same file. If `init` does not yet exist, add one:

```go
var fakeAudio bool

func init() {
	callCmd.Flags().BoolVar(&fakeAudio, "fake-audio", false, "use Plan 1 stdin/stdout audio fakes (headless testing)")
}
```

- [ ] **Step 3: Rewrite `runCall` as a dispatcher**

Replace the existing `runCall` function with:

```go
func runCall(cmd *cobra.Command, args []string) error {
	conf := config.LoadConfig(configFile)

	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if fakeAudio {
		return runCallWithFakes(ctx, conf)
	}
	return runCallWithRealAudio(ctx, conf)
}
```

- [ ] **Step 4: Extract the Plan-1 fake wiring into `runCallWithFakes`**

Add a new function that is the Plan-1 body, almost verbatim:

```go
func runCallWithFakes(ctx context.Context, conf config.Config) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	stubPath := filepath.Join(repoRoot, "sidecar", "stub.ts")
	sc, err := sidecar.Spawn(ctx, "bun", "run", stubPath)
	if err != nil {
		return fmt.Errorf("spawn sidecar: %w", err)
	}
	defer sc.Close()

	tr := fakes.NewLineReaderTranscriber(os.Stdin)
	defer tr.Stop()

	speaker := fakes.NewStdoutSpeaker(os.Stdout)
	pm := fakes.NewScriptedPM()

	hkChan := make(chan struct{}, 4)
	sigChan := make(chan os.Signal, 4)
	signal.Notify(sigChan, syscall.SIGUSR1)
	defer func() {
		signal.Stop(sigChan)
		close(sigChan)
	}()
	go func() {
		for range sigChan {
			hkChan <- struct{}{}
		}
	}()
	hk := &channelHotkey{ch: hkChan}

	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     speaker,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     sc,
	})

	fmt.Fprintln(os.Stderr, "freeman: ready. SIGUSR1 to start a call, type utterances as lines.")
	fmt.Fprintf(os.Stderr, "freeman: pid=%d\n", os.Getpid())
	return session.Run(ctx)
}
```

Leave the existing `channelHotkey` struct and `findRepoRoot` helper at the bottom of the file — they are still used by the fakes path.

- [ ] **Step 5: Add `runCallWithRealAudio`**

Append:

```go
func runCallWithRealAudio(ctx context.Context, conf config.Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 1. Shared audio context (malgo).
	actx, err := audio.New(logger)
	if err != nil {
		return fmt.Errorf("audio init: %w", err)
	}
	defer actx.Close()

	// 2. Kokoro engine (TTS).
	eng, err := engine.NewTTSEngine(
		filepath.Join(conf.Model.Dir, conf.Model.ModelFile),
		filepath.Join(conf.Model.Dir, conf.Model.VoicesFile),
		filepath.Join(conf.Model.Dir, conf.Model.TokensFile),
		filepath.Join(conf.Model.Dir, conf.Model.DataDir),
	)
	if err != nil {
		return fmt.Errorf("engine init: %w", err)
	}

	// 3. whisper-server subprocess.
	mgr := stt.NewManager(stt.ManagerConfig{
		ServerPath:       conf.Freeman.STT.ServerPath,
		Host:             "127.0.0.1",
		Port:             conf.Freeman.STT.ServerPort,
		ModelPath:        conf.Freeman.STT.ModelPath,
		StartupTimeoutMs: conf.Freeman.STT.StartupTimeoutMS,
	})
	fmt.Fprintln(os.Stderr, "freeman: warming up whisper…")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("whisper-server: %w", err)
	}
	defer mgr.Stop()

	// 4. Mic capture.
	cap, err := capture.Open(actx, capture.Config{
		DeviceID:   conf.Freeman.Audio.InputDevice,
		SampleRate: 16000,
		Channels:   1,
		FrameMs:    20,
	})
	if err != nil {
		return fmt.Errorf("capture open: %w", err)
	}
	defer cap.Stop()
	if err := cap.Start(); err != nil {
		return fmt.Errorf("capture start: %w", err)
	}

	// 5. VAD endpointing.
	v, err := vad.New(vad.Config{
		SilenceMs:      conf.Freeman.STT.VAD.SilenceMS,
		MinSpeechMs:    conf.Freeman.STT.VAD.MinSpeechMS,
		HangoverMs:     conf.Freeman.STT.VAD.HangoverMS,
		Aggressiveness: conf.Freeman.STT.VAD.Aggressiveness,
		SampleRate:     16000,
		FrameMs:        20,
	})
	if err != nil {
		return fmt.Errorf("vad init: %w", err)
	}
	uttCh := v.Run(ctx, cap.Frames())

	// 6. STT Transcriber (also implements audio.Muter).
	client := stt.NewClient(mgr.BaseURL(), 10*time.Second)
	tr := stt.NewTranscriber(client, uttCh, 16000)
	tr.Run(ctx)

	// 7. Playback Speaker, pointed at the Transcriber as its Muter.
	sp, err := playback.Open(actx, playback.Config{
		DeviceID: conf.Freeman.Audio.OutputDevice,
		ChunkMs:  50,
		Voice:    conf.TTS.DefaultVoice,
		Speed:    conf.TTS.DefaultSpeed,
	}, eng, tr)
	if err != nil {
		return fmt.Errorf("playback open: %w", err)
	}
	defer sp.Close()

	// 8. Hotkey (TTY or stdin-line).
	hk, err := hotkey.Open(hotkey.Config{
		Mode: conf.Freeman.Hotkey.Mode,
		Key:  conf.Freeman.Hotkey.Key,
	})
	if err != nil {
		return fmt.Errorf("hotkey open: %w", err)
	}
	defer hk.Stop()

	// 9. Stub sidecar (unchanged).
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	stubPath := filepath.Join(repoRoot, "sidecar", "stub.ts")
	sc, err := sidecar.Spawn(ctx, "bun", "run", stubPath)
	if err != nil {
		return fmt.Errorf("spawn sidecar: %w", err)
	}
	defer sc.Close()

	fmt.Fprintln(os.Stderr, "freeman: ready")

	// 10. Session (ScriptedPM unchanged).
	pm := fakes.NewScriptedPM()
	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     sp,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     sc,
	})
	return session.Run(ctx)
}
```

Note the field name `StartupTimeoutMs` here is what `stt.ManagerConfig` expects (from Task 6); the config struct field is `StartupTimeoutMS` (from Task 1). The mismatch is intentional — it's a conversion at the boundary and keeps the existing config casing convention intact.

- [ ] **Step 6: Update imports**

The existing Plan-1 import block is:

```go
import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/call/fakes"
	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/sidecar"
	"github.com/spf13/cobra"
)
```

Add:

```go
	"log/slog"
	"time"

	"github.com/Renderix/freeman/internal/audio"
	"github.com/Renderix/freeman/internal/audio/capture"
	"github.com/Renderix/freeman/internal/audio/hotkey"
	"github.com/Renderix/freeman/internal/audio/playback"
	"github.com/Renderix/freeman/internal/audio/stt"
	"github.com/Renderix/freeman/internal/audio/vad"
	"github.com/Renderix/freeman/internal/engine"
```

Run `goimports -w cmd/freeman/call.go` if it's available, otherwise hand-merge the imports.

- [ ] **Step 7: Build and run go vet**

Run: `go build ./... && go vet ./...`
Expected: clean build.

- [ ] **Step 8: Run the full test suite with race detector**

Run: `go test -race ./...`
Expected: all tests PASS. The Plan 1 `session_test.go` must keep passing unchanged — that is the design invariant of Plan 2.

- [ ] **Step 9: Smoke test the `--fake-audio` path**

In one terminal, run `./freeman call --fake-audio`. In another, drive the Plan 1 smoke procedure (SIGUSR1 + stdin lines). Expected behavior is identical to Plan 1: greeting, intake, summary, done, back to idle.

- [ ] **Step 10: Commit**

```bash
git add cmd/freeman/call.go
git commit -m "cmd: wire real audio into freeman call, keep --fake-audio for tests"
```

---

## Task 10: Add `scripts/setup_whisper_model.sh` and README prerequisites

**Files:**
- Create: `scripts/setup_whisper_model.sh`
- Modify: `README.md`

- [ ] **Step 1: Create the download script**

Create `scripts/setup_whisper_model.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

MODEL_DIR="${MODEL_DIR:-./models/whisper}"
MODEL_NAME="${MODEL_NAME:-ggml-large-v3-turbo.bin}"
URL="${URL:-https://huggingface.co/ggerganov/whisper.cpp/resolve/main/${MODEL_NAME}}"

mkdir -p "${MODEL_DIR}"
OUT="${MODEL_DIR}/${MODEL_NAME}"

if [[ -f "${OUT}" ]]; then
	echo "whisper model already at ${OUT}"
	exit 0
fi

echo "downloading ${MODEL_NAME} (~1.5 GB) to ${OUT}…"
if command -v curl >/dev/null 2>&1; then
	curl -L --fail --progress-bar "${URL}" -o "${OUT}.part"
else
	wget --progress=bar:force -O "${OUT}.part" "${URL}"
fi
mv "${OUT}.part" "${OUT}"
echo "done: ${OUT}"
```

Run: `chmod +x scripts/setup_whisper_model.sh`

- [ ] **Step 2: Update README**

Modify `README.md`. Add a new section (append or slot near existing setup instructions):

```markdown
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
   Default location: `models/whisper/ggml-large-v3-turbo.bin` (~1.5 GB).

3. **Microphone permission** — on first run, macOS will prompt to grant Freeman
   access. If you decline, Freeman exits with a message pointing at
   `System Settings → Privacy & Security → Microphone`.

After that, `./freeman call`, wait for `freeman: ready`, and press Enter.
```

- [ ] **Step 3: Commit**

```bash
git add scripts/setup_whisper_model.sh README.md
git commit -m "scripts: add whisper model downloader and README prerequisites"
```

---

## Task 11: Plan 2 completion smoke test

**Files:** none (this task only runs the program and confirms behavior).

This is the gate for declaring Plan 2 done. Do not mark the plan complete unless every step produces the expected outcome.

- [ ] **Step 1: Preconditions**

Run:
```
go build -o freeman ./cmd/freeman
ls models/whisper/ggml-large-v3-turbo.bin
which whisper-server
```
Expected: binary built, model file present, `whisper-server` resolved to a path.

- [ ] **Step 2: Start the call**

Run: `./freeman call`
Expected stderr (roughly):
```
freeman: warming up whisper…
freeman: ready
```
Terminal is in raw mode (keypresses not echoed).

- [ ] **Step 3: Press Enter (hotkey)**

Expected: hear Kokoro say `hi. what are we building?` through your speakers.

- [ ] **Step 4: Speak a phrase**

Say: `build a feature flag`
Expected: after you stop talking and the 800 ms silence window elapses, Kokoro says `tell me more about constraints.`

- [ ] **Step 5: Speak another phrase**

Say: `off ten percent`
Expected: Kokoro says the canned `scripted summary. should i start?`

- [ ] **Step 6: Confirm**

Say: `yes`
Expected: Kokoro says `starting now.` followed shortly by `done. stub edited 0 files and made coffee`.

- [ ] **Step 7: Hang up**

Press Enter.
Expected: Session returns to Idle. The terminal is still in raw mode, ready for another call.

- [ ] **Step 8: Exit cleanly**

Press Ctrl-C.
Expected: Freeman exits. The terminal is restored to cooked mode (`echo` works again, arrow keys behave normally). `whisper-server` child process is gone (`pgrep whisper-server` returns nothing).

- [ ] **Step 9: Run full test suite one more time**

Run: `go test -race ./...`
Expected: all PASS, no regressions.

- [ ] **Step 10: Final commit and summary**

If any small fixes were needed during the smoke test, commit them and note what was tuned (e.g., VAD silence window). Then:

```bash
git log --oneline -20
```

Expected: a clean sequence of Plan 2 commits. Freeman now does real audio end-to-end; Plan 3 (Haiku PM + real pi-coding-agent sidecar) is unblocked.
