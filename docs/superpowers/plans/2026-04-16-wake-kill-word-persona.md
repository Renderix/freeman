# Wake/Kill Word + Persona System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hotkey system with Porcupine wake word detection and introduce a configurable persona system, starting with "Horus" as the first persona.

**Architecture:** Porcupine runs as an independent audio consumer alongside VAD, reading raw capture frames via a new Subscribe() fan-out on the capture device. The session event loop replaces its hotkey case with a wakeword events case, managing Idle/Awake/Shutdown states. All persona-specific configuration (name, greeting, traits, rules, keywords) lives in a top-level `persona:` block in config.yaml.

**Tech Stack:** Go, Porcupine Go SDK (`github.com/Picovoice/porcupine/binding/go`), malgo, WebRTC VAD, Whisper STT

**Spec:** `docs/superpowers/specs/2026-04-16-wake-kill-word-persona-design.md`

---

### Task 1: Add Persona Config

**Files:**
- Modify: `internal/config/config.go` (lines 10-80)
- Modify: `config.yaml`

- [ ] **Step 1: Add PersonaConfig struct to config.go**

Add after the existing `LoggingConfig` struct (around line 82):

```go
type KeywordPathsConfig struct {
	Wake string `yaml:"wake"`
	Mute string `yaml:"mute"`
	Stop string `yaml:"stop"`
}

type SensitivitiesConfig struct {
	Wake float32 `yaml:"wake"`
	Mute float32 `yaml:"mute"`
	Stop float32 `yaml:"stop"`
}

type PersonaConfig struct {
	Name          string              `yaml:"name"`
	Greeting      string              `yaml:"greeting"`
	Traits        []string            `yaml:"traits"`
	Rules         []string            `yaml:"rules"`
	AccessKeyEnv  string              `yaml:"access_key_env"`
	KeywordPaths  KeywordPathsConfig  `yaml:"keyword_paths"`
	Sensitivities SensitivitiesConfig `yaml:"sensitivities"`
}
```

- [ ] **Step 2: Add Persona field to top-level Config struct**

In the `Config` struct (line 10), add a new field:

```go
type Config struct {
	Server  struct { ... } `yaml:"server"`
	Model   struct { ... } `yaml:"model"`
	TTS     struct { ... } `yaml:"tts"`
	Freeman FreemanConfig  `yaml:"freeman"`
	Persona PersonaConfig  `yaml:"persona"`
}
```

- [ ] **Step 3: Remove HotkeyConfig from FreemanConfig**

In `FreemanConfig` (line 45), remove the `Hotkey` field:

```go
type FreemanConfig struct {
	PM      PMConfig      `yaml:"pm"`
	Worker  WorkerConfig  `yaml:"worker"`
	Audio   AudioConfig   `yaml:"audio"`
	STT     STTConfig     `yaml:"stt"`
	Logging LoggingConfig `yaml:"logging"`
}
```

Delete the `HotkeyConfig` struct (lines 40-43) entirely.

- [ ] **Step 4: Update config.yaml**

Replace the `freeman.hotkey` block and add top-level `persona`:

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

Remove the `hotkey:` block from under `freeman:`.

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: Build succeeds (hotkey config references will break in later tasks).

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go config.yaml
git commit -m "feat(config): add persona config, remove hotkey config"
```

---

### Task 2: Capture Device Fan-Out with Subscribe/Unsubscribe

**Files:**
- Modify: `internal/audio/capture/device.go` (lines 24-156)
- Create: `internal/audio/capture/device_test.go`

- [ ] **Step 1: Write test for Subscribe fan-out**

Create `internal/audio/capture/device_test.go`:

```go
package capture

import (
	"testing"
	"time"
)

