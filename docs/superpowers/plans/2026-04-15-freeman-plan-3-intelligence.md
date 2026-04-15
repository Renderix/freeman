# Freeman Plan 3 (Intelligence) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Plan 1/2 stubs with a real Haiku PM (Anthropic API + tool-use), a pi-coding-agent sidecar, and VAD-triggered barge-in so the user can interrupt Freeman mid-speech.

**Architecture:** VAD gains a `SpeechOnsets()` channel; the call layer adds `InterruptedText` threading and async Speak with goroutine cancellation; `internal/pm` wraps the Anthropic Messages API with structured tool-use output; `sidecar/sidecar.ts` replaces the stub with a real `createAgentSession` call; wiring changes only touch `cmd/freeman/call.go`.

**Tech Stack:** Go 1.25, `github.com/anthropics/anthropic-sdk-go`, TypeScript/Bun, `@mariozechner/pi-coding-agent`

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `internal/audio/vad/vad.go` | Modify | Add `onsets chan struct{}`, `SpeechOnsets()`, onset send on state transition |
| `internal/audio/vad/vad_test.go` | Modify | Add onset timing test |
| `internal/call/events.go` | Modify | `UserUtterance` gains `InterruptedText string` |
| `internal/call/types.go` | Modify | `IntakeInput` and `RouteInput` gain `InterruptedText string` |
| `internal/call/effects.go` | Modify | Add `ResetPMEffect` |
| `internal/call/ports.go` | Modify | `PM` gains `Reset()`; `SessionDeps` gains `SpeechOnsets <-chan struct{}` |
| `internal/call/fakes/fakes.go` | Modify | `ScriptedPM.Reset()` resets `intakeCnt` |
| `internal/call/machine.go` | Modify | `handleIdle` prepends `ResetPMEffect`; pass `InterruptedText` through intake |
| `internal/call/session.go` | Modify | Async Speak goroutine; barge-in cases; `ResetPMEffect` case; `interruptedText` tracking |
| `internal/call/session_test.go` | Modify | Add barge-in tests |
| `internal/pm/prompts.go` | Create | Intake and router system prompt constants |
| `internal/pm/haiku.go` | Create | `HaikuPM` implementing `call.PM` via Anthropic API |
| `internal/pm/haiku_test.go` | Create | 7 tests against `httptest.NewServer` |
| `sidecar/sidecar.ts` | Create | Real pi-coding-agent session |
| `sidecar/package.json` | Modify | Add `@mariozechner/pi-coding-agent` dependency and `sidecar` script |
| `cmd/freeman/call.go` | Modify | Wire `HaikuPM`, `SpeechOnsets`, `sidecar.ts` in `runCallWithRealAudio` |

---

### Task 1: VAD Speech Onset

**Files:**
- Modify: `internal/audio/vad/vad.go`
- Modify: `internal/audio/vad/vad_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/audio/vad/vad_test.go` (after the existing tests):

```go
// Script: 5 silence frames then 20 speech frames.
// SpeechOnsets() must fire once, before the utterance channel fires.
func TestVAD_SpeechOnset(t *testing.T) {
	script := append(boolN(5, false), boolN(20, true)...)
	script = append(script, boolN(40, false)...) // 800 ms silence to flush utterance
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

	// Onset must arrive before or simultaneously with the first utterance.
	onsets := v.SpeechOnsets()

	select {
	case <-onsets:
		// good: got an onset
	case <-out:
		t.Fatal("utterance arrived before onset")
	case <-ctx.Done():
		t.Fatal("timed out waiting for onset")
	}

	// Utterance should follow.
	select {
	case u, ok := <-out:
		if !ok {
			t.Fatal("utterance channel closed without emitting")
		}
		if u.DurationMs != 400 {
			t.Errorf("duration = %d, want 400", u.DurationMs)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for utterance after onset")
	}
}
```

- [ ] **Step 2: Run it — expect compile failure**

```bash
cd /Users/ayusman/charlotte/freeman
go test ./internal/audio/vad/... 2>&1 | head -20
```

Expected: `v.SpeechOnsets undefined`

- [ ] **Step 3: Add the onset channel to `vad.go`**

Replace the `VAD` struct and `NewWithDetector` in `internal/audio/vad/vad.go`:

```go
// VAD owns the detector and the endpointing SM.
type VAD struct {
	cfg    Config
	det    Detector
	onsets chan struct{}
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
	return &VAD{cfg: cfg, det: d, onsets: make(chan struct{}, 1)}
}

// SpeechOnsets returns a channel that receives a value on every
// stateSilent → stateSpeech transition. The channel has capacity 1;
// extra onsets are dropped rather than blocking the VAD goroutine.
func (v *VAD) SpeechOnsets() <-chan struct{} { return v.onsets }
```

- [ ] **Step 4: Send on onset in the Run goroutine**

Inside the `Run` goroutine in `vad.go`, in the `case stateSilent:` branch, add the non-blocking send immediately after `state = stateSpeech`:

```go
case stateSilent:
    if isSpeech {
        state = stateSpeech
        // Notify listeners of speech onset; non-blocking to never stall the goroutine.
        select {
        case v.onsets <- struct{}{}:
        default:
        }
        buf = append(buf, frame...)
        bufFrames++
        silenceFrames = 0
    }
```

- [ ] **Step 5: Run the test — expect pass**

```bash
go test ./internal/audio/vad/... -v -run TestVAD_SpeechOnset
```

Expected:
```
--- PASS: TestVAD_SpeechOnset (0.00s)
PASS
```

- [ ] **Step 6: Run the full vad suite — expect all pass**

```bash
go test ./internal/audio/vad/... -v
```

Expected: 3 tests pass (SingleUtterance, DropsShortSpeech, TwoUtterances, SpeechOnset).

- [ ] **Step 7: Commit**

```bash
git add internal/audio/vad/vad.go internal/audio/vad/vad_test.go
git commit -m "feat(vad): add SpeechOnsets() channel for barge-in"
```

---

### Task 2: Call Layer Data Model

**Files:**
- Modify: `internal/call/events.go`
- Modify: `internal/call/types.go`
- Modify: `internal/call/effects.go`
- Modify: `internal/call/ports.go`
- Modify: `internal/call/fakes/fakes.go`

- [ ] **Step 1: Add `InterruptedText` to `UserUtterance`**

In `internal/call/events.go`, replace the `UserUtterance` struct:

```go
// UserUtterance is a finalized transcript from the user.
type UserUtterance struct {
	Text            string
	InterruptedText string // non-empty when user barged in during Freeman's speech
}
```

- [ ] **Step 2: Add `InterruptedText` to `IntakeInput` and `RouteInput`**

In `internal/call/types.go`, replace both structs:

```go
// IntakeInput is what the session hands the PM on each intake call.
type IntakeInput struct {
	Transcript      []string // alternating user/PM utterances, oldest first
	Latest          string   // the most recent user utterance
	InterruptedText string   // what Freeman was saying when user interrupted; empty if no barge-in
}

// RouteInput is what the session hands the PM on each ask_user routing call.
type RouteInput struct {
	Objective       Objective
	Transcript      []string
	Question        string
	InterruptedText string // what Freeman was saying when user interrupted; empty if no barge-in
}
```

- [ ] **Step 3: Add `ResetPMEffect` to `effects.go`**

Append to `internal/call/effects.go`:

