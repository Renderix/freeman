# Freeman Plan 1 — Skeleton

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a fully testable `freeman call` CLI skeleton with no real audio, no real AI, and no real `pi-coding-agent` — just the state machine, sidecar JSONL protocol, and port interfaces, wired end-to-end with fakes and a stub sidecar.

**Architecture:** A pure transition-based state machine at the core, driven by a Session goroutine that multiplexes events from injected ports (Transcriber, Speaker, PM, Hotkey, Sidecar). The machine returns Effects that the session executes. All ports have fake implementations used in tests and in the `freeman call` CLI for Plan 1, which reads simulated user utterances from stdin and prints simulated TTS to stdout. The real Kokoro/whisper.cpp/Haiku/pi wiring comes in Plans 2 and 3.

**Tech Stack:** Go 1.25.6, standard library `testing`, `gopkg.in/yaml.v3` (already in deps), Cobra (already in deps). TypeScript stub sidecar uses Node 20+ with `tsx` for on-the-fly TS execution.

---

## File Structure

**New packages under `internal/`:**

- `internal/call/` — call session, state machine, ports, domain types
  - `types.go` — `Objective`, `RouteDecision`, `PMIntakeResult`, `PMRouteResult`
  - `ports.go` — `Transcriber`, `Speaker`, `PM`, `Hotkey` interfaces
  - `state.go` — `State` enum with `String()`
  - `events.go` — `Event` sealed-interface + concrete event types
  - `effects.go` — `Effect` sealed-interface + concrete effect types
  - `machine.go` — `Machine` struct, `NewMachine`, `Handle(Event) []Effect`
  - `machine_test.go` — table-driven transition tests
  - `session.go` — `Session` struct, `Run(ctx)` event loop
  - `session_test.go` — happy-path integration test with fakes + in-memory sidecar
- `internal/call/fakes/` — shared fake implementations for tests and the Plan-1 CLI
  - `fakes.go` — `StdinTranscriber`, `StdoutSpeaker`, `ScriptedPM`, `StdinHotkey`
  - `fakes_test.go` — unit tests for each fake
- `internal/sidecar/` — sidecar protocol and client
  - `protocol.go` — JSONL message structs + `Encode/Decode` helpers
  - `protocol_test.go` — round-trip marshal/unmarshal tests
  - `client.go` — `Client` struct, `NewClientFromPipes` (for tests), `Spawn` (for production)
  - `client_test.go` — pipe-based client test (no subprocess needed)

**New TSX stub sidecar under `sidecar/`:**
- `sidecar/package.json` — minimal package.json declaring `tsx` dev dep
- `sidecar/tsconfig.json` — strict TS config
- `sidecar/stub.ts` — echo stub: reads JSONL from stdin, responds with scripted events

**Existing files modified:**
- `internal/config/config.go` — add `FreemanConfig` substruct
- `internal/config/config_test.go` — new
- `cmd/freeman/main.go` — register new `call` command
- `cmd/freeman/call.go` — new Cobra command that runs Session with fakes

**Out of scope for Plan 1:** whisper.cpp, Kokoro, real Haiku, real `pi-coding-agent`, hotkey daemon, barge-in, error recovery beyond printing, `--log-transcript`.

---

## Task 1: Extend config with `freeman` section

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Freeman_Defaults(t *testing.T) {
	conf := LoadConfig("/nonexistent/path.yaml")
	if conf.Freeman.PM.Model != "claude-haiku-4-5" {
		t.Errorf("default PM model = %q, want claude-haiku-4-5", conf.Freeman.PM.Model)
	}
	if conf.Freeman.PM.ConfidenceThreshold != 0.8 {
		t.Errorf("default confidence = %v, want 0.8", conf.Freeman.PM.ConfidenceThreshold)
	}
	if conf.Freeman.Worker.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("default worker model = %q", conf.Freeman.Worker.DefaultModel)
	}
	if conf.Freeman.Hotkey != "option+space" {
		t.Errorf("default hotkey = %q", conf.Freeman.Hotkey)
	}
}