func TestSubscribeReceivesFrames(t *testing.T) {
	ring := NewRing(320, 320*200)
	d := &Device{
		ring:      ring,
		frameSize: 320,
		subs:      make(map[chan []int16]struct{}),
	}

	ch := d.Subscribe()
	if ch == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	frame := make([]int16, 320)
	for i := range frame {
		frame[i] = int16(i)
	}
	d.broadcast(frame)

	select {
	case got := <-ch:
		if len(got) != 320 {
			t.Fatalf("expected 320 samples, got %d", len(got))
		}
		if got[0] != 0 || got[1] != 1 {
			t.Fatal("frame data mismatch")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for frame")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	d := &Device{
		frameSize: 320,
		subs:      make(map[chan []int16]struct{}),
	}

	ch1 := d.Subscribe()
	ch2 := d.Subscribe()

	frame := make([]int16, 320)
	d.broadcast(frame)

	select {
	case <-ch1:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1 timeout")
	}
	select {
	case <-ch2:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 timeout")
	}
}

func TestUnsubscribe(t *testing.T) {
	d := &Device{
		frameSize: 320,
		subs:      make(map[chan []int16]struct{}),
	}

	ch := d.Subscribe()
	d.Unsubscribe(ch)

	frame := make([]int16, 320)
	d.broadcast(frame)

	select {
	case <-ch:
		t.Fatal("should not receive after unsubscribe")
	case <-time.After(50 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/audio/capture/ -run TestSubscribe -v`
Expected: FAIL — `subs` field and `Subscribe`/`broadcast` methods don't exist yet.

- [ ] **Step 3: Add subscriber tracking to Device struct**

In `device.go`, add to the `Device` struct (line 24):

```go
type Device struct {
	cfg       Config
	dev       *malgo.Device
	ring      *Ring
	frames    chan []int16  // keep for backward compat during migration
	subs      map[chan []int16]struct{}
	subsMu    sync.RWMutex
	stopCh    chan struct{}
	frameSize int
	log       func(string, ...any)
}
```

Add `sync` to imports if not already present.

- [ ] **Step 4: Implement Subscribe, Unsubscribe, broadcast**

Add to `device.go`:

```go
func (d *Device) Subscribe() <-chan []int16 {
	ch := make(chan []int16, 50)
	d.subsMu.Lock()
	d.subs[ch] = struct{}{}
	d.subsMu.Unlock()
	return ch
}

func (d *Device) Unsubscribe(ch <-chan []int16) {
	writeCh := (chan []int16)(ch)
	d.subsMu.Lock()
	delete(d.subs, writeCh)
	d.subsMu.Unlock()
	close(writeCh)
}

func (d *Device) broadcast(frame []int16) {
	d.subsMu.RLock()
	defer d.subsMu.RUnlock()
	for ch := range d.subs {
		cp := make([]int16, len(frame))
		copy(cp, frame)
		select {
		case ch <- cp:
		default:
		}
	}
}
```

- [ ] **Step 5: Initialize subs map in Open()**

In the `Open()` function where `Device` is constructed, add `subs: make(map[chan []int16]struct{})`.

- [ ] **Step 6: Update drain goroutine to use broadcast**

In the `drain()` method, replace the frame channel push (lines 140-146):

```go
// Old:
select {
case d.frames <- frame:
default:
    d.logLaggingDrop()
}

// New:
d.broadcast(frame)
```

- [ ] **Step 7: Run tests**

Run: `go test ./internal/audio/capture/ -v`
Expected: All three tests PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/audio/capture/device.go internal/audio/capture/device_test.go
git commit -m "feat(capture): add Subscribe/Unsubscribe fan-out for multi-consumer frames"
```

---

### Task 3: Wakeword Detector Package

**Files:**
- Create: `internal/audio/wakeword/wakeword.go`
- Create: `internal/audio/wakeword/wakeword_test.go`

- [ ] **Step 1: Add Porcupine dependency**

Run: `go get github.com/Picovoice/porcupine/binding/go`

- [ ] **Step 2: Write test for keyword event mapping**

Create `internal/audio/wakeword/wakeword_test.go`:

```go
package wakeword

import (
	"testing"
)

func TestKeywordKindFromIndex(t *testing.T) {
	tests := []struct {
		index int
		want  KeywordKind
	}{
		{0, KeywordWake},
		{1, KeywordMute},
		{2, KeywordStop},
	}
	for _, tt := range tests {
		got := KeywordKind(tt.index)
		if got != tt.want {
			t.Errorf("index %d: got %d, want %d", tt.index, got, tt.want)
		}
	}
}

func TestKeywordKindString(t *testing.T) {
	tests := []struct {
		kind KeywordKind
		want string
	}{
		{KeywordWake, "wake"},
		{KeywordMute, "mute"},
		{KeywordStop, "stop"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("KeywordKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/audio/wakeword/ -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 4: Implement wakeword package**

Create `internal/audio/wakeword/wakeword.go`:

```go
package wakeword

import (
	"log/slog"

	porcupine "github.com/Picovoice/porcupine/binding/go"
)

type KeywordKind int

const (
	KeywordWake KeywordKind = iota
	KeywordMute
	KeywordStop
)

func (k KeywordKind) String() string {
	switch k {
	case KeywordWake:
		return "wake"
	case KeywordMute:
		return "mute"
	case KeywordStop:
		return "stop"
	default:
		return "unknown"
	}
}

type Config struct {
	AccessKey     string
	KeywordPaths  []string
	Sensitivities []float32
	Logger        *slog.Logger
}

type Detector struct {
	porc   porcupine.Porcupine
	events chan KeywordKind
	stopCh chan struct{}
	log    *slog.Logger
}

func NewDetector(cfg Config) (*Detector, error) {
	p := porcupine.Porcupine{
		AccessKey:     cfg.AccessKey,
		KeywordPaths:  cfg.KeywordPaths,
		Sensitivities: cfg.Sensitivities,
	}
	if err := p.Init(); err != nil {
		return nil, err
	}
	return &Detector{
		porc:   p,
		events: make(chan KeywordKind, 4),
		stopCh: make(chan struct{}),
		log:    cfg.Logger,
	}, nil
}

func (d *Detector) Events() <-chan KeywordKind {
	return d.events
}

func (d *Detector) Run(frames <-chan []int16) {
	go d.readLoop(frames)
}

func (d *Detector) readLoop(frames <-chan []int16) {
	frameLen := porcupine.FrameLength
	var buf []int16

	for {
		select {
		case <-d.stopCh:
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			buf = append(buf, frame...)
			for len(buf) >= frameLen {
				idx, err := d.porc.Process(buf[:frameLen])
				if err != nil {
					d.log.Error("porcupine process error", "err", err)
					buf = buf[frameLen:]
					continue
				}
				if idx >= 0 {
					kind := KeywordKind(idx)
					d.log.Info("keyword detected", "keyword", kind.String())
					select {
					case d.events <- kind:
					default:
					}
				}
				buf = buf[frameLen:]
			}
		}
	}
}

func (d *Detector) Stop() {
	close(d.stopCh)
	d.porc.Delete()
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/audio/wakeword/ -v`
Expected: PASS (KeywordKind tests don't require Porcupine init).

- [ ] **Step 6: Verify build**

Run: `go build ./internal/audio/wakeword/`
Expected: Build succeeds. Porcupine native lib linked via CGO.

- [ ] **Step 7: Commit**

```bash
git add internal/audio/wakeword/
git commit -m "feat(wakeword): add Porcupine wake word detector package"
```

---

### Task 4: Update Session to Use Wakeword Events

**Files:**
- Modify: `internal/conv/session.go` (lines 33-258)
- Modify: `internal/conv/types.go` (if Hotkey interface is defined there)

- [ ] **Step 1: Check where the Hotkey interface/type is defined**

Look at the `Hotkey` type referenced in `Deps` struct (line 37 of session.go). It may be an interface defined in session.go or types.go.

- [ ] **Step 2: Update Deps struct**

In `session.go`, replace the `Hotkey` field in `Deps` (line 37):

```go
type Deps struct {
	Transcriber    Transcriber
	Speaker        Speaker
	WakewordEvents <-chan wakeword.KeywordKind
	SpeechOnsets   <-chan struct{}
	TaskManager    *TaskManager
	ChatAgent      agent.ChatAgent
	ModelResolver  func(hint string) string

	Persona  config.PersonaConfig
	Model    string
	RepoRoot string
	Logger   *slog.Logger
}
```

Note: `SystemPrompt` field replaced by `Persona` — the system prompt will be assembled from persona fields.

Add import: `"github.com/Renderix/freeman/internal/audio/wakeword"`

- [ ] **Step 3: Add system prompt assembly function**

Add to `session.go`:

```go
func buildSystemPrompt(p config.PersonaConfig) string {
	var b strings.Builder
	b.WriteString("You are ")
	b.WriteString(p.Name)
	b.WriteString(", a voice assistant that helps with coding tasks.")
	if len(p.Traits) > 0 {
		b.WriteString(" Your personality: ")
		b.WriteString(strings.Join(p.Traits, ", "))
		b.WriteString(".")
	}
	if len(p.Rules) > 0 {
		b.WriteString(" Rules you must follow: ")
		for i, r := range p.Rules {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(r)
		}
		b.WriteString(".")
	}
	return b.String()
}
```

Add `"strings"` to imports.

- [ ] **Step 4: Update the event loop**

In the `Run()` method, replace the hotkey channel variable (around line 141):

```go
// Old:
hotkeys := s.deps.Hotkey.Events()

// New:
wakeEvents := s.deps.WakewordEvents
```

Replace the hotkey select case (lines 180-182) with wakeword handling:

```go
case kind := <-wakeEvents:
	switch kind {
	case wakeword.KeywordWake:
		s.log.Info("wake word detected")
		if !s.awake {
			s.awake = true
			s.deps.Transcriber.Unmute()
			greet()
		}
	case wakeword.KeywordMute:
		s.log.Info("mute word detected")
		if s.cancelSpeak != nil {
			s.cancelSpeak()
			s.cancelSpeak = nil
		}
		s.awake = false
		s.convBusy = false
		s.deps.Transcriber.Mute()
		s.log.Info("muted — waiting for wake word")
	case wakeword.KeywordStop:
		s.log.Info("stop word detected — shutting down")
		if s.cancelSpeak != nil {
			s.cancelSpeak()
		}
		s.deps.TaskManager.Cancel()
		s.deps.ChatAgent.Close()
		return
	}
```

- [ ] **Step 5: Add `awake` field to Session struct**

In the Session struct, add:

```go
type Session struct {
	// existing fields...
	awake bool
}
```

Initialize `awake` to `false` in `NewSession()` (session starts idle).

- [ ] **Step 6: Gate utterance processing on awake state**

In the utterance case (lines 184-196), add an awake check:

```go
case text, ok := <-utterances:
	if !ok {
		utterances = nil
		continue
	}
	if !s.awake {
		continue
	}
	// ... rest of existing utterance handling
```

- [ ] **Step 7: Update greet() to use persona greeting**

Update the `greet()` function to speak the persona greeting via TTS before dispatching `<call started>`:

```go
greet := func() {
	if s.convBusy {
		s.log.Info("conv busy — ignoring wake word")
		return
	}
	for {
		select {
		case _, ok := <-utterances:
			if !ok {
				utterances = nil
			}
			continue
		default:
		}
		break
	}
	if s.deps.Persona.Greeting != "" {
		speakCh <- s.deps.Persona.Greeting
	}
	s.dispatchUserSay("<call started>", currentTaskState)
}
```

- [ ] **Step 8: Update system prompt usage**

Find where `s.deps.SystemPrompt` is passed to the chat agent init and replace with:

```go
buildSystemPrompt(s.deps.Persona)
```

- [ ] **Step 9: Remove the Hotkey interface**

Delete the `Hotkey` interface definition (wherever it lives — session.go or types.go).

- [ ] **Step 10: Verify build**

Run: `go build ./internal/conv/`
Expected: Build succeeds. May fail on call.go (fixed in Task 5).

- [ ] **Step 11: Commit**

```bash
git add internal/conv/
git commit -m "feat(conv): replace hotkey with wakeword events, add awake state machine"
```

---

### Task 5: Rewire Call Command

**Files:**
- Modify: `cmd/freeman/call.go` (lines 38-179)

- [ ] **Step 1: Add wakeword import and remove hotkey import**

```go
// Remove:
"github.com/Renderix/freeman/internal/audio/hotkey"

// Add:
"github.com/Renderix/freeman/internal/audio/wakeword"
```

- [ ] **Step 2: Replace hotkey initialization with wakeword detector**

Remove the hotkey block (lines 143-150):

```go
// Remove:
hk, err := hotkey.Open(hotkey.Config{
    Mode: conf.Freeman.Hotkey.Mode,
    Key:  conf.Freeman.Hotkey.Key,
})
if err != nil { ... }
defer hk.Stop()
```

Replace with wakeword detector initialization. Insert after Speaker setup (after line 140):

```go
accessKey := os.Getenv(conf.Persona.AccessKeyEnv)
if accessKey == "" {
	return fmt.Errorf("environment variable %s not set (Picovoice access key)", conf.Persona.AccessKeyEnv)
}
wkFrames := cap.Subscribe()
defer cap.Unsubscribe(wkFrames)
wk, err := wakeword.NewDetector(wakeword.Config{
	AccessKey: accessKey,
	KeywordPaths: []string{
		conf.Persona.KeywordPaths.Wake,
		conf.Persona.KeywordPaths.Mute,
		conf.Persona.KeywordPaths.Stop,
	},
	Sensitivities: []float32{
		conf.Persona.Sensitivities.Wake,
		conf.Persona.Sensitivities.Mute,
		conf.Persona.Sensitivities.Stop,
	},
	Logger: logger,
})
if err != nil {
	return fmt.Errorf("wakeword detector: %w", err)
}
defer wk.Stop()
wk.Run(wkFrames)

fmt.Fprintf(os.Stderr, "%s listening... say %q to begin\n", conf.Persona.Name, conf.Persona.Name)
```

Add `"os"` and `"fmt"` to imports if not present.

- [ ] **Step 3: Update VAD to use Subscribe**

Replace the VAD frame source (line 121):

```go
// Old:
uttCh := v.Run(ctx, cap.Frames())

// New:
vadFrames := cap.Subscribe()
defer cap.Unsubscribe(vadFrames)
uttCh := v.Run(ctx, vadFrames)
```

- [ ] **Step 4: Start VAD and transcriber muted**

After creating VAD and transcriber, mute them immediately:

```go
v.Mute()
tr.Mute()
```

This ensures no audio processing until the wake word is detected.

- [ ] **Step 5: Update conv.Deps construction**

Replace the Session deps (lines 161-176):

```go
convSession, err := conv.NewSession(ctx, conv.Deps{
	Transcriber:    tr,
	Speaker:        sp,
	WakewordEvents: wk.Events(),
	SpeechOnsets:   v.SpeechOnsets(),
	TaskManager:    taskMgr,
	ChatAgent:      chatAgent,
	ModelResolver:  taskFactory.ResolveModel,
	Persona:        conf.Persona,
	RepoRoot:       repoRoot,
	Model:          conf.Freeman.PM.Model,
	Logger:         logger,
})
```

- [ ] **Step 6: Verify build**

Run: `go build ./cmd/freeman/`
Expected: Build succeeds.

- [ ] **Step 7: Commit**

```bash
git add cmd/freeman/call.go
git commit -m "feat(cmd): wire wakeword detector, remove hotkey, start VAD/STT muted"
```

---

### Task 6: Delete Hotkey Package and Drop x/term

**Files:**
- Delete: `internal/audio/hotkey/` (entire directory)
- Modify: `go.mod`

- [ ] **Step 1: Delete the hotkey package**

```bash
rm -rf internal/audio/hotkey/
```

- [ ] **Step 2: Verify no remaining hotkey imports**

Run: `grep -r "audio/hotkey" --include="*.go" .`
Expected: No matches (all references removed in Tasks 4 and 5).

- [ ] **Step 3: Remove x/term if unused**

Check if `golang.org/x/term` is used anywhere else:

Run: `grep -r "golang.org/x/term" --include="*.go" .`

If no matches, remove it:

```bash
go mod tidy
```

- [ ] **Step 4: Verify build and tests**

Run: `go build ./... && go test ./...`
Expected: Build and tests pass.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: delete hotkey package, drop golang.org/x/term"
```

---

### Task 7: Setup Script for Wakeword Models

**Files:**
- Create: `scripts/setup_wakeword_models.sh`
- Create: `models/wakeword/.gitkeep`

- [ ] **Step 1: Create the models directory placeholder**

```bash
mkdir -p models/wakeword
touch models/wakeword/.gitkeep
```

- [ ] **Step 2: Create setup script**

Create `scripts/setup_wakeword_models.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

MODELS_DIR="$(cd "$(dirname "$0")/.." && pwd)/models/wakeword"
mkdir -p "$MODELS_DIR"

echo "=== Porcupine Wake Word Model Setup ==="
echo ""
echo "Freeman requires custom .ppn keyword files from Picovoice Console."
echo ""
echo "Steps:"
echo "  1. Sign up at https://console.picovoice.ai/"
echo "  2. Create three custom keywords:"
echo "     - \"Horus\"       (save as horus.ppn)"
echo "     - \"Mute\"        (save as mute.ppn)"
echo "     - \"Horus stop\"  (save as horus-stop.ppn)"
echo "  3. Download each .ppn file for your platform (macOS / Linux)"
echo "  4. Place them in: $MODELS_DIR/"
echo ""
echo "  5. Set your Picovoice access key:"
echo "     export PICOVOICE_ACCESS_KEY=your-key-here"
echo ""

MISSING=0
for f in horus.ppn mute.ppn horus-stop.ppn; do
    if [ ! -f "$MODELS_DIR/$f" ]; then
        echo "  MISSING: $MODELS_DIR/$f"
        MISSING=1
    else
        echo "  OK: $MODELS_DIR/$f"
    fi
done

if [ "$MISSING" -eq 1 ]; then
    echo ""
    echo "Some keyword files are missing. See instructions above."
    exit 1
fi

echo ""
echo "All keyword files present. Ready to run: ./freeman call"
```

- [ ] **Step 3: Make executable**

```bash
chmod +x scripts/setup_wakeword_models.sh
```

- [ ] **Step 4: Commit**

```bash
git add scripts/setup_wakeword_models.sh models/wakeword/.gitkeep
git commit -m "feat(scripts): add wakeword model setup script and directory"
```

---

### Task 8: Update CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update CLAUDE.md**

Add wakeword setup to the Build & Run section:

```bash
# Wakeword models (required for voice call)
./scripts/setup_wakeword_models.sh  # Instructions + check for .ppn files
```

Update the `freeman call` entry:

```bash
# Run voice call (requires PICOVOICE_ACCESS_KEY env var)
./freeman call --config config.yaml
```

Add to Architecture section under Layer 1:

```
- `wakeword/`: Porcupine wake word detector (always-on, never muted)
```

Add to Configuration section:

```
Top-level `persona:` key configures the voice persona (name, greeting, traits, rules, wake/kill word paths, sensitivities).
```

Remove any references to the hotkey system.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for wakeword and persona system"
```

---

### Task 9: Integration Test

**Files:**
- None (manual testing)

- [ ] **Step 1: Create .ppn files**

Run `./scripts/setup_wakeword_models.sh` and follow the instructions to create keyword files from Picovoice Console.

- [ ] **Step 2: Set access key**

```bash
export PICOVOICE_ACCESS_KEY=your-key-here
```

- [ ] **Step 3: Start the voice call**

```bash
./freeman call --config config.yaml
```

Expected: Stderr prints `Horus listening... say "Horus" to begin`

- [ ] **Step 4: Test wake word**

Say "Horus" into the microphone.
Expected: TTS speaks "I'm here", conversation mode activates, VAD+STT start processing.

- [ ] **Step 5: Test conversation**

Say something like "What can you help me with?"
Expected: LLM responds via TTS as "Horus" persona.

- [ ] **Step 6: Test mute**

Say "Mute" during or after a response.
Expected: TTS stops, system goes idle. Stderr prints muted status. Speaking further text produces no response.

- [ ] **Step 7: Test re-wake after mute**

Say "Horus" again.
Expected: TTS speaks greeting, conversation resumes.

- [ ] **Step 8: Test shutdown**

Say "Horus stop".
Expected: Process exits cleanly. Stderr prints `Shutting down...`.