```go
// ResetPMEffect tells the session to call PM.Reset(), clearing conversation history.
// Emitted by the machine when entering Intake from Idle (new call begins).
type ResetPMEffect struct{}

func (ResetPMEffect) isEffect() {}
```

- [ ] **Step 4: Add `Reset()` to the `PM` interface and `SpeechOnsets` to `SessionDeps`**

Replace the `PM` interface and add the `SpeechOnsets` field in `internal/call/ports.go`:

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
	// Reset clears conversation history. Called at the start of every new call.
	Reset()
}

// Hotkey emits an event whenever the call hotkey is pressed.
// For Plan 1, the fake reads blank lines from stdin.
type Hotkey interface {
	Events() <-chan struct{}
	Stop()
}

// SessionDeps are the port implementations injected into a Session.
// (Moved here from session.go so ports.go is the single source of truth.)
```

Then update `internal/call/session.go` to move `SessionDeps` into `ports.go`. Actually, `SessionDeps` is currently in `session.go`. We'll update it there. For now, just update `ports.go` as shown above (without `SessionDeps` — that stays in `session.go`).

Replace `internal/call/ports.go` with exactly:

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
	// Reset clears conversation history. Called at the start of every new call.
	Reset()
}

// Hotkey emits an event whenever the call hotkey is pressed.
// For Plan 1, the fake reads blank lines from stdin.
type Hotkey interface {
	Events() <-chan struct{}
	Stop()
}
```

- [ ] **Step 5: Update `SessionDeps` in `session.go` to add `SpeechOnsets`**

In `internal/call/session.go`, replace the `SessionDeps` struct:

```go
// SessionDeps are the port implementations injected into a Session.
type SessionDeps struct {
	Transcriber  Transcriber
	Speaker      Speaker
	PM           PM
	Hotkey       Hotkey
	Sidecar      *sidecar.Client
	SpeechOnsets <-chan struct{} // from vad.VAD.SpeechOnsets(); nil disables barge-in
}
```

- [ ] **Step 6: Add `Reset()` to `ScriptedPM` in `fakes.go`**

Append to `internal/call/fakes/fakes.go` (after the existing `Route` method):

```go
// Reset implements call.PM. Resets intakeCnt so the next call starts fresh.
func (p *ScriptedPM) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.intakeCnt = 0
}
```

- [ ] **Step 7: Verify compile — all packages must build**

```bash
go build ./...
```