func TestLoadConfig_Freeman_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
freeman:
  pm:
    model: custom-pm
    confidence_threshold: 0.5
    api_key_env: MY_KEY
  worker:
    default_model: custom-sonnet
    opus_model: custom-opus
    auth: api_key
  stt:
    model: whisper-tiny
    model_path: ./m.bin
    vad:
      silence_ms: 500
      min_speech_ms: 200
  hotkey: ctrl+space
  logging:
    transcript_dir: ./t
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := LoadConfig(path)
	if conf.Freeman.PM.Model != "custom-pm" {
		t.Errorf("pm.model = %q", conf.Freeman.PM.Model)
	}
	if conf.Freeman.Worker.OpusModel != "custom-opus" {
		t.Errorf("worker.opus = %q", conf.Freeman.Worker.OpusModel)
	}
	if conf.Freeman.STT.VAD.SilenceMS != 500 {
		t.Errorf("vad.silence = %d", conf.Freeman.STT.VAD.SilenceMS)
	}
	if conf.Freeman.Hotkey != "ctrl+space" {
		t.Errorf("hotkey = %q", conf.Freeman.Hotkey)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ayusman/charlotte/freeman && go test ./internal/config/... -v`
Expected: compile error — `conf.Freeman` undefined.

- [ ] **Step 3: Extend the config struct**

In `internal/config/config.go`, add a new top-level field `Freeman` and its nested struct, plus defaults. Replace the file contents with:

```go
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`

	Model struct {
		Dir        string `yaml:"dir"`
		ModelFile  string `yaml:"model_file"`
		VoicesFile string `yaml:"voices_file"`
		TokensFile string `yaml:"tokens_file"`
		DataDir    string `yaml:"data_dir"`
	} `yaml:"model"`

	TTS struct {
		DefaultVoice              string  `yaml:"default_voice"`
		DefaultSpeed              float64 `yaml:"default_speed"`
		MaxSentenceChars          int     `yaml:"max_sentence_chars"`
		PartialSentenceTimeoutSec float64 `yaml:"partial_sentence_timeout_sec"`
	} `yaml:"tts"`

	Freeman FreemanConfig `yaml:"freeman"`
}

type FreemanConfig struct {
	PM      PMConfig      `yaml:"pm"`
	Worker  WorkerConfig  `yaml:"worker"`
	STT     STTConfig     `yaml:"stt"`
	Hotkey  string        `yaml:"hotkey"`
	Logging LoggingConfig `yaml:"logging"`
}

type PMConfig struct {
	Model               string  `yaml:"model"`
	ConfidenceThreshold float64 `yaml:"confidence_threshold"`
	APIKeyEnv           string  `yaml:"api_key_env"`
}

type WorkerConfig struct {
	DefaultModel string `yaml:"default_model"`
	OpusModel    string `yaml:"opus_model"`
	Auth         string `yaml:"auth"`
}

type STTConfig struct {
	Model     string    `yaml:"model"`
	ModelPath string    `yaml:"model_path"`
	VAD       VADConfig `yaml:"vad"`
}

type VADConfig struct {
	SilenceMS   int `yaml:"silence_ms"`
	MinSpeechMS int `yaml:"min_speech_ms"`
}

type LoggingConfig struct {
	TranscriptDir string `yaml:"transcript_dir"`
}

var DefaultConfig = Config{
	Server: struct {
		Port int `yaml:"port"`
	}{
		Port: 17000,
	},
	Model: struct {
		Dir        string `yaml:"dir"`
		ModelFile  string `yaml:"model_file"`
		VoicesFile string `yaml:"voices_file"`
		TokensFile string `yaml:"tokens_file"`
		DataDir    string `yaml:"data_dir"`
	}{
		Dir:        "./models",
		ModelFile:  "model.onnx",
		VoicesFile: "voices.bin",
		TokensFile: "tokens.txt",
		DataDir:    "espeak-ng-data",
	},
	TTS: struct {
		DefaultVoice              string  `yaml:"default_voice"`
		DefaultSpeed              float64 `yaml:"default_speed"`
		MaxSentenceChars          int     `yaml:"max_sentence_chars"`
		PartialSentenceTimeoutSec float64 `yaml:"partial_sentence_timeout_sec"`
	}{
		DefaultVoice:              "af_heart",
		DefaultSpeed:              1.0,
		MaxSentenceChars:          150,
		PartialSentenceTimeoutSec: 2.0,
	},
	Freeman: FreemanConfig{
		PM: PMConfig{
			Model:               "claude-haiku-4-5",
			ConfidenceThreshold: 0.8,
			APIKeyEnv:           "ANTHROPIC_API_KEY",
		},
		Worker: WorkerConfig{
			DefaultModel: "claude-sonnet-4-6",
			OpusModel:    "claude-opus-4-6",
			Auth:         "subscription",
		},
		STT: STTConfig{
			Model:     "whisper-large-v3-turbo",
			ModelPath: "./models/whisper/ggml-large-v3-turbo.bin",
			VAD: VADConfig{
				SilenceMS:   800,
				MinSpeechMS: 300,
			},
		},
		Hotkey: "option+space",
		Logging: LoggingConfig{
			TranscriptDir: "./.freeman/transcripts",
		},
	},
}

// LoadConfig loads configuration from config.yaml or returns defaults.
func LoadConfig(configPath string) Config {
	if configPath == "" {
		configPath = "config.yaml"
	}

	file, err := os.ReadFile(configPath)
	if err != nil {
		return DefaultConfig
	}

	conf := DefaultConfig
	if err := yaml.Unmarshal(file, &conf); err != nil {
		return DefaultConfig
	}

	return conf
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/config/... -v`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "config: add freeman section with PM/worker/STT/hotkey defaults"
```

---

## Task 2: Call state enum and events

**Files:**
- Create: `internal/call/state.go`
- Create: `internal/call/events.go`
- Create: `internal/call/state_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/call/state_test.go`:

```go
package call

import "testing"

func TestStateString(t *testing.T) {
	cases := []struct {
		s    State
		want string
	}{
		{StateIdle, "idle"},
		{StateIntake, "intake"},
		{StateAwaitingConfirm, "awaiting_confirm"},
		{StateWorking, "working"},
		{StateEscalating, "escalating"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("State(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestEventKind(t *testing.T) {
	// Compile-time check: every event type implements Event.
	var _ Event = HotkeyPress{}
	var _ Event = UserUtterance{Text: "hi"}
	var _ Event = PMIntakeResult{}
	var _ Event = PMRouteResult{}
	var _ Event = SidecarAssistantText{}
	var _ Event = SidecarAskUser{}
	var _ Event = SidecarDone{}
	var _ Event = SidecarError{}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/call/... -v`
Expected: compile error — package `call` does not exist.

- [ ] **Step 3: Implement state and events**

Create `internal/call/state.go`:

```go
package call

// State is the current phase of a call session.
type State int

const (
	StateIdle State = iota
	StateIntake
	StateAwaitingConfirm
	StateWorking
	StateEscalating
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateIntake:
		return "intake"
	case StateAwaitingConfirm:
		return "awaiting_confirm"
	case StateWorking:
		return "working"
	case StateEscalating:
		return "escalating"
	default:
		return "unknown"
	}
}
```

Create `internal/call/events.go`:

```go
package call

// Event is a sealed interface; only types in this file implement it.
type Event interface{ isEvent() }

// HotkeyPress means the user pressed the call hotkey.
type HotkeyPress struct{}

func (HotkeyPress) isEvent() {}

// UserUtterance is a finalized transcript from the user.
type UserUtterance struct {
	Text string
}

func (UserUtterance) isEvent() {}

// PMIntakeResult is the PM's response during intake.
// If NeedsMore is true, Question holds the follow-up to speak.
// Otherwise Objective holds the completed spec.
type PMIntakeResult struct {
	NeedsMore bool
	Question  string
	Objective *Objective
}

func (PMIntakeResult) isEvent() {}

// PMRouteResult is the PM's decision for an ask_user question.
// Exactly one of AnswerInline or SpokenQuestion is non-empty.
type PMRouteResult struct {
	ID             string
	AnswerInline   string
	SpokenQuestion string
}

func (PMRouteResult) isEvent() {}

// SidecarAssistantText is intermediate Claude output (logged, not spoken).
type SidecarAssistantText struct {
	Text string
}

func (SidecarAssistantText) isEvent() {}

// SidecarAskUser is Claude calling the ask_user tool.
type SidecarAskUser struct {
	ID       string
	Question string
}

func (SidecarAskUser) isEvent() {}

// SidecarDone is the sidecar reporting clean completion.
type SidecarDone struct {
	Summary string
}

func (SidecarDone) isEvent() {}

// SidecarError is the sidecar reporting an error.
type SidecarError struct {
	Message string
}

func (SidecarError) isEvent() {}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/call/... -v`
Expected: PASS (note: `Objective` type referenced by `PMIntakeResult` — it will be added in Task 3. Until then, this won't compile. That's fine — we continue to Task 3 and verify tests after Task 3 is done.)

**Update: expected result is compile error "undefined: Objective". This is resolved by Task 3. Do not commit yet — commit happens after Task 3.**

---

## Task 3: Ports and domain types

**Files:**
- Create: `internal/call/types.go`
- Create: `internal/call/ports.go`

- [ ] **Step 1: Write the types file**

Create `internal/call/types.go`:

```go
package call

import "context"

// Objective is the structured task spec built during intake.
type Objective struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	ModelHint          string   `json:"model_hint"` // "sonnet" or "opus"
	SpokenSummary      string   `json:"spoken_summary"`
}

// IntakeInput is what the session hands the PM on each intake call.
type IntakeInput struct {
	Transcript []string // alternating user/PM utterances, oldest first
	Latest     string   // the most recent user utterance
}

// RouteInput is what the session hands the PM on each ask_user routing call.
type RouteInput struct {
	Objective Objective
	Transcript []string
	Question   string
}
```

- [ ] **Step 2: Write the ports file**

Create `internal/call/ports.go`:

```go
package call

import "context"

// Transcriber turns microphone audio into finalized text utterances.
// For Plan 1, the fake reads lines from stdin.
type Transcriber interface {
	// Utterances returns a channel that yields a value every time a complete
	// user utterance is available. The channel is closed when Stop is called.
	Utterances() <-chan string
	Stop()
}

// Speaker turns text into spoken audio. For Plan 1, the fake prints to stdout.
// Speak blocks until playback completes or ctx is canceled.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// PM is the conversational brain (Haiku in production, scripted in Plan 1).
type PM interface {
	Intake(ctx context.Context, in IntakeInput) (PMIntakeResult, error)
	Route(ctx context.Context, in RouteInput) (PMRouteResult, error)
}

// Hotkey emits an event whenever the call hotkey is pressed.
// For Plan 1, the fake reads blank lines from stdin.
type Hotkey interface {
	Events() <-chan struct{}
	Stop()
}
```

- [ ] **Step 3: Verify the package compiles and Task 2 tests pass**

Run: `go test ./internal/call/... -v`
Expected: PASS for `TestStateString` and `TestEventKind`.

- [ ] **Step 4: Commit Tasks 2 and 3 together**

```bash
git add internal/call/state.go internal/call/events.go internal/call/types.go internal/call/ports.go internal/call/state_test.go
git commit -m "call: add state enum, events, domain types, and port interfaces"
```

---

## Task 4: Pure state machine with transition tests

**Files:**
- Create: `internal/call/effects.go`
- Create: `internal/call/machine.go`
- Create: `internal/call/machine_test.go`

- [ ] **Step 1: Write the effects file**

Create `internal/call/effects.go`:

```go
package call

// Effect is a sealed interface; only types in this file implement it.
// The machine emits effects; the session executes them.
type Effect interface{ isEffect() }

// SpeakEffect tells the session to speak the given text via Speaker.
type SpeakEffect struct {
	Text string
}

func (SpeakEffect) isEffect() {}

// CallPMIntakeEffect tells the session to invoke PM.Intake asynchronously.
type CallPMIntakeEffect struct {
	Input IntakeInput
}

func (CallPMIntakeEffect) isEffect() {}

// CallPMRouteEffect tells the session to invoke PM.Route asynchronously.
type CallPMRouteEffect struct {
	ID    string
	Input RouteInput
}

func (CallPMRouteEffect) isEffect() {}

// SendSidecarStartEffect tells the session to dispatch the objective
// to the sidecar as a start message.
type SendSidecarStartEffect struct {
	Objective Objective
}

func (SendSidecarStartEffect) isEffect() {}

// SendSidecarReplyEffect tells the session to send an ask_user reply
// to the sidecar.
type SendSidecarReplyEffect struct {
	ID     string
	Answer string
}

func (SendSidecarReplyEffect) isEffect() {}
```

- [ ] **Step 2: Write the failing machine test**

Create `internal/call/machine_test.go`:

```go
package call

import "testing"

func TestMachine_IdleToIntake(t *testing.T) {
	m := NewMachine()
	effects := m.Handle(HotkeyPress{})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d, want 1", len(effects))
	}
	if _, ok := effects[0].(SpeakEffect); !ok {
		t.Errorf("effects[0] = %T, want SpeakEffect", effects[0])
	}
}

func TestMachine_IntakeUtteranceCallsPM(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	effects := m.Handle(UserUtterance{Text: "build a feature flag"})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d, want 1", len(effects))
	}
	e, ok := effects[0].(CallPMIntakeEffect)
	if !ok {
		t.Fatalf("effects[0] = %T, want CallPMIntakeEffect", effects[0])
	}
	if e.Input.Latest != "build a feature flag" {
		t.Errorf("latest = %q", e.Input.Latest)
	}
	if len(e.Input.Transcript) != 1 || e.Input.Transcript[0] != "build a feature flag" {
		t.Errorf("transcript = %v", e.Input.Transcript)
	}
}

func TestMachine_IntakeNeedsMore(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build a feature flag"})
	effects := m.Handle(PMIntakeResult{NeedsMore: true, Question: "on or off by default?"})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok || s.Text != "on or off by default?" {
		t.Errorf("effect = %+v", effects[0])
	}
}

func TestMachine_IntakeCompleteToAwaitingConfirm(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build a feature flag"})
	obj := Objective{
		Goal:          "add feature flag",
		ModelHint:     "sonnet",
		SpokenSummary: "add a feature flag for checkout",
	}
	effects := m.Handle(PMIntakeResult{NeedsMore: false, Objective: &obj})
	if m.State() != StateAwaitingConfirm {
		t.Fatalf("state = %s, want awaiting_confirm", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if s.Text == "" {
		t.Error("speak text empty")
	}
}

func TestMachine_AwaitingConfirmYes(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build it"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "do the thing"}
	m.Handle(PMIntakeResult{Objective: &obj})
	effects := m.Handle(UserUtterance{Text: "yes"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	var sawStart, sawSpeak bool
	for _, e := range effects {
		switch e.(type) {
		case SendSidecarStartEffect:
			sawStart = true
		case SpeakEffect:
			sawSpeak = true
		}
	}
	if !sawStart || !sawSpeak {
		t.Errorf("effects = %v", effects)
	}
}

func TestMachine_AwaitingConfirmForceStart(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build it"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "sum"}
	m.Handle(PMIntakeResult{Objective: &obj})
	effects := m.Handle(UserUtterance{Text: "just go"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	found := false
	for _, e := range effects {
		if _, ok := e.(SendSidecarStartEffect); ok {
			found = true
		}
	}
	if !found {
		t.Error("no SendSidecarStartEffect")
	}
}

func TestMachine_AwaitingConfirmRejectGoesBackToIntake(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build it"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "sum"}
	m.Handle(PMIntakeResult{Objective: &obj})
	effects := m.Handle(UserUtterance{Text: "no actually let's also add telemetry"})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	if _, ok := effects[0].(CallPMIntakeEffect); !ok {
		t.Errorf("effect = %T", effects[0])
	}
}

func TestMachine_WorkingAskUserRoutes(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarAskUser{ID: "q1", Question: "use existing client?"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	r, ok := effects[0].(CallPMRouteEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if r.ID != "q1" {
		t.Errorf("id = %q", r.ID)
	}
	if r.Input.Question != "use existing client?" {
		t.Errorf("question = %q", r.Input.Question)
	}
}

func TestMachine_WorkingRouteAnswerInline(t *testing.T) {
	m := driveToWorking(t)
	m.Handle(SidecarAskUser{ID: "q1", Question: "use existing?"})
	effects := m.Handle(PMRouteResult{ID: "q1", AnswerInline: "yes"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	reply, ok := effects[0].(SendSidecarReplyEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if reply.ID != "q1" || reply.Answer != "yes" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestMachine_WorkingRouteEscalate(t *testing.T) {
	m := driveToWorking(t)
	m.Handle(SidecarAskUser{ID: "q1", Question: "use existing?"})
	effects := m.Handle(PMRouteResult{ID: "q1", SpokenQuestion: "existing or new?"})
	if m.State() != StateEscalating {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok || s.Text != "existing or new?" {
		t.Errorf("effect = %+v", effects[0])
	}
}

func TestMachine_EscalatingUserReplyGoesBackToWorking(t *testing.T) {
	m := driveToWorking(t)
	m.Handle(SidecarAskUser{ID: "q1", Question: "q"})
	m.Handle(PMRouteResult{ID: "q1", SpokenQuestion: "spoken"})
	effects := m.Handle(UserUtterance{Text: "existing"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	r, ok := effects[0].(SendSidecarReplyEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if r.ID != "q1" || r.Answer != "existing" {
		t.Errorf("reply = %+v", r)
	}
}

func TestMachine_SidecarDoneGoesIdle(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarDone{Summary: "edited 3 files"})
	if m.State() != StateIdle {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if s.Text == "" {
		t.Error("empty summary speak")
	}
}

func TestMachine_SidecarErrorGoesIdle(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarError{Message: "oops"})
	if m.State() != StateIdle {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	if _, ok := effects[0].(SpeakEffect); !ok {
		t.Errorf("effect = %T", effects[0])
	}
}

func TestMachine_AssistantTextDoesNothing(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarAssistantText{Text: "editing file"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 0 {
		t.Errorf("expected no effects, got %v", effects)
	}
}

// driveToWorking is a test helper that walks a fresh machine through
// Idle → Intake → AwaitingConfirm → Working.
func driveToWorking(t *testing.T) *Machine {
	t.Helper()
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build a thing"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "sum"}
	m.Handle(PMIntakeResult{Objective: &obj})
	m.Handle(UserUtterance{Text: "yes"})
	if m.State() != StateWorking {
		t.Fatalf("failed to reach Working, at %s", m.State())
	}
	return m
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/call/... -v`
Expected: compile error — `NewMachine`, `Machine`, `Handle`, `State` (method) undefined.

- [ ] **Step 4: Implement the machine**

Create `internal/call/machine.go`:

```go
package call

import "strings"

// Machine holds call session state and computes transitions + effects.
// Not thread-safe; the session goroutine owns it.
type Machine struct {
	state            State
	transcript       []string
	objective        *Objective
	pendingAskUserID string
}

// NewMachine returns a fresh machine in StateIdle.
func NewMachine() *Machine {
	return &Machine{state: StateIdle, transcript: []string{}}
}

// State returns the current state.
func (m *Machine) State() State { return m.state }

// Handle advances the machine based on an event and returns effects to run.
func (m *Machine) Handle(e Event) []Effect {
	switch m.state {
	case StateIdle:
		return m.handleIdle(e)
	case StateIntake:
		return m.handleIntake(e)
	case StateAwaitingConfirm:
		return m.handleAwaitingConfirm(e)
	case StateWorking:
		return m.handleWorking(e)
	case StateEscalating:
		return m.handleEscalating(e)
	}
	return []Effect{}
}

func (m *Machine) handleIdle(e Event) []Effect {
	if _, ok := e.(HotkeyPress); ok {
		m.state = StateIntake
		m.transcript = []string{}
		m.objective = nil
		m.pendingAskUserID = ""
		return []Effect{SpeakEffect{Text: "hi. what are we building?"}}
	}
	return []Effect{}
}

func (m *Machine) handleIntake(e Event) []Effect {
	switch ev := e.(type) {
	case UserUtterance:
		m.transcript = append(m.transcript, ev.Text)
		return []Effect{CallPMIntakeEffect{Input: IntakeInput{
			Transcript: append([]string{}, m.transcript...),
			Latest:     ev.Text,
		}}}
	case PMIntakeResult:
		if ev.NeedsMore {
			m.transcript = append(m.transcript, ev.Question)
			return []Effect{SpeakEffect{Text: ev.Question}}
		}
		if ev.Objective == nil {
			return []Effect{}
		}
		m.objective = ev.Objective
		m.state = StateAwaitingConfirm
		text := ev.Objective.SpokenSummary + " should i start?"
		m.transcript = append(m.transcript, text)
		return []Effect{SpeakEffect{Text: text}}
	}
	return []Effect{}
}

func (m *Machine) handleAwaitingConfirm(e Event) []Effect {
	ev, ok := e.(UserUtterance)
	if !ok {
		return []Effect{}
	}
	m.transcript = append(m.transcript, ev.Text)
	if isAffirmative(ev.Text) && m.objective != nil {
		m.state = StateWorking
		return []Effect{
			SendSidecarStartEffect{Objective: *m.objective},
			SpeakEffect{Text: "starting now."},
		}
	}
	// Not affirmative — treat as continued intake.
	m.state = StateIntake
	return []Effect{CallPMIntakeEffect{Input: IntakeInput{
		Transcript: append([]string{}, m.transcript...),
		Latest:     ev.Text,
	}}}
}

func (m *Machine) handleWorking(e Event) []Effect {
	switch ev := e.(type) {
	case SidecarAssistantText:
		return []Effect{}
	case SidecarAskUser:
		m.pendingAskUserID = ev.ID
		return []Effect{CallPMRouteEffect{
			ID: ev.ID,
			Input: RouteInput{
				Objective:  *m.objective,
				Transcript: append([]string{}, m.transcript...),
				Question:   ev.Question,
			},
		}}
	case PMRouteResult:
		if ev.AnswerInline != "" {
			m.pendingAskUserID = ""
			return []Effect{SendSidecarReplyEffect{ID: ev.ID, Answer: ev.AnswerInline}}
		}
		if ev.SpokenQuestion != "" {
			m.state = StateEscalating
			return []Effect{SpeakEffect{Text: ev.SpokenQuestion}}
		}
		return []Effect{}
	case SidecarDone:
		m.state = StateIdle
		return []Effect{SpeakEffect{Text: "done. " + ev.Summary}}
	case SidecarError:
		m.state = StateIdle
		return []Effect{SpeakEffect{Text: "error from worker: " + ev.Message}}
	}
	return []Effect{}
}

func (m *Machine) handleEscalating(e Event) []Effect {
	ev, ok := e.(UserUtterance)
	if !ok {
		return []Effect{}
	}
	m.transcript = append(m.transcript, ev.Text)
	m.state = StateWorking
	id := m.pendingAskUserID
	m.pendingAskUserID = ""
	return []Effect{SendSidecarReplyEffect{ID: id, Answer: ev.Text}}
}

func isAffirmative(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "yes", "yeah", "yep", "go", "just go", "ship it", "do it", "start", "sure":
		return true
	}
	return false
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/call/... -v`
Expected: all tests PASS (TestStateString, TestEventKind, TestMachine_*).

- [ ] **Step 6: Commit**

```bash
git add internal/call/effects.go internal/call/machine.go internal/call/machine_test.go
git commit -m "call: add pure state machine with transition tests"
```

---

## Task 5: Sidecar JSONL protocol types

**Files:**
- Create: `internal/sidecar/protocol.go`
- Create: `internal/sidecar/protocol_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/sidecar/protocol_test.go`:

```go
package sidecar

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeDecodeStart(t *testing.T) {
	msg := StartMsg{
		Type: MsgTypeStart,
		Objective: ObjectivePayload{
			Goal:               "build flag",
			AcceptanceCriteria: []string{"tests pass"},
			Constraints:        []string{"no db changes"},
			Model:              "sonnet",
		},
	}
	var buf bytes.Buffer
	if err := Encode(&buf, msg); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	if !strings.Contains(line, `"type":"start"`) {
		t.Errorf("missing type: %s", line)
	}
	// Round-trip.
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["type"] != "start" {
		t.Errorf("type = %v", raw["type"])
	}
}

func TestDecodeAskUser(t *testing.T) {
	line := `{"type":"ask_user","id":"q1","question":"use existing client?"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	au, ok := m.(AskUserMsg)
	if !ok {
		t.Fatalf("got %T, want AskUserMsg", m)
	}
	if au.ID != "q1" || au.Question != "use existing client?" {
		t.Errorf("got %+v", au)
	}
}

func TestDecodeAssistantText(t *testing.T) {
	line := `{"type":"assistant_text","text":"editing file"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	at, ok := m.(AssistantTextMsg)
	if !ok || at.Text != "editing file" {
		t.Errorf("got %T %+v", m, m)
	}
}

func TestDecodeDone(t *testing.T) {
	line := `{"type":"done","summary":"edited 3 files"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := m.(DoneMsg)
	if !ok || d.Summary != "edited 3 files" {
		t.Errorf("got %T %+v", m, m)
	}
}

func TestDecodeError(t *testing.T) {
	line := `{"type":"error","message":"boom"}`
	m, err := Decode([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := m.(ErrorMsg)
	if !ok || e.Message != "boom" {
		t.Errorf("got %T %+v", m, m)
	}
}

func TestDecodeUnknownType(t *testing.T) {
	line := `{"type":"huh","foo":"bar"}`
	_, err := Decode([]byte(line))
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestEncodeAskUserReply(t *testing.T) {
	msg := AskUserReplyMsg{
		Type:   MsgTypeAskUserReply,
		ID:     "q1",
		Answer: "yes",
	}
	var buf bytes.Buffer
	if err := Encode(&buf, msg); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(buf.String())
	if !strings.Contains(line, `"type":"ask_user_reply"`) {
		t.Errorf("missing type: %s", line)
	}
	if !strings.Contains(line, `"id":"q1"`) {
		t.Errorf("missing id: %s", line)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/... -v`
Expected: package does not exist.

- [ ] **Step 3: Implement the protocol**

Create `internal/sidecar/protocol.go`:

```go
package sidecar

import (
	"encoding/json"
	"fmt"
	"io"
)

// Message type constants (sent over JSONL in both directions).
const (
	// Freeman → Sidecar
	MsgTypeStart        = "start"
	MsgTypeAskUserReply = "ask_user_reply"
	MsgTypeCancel       = "cancel"

	// Sidecar → Freeman
	MsgTypeAssistantText = "assistant_text"
	MsgTypeAskUser       = "ask_user"
	MsgTypeDone          = "done"
	MsgTypeError         = "error"
)

// Message is the common interface for any protocol message.
type Message interface{ isMessage() }

// ObjectivePayload is the serialized form of a call.Objective sent to the sidecar.
type ObjectivePayload struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	Model              string   `json:"model"` // "sonnet" or "opus"
}

// StartMsg kicks off a sidecar session.
type StartMsg struct {
	Type      string           `json:"type"`
	Objective ObjectivePayload `json:"objective"`
}

func (StartMsg) isMessage() {}

// AskUserReplyMsg is Freeman's answer to a previous AskUser from the sidecar.
type AskUserReplyMsg struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

func (AskUserReplyMsg) isMessage() {}

// CancelMsg aborts the sidecar session.
type CancelMsg struct {
	Type string `json:"type"`
}

func (CancelMsg) isMessage() {}

// AssistantTextMsg is streamed intermediate text from Claude.
type AssistantTextMsg struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (AssistantTextMsg) isMessage() {}

// AskUserMsg is the sidecar asking Freeman a question via the ask_user tool.
type AskUserMsg struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Question string `json:"question"`
}

func (AskUserMsg) isMessage() {}

// DoneMsg is clean completion.
type DoneMsg struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
}

func (DoneMsg) isMessage() {}

// ErrorMsg is an error from the sidecar.
type ErrorMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (ErrorMsg) isMessage() {}

// Encode writes a single message as a JSONL line to w.
func Encode(w io.Writer, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

// Decode parses a single JSONL line into a typed message.
func Decode(line []byte) (Message, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	switch head.Type {
	case MsgTypeStart:
		var m StartMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeAskUserReply:
		var m AskUserReplyMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeCancel:
		var m CancelMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeAssistantText:
		var m AssistantTextMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeAskUser:
		var m AskUserMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeDone:
		var m DoneMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeError:
		var m ErrorMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unknown message type: %q", head.Type)
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/sidecar/... -v`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/protocol.go internal/sidecar/protocol_test.go
git commit -m "sidecar: add JSONL protocol types and codec"
```

---

## Task 6: Sidecar stub (TypeScript)

**Files:**
- Create: `sidecar/package.json`
- Create: `sidecar/tsconfig.json`
- Create: `sidecar/stub.ts`
- Create: `sidecar/.gitignore`

This stub is used by Plan 1's `freeman call` CLI for manual end-to-end smoke testing. Go tests use the in-memory pipe client instead, so there are no Go tests that require Node.

- [ ] **Step 1: Create package.json**

Create `sidecar/package.json`:

```json
{
  "name": "freeman-sidecar",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "stub": "tsx stub.ts",
    "typecheck": "tsc --noEmit"
  },
  "devDependencies": {
    "tsx": "^4.19.2",
    "typescript": "^5.6.3",
    "@types/node": "^22.9.0"
  }
}
```

- [ ] **Step 2: Create tsconfig.json**

Create `sidecar/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "noEmit": true,
    "types": ["node"]
  },
  "include": ["*.ts"]
}
```

- [ ] **Step 3: Create .gitignore**

Create `sidecar/.gitignore`:

```
node_modules/
dist/
```

- [ ] **Step 4: Write the stub**

Create `sidecar/stub.ts`:

```typescript
// Stub sidecar for Plan 1. Reads JSONL from stdin, writes JSONL to stdout.
// On start: emits assistant_text, then ask_user, waits for reply, emits done.

import * as readline from "node:readline";

type StartMsg = { type: "start"; objective: unknown };
type AskUserReplyMsg = { type: "ask_user_reply"; id: string; answer: string };
type CancelMsg = { type: "cancel" };
type InMsg = StartMsg | AskUserReplyMsg | CancelMsg;

function send(obj: Record<string, unknown>): void {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

async function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

const rl = readline.createInterface({ input: process.stdin });
const pendingReplies = new Map<string, (answer: string) => void>();
let canceled = false;

rl.on("line", (raw: string) => {
  let msg: InMsg;
  try {
    msg = JSON.parse(raw) as InMsg;
  } catch {
    send({ type: "error", message: `bad json: ${raw}` });
    return;
  }
  if (msg.type === "start") {
    void runSession();
  } else if (msg.type === "ask_user_reply") {
    const cb = pendingReplies.get(msg.id);
    if (cb) {
      pendingReplies.delete(msg.id);
      cb(msg.answer);
    }
  } else if (msg.type === "cancel") {
    canceled = true;
    process.exit(0);
  }
});

async function runSession(): Promise<void> {
  send({ type: "assistant_text", text: "thinking..." });
  await sleep(200);
  if (canceled) return;

  const id = "q-" + Date.now();
  const askPromise = new Promise<string>((resolve) => {
    pendingReplies.set(id, resolve);
  });
  send({ type: "ask_user", id, question: "should i use the existing client?" });
  const answer = await askPromise;
  if (canceled) return;

  send({ type: "assistant_text", text: `got answer: ${answer}. proceeding.` });
  await sleep(200);
  if (canceled) return;

  send({ type: "done", summary: "stub edited 0 files and made coffee" });
  process.exit(0);
}
```

- [ ] **Step 5: Verify the TypeScript compiles**

Run:
```bash
cd /Users/ayusman/charlotte/freeman/sidecar
npm install
npm run typecheck
```
Expected: no errors.

- [ ] **Step 6: Smoke test the stub manually**

Run:
```bash
cd /Users/ayusman/charlotte/freeman/sidecar
( echo '{"type":"start","objective":{"goal":"x","acceptance_criteria":[],"constraints":[],"notes":[],"model":"sonnet"}}'; sleep 1; echo '{"type":"ask_user_reply","id":"q-__REPLACE__","answer":"yes"}' ) | npx tsx stub.ts
```

You'll see lines like:
```
{"type":"assistant_text","text":"thinking..."}
{"type":"ask_user","id":"q-1744...","question":"should i use the existing client?"}
```

The reply won't match because the id is time-based. That's OK — we're only verifying the stub runs. Hit Ctrl-C.

- [ ] **Step 7: Commit**

```bash
git add sidecar/
git commit -m "sidecar: add TypeScript stub for Plan 1 smoke testing"
```

---

## Task 7: Sidecar Go client

**Files:**
- Create: `internal/sidecar/client.go`
- Create: `internal/sidecar/client_test.go`

The client multiplexes stdin/stdout pipes. Production code uses `Spawn()` to fork a subprocess; tests use `NewClientFromPipes()` to inject in-memory pipes and avoid needing Node installed.

- [ ] **Step 1: Write the failing test**

Create `internal/sidecar/client_test.go`:

```go
package sidecar

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestClient_RoundTrip(t *testing.T) {
	// Freeman writes to clientOut (the sidecar's stdin) and reads from clientIn
	// (the sidecar's stdout). We simulate a sidecar by driving the other ends.
	sidecarStdinR, sidecarStdinW := io.Pipe()
	sidecarStdoutR, sidecarStdoutW := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClientFromPipes(sidecarStdinW, sidecarStdoutR)
	defer client.Close()

	// Fake sidecar goroutine: reads start, emits assistant_text, ask_user.
	go func() {
		defer sidecarStdoutW.Close()
		// Read one line (the start msg).
		buf := make([]byte, 4096)
		n, err := sidecarStdinR.Read(buf)
		if err != nil {
			return
		}
		_, err = Decode(trimNewline(buf[:n]))
		if err != nil {
			return
		}
		_ = Encode(sidecarStdoutW, AssistantTextMsg{Type: MsgTypeAssistantText, Text: "hi"})
		_ = Encode(sidecarStdoutW, AskUserMsg{Type: MsgTypeAskUser, ID: "q1", Question: "ok?"})
	}()

	// Send start.
	err := client.Send(StartMsg{
		Type: MsgTypeStart,
		Objective: ObjectivePayload{Goal: "g", Model: "sonnet"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read two events.
	events := client.Events()
	var got1, got2 Message
	select {
	case got1 = <-events:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first event")
	}
	select {
	case got2 = <-events:
	case <-ctx.Done():
		t.Fatal("timed out waiting for second event")
	}

	if _, ok := got1.(AssistantTextMsg); !ok {
		t.Errorf("got1 = %T, want AssistantTextMsg", got1)
	}
	au, ok := got2.(AskUserMsg)
	if !ok {
		t.Fatalf("got2 = %T, want AskUserMsg", got2)
	}
	if au.ID != "q1" || au.Question != "ok?" {
		t.Errorf("au = %+v", au)
	}
}

func TestClient_SendAskUserReply(t *testing.T) {
	sidecarStdinR, sidecarStdinW := io.Pipe()
	_, sidecarStdoutR := io.Pipe()

	client := NewClientFromPipes(sidecarStdinW, sidecarStdoutR)
	defer client.Close()

	done := make(chan Message, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := sidecarStdinR.Read(buf)
		if err != nil {
			done <- nil
			return
		}
		m, err := Decode(trimNewline(buf[:n]))
		if err != nil {
			done <- nil
			return
		}
		done <- m
	}()

	if err := client.Send(AskUserReplyMsg{
		Type: MsgTypeAskUserReply, ID: "q1", Answer: "yes",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case m := <-done:
		r, ok := m.(AskUserReplyMsg)
		if !ok || r.ID != "q1" || r.Answer != "yes" {
			t.Errorf("got %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sidecar/... -v`
Expected: compile error — `NewClientFromPipes`, `Client`, `Send`, `Events`, `Close` undefined.

- [ ] **Step 3: Implement the client**

Create `internal/sidecar/client.go`:

```go
package sidecar

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// Client manages stdin/stdout JSONL communication with a sidecar process.
type Client struct {
	stdin  io.Writer
	stdout io.Reader
	events chan Message
	closer io.Closer // optional; non-nil when Spawn was used
	proc   *exec.Cmd // optional
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// NewClientFromPipes wires a client to existing stdin/stdout pipes.
// Use this in tests. In production, call Spawn instead.
func NewClientFromPipes(stdin io.Writer, stdout io.Reader) *Client {
	c := &Client{
		stdin:  stdin,
		stdout: stdout,
		events: make(chan Message, 16),
	}
	c.wg.Add(1)
	go c.readLoop()
	return c
}

// Spawn launches a subprocess and wires its stdin/stdout to a new Client.
// Example: Spawn(ctx, "node", "sidecar/stub.ts")
func Spawn(ctx context.Context, name string, args ...string) (*Client, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	c := &Client{
		stdin:  stdin,
		stdout: stdout,
		events: make(chan Message, 16),
		closer: stdin,
		proc:   cmd,
	}
	c.wg.Add(1)
	go c.readLoop()
	return c, nil
}

// Events returns the channel of inbound messages from the sidecar.
// The channel is closed when the sidecar stdout EOFs or Close is called.
func (c *Client) Events() <-chan Message { return c.events }

// Send writes a single JSONL message to the sidecar stdin.
func (c *Client) Send(m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("sidecar client closed")
	}
	return Encode(c.stdin, m)
}

// Close shuts down the client. Safe to call multiple times.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	closer := c.closer
	proc := c.proc
	c.mu.Unlock()

	if closer != nil {
		_ = closer.Close()
	}
	if proc != nil {
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}
	c.wg.Wait()
	return nil
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	defer close(c.events)
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := Decode(line)
		if err != nil {
			// Wrap as ErrorMsg so the session sees it.
			c.events <- ErrorMsg{Type: MsgTypeError, Message: err.Error()}
			continue
		}
		c.events <- msg
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/sidecar/... -v`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sidecar/client.go internal/sidecar/client_test.go
git commit -m "sidecar: add Go client with pipe and Spawn constructors"
```

---

## Task 8: Fakes package for ports

**Files:**
- Create: `internal/call/fakes/fakes.go`
- Create: `internal/call/fakes/fakes_test.go`

Fakes live in their own subpackage so that production code and test code can both import them. They are plain implementations of the `call.Transcriber`, `call.Speaker`, `call.PM`, and `call.Hotkey` interfaces backed by stdin/stdout or scripted data.

- [ ] **Step 1: Write the failing test**

Create `internal/call/fakes/fakes_test.go`:

```go
package fakes

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/call"
)

func TestStdoutSpeaker(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdoutSpeaker(&buf)
	if err := s.Speak(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("buf = %q", out)
	}
	if !strings.Contains(out, "tts") {
		t.Errorf("missing tts prefix: %q", out)
	}
}

func TestLineReaderTranscriber(t *testing.T) {
	r := strings.NewReader("hello world\nsecond line\n")
	tr := NewLineReaderTranscriber(r)
	defer tr.Stop()

	u := tr.Utterances()
	got := []string{}
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case t := <-u:
			got = append(got, t)
		case <-timeout:
			t.Fatalf("timed out; got %v", got)
		}
	}
	if got[0] != "hello world" || got[1] != "second line" {
		t.Errorf("got = %v", got)
	}
}

func TestScriptedPM_Intake(t *testing.T) {
	pm := NewScriptedPM()
	ctx := context.Background()
	r1, err := pm.Intake(ctx, call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatal(err)
	}
	if !r1.NeedsMore {
		t.Error("first intake should need more")
	}
	r2, err := pm.Intake(ctx, call.IntakeInput{Latest: "off, 10 percent"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.NeedsMore || r2.Objective == nil {
		t.Errorf("second intake should complete, got %+v", r2)
	}
	if r2.Objective.ModelHint == "" {
		t.Errorf("model hint empty")
	}
}

func TestScriptedPM_RouteAlwaysInline(t *testing.T) {
	pm := NewScriptedPM()
	r, err := pm.Route(context.Background(), call.RouteInput{Question: "x?"})
	if err != nil {
		t.Fatal(err)
	}
	if r.AnswerInline == "" {
		t.Errorf("expected inline answer, got %+v", r)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/call/fakes/... -v`
Expected: package does not exist.

- [ ] **Step 3: Implement the fakes**

Create `internal/call/fakes/fakes.go`:

```go
package fakes

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/Renderix/freeman/internal/call"
)

// StdoutSpeaker prints "[tts] <text>" to the given writer.
type StdoutSpeaker struct {
	w  io.Writer
	mu sync.Mutex
}

// NewStdoutSpeaker returns a Speaker that writes to w.
func NewStdoutSpeaker(w io.Writer) *StdoutSpeaker {
	return &StdoutSpeaker{w: w}
}

// Speak implements call.Speaker.
func (s *StdoutSpeaker) Speak(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := fmt.Fprintf(s.w, "[tts] %s\n", text)
	return err
}

// LineReaderTranscriber reads lines from an io.Reader and emits each as an utterance.
type LineReaderTranscriber struct {
	r      io.Reader
	out    chan string
	stop   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// NewLineReaderTranscriber returns a Transcriber that reads lines from r.
func NewLineReaderTranscriber(r io.Reader) *LineReaderTranscriber {
	t := &LineReaderTranscriber{
		r:    r,
		out:  make(chan string, 4),
		stop: make(chan struct{}),
	}
	t.wg.Add(1)
	go t.loop()
	return t
}

// Utterances implements call.Transcriber.
func (t *LineReaderTranscriber) Utterances() <-chan string { return t.out }

// Stop implements call.Transcriber.
func (t *LineReaderTranscriber) Stop() {
	t.once.Do(func() {
		close(t.stop)
	})
}

func (t *LineReaderTranscriber) loop() {
	defer t.wg.Done()
	defer close(t.out)
	scanner := bufio.NewScanner(t.r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		select {
		case <-t.stop:
			return
		case t.out <- line:
		}
	}
}

// StdinHotkey reads blank lines (or any line starting with a space) from r
// and emits a hotkey event for each.
// For Plan 1 the CLI uses a different input stream for hotkey vs utterances,
// but tests instantiate this directly.
type StdinHotkey struct {
	r    io.Reader
	out  chan struct{}
	stop chan struct{}
	once sync.Once
}

// NewStdinHotkey returns a Hotkey that emits one event per line read from r.
func NewStdinHotkey(r io.Reader) *StdinHotkey {
	h := &StdinHotkey{
		r:    r,
		out:  make(chan struct{}, 4),
		stop: make(chan struct{}),
	}
	go h.loop()
	return h
}

// Events implements call.Hotkey.
func (h *StdinHotkey) Events() <-chan struct{} { return h.out }

// Stop implements call.Hotkey.
func (h *StdinHotkey) Stop() {
	h.once.Do(func() {
		close(h.stop)
	})
}

func (h *StdinHotkey) loop() {
	defer close(h.out)
	scanner := bufio.NewScanner(h.r)
	for scanner.Scan() {
		select {
		case <-h.stop:
			return
		case h.out <- struct{}{}:
		}
	}
}

// ScriptedPM is a deterministic fake PM:
//   - Intake: first call returns NeedsMore; second call returns a completed Objective.
//   - Route:  always returns AnswerInline: "yes".
type ScriptedPM struct {
	mu        sync.Mutex
	intakeCnt int
}

// NewScriptedPM returns a fake PM.
func NewScriptedPM() *ScriptedPM { return &ScriptedPM{} }

// Intake implements call.PM.
func (p *ScriptedPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.intakeCnt++
	if p.intakeCnt == 1 {
		return call.PMIntakeResult{
			NeedsMore: true,
			Question:  "tell me more about constraints.",
		}, nil
	}
	return call.PMIntakeResult{
		NeedsMore: false,
		Objective: &call.Objective{
			Goal:               "scripted goal from: " + in.Latest,
			AcceptanceCriteria: []string{"tests pass"},
			Constraints:        []string{"no breaking changes"},
			ModelHint:          "sonnet",
			SpokenSummary:      "scripted summary.",
		},
	}, nil
}

// Route implements call.PM.
func (p *ScriptedPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error) {
	return call.PMRouteResult{
		AnswerInline: "yes",
	}, nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/call/fakes/... -v`
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/call/fakes/
git commit -m "call/fakes: add stdin/stdout/scripted fakes for Plan 1 CLI"
```

---

## Task 9: Session driver with integration test

**Files:**
- Create: `internal/call/session.go`
- Create: `internal/call/session_test.go`

The Session owns the Machine and the ports. `Run(ctx)` loops on a select over every input channel, feeds events to the machine, and executes effects. PM calls are executed in goroutines and their results come back as events.

- [ ] **Step 1: Write the failing integration test**

Create `internal/call/session_test.go`:

```go
package call_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/call/fakes"
	"github.com/Renderix/freeman/internal/sidecar"
)

func TestSession_HappyPath(t *testing.T) {
	// Pipes simulating a sidecar process.
	sidecarStdinR, sidecarStdinW := io.Pipe()
	sidecarStdoutR, sidecarStdoutW := io.Pipe()
	client := sidecar.NewClientFromPipes(sidecarStdinW, sidecarStdoutR)
	defer client.Close()

	// Stub sidecar goroutine.
	sidecarDone := make(chan struct{})
	go func() {
		defer close(sidecarDone)
		defer sidecarStdoutW.Close()
		scanner := newLineScanner(sidecarStdinR)
		// First line: start.
		if !scanner.Scan() {
			return
		}
		_, err := sidecar.Decode(scanner.Bytes())
		if err != nil {
			return
		}
		// Emit ask_user.
		_ = sidecar.Encode(sidecarStdoutW, sidecar.AskUserMsg{
			Type: sidecar.MsgTypeAskUser, ID: "q1", Question: "use existing?",
		})
		// Read reply.
		if !scanner.Scan() {
			return
		}
		reply, _ := sidecar.Decode(scanner.Bytes())
		r, ok := reply.(sidecar.AskUserReplyMsg)
		if !ok || r.ID != "q1" {
			return
		}
		// Emit done.
		_ = sidecar.Encode(sidecarStdoutW, sidecar.DoneMsg{
			Type: sidecar.MsgTypeDone, Summary: "ok",
		})
	}()

	// Fakes.
	var speakerBuf bytes.Buffer
	speaker := fakes.NewStdoutSpeaker(&speakerBuf)

	// The transcriber + hotkey are driven by in-memory buffers that we write
	// to as the test progresses.
	trInput, trWriter := io.Pipe()
	tr := fakes.NewLineReaderTranscriber(trInput)
	defer tr.Stop()

	hkInput, hkWriter := io.Pipe()
	hk := fakes.NewStdinHotkey(hkInput)
	defer hk.Stop()

	pm := fakes.NewScriptedPM()

	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     speaker,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     client,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- session.Run(ctx)
	}()

	// Drive the flow.
	// 1. Press hotkey → session enters Intake, speaks greeting.
	_, _ = hkWriter.Write([]byte("\n"))
	waitFor(t, &speakerBuf, "what are we building", 1*time.Second)

	// 2. Utterance 1 → PM returns NeedsMore → speaker gets follow-up.
	_, _ = trWriter.Write([]byte("build a feature flag\n"))
	waitFor(t, &speakerBuf, "constraints", 1*time.Second)

	// 3. Utterance 2 → PM returns Objective → speaker gets confirmation.
	_, _ = trWriter.Write([]byte("off, 10 percent\n"))
	waitFor(t, &speakerBuf, "should i start", 1*time.Second)

	// 4. Confirm → dispatch → sidecar gets start, emits ask_user.
	// PM routes inline → session replies → sidecar emits done → speaker gets summary.
	_, _ = trWriter.Write([]byte("yes\n"))
	waitFor(t, &speakerBuf, "done", 2*time.Second)

	cancel()
	<-runDone
	<-sidecarDone
}

// waitFor polls buf until it contains want, or fails.
func waitFor(t *testing.T, buf *bytes.Buffer, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in speaker output; got:\n%s", want, buf.String())
}

// newLineScanner wraps an io.Reader in a bufio.Scanner configured for JSONL.
func newLineScanner(r io.Reader) interface {
	Scan() bool
	Bytes() []byte
} {
	s := &lineScanner{r: r, buf: make([]byte, 0, 4096)}
	return s
}

type lineScanner struct {
	r    io.Reader
	buf  []byte
	line []byte
	err  error
}

func (s *lineScanner) Scan() bool {
	s.line = nil
	for {
		if i := indexByte(s.buf, '\n'); i >= 0 {
			s.line = append([]byte(nil), s.buf[:i]...)
			s.buf = s.buf[i+1:]
			return true
		}
		tmp := make([]byte, 4096)
		n, err := s.r.Read(tmp)
		if n > 0 {
			s.buf = append(s.buf, tmp[:n]...)
		}
		if err != nil {
			return false
		}
	}
}

func (s *lineScanner) Bytes() []byte { return s.line }

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/call/... -run TestSession_HappyPath -v`
Expected: compile error — `call.NewSession`, `call.SessionDeps`, `session.Run` undefined.

- [ ] **Step 3: Implement the session**

Create `internal/call/session.go`:

```go
package call

import (
	"context"
	"fmt"

	"github.com/Renderix/freeman/internal/sidecar"
)

// SessionDeps are the port implementations injected into a Session.
type SessionDeps struct {
	Transcriber Transcriber
	Speaker     Speaker
	PM          PM
	Hotkey      Hotkey
	Sidecar     *sidecar.Client
}

// Session wires a Machine to its ports and runs the event loop.
type Session struct {
	deps    SessionDeps
	machine *Machine
	// internal channel for PM results so they interleave with external events.
	pmResults chan Event
}

// NewSession constructs a Session.
func NewSession(deps SessionDeps) *Session {
	return &Session{
		deps:      deps,
		machine:   NewMachine(),
		pmResults: make(chan Event, 4),
	}
}

// Run blocks until ctx is canceled, processing events and effects.
func (s *Session) Run(ctx context.Context) error {
	utterances := s.deps.Transcriber.Utterances()
	hotkeys := s.deps.Hotkey.Events()
	sidecarEvents := s.deps.Sidecar.Events()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-hotkeys:
			s.handleEvent(ctx, HotkeyPress{})
		case text, ok := <-utterances:
			if !ok {
				utterances = nil
				continue
			}
			s.handleEvent(ctx, UserUtterance{Text: text})
		case msg, ok := <-sidecarEvents:
			if !ok {
				sidecarEvents = nil
				continue
			}
			s.handleSidecarMessage(ctx, msg)
		case ev := <-s.pmResults:
			s.handleEvent(ctx, ev)
		}
	}
}

func (s *Session) handleEvent(ctx context.Context, e Event) {
	effects := s.machine.Handle(e)
	for _, eff := range effects {
		s.runEffect(ctx, eff)
	}
}

func (s *Session) handleSidecarMessage(ctx context.Context, msg sidecar.Message) {
	switch m := msg.(type) {
	case sidecar.AssistantTextMsg:
		s.handleEvent(ctx, SidecarAssistantText{Text: m.Text})
	case sidecar.AskUserMsg:
		s.handleEvent(ctx, SidecarAskUser{ID: m.ID, Question: m.Question})
	case sidecar.DoneMsg:
		s.handleEvent(ctx, SidecarDone{Summary: m.Summary})
	case sidecar.ErrorMsg:
		s.handleEvent(ctx, SidecarError{Message: m.Message})
	}
}

func (s *Session) runEffect(ctx context.Context, e Effect) {
	switch eff := e.(type) {
	case SpeakEffect:
		_ = s.deps.Speaker.Speak(ctx, eff.Text)
	case CallPMIntakeEffect:
		in := eff.Input
		go func() {
			res, err := s.deps.PM.Intake(ctx, in)
			if err != nil {
				s.pmResults <- SidecarError{Message: fmt.Sprintf("pm intake: %v", err)}
				return
			}
			s.pmResults <- res
		}()
	case CallPMRouteEffect:
		in := eff.Input
		id := eff.ID
		go func() {
			res, err := s.deps.PM.Route(ctx, in)
			if err != nil {
				s.pmResults <- SidecarError{Message: fmt.Sprintf("pm route: %v", err)}
				return
			}
			res.ID = id
			s.pmResults <- res
		}()
	case SendSidecarStartEffect:
		payload := sidecar.ObjectivePayload{
			Goal:               eff.Objective.Goal,
			AcceptanceCriteria: eff.Objective.AcceptanceCriteria,
			Constraints:        eff.Objective.Constraints,
			Notes:              eff.Objective.Notes,
			Model:              eff.Objective.ModelHint,
		}
		_ = s.deps.Sidecar.Send(sidecar.StartMsg{
			Type:      sidecar.MsgTypeStart,
			Objective: payload,
		})
	case SendSidecarReplyEffect:
		_ = s.deps.Sidecar.Send(sidecar.AskUserReplyMsg{
			Type:   sidecar.MsgTypeAskUserReply,
			ID:     eff.ID,
			Answer: eff.Answer,
		})
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/call/... -v`
Expected: all tests PASS, including TestSession_HappyPath.

- [ ] **Step 5: Commit**

```bash
git add internal/call/session.go internal/call/session_test.go
git commit -m "call: add Session driver with happy-path integration test"
```

---

## Task 10: `freeman call` Cobra command + manual smoke test

**Files:**
- Create: `cmd/freeman/call.go`
- Modify: `cmd/freeman/main.go`

The command wires fakes (stdin for utterances, a separate FD for hotkey) to a Session with a real spawned TSX stub sidecar. For Plan 1 the hotkey is triggered by sending SIGUSR1 to the process — no real hotkey daemon yet. This keeps the CLI testable from the terminal and the binary functional end-to-end.

- [ ] **Step 1: Write the call command**

Create `cmd/freeman/call.go`:

```go
package main

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

var callCmd = &cobra.Command{
	Use:   "call",
	Short: "Start a Freeman voice call (Plan 1: fakes + stub sidecar)",
	Long: `Plan 1 harness: reads user utterances as lines from stdin,
writes spoken output as '[tts] ...' lines to stdout, uses a scripted
PM, and spawns the TSX stub sidecar.

Send SIGUSR1 to the process to simulate a hotkey press.`,
	RunE: runCall,
}

func init() {
	// callCmd is registered in main.go to share the persistent --config flag.
}

func runCall(cmd *cobra.Command, args []string) error {
	_ = config.LoadConfig(configFile) // not yet used in Plan 1; ensures config is loadable

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// 1. Spawn the TSX stub sidecar.
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	stubPath := filepath.Join(repoRoot, "sidecar", "stub.ts")
	sc, err := sidecar.Spawn(ctx, "npx", "--yes", "tsx", stubPath)
	if err != nil {
		return fmt.Errorf("spawn sidecar: %w", err)
	}
	defer sc.Close()

	// 2. Build fakes.
	tr := fakes.NewLineReaderTranscriber(os.Stdin)
	defer tr.Stop()

	speaker := fakes.NewStdoutSpeaker(os.Stdout)
	pm := fakes.NewScriptedPM()

	// 3. Hotkey via SIGUSR1.
	hkChan := make(chan struct{}, 4)
	sigChan := make(chan os.Signal, 4)
	signal.Notify(sigChan, syscall.SIGUSR1)
	go func() {
		for range sigChan {
			hkChan <- struct{}{}
		}
	}()
	hk := &channelHotkey{ch: hkChan}

	// 4. Session.
	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     speaker,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     sc,
	})

	fmt.Fprintln(os.Stderr, "freeman: ready. send SIGUSR1 to start a call, then type utterances as lines.")
	fmt.Fprintf(os.Stderr, "freeman: pid=%d\n", os.Getpid())
	return session.Run(ctx)
}

// channelHotkey adapts a plain channel to the Hotkey interface.
type channelHotkey struct{ ch chan struct{} }

func (c *channelHotkey) Events() <-chan struct{} { return c.ch }
func (c *channelHotkey) Stop()                   {}

// findRepoRoot walks up from the current working directory until it finds go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found walking up from cwd")
		}
		dir = parent
	}
}
```

- [ ] **Step 2: Register the command in main.go**

Modify `cmd/freeman/main.go` — change the line that reads:

```go
	rootCmd.AddCommand(startCmd, versionCmd)
```

to:

```go
	rootCmd.AddCommand(startCmd, versionCmd, callCmd)
```

- [ ] **Step 3: Verify the binary builds**

Run: `go build -o /tmp/freeman ./cmd/freeman`
Expected: no errors, binary at `/tmp/freeman`.

- [ ] **Step 4: Verify `go vet` and `go test ./...` still pass**

Run:
```bash
go vet ./...
go test ./...
```
Expected: all pass.

- [ ] **Step 5: Manual smoke test**

In one terminal:
```bash
cd /Users/ayusman/charlotte/freeman
go run ./cmd/freeman call
```
You should see `freeman: ready...` and `freeman: pid=NNNN` on stderr.

In another terminal, find the pid and send SIGUSR1:
```bash
kill -USR1 <pid>
```

Back in the first terminal, you should see:
```
[tts] hi. what are we building?
```

Type a line: `build a feature flag` and press Enter. Expect:
```
[tts] tell me more about constraints.
```

Type: `off, 10 percent` and press Enter. Expect:
```
[tts] scripted summary. should i start?
```

Type: `yes` and press Enter. Expect (in order):
```
[tts] starting now.
[tts] done. stub edited 0 files and made coffee
```

(The sidecar stub will ask its own question which the session routes inline via the scripted PM, so you won't be prompted for it.)

Hit Ctrl-C to exit.

- [ ] **Step 6: Commit**

```bash
git add cmd/freeman/call.go cmd/freeman/main.go
git commit -m "cmd: add freeman call command wiring fakes + TSX stub sidecar"
```

---

## Self-Review Checklist

After completing all tasks, verify:

- [ ] `go test ./...` passes with no failures.
- [ ] `go vet ./...` is clean.
- [ ] `go build ./...` succeeds.
- [ ] The manual smoke test in Task 10 Step 5 produces the exact expected TTS lines.
- [ ] No TODO, FIXME, or placeholder comments left in the new files.
- [ ] `docs/superpowers/specs/2026-04-15-freeman-voice-companion-design.md` is unchanged (this plan only implements the skeleton of what the spec describes).

## What Plan 1 deliberately does NOT do

The following are all addressed in Plans 2 and 3:

- No real microphone input, no whisper.cpp, no VAD, no endpointing.
- No real Kokoro TTS output — speak goes to stdout as `[tts] ...` lines.
- No real Haiku — PM is the scripted fake.
- No real `pi-coding-agent` — sidecar is the TSX stub.
- No real hotkey listener — hotkey is simulated via SIGUSR1.
- No barge-in, no cancelable TTS, no hotkey-as-hangup.
- No `--log-transcript`, no replay tool, no tuning flags.
- No error recovery beyond logging to stderr (e.g. PM API errors degrade to "always escalate", which isn't implemented because the fake PM can't fail).

After Plan 1 ships, you have a binary you can run end-to-end, a state machine with full test coverage, a sidecar protocol ready to accept real pi events, and ports ready to accept real audio/LLM implementations.