Expected: no errors. (The `session.go` `SpeakEffect` case and the missing `ResetPMEffect` case will produce no compile errors — they're just unhandled switch arms.)

- [ ] **Step 8: Commit**

```bash
git add internal/call/events.go internal/call/types.go internal/call/effects.go \
        internal/call/ports.go internal/call/session.go internal/call/fakes/fakes.go
git commit -m "feat(call): data model for InterruptedText, ResetPMEffect, PM.Reset"
```

---

### Task 3: Machine — ResetPM and InterruptedText Passthrough

**Files:**
- Modify: `internal/call/machine.go`

- [ ] **Step 1: Write failing tests (in `machine_test.go` — create if missing)**

Check whether `machine_test.go` exists:

```bash
ls internal/call/machine_test.go 2>/dev/null || echo "missing"
```

If missing, create `internal/call/machine_test.go`:

```go
package call

import (
	"testing"
)

func TestMachine_IdleHotkeyEmitsResetPM(t *testing.T) {
	m := NewMachine()
	effects := m.Handle(HotkeyPress{})
	if len(effects) < 2 {
		t.Fatalf("want ≥2 effects, got %d", len(effects))
	}
	if _, ok := effects[0].(ResetPMEffect); !ok {
		t.Errorf("effects[0] = %T, want ResetPMEffect", effects[0])
	}
	if _, ok := effects[1].(SpeakEffect); !ok {
		t.Errorf("effects[1] = %T, want SpeakEffect", effects[1])
	}
}

func TestMachine_IntakePassesInterruptedText(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{}) // enter Intake
	effects := m.Handle(UserUtterance{Text: "build a feature flag", InterruptedText: "what should i call it?"})
	if len(effects) != 1 {
		t.Fatalf("want 1 effect, got %d", len(effects))
	}
	eff, ok := effects[0].(CallPMIntakeEffect)
	if !ok {
		t.Fatalf("effect = %T, want CallPMIntakeEffect", effects[0])
	}
	if eff.Input.InterruptedText != "what should i call it?" {
		t.Errorf("InterruptedText = %q, want %q", eff.Input.InterruptedText, "what should i call it?")
	}
}
```

- [ ] **Step 2: Run — expect failures**

```bash
go test ./internal/call/... -run "TestMachine_IdleHotkeyEmitsResetPM|TestMachine_IntakePassesInterruptedText" -v
```

Expected: `FAIL` — `ResetPMEffect` not emitted yet, `InterruptedText` not passed.

- [ ] **Step 3: Update `handleIdle` to prepend `ResetPMEffect`**

In `internal/call/machine.go`, replace `handleIdle`:

```go
func (m *Machine) handleIdle(e Event) []Effect {
	if _, ok := e.(HotkeyPress); ok {
		m.state = StateIntake
		m.transcript = []string{}
		m.objective = nil
		m.pendingAskUserID = ""
		return []Effect{ResetPMEffect{}, SpeakEffect{Text: "hi. what are we building?"}}
	}
	return []Effect{}
}
```

- [ ] **Step 4: Update `handleIntake` to pass `InterruptedText`**

In `internal/call/machine.go`, replace the `UserUtterance` case inside `handleIntake`:

```go
case UserUtterance:
    m.transcript = append(m.transcript, ev.Text)
    return []Effect{CallPMIntakeEffect{Input: IntakeInput{
        Transcript:      append([]string{}, m.transcript...),
        Latest:          ev.Text,
        InterruptedText: ev.InterruptedText,
    }}}
```

- [ ] **Step 5: Update `handleAwaitingConfirm` to pass `InterruptedText`**

In `internal/call/machine.go`, replace the non-affirmative branch in `handleAwaitingConfirm`:

```go
// Not affirmative — treat as continued intake.
m.state = StateIntake
return []Effect{CallPMIntakeEffect{Input: IntakeInput{
    Transcript:      append([]string{}, m.transcript...),
    Latest:          ev.Text,
    InterruptedText: ev.InterruptedText,
}}}
```

- [ ] **Step 6: Run tests — expect pass**

```bash
go test ./internal/call/... -v
```

Expected: all tests pass including the two new machine tests and the existing `TestSession_HappyPath`.

- [ ] **Step 7: Commit**

```bash
git add internal/call/machine.go internal/call/machine_test.go
git commit -m "feat(call/machine): ResetPMEffect on new call, pass InterruptedText through intake"
```

---

### Task 4: Session Async Speak + Barge-in

**Files:**
- Modify: `internal/call/session.go`
- Modify: `internal/call/session_test.go`

- [ ] **Step 1: Write the failing barge-in test**

Add to `internal/call/session_test.go` (after `TestSession_HappyPath`):

```go
// TestSession_BargeinCancelsSpeak: inject SpeakEffect, then send a speech onset
// before Speak completes. Assert the next UserUtterance carries InterruptedText.
func TestSession_BargeinCancelsSpeak(t *testing.T) {
	// Sidecar pipes (not used in this test but NewSession requires a *sidecar.Client).
	scStdinR, scStdinW := io.Pipe()
	scStdoutR, scStdoutW := io.Pipe()
	defer scStdinW.Close()
	defer scStdoutW.Close()
	defer scStdoutR.Close()
	defer scStdinR.Close()
	sc := sidecar.NewClientFromPipes(scStdinW, scStdoutR)
	defer sc.Close()

	var speakerBuf syncBuffer
	// slowSpeaker blocks until its context is canceled, simulating long TTS.
	slowSpeaker := &slowCancelSpeaker{buf: &speakerBuf}

	trInput, trWriter := io.Pipe()
	tr := fakes.NewLineReaderTranscriber(trInput)
	defer tr.Stop()

	hkInput, hkWriter := io.Pipe()
	hk := fakes.NewStdinHotkey(hkInput)
	defer hk.Stop()

	pm := fakes.NewScriptedPM()
	onsets := make(chan struct{}, 1)

	session := call.NewSession(call.SessionDeps{
		Transcriber:  tr,
		Speaker:      slowSpeaker,
		PM:           pm,
		Hotkey:       hk,
		Sidecar:      sc,
		SpeechOnsets: onsets,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(ctx) }()

	// Press hotkey → enters Intake, greeting SpeakEffect fires (async).
	_, _ = hkWriter.Write([]byte("\n"))

	// Wait for Speak to have started (slowSpeaker sets a flag).
	if !slowSpeaker.waitStarted(time.Second) {
		t.Fatal("Speak never started")
	}

	// Fire a speech onset — this should cancel the in-flight Speak.
	onsets <- struct{}{}

	// Wait for Speak to have been canceled.
	if !slowSpeaker.waitCanceled(time.Second) {
		t.Fatal("Speak was not canceled after onset")
	}

	// Send an utterance — it should carry InterruptedText.
	_, _ = trWriter.Write([]byte("build a feature flag\n"))

	// PM will be called; wait for the follow-up question (NeedsMore=true).
	waitFor(t, &speakerBuf, "constraints", time.Second)

	cancel()
	<-runDone
}

// TestSession_NoInterruptedTextWithoutBargein: Speak completes normally;
// the next UserUtterance has empty InterruptedText.
func TestSession_NoInterruptedTextWithoutBargein(t *testing.T) {
	scStdinR, scStdinW := io.Pipe()
	scStdoutR, scStdoutW := io.Pipe()
	defer scStdinW.Close()
	defer scStdoutW.Close()
	defer scStdoutR.Close()
	defer scStdinR.Close()
	sc := sidecar.NewClientFromPipes(scStdinW, scStdoutR)
	defer sc.Close()

	var speakerBuf syncBuffer
	speaker := fakes.NewStdoutSpeaker(&speakerBuf)

	trInput, trWriter := io.Pipe()
	tr := fakes.NewLineReaderTranscriber(trInput)
	defer tr.Stop()

	hkInput, hkWriter := io.Pipe()
	hk := fakes.NewStdinHotkey(hkInput)
	defer hk.Stop()

	pm := &recordingPM{inner: fakes.NewScriptedPM()}
	onsets := make(chan struct{}, 1) // never sent

	session := call.NewSession(call.SessionDeps{
		Transcriber:  tr,
		Speaker:      speaker,
		PM:           pm,
		Hotkey:       hk,
		Sidecar:      sc,
		SpeechOnsets: onsets,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(ctx) }()

	_, _ = hkWriter.Write([]byte("\n"))
	waitFor(t, &speakerBuf, "what are we building", time.Second)

	_, _ = trWriter.Write([]byte("build a feature flag\n"))
	waitFor(t, &speakerBuf, "constraints", time.Second)

	if pm.lastInterruptedText() != "" {
		t.Errorf("InterruptedText = %q, want empty", pm.lastInterruptedText())
	}

	cancel()
	<-runDone
}
```

Add the test helpers after `waitFor` in `session_test.go`:

```go
// slowCancelSpeaker blocks until its context is canceled, then returns.
// Used to simulate long TTS that can be barged in on.
type slowCancelSpeaker struct {
	buf     *syncBuffer
	mu      sync.Mutex
	started bool
	done    bool
}

func (s *slowCancelSpeaker) Speak(ctx context.Context, text string) error {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	_, _ = fmt.Fprintf(s.buf, "[tts] %s\n", text)
	<-ctx.Done()
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	return ctx.Err()
}

func (s *slowCancelSpeaker) waitStarted(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		v := s.started
		s.mu.Unlock()
		if v {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func (s *slowCancelSpeaker) waitCanceled(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		v := s.done
		s.mu.Unlock()
		if v {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// recordingPM wraps ScriptedPM and records the last IntakeInput.
type recordingPM struct {
	inner *fakes.ScriptedPM
	mu    sync.Mutex
	last  call.IntakeInput
}

func (p *recordingPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error) {
	p.mu.Lock()
	p.last = in
	p.mu.Unlock()
	return p.inner.Intake(ctx, in)
}

func (p *recordingPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error) {
	return p.inner.Route(ctx, in)
}

func (p *recordingPM) Reset() { p.inner.Reset() }

func (p *recordingPM) lastInterruptedText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last.InterruptedText
}
```

Also add missing imports to `session_test.go` (the file already imports `call`, `fakes`, `sidecar`; add `fmt` if not present):

```go
import (
    "bytes"
    "context"
    "fmt"
    "io"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/Renderix/freeman/internal/call"
    "github.com/Renderix/freeman/internal/call/fakes"
    "github.com/Renderix/freeman/internal/sidecar"
)
```

- [ ] **Step 2: Run — expect compile or test failures**

```bash
go test ./internal/call/... 2>&1 | head -30
```

Expected: compile errors (unknown field `SpeechOnsets` in `SessionDeps`, etc.) or test failures. We'll fix them next.

- [ ] **Step 3: Rewrite `session.go` with async Speak + barge-in**

Replace the full content of `internal/call/session.go`:

```go
package call

import (
	"context"
	"fmt"

	"github.com/Renderix/freeman/internal/sidecar"
)

// Session wires a Machine to its ports and runs the event loop.
type Session struct {
	deps    SessionDeps
	machine *Machine
	// internal channel for PM results so they interleave with external events.
	pmResults chan Event

	// Async Speak state — read/written only from the Run goroutine; no mutex.
	cancelSpeak      func()       // non-nil when a Speak goroutine is in flight
	currentSpeakText string       // text being spoken; stored for interrupted-text context
	speakDone        chan struct{} // Speak goroutine sends here when it finishes
	interruptedText  string       // set on barge-in; attached to next UserUtterance
}

// NewSession constructs a Session.
func NewSession(deps SessionDeps) *Session {
	return &Session{
		deps:      deps,
		machine:   NewMachine(),
		pmResults: make(chan Event, 4),
		speakDone: make(chan struct{}, 1),
	}
}

// Run blocks until ctx is canceled, processing events and effects.
func (s *Session) Run(ctx context.Context) error {
	utterances := s.deps.Transcriber.Utterances()
	hotkeys := s.deps.Hotkey.Events()
	sidecarEvents := s.deps.Sidecar.Events()

	// Convert nil SpeechOnsets to a channel that never fires.
	speechOnsets := s.deps.SpeechOnsets
	if speechOnsets == nil {
		speechOnsets = make(chan struct{})
	}

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
			ev := UserUtterance{Text: text, InterruptedText: s.interruptedText}
			s.interruptedText = ""
			s.handleEvent(ctx, ev)

		case msg, ok := <-sidecarEvents:
			if !ok {
				sidecarEvents = nil
				continue
			}
			s.handleSidecarMessage(ctx, msg)

		case ev := <-s.pmResults:
			s.handleEvent(ctx, ev)

		case <-s.speakDone:
			s.cancelSpeak = nil
			s.currentSpeakText = ""

		case <-speechOnsets:
			if s.cancelSpeak != nil {
				s.interruptedText = s.currentSpeakText
				s.cancelSpeak()
				s.cancelSpeak = nil
				s.currentSpeakText = ""
			}
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
		ctx2, cancel := context.WithCancel(ctx)
		s.cancelSpeak = cancel
		s.currentSpeakText = eff.Text
		go func() {
			_ = s.deps.Speaker.Speak(ctx2, eff.Text)
			cancel() // idempotent: safe to call even if already canceled
			select {
			case s.speakDone <- struct{}{}:
			default:
			}
		}()

	case ResetPMEffect:
		s.deps.PM.Reset()
		s.interruptedText = ""

	case CallPMIntakeEffect:
		in := eff.Input
		go func() {
			var ev Event
			res, err := s.deps.PM.Intake(ctx, in)
			if err != nil {
				ev = SidecarError{Message: fmt.Sprintf("pm intake: %v", err)}
			} else {
				ev = res
			}
			select {
			case s.pmResults <- ev:
			case <-ctx.Done():
			}
		}()

	case CallPMRouteEffect:
		in := eff.Input
		id := eff.ID
		go func() {
			var ev Event
			res, err := s.deps.PM.Route(ctx, in)
			if err != nil {
				ev = SidecarError{Message: fmt.Sprintf("pm route: %v", err)}
			} else {
				res.ID = id
				ev = res
			}
			select {
			case s.pmResults <- ev:
			case <-ctx.Done():
			}
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

- [ ] **Step 4: Run the full call test suite — expect all pass**

```bash
go test ./internal/call/... -v -race -timeout 30s
```

Expected:
```
--- PASS: TestSession_HappyPath
--- PASS: TestSession_BargeinCancelsSpeak
--- PASS: TestSession_NoInterruptedTextWithoutBargein
--- PASS: TestMachine_IdleHotkeyEmitsResetPM
--- PASS: TestMachine_IntakePassesInterruptedText
PASS
```

- [ ] **Step 5: Commit**

```bash
git add internal/call/session.go internal/call/session_test.go
git commit -m "feat(call/session): async Speak goroutine and VAD barge-in"
```

---

### Task 5: Anthropic SDK and HaikuPM Prompts

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/pm/prompts.go`

- [ ] **Step 1: Add the Anthropic SDK dependency**

```bash
cd /Users/ayusman/charlotte/freeman
go get github.com/anthropics/anthropic-sdk-go@latest
go mod tidy
```

Expected: `go.mod` now lists `github.com/anthropics/anthropic-sdk-go`.

- [ ] **Step 2: Verify it builds**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Create `internal/pm/prompts.go`**

```go
package pm

// intakeSystemPrompt instructs Haiku during intake mode.
// Haiku must call exactly one tool per turn: ask_followup or complete_objective.
const intakeSystemPrompt = `You are Freeman's requirements analyst. Your job is to turn a voice request into a precise engineering objective.

Rules:
- Ask exactly one follow-up question at a time if you need more information.
- When you have enough to write a complete spec, call complete_objective immediately.
- Treat "just go", "ship it", "start", or any clear force-start phrase as complete_objective immediately — use whatever you have.
- Classify model_hint as "opus" for cross-cutting refactors, architectural changes, or subtle multi-file reasoning; "sonnet" for everything else.
- The spoken_summary must be one sentence suitable for text-to-speech — no markdown, no lists.

Context hint: when interrupted_text is present in a user message, the user was interrupting Freeman who was in the middle of saying that text. Treat the user's utterance as a direct reply to interrupted_text.`

// routerSystemPrompt instructs Haiku during routing mode.
// Haiku must call exactly one tool per turn: answer_inline or escalate.
const routerSystemPrompt = `You are Freeman's routing assistant. A coding agent is executing a task and has asked the user a yes/no or short-answer question.

Rules:
- If you can answer the question confidently from the objective, transcript, and common sense, call answer_inline with a direct answer and your confidence (0.0-1.0).
- If you are not confident, or the question requires user judgment, call escalate with a spoken_question rephrasing the agent's question naturally for text-to-speech (one sentence, no markdown).
- Confidence below 0.8 means escalate regardless.

Context hint: when interrupted_text is present, the user was interrupting Freeman's speech. Factor that into your answer.`
```

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/pm/prompts.go
git commit -m "feat(pm): add Anthropic SDK and system prompt constants"
```

---

### Task 6: HaikuPM Client

**Files:**
- Create: `internal/pm/haiku.go`
- Create: `internal/pm/haiku_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/pm/haiku_test.go`:

```go
package pm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/pm"
)

// anthropicResp builds a minimal Anthropic Messages API response with a tool_use block.
func anthropicResp(toolName string, input any) map[string]any {
	inputJSON, _ := json.Marshal(input)
	return map[string]any{
		"id":   "msg_test",
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{
			{
				"type":  "tool_use",
				"id":    "tu_test",
				"name":  toolName,
				"input": json.RawMessage(inputJSON),
			},
		},
		"model":       "claude-haiku-4-5-20251001",
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 50, "output_tokens": 20},
	}
}

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestHaikuPM_IntakeNeedsMore(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("ask_followup", map[string]any{
			"question": "what are the constraints?",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Intake(context.Background(), call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if !res.NeedsMore {
		t.Error("NeedsMore = false, want true")
	}
	if res.Question != "what are the constraints?" {
		t.Errorf("Question = %q, want %q", res.Question, "what are the constraints?")
	}
}

func TestHaikuPM_IntakeObjective(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("complete_objective", map[string]any{
			"goal":                "add feature flag system",
			"acceptance_criteria": []string{"flag defaults off", "tests pass"},
			"constraints":         []string{"no breaking changes"},
			"notes":               []string{},
			"model_hint":          "sonnet",
			"spoken_summary":      "build a feature flag system that defaults off",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Intake(context.Background(), call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if res.NeedsMore {
		t.Error("NeedsMore = true, want false")
	}
	if res.Objective == nil {
		t.Fatal("Objective is nil")
	}
	if res.Objective.Goal != "add feature flag system" {
		t.Errorf("Goal = %q", res.Objective.Goal)
	}
	if res.Objective.ModelHint != "sonnet" {
		t.Errorf("ModelHint = %q, want sonnet", res.Objective.ModelHint)
	}
}

func TestHaikuPM_IntakeJustGo(t *testing.T) {
	// "just go" should still result in a complete_objective from Haiku.
	// (The prompt instructs this; the test just verifies the PM parses the result.)
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("complete_objective", map[string]any{
			"goal":                "build something",
			"acceptance_criteria": []string{},
			"constraints":         []string{},
			"notes":               []string{},
			"model_hint":          "sonnet",
			"spoken_summary":      "ok, starting now",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Intake(context.Background(), call.IntakeInput{Latest: "just go"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if res.NeedsMore {
		t.Error("NeedsMore = true for 'just go', want false")
	}
}

func TestHaikuPM_RouterAnswerInlineAboveThreshold(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("answer_inline", map[string]any{
			"answer":     "yes",
			"confidence": 0.95,
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Route(context.Background(), call.RouteInput{
		Question: "use existing client?",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.AnswerInline != "yes" {
		t.Errorf("AnswerInline = %q, want yes", res.AnswerInline)
	}
	if res.SpokenQuestion != "" {
		t.Errorf("SpokenQuestion = %q, want empty", res.SpokenQuestion)
	}
}

func TestHaikuPM_RouterAnswerInlineBelowThreshold(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("answer_inline", map[string]any{
			"answer":     "maybe",
			"confidence": 0.5,
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Route(context.Background(), call.RouteInput{
		Question: "use existing client?",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	// Low confidence → upgraded to escalate; spoken question = raw question.
	if res.AnswerInline != "" {
		t.Errorf("AnswerInline = %q, want empty (low confidence should escalate)", res.AnswerInline)
	}
	if res.SpokenQuestion != "use existing client?" {
		t.Errorf("SpokenQuestion = %q, want %q", res.SpokenQuestion, "use existing client?")
	}
}

func TestHaikuPM_RouterEscalate(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("escalate", map[string]any{
			"spoken_question": "should i use the existing auth client?",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	res, err := p.Route(context.Background(), call.RouteInput{
		Question: "use existing client?",
	})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.SpokenQuestion != "should i use the existing auth client?" {
		t.Errorf("SpokenQuestion = %q", res.SpokenQuestion)
	}
}

func TestHaikuPM_ResetClearsHistory(t *testing.T) {
	callCount := 0
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("ask_followup", map[string]any{
			"question": "tell me more",
		}))
	})

	p := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv.URL)

	ctx := context.Background()

	// Two intake calls build history.
	_, _ = p.Intake(ctx, call.IntakeInput{Latest: "first"})
	_, _ = p.Intake(ctx, call.IntakeInput{Latest: "second"})

	// Reset clears history.
	p.Reset()

	// After Reset, the next Intake should send only 1 message (the new user turn),
	// not the accumulated history. We verify by checking what the server receives.
	var reqBody map[string]any
	srv2 := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&reqBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(anthropicResp("ask_followup", map[string]any{
			"question": "fresh start",
		}))
	})

	p2 := pm.NewWithBaseURL(pm.Config{
		APIKey:              "test",
		Model:               "claude-haiku-4-5-20251001",
		ConfidenceThreshold: 0.8,
	}, srv2.URL)
	_, _ = p2.Intake(ctx, call.IntakeInput{Latest: "first"})
	_, _ = p2.Intake(ctx, call.IntakeInput{Latest: "second"})
	p2.Reset()
	reqBody = nil
	_, _ = p2.Intake(ctx, call.IntakeInput{Latest: "after reset"})

	if reqBody == nil {
		t.Fatal("server not called after Reset")
	}
	msgs, _ := reqBody["messages"].([]interface{})
	// After Reset, history is empty. Only the new user message is sent.
	if len(msgs) != 1 {
		t.Errorf("messages after Reset = %d, want 1 (only the new user turn)", len(msgs))
	}
}
```

- [ ] **Step 2: Run — expect compile failure**

```bash
go test ./internal/pm/... 2>&1 | head -20
```

Expected: `no required module provides package github.com/Renderix/freeman/internal/pm`.

- [ ] **Step 3: Create `internal/pm/haiku.go`**

```go
package pm

import (
	"context"
	"encoding/json"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/Renderix/freeman/internal/call"
)

// Config configures the HaikuPM client.
type Config struct {
	APIKey              string
	Model               string  // e.g. "claude-haiku-4-5-20251001"
	ConfidenceThreshold float64 // default 0.8; answer_inline below this → escalate
}

// HaikuPM implements call.PM using Claude Haiku via the Anthropic Messages API.
// Intake is multi-turn (history preserved between calls); Router is stateless.
// History is cleared by Reset().
type HaikuPM struct {
	client  *anthropic.Client
	cfg     Config
	history []anthropic.MessageParam // Intake conversation history; nil after Reset
}

// New creates a HaikuPM connected to api.anthropic.com.
func New(cfg Config) *HaikuPM {
	if cfg.ConfidenceThreshold == 0 {
		cfg.ConfidenceThreshold = 0.8
	}
	return &HaikuPM{
		client: anthropic.NewClient(option.WithAPIKey(cfg.APIKey)),
		cfg:    cfg,
	}
}

// NewWithBaseURL creates a HaikuPM pointing at a custom base URL (used in tests).
func NewWithBaseURL(cfg Config, baseURL string) *HaikuPM {
	if cfg.ConfidenceThreshold == 0 {
		cfg.ConfidenceThreshold = 0.8
	}
	return &HaikuPM{
		client: anthropic.NewClient(
			option.WithAPIKey(cfg.APIKey),
			option.WithBaseURL(baseURL),
		),
		cfg: cfg,
	}
}

// Reset clears Intake conversation history. Call at the start of each new call.
func (p *HaikuPM) Reset() {
	p.history = nil
}

// Intake implements call.PM. Appends the user turn to history, calls the API,
// and returns either NeedsMore (ask_followup) or a completed Objective.
func (p *HaikuPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error) {
	// Build the user message content.
	userText := in.Latest
	if in.InterruptedText != "" {
		userText = fmt.Sprintf("[interrupted: %q] %s", in.InterruptedText, in.Latest)
	}
	p.history = append(p.history, anthropic.NewUserMessage(anthropic.NewTextBlock(userText)))

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(p.cfg.Model)),
		MaxTokens: anthropic.F(int64(1024)),
		System: anthropic.F([]anthropic.TextBlockParam{
			anthropic.NewTextBlock(intakeSystemPrompt),
		}),
		Messages:   anthropic.F(p.history),
		Tools:      anthropic.F(intakeTools()),
		ToolChoice: anthropic.F[anthropic.ToolChoiceUnionParam](anthropic.ToolChoiceAnyParam{Type: "any"}),
	})
	if err != nil {
		return call.PMIntakeResult{}, fmt.Errorf("anthropic intake: %w", err)
	}

	// Append assistant message to history before parsing.
	p.history = append(p.history, resp.ToParam())

	// Find the tool_use block.
	for _, block := range resp.Content {
		tu, ok := block.AsUnion().(anthropic.ToolUseBlock)
		if !ok {
			continue
		}
		switch tu.Name {
		case "ask_followup":
			var args struct {
				Question string `json:"question"`
			}
			if err := json.Unmarshal([]byte(tu.Input), &args); err != nil {
				return call.PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}, nil
			}
			return call.PMIntakeResult{NeedsMore: true, Question: args.Question}, nil

		case "complete_objective":
			var args struct {
				Goal               string   `json:"goal"`
				AcceptanceCriteria []string `json:"acceptance_criteria"`
				Constraints        []string `json:"constraints"`
				Notes              []string `json:"notes"`
				ModelHint          string   `json:"model_hint"`
				SpokenSummary      string   `json:"spoken_summary"`
			}
			if err := json.Unmarshal([]byte(tu.Input), &args); err != nil {
				return call.PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}, nil
			}
			return call.PMIntakeResult{
				NeedsMore: false,
				Objective: &call.Objective{
					Goal:               args.Goal,
					AcceptanceCriteria: args.AcceptanceCriteria,
					Constraints:        args.Constraints,
					Notes:              args.Notes,
					ModelHint:          args.ModelHint,
					SpokenSummary:      args.SpokenSummary,
				},
			}, nil
		}
	}

	// No tool call found — ask for more time.
	return call.PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}, nil
}

// Route implements call.PM. Stateless one-shot call. Returns inline answer or
// escalates to spoken question. Low-confidence answers are upgraded to escalate.
func (p *HaikuPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error) {
	userText := fmt.Sprintf("Objective: %s\n\nQuestion: %s", in.Objective.Goal, in.Question)
	if in.InterruptedText != "" {
		userText += fmt.Sprintf("\n\nNote: Freeman was interrupted saying %q when the agent asked this question.", in.InterruptedText)
	}

	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(p.cfg.Model)),
		MaxTokens: anthropic.F(int64(256)),
		System: anthropic.F([]anthropic.TextBlockParam{
			anthropic.NewTextBlock(routerSystemPrompt),
		}),
		Messages:   anthropic.F([]anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(userText))}),
		Tools:      anthropic.F(routerTools()),
		ToolChoice: anthropic.F[anthropic.ToolChoiceUnionParam](anthropic.ToolChoiceAnyParam{Type: "any"}),
	})
	if err != nil {
		// On error, escalate with raw question.
		return call.PMRouteResult{SpokenQuestion: in.Question}, nil
	}

	for _, block := range resp.Content {
		tu, ok := block.AsUnion().(anthropic.ToolUseBlock)
		if !ok {
			continue
		}
		switch tu.Name {
		case "answer_inline":
			var args struct {
				Answer     string  `json:"answer"`
				Confidence float64 `json:"confidence"`
			}
			if err := json.Unmarshal([]byte(tu.Input), &args); err != nil {
				return call.PMRouteResult{SpokenQuestion: in.Question}, nil
			}
			if args.Confidence < p.cfg.ConfidenceThreshold {
				return call.PMRouteResult{SpokenQuestion: in.Question}, nil
			}
			return call.PMRouteResult{AnswerInline: args.Answer}, nil

		case "escalate":
			var args struct {
				SpokenQuestion string `json:"spoken_question"`
			}
			if err := json.Unmarshal([]byte(tu.Input), &args); err != nil {
				return call.PMRouteResult{SpokenQuestion: in.Question}, nil
			}
			return call.PMRouteResult{SpokenQuestion: args.SpokenQuestion}, nil
		}
	}

	return call.PMRouteResult{SpokenQuestion: in.Question}, nil
}

// intakeTools returns the two tools Haiku can call during intake.
func intakeTools() []anthropic.ToolParam {
	return []anthropic.ToolParam{
		{
			Name:        anthropic.F("ask_followup"),
			Description: anthropic.F("Ask the user one follow-up question to clarify their request."),
			InputSchema: anthropic.F(anthropic.ToolInputSchemaParam{
				Type: anthropic.F(anthropic.ToolInputSchemaTypeObject),
				Properties: anthropic.F(map[string]interface{}{
					"question": map[string]interface{}{
						"type":        "string",
						"description": "The follow-up question to ask the user.",
					},
				}),
				Required: anthropic.F([]string{"question"}),
			}),
		},
		{
			Name:        anthropic.F("complete_objective"),
			Description: anthropic.F("Signal that you have enough information and provide the completed engineering objective."),
			InputSchema: anthropic.F(anthropic.ToolInputSchemaParam{
				Type: anthropic.F(anthropic.ToolInputSchemaTypeObject),
				Properties: anthropic.F(map[string]interface{}{
					"goal": map[string]interface{}{
						"type":        "string",
						"description": "A concise description of what to build.",
					},
					"acceptance_criteria": map[string]interface{}{
						"type":  "array",
						"items": map[string]interface{}{"type": "string"},
						"description": "List of conditions that must be true for the task to be complete.",
					},
					"constraints": map[string]interface{}{
						"type":  "array",
						"items": map[string]interface{}{"type": "string"},
						"description": "List of constraints or limitations to respect.",
					},
					"notes": map[string]interface{}{
						"type":  "array",
						"items": map[string]interface{}{"type": "string"},
						"description": "Optional notes or context for the implementer.",
					},
					"model_hint": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"sonnet", "opus"},
						"description": "Use opus for cross-cutting refactors and architectural changes; sonnet for everything else.",
					},
					"spoken_summary": map[string]interface{}{
						"type":        "string",
						"description": "A one-sentence summary suitable for text-to-speech. No markdown.",
					},
				}),
				Required: anthropic.F([]string{"goal", "acceptance_criteria", "constraints", "notes", "model_hint", "spoken_summary"}),
			}),
		},
	}
}

// routerTools returns the two tools Haiku can call during routing.
func routerTools() []anthropic.ToolParam {
	return []anthropic.ToolParam{
		{
			Name:        anthropic.F("answer_inline"),
			Description: anthropic.F("Answer the agent's question directly without asking the user."),
			InputSchema: anthropic.F(anthropic.ToolInputSchemaParam{
				Type: anthropic.F(anthropic.ToolInputSchemaTypeObject),
				Properties: anthropic.F(map[string]interface{}{
					"answer": map[string]interface{}{
						"type":        "string",
						"description": "The direct answer to give the coding agent.",
					},
					"confidence": map[string]interface{}{
						"type":        "number",
						"description": "Confidence in the answer (0.0–1.0). Use escalate if below 0.8.",
					},
				}),
				Required: anthropic.F([]string{"answer", "confidence"}),
			}),
		},
		{
			Name:        anthropic.F("escalate"),
			Description: anthropic.F("Ask the user this question out loud because it requires their judgment."),
			InputSchema: anthropic.F(anthropic.ToolInputSchemaParam{
				Type: anthropic.F(anthropic.ToolInputSchemaTypeObject),
				Properties: anthropic.F(map[string]interface{}{
					"spoken_question": map[string]interface{}{
						"type":        "string",
						"description": "The question to speak aloud, phrased naturally for TTS. One sentence, no markdown.",
					},
				}),
				Required: anthropic.F([]string{"spoken_question"}),
			}),
		},
	}
}
```

**Note on SDK API:** If `ToolChoiceAnyParam{Type: "any"}` doesn't compile, run `go doc github.com/anthropics/anthropic-sdk-go.ToolChoiceAnyParam` to find the exact field name. Similarly for `block.AsUnion().(anthropic.ToolUseBlock)` — check `go doc anthropic.ContentBlock` if the type assertion fails.

- [ ] **Step 4: Run the tests — expect all 7 pass**

```bash
go test ./internal/pm/... -v
```

Expected:
```
--- PASS: TestHaikuPM_IntakeNeedsMore
--- PASS: TestHaikuPM_IntakeObjective
--- PASS: TestHaikuPM_IntakeJustGo
--- PASS: TestHaikuPM_RouterAnswerInlineAboveThreshold
--- PASS: TestHaikuPM_RouterAnswerInlineBelowThreshold
--- PASS: TestHaikuPM_RouterEscalate
--- PASS: TestHaikuPM_ResetClearsHistory
PASS
```

If the SDK API doesn't match exactly (tool choice or content block types), fix the compile errors by consulting `go doc` on the failing types, then re-run.

- [ ] **Step 5: Run the full test suite**

```bash
go test ./... -race -timeout 60s
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/pm/haiku.go internal/pm/haiku_test.go internal/pm/prompts.go
git commit -m "feat(pm): HaikuPM client with Anthropic tool-use"
```

---

### Task 7: Real Sidecar

**Files:**
- Modify: `sidecar/package.json`
- Create: `sidecar/sidecar.ts`

- [ ] **Step 1: Add pi-coding-agent to `sidecar/package.json`**

Replace `sidecar/package.json`:

```json
{
  "name": "freeman-sidecar",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "stub": "bun run stub.ts",
    "sidecar": "bun run sidecar.ts"
  },
  "dependencies": {
    "@mariozechner/pi-coding-agent": "latest"
  }
}
```

- [ ] **Step 2: Install dependencies**

```bash
cd /Users/ayusman/charlotte/freeman/sidecar
bun install
```

Expected: `node_modules/@mariozechner/pi-coding-agent` appears.

- [ ] **Step 3: Write the failing tests**

Create `sidecar/sidecar.test.ts`:

```typescript
import { describe, test, expect, mock, beforeEach } from "bun:test";
import { Writable, Readable, PassThrough } from "node:stream";

// ─── Mock the pi-coding-agent module ─────────────────────────────────────────
const mockAbort = mock(() => {});
let mockToolCallHandler: ((toolName: string, input: unknown) => Promise<string>) | null = null;
let mockSessionResult: string = "I edited 2 files.";
let mockSessionError: Error | null = null;

const mockCreateAgentSession = mock((opts: unknown) => ({
  run: async (prompt: string) => {
    if (mockSessionError) throw mockSessionError;
    // Call the ask_user tool if the handler is registered.
    // (Simulated by the test via mockToolCallHandler.)
    return { finalMessage: mockSessionResult };
  },
  abort: mockAbort,
  onToolCall: (handler: typeof mockToolCallHandler) => {
    mockToolCallHandler = handler;
  },
}));

mock.module("@mariozechner/pi-coding-agent", () => ({
  createAgentSession: mockCreateAgentSession,
}));

// ─── Helpers ─────────────────────────────────────────────────────────────────

function makePipes(): { stdin: PassThrough; stdout: PassThrough } {
  return { stdin: new PassThrough(), stdout: new PassThrough() };
}

function writeJSON(stream: PassThrough, obj: unknown): void {
  stream.write(JSON.stringify(obj) + "\n");
}

async function readNextJSON(stream: PassThrough): Promise<unknown> {
  return new Promise((resolve) => {
    stream.once("data", (chunk: Buffer) => {
      const line = chunk.toString().trim();
      resolve(JSON.parse(line));
    });
  });
}

const startMsg = {
  type: "start",
  objective: {
    goal: "add feature flag",
    acceptance_criteria: ["flag defaults off"],
    constraints: [],
    notes: [],
    model: "claude-sonnet-4-6",
  },
};

// ─── Tests ───────────────────────────────────────────────────────────────────

describe("sidecar", () => {
  beforeEach(() => {
    mockAbort.mockClear();
    mockCreateAgentSession.mockClear();
    mockToolCallHandler = null;
    mockSessionResult = "I edited 2 files.";
    mockSessionError = null;
  });

  test("done emits summary after session completes", async () => {
    // Import the sidecar module after mocking its dependencies.
    // The sidecar attaches to process.stdin/stdout, so we need to intercept differently.
    // For this test, we use a simpler approach: import and call the exported runSidecar.
    const { runSidecar } = await import("./sidecar.ts");

    const { stdin, stdout } = makePipes();
    const donePromise = readNextJSON(stdout);

    runSidecar(stdin, stdout);
    writeJSON(stdin, startMsg);

    const msg = await donePromise;
    expect((msg as any).type).toBe("done");
    expect(typeof (msg as any).summary).toBe("string");
    expect((msg as any).summary.length).toBeGreaterThan(0);
  });

  test("cancel calls abort and exits cleanly", async () => {
    const { runSidecar } = await import("./sidecar.ts");
    const { stdin, stdout } = makePipes();

    let exitCalled = false;
    const origExit = process.exit;
    (process as any).exit = (code: number) => { exitCalled = true; };

    runSidecar(stdin, stdout);
    writeJSON(stdin, startMsg);
    writeJSON(stdin, { type: "cancel" });

    await new Promise(r => setTimeout(r, 50));
    expect(mockAbort).toHaveBeenCalled();
    expect(exitCalled).toBe(true);

    (process as any).exit = origExit;
  });
});
```

- [ ] **Step 4: Run — expect failure (sidecar.ts doesn't exist yet)**

```bash
cd /Users/ayusman/charlotte/freeman/sidecar
bun test sidecar.test.ts 2>&1 | head -30
```

Expected: import error or missing export.

- [ ] **Step 5: Create `sidecar/sidecar.ts`**

```typescript
// Real sidecar for Freeman. Uses @mariozechner/pi-coding-agent to dispatch
// a Claude session for the given objective. Communicates with the Go parent
// via JSONL on stdin/stdout.

import * as readline from "node:readline";
import { Readable, Writable } from "node:stream";
import { createAgentSession } from "@mariozechner/pi-coding-agent";

type StartMsg = {
  type: "start";
  objective: {
    goal: string;
    acceptance_criteria: string[];
    constraints: string[];
    notes: string[];
    model: string;
  };
};
type AskUserReplyMsg = { type: "ask_user_reply"; id: string; answer: string };
type CancelMsg = { type: "cancel" };
type InMsg = StartMsg | AskUserReplyMsg | CancelMsg;

function send(out: Writable, obj: Record<string, unknown>): void {
  out.write(JSON.stringify(obj) + "\n");
}

function buildPrompt(obj: StartMsg["objective"]): string {
  const lines: string[] = [`Goal: ${obj.goal}`, ""];

  if (obj.acceptance_criteria.length > 0) {
    lines.push("Acceptance criteria:");
    for (const c of obj.acceptance_criteria) lines.push(`- ${c}`);
    lines.push("");
  }

  if (obj.constraints.length > 0) {
    lines.push("Constraints:");
    for (const c of obj.constraints) lines.push(`- ${c}`);
    lines.push("");
  }

  if (obj.notes.length > 0) {
    lines.push("Notes:");
    for (const n of obj.notes) lines.push(`- ${n}`);
    lines.push("");
  }

  return lines.join("\n").trimEnd();
}

/** runSidecar wires the JSONL protocol to a pi-coding-agent session.
 *  Exported for testing; main() calls it with process.stdin/stdout. */
export function runSidecar(inp: Readable, out: Writable): void {
  const pendingReplies = new Map<string, (answer: string) => void>();
  let currentSession: ReturnType<typeof createAgentSession> | null = null;

  const rl = readline.createInterface({ input: inp, terminal: false });

  rl.on("line", (raw: string) => {
    let msg: InMsg;
    try {
      msg = JSON.parse(raw) as InMsg;
    } catch {
      send(out, { type: "error", message: `bad json: ${raw}` });
      return;
    }

    if (msg.type === "start") {
      void runSession(msg);
    } else if (msg.type === "ask_user_reply") {
      const resolve = pendingReplies.get(msg.id);
      if (resolve) {
        pendingReplies.delete(msg.id);
        resolve(msg.answer);
      }
    } else if (msg.type === "cancel") {
      if (currentSession) currentSession.abort();
      process.exit(0);
    }
  });

  async function runSession(msg: StartMsg): Promise<void> {
    // Custom ask_user tool — registered before session starts.
    const askUserTool = {
      name: "ask_user",
      description: "Ask the user a question and wait for their spoken reply.",
      input_schema: {
        type: "object" as const,
        properties: {
          question: { type: "string", description: "The question to ask." },
        },
        required: ["question"],
      },
      run: async (input: { question: string }): Promise<string> => {
        const id = crypto.randomUUID();
        const answer = await new Promise<string>((resolve) => {
          pendingReplies.set(id, resolve);
          send(out, { type: "ask_user", id, question: input.question });
        });
        return answer;
      },
    };

    const session = createAgentSession({
      model: msg.objective.model,
      workingDir: process.cwd(),
      tools: [askUserTool],
      auth: "subscription",
    });
    currentSession = session;

    const prompt = buildPrompt(msg.objective);

    try {
      const result = await session.run(prompt);
      // Extract a one-sentence summary from the final assistant message.
      const raw: string =
        typeof result?.finalMessage === "string"
          ? result.finalMessage
          : JSON.stringify(result ?? "done");
      const summary = raw.split(/[.!?]/)[0]?.trim() || "done";
      send(out, { type: "done", summary });
      process.exit(0);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      send(out, { type: "error", message });
      process.exit(1);
    }
  }
}

// Entry point when run directly.
if (import.meta.main) {
  runSidecar(process.stdin, process.stdout);
}
```

- [ ] **Step 6: Run sidecar tests**

```bash
cd /Users/ayusman/charlotte/freeman/sidecar
bun test sidecar.test.ts
```

Expected: tests pass. If the `pi-coding-agent` API doesn't match the mock (e.g., `session.run()` returns a different shape), adjust the `result.finalMessage` extraction in `runSession` accordingly.

- [ ] **Step 7: Commit**

```bash
cd /Users/ayusman/charlotte/freeman
git add sidecar/package.json sidecar/sidecar.ts sidecar/sidecar.test.ts sidecar/bun.lockb
git commit -m "feat(sidecar): real pi-coding-agent session with ask_user tool"
```

---

### Task 8: Wire It All Together

**Files:**
- Modify: `cmd/freeman/call.go`

- [ ] **Step 1: Write a compile-only smoke test (no new test file needed)**

Verify the existing fake-audio integration test still passes before wiring:

```bash
go test ./... -race -timeout 60s
```

Expected: all pass (uses fakes, no real API calls).

- [ ] **Step 2: Update `runCallWithRealAudio` in `cmd/freeman/call.go`**

Three changes: replace ScriptedPM with HaikuPM, pass SpeechOnsets, update sidecar path.

Replace the relevant imports at the top of `cmd/freeman/call.go` — add the pm import:

```go
import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "os/signal"
    "path/filepath"
    "syscall"
    "time"

    "github.com/Renderix/freeman/internal/audio"
    "github.com/Renderix/freeman/internal/audio/capture"
    "github.com/Renderix/freeman/internal/audio/hotkey"
    "github.com/Renderix/freeman/internal/audio/playback"
    "github.com/Renderix/freeman/internal/audio/stt"
    "github.com/Renderix/freeman/internal/audio/vad"
    "github.com/Renderix/freeman/internal/call"
    "github.com/Renderix/freeman/internal/call/fakes"
    "github.com/Renderix/freeman/internal/config"
    "github.com/Renderix/freeman/internal/engine"
    "github.com/Renderix/freeman/internal/pm"
    "github.com/Renderix/freeman/internal/sidecar"
    "github.com/spf13/cobra"
)
```

Replace the bottom of `runCallWithRealAudio` — starting from `// 9. Stub sidecar (unchanged).` through the end of the function:

```go
    // 9. Real sidecar (pi-coding-agent).
    repoRoot, err := findRepoRoot()
    if err != nil {
        return err
    }
    sidecarPath := filepath.Join(repoRoot, "sidecar", "sidecar.ts")
    sc, err := sidecar.Spawn(ctx, "bun", "run", sidecarPath)
    if err != nil {
        return fmt.Errorf("spawn sidecar: %w", err)
    }
    defer sc.Close()

    fmt.Fprintln(os.Stderr, "freeman: ready")

    // 10. Haiku PM.
    apiKey := os.Getenv(conf.Freeman.PM.APIKeyEnv)
    haiku := pm.New(pm.Config{
        APIKey:              apiKey,
        Model:               conf.Freeman.PM.Model,
        ConfidenceThreshold: conf.Freeman.PM.ConfidenceThreshold,
    })

    // 11. Session with speech onsets for barge-in.
    session := call.NewSession(call.SessionDeps{
        Transcriber:  tr,
        Speaker:      sp,
        PM:           haiku,
        Hotkey:       hk,
        Sidecar:      sc,
        SpeechOnsets: v.SpeechOnsets(),
    })
    return session.Run(ctx)
```

**Note:** The variable `v` is the `*vad.VAD` declared in step 5 of `runCallWithRealAudio`. It already exists in the function.

- [ ] **Step 3: Build — verify no compile errors**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Run the full test suite one final time**

```bash
go test ./... -race -timeout 60s
```

Expected: all pass. The `--fake-audio` path is exercised by `TestSession_HappyPath` and uses `stub.ts`, which is unchanged.

- [ ] **Step 5: Commit**

```bash
git add cmd/freeman/call.go
git commit -m "feat(cmd): wire HaikuPM, SpeechOnsets, and real sidecar in runCallWithRealAudio"
```

- [ ] **Step 6: Push to GitHub**

```bash
git push
```

---

## Post-implementation manual smoke test (optional, requires `ANTHROPIC_API_KEY`)

```bash
export ANTHROPIC_API_KEY=sk-ant-...
./freeman call
```

Press Enter to start a call, speak a request. Verify:
1. Freeman greets and asks follow-up questions.
2. Speaking mid-greeting cancels TTS and Freeman incorporates your words.
3. After confirmation Freeman starts the sidecar and routes inline questions.
4. `--fake-audio` mode still works: `./freeman call --fake-audio` with SIGUSR1 hotkey.

---

## Spec Coverage Check

| Spec section | Task |
|---|---|
| VAD speech onset channel | Task 1 |
| `UserUtterance.InterruptedText` | Task 2 |
| `IntakeInput.InterruptedText`, `RouteInput.InterruptedText` | Task 2 |
| `ResetPMEffect` | Task 2 |
| `PM.Reset()` interface + fakes | Task 2 |
| `SessionDeps.SpeechOnsets` | Task 2 |
| `handleIdle` prepends `ResetPMEffect` | Task 3 |
| `handleIntake` passes `InterruptedText` | Task 3 |
| `handleAwaitingConfirm` passes `InterruptedText` | Task 3 |
| Session async Speak goroutine + cancelation | Task 4 |
| Barge-in case (`speakDone`, `speechOnsets`) | Task 4 |
| `ResetPMEffect` case in `runEffect` | Task 4 |
| Anthropic SDK dependency | Task 5 |
| Intake + router system prompts | Task 5 |
| `HaikuPM` struct and `New` / `NewWithBaseURL` | Task 6 |
| `Intake`: multi-turn history, tool parsing, interrupted_text | Task 6 |
| `Route`: stateless, confidence threshold, escalate upgrade | Task 6 |
| 7 PM tests against httptest server | Task 6 |
| `sidecar/sidecar.ts` with `createAgentSession` | Task 7 |
| `ask_user` tool with promise correlation | Task 7 |
| `cancel` → `session.abort()` | Task 7 |
| `done` summary emission | Task 7 |
| Wire HaikuPM in `runCallWithRealAudio` | Task 8 |
| Wire `SpeechOnsets` in `SessionDeps` | Task 8 |
| Change sidecar path to `sidecar.ts` | Task 8 |
