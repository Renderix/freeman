# Freeman — Plan 3 (Intelligence) Design

**Status:** Design
**Date:** 2026-04-15
**Builds on:** Plan 2 (Audio I/O) — `docs/superpowers/specs/2026-04-15-freeman-voice-companion-design.md`

## Goal

Replace the Plan 1/2 stubs with real intelligence: a Haiku PM driving intake and routing, a real `pi-coding-agent` sidecar dispatching to Sonnet/Opus, and VAD-triggered barge-in so the user can interrupt Freeman mid-speech. The result is a fully working voice-to-code pipeline — speak a goal, hear Freeman confirm it, hear the worker execute it.

## What changes

| Component | Plan 2 | Plan 3 |
|---|---|---|
| PM | `fakes.ScriptedPM` | `pm.HaikuPM` (Anthropic API, Haiku 4.5) |
| Sidecar | `sidecar/stub.ts` | `sidecar/sidecar.ts` (pi-coding-agent SDK) |
| Speak | Synchronous, blocks event loop | Goroutine, cancellable via context |
| Barge-in | None | VAD speech onset → cancel TTS, pass interrupted text to PM |

The `--fake-audio` path (Plan 1 fakes + `stub.ts`) is unchanged.

---

## Architecture changes

### 1. VAD speech onset (`internal/audio/vad/vad.go`)

The VAD `Run` goroutine already tracks `stateSilent` / `stateSpeech`. Add a buffered onset channel (capacity 1) and a getter:

```go
type VAD struct {
    // existing fields …
    onsets chan struct{}
}

func (v *VAD) SpeechOnsets() <-chan struct{} { return v.onsets }
```

In the `Run` goroutine, on every `stateSilent → stateSpeech` transition, do a non-blocking send:

```go
select { case v.onsets <- struct{}{}: default: }
```

This fires regardless of whether the Transcriber is muted. Capacity-1 buffer: if the session hasn't drained the previous onset yet, the new one is dropped rather than blocking the VAD goroutine.

---

### 2. Barge-in interrupted text

When the user barges in while Freeman is speaking, Haiku needs to know what Freeman was saying. Without that, a reply like "use the existing one" has no referent.

**`UserUtterance` event** gains one field:

```go
type UserUtterance struct {
    Text            string
    InterruptedText string // non-empty when user barged in mid-speech
}
```

**`IntakeInput`** gains a matching field:

```go
type IntakeInput struct {
    Transcript      []string
    Latest          string
    InterruptedText string // what Freeman was saying when interrupted; empty if no barge-in
}
```

The machine passes `ev.InterruptedText` straight through to `IntakeInput` and `RouteInput` (RouteInput gains the same field). The HaikuPM system prompt instructs Haiku: when `interrupted_text` is present, treat the user's utterance as a direct reply to the interrupted speech.

---

### 3. PM reset on new call

The HaikuPM is stateful during Intake (it holds conversation history). When a new call starts (hotkey in Idle), that history must be cleared.

**`call.PM` interface** gains one method:

```go
type PM interface {
    Intake(ctx context.Context, in IntakeInput) (PMIntakeResult, error)
    Route(ctx context.Context, in RouteInput) (PMRouteResult, error)
    Reset()   // called at the start of every new call; clears conversation history
}
```

**`effects.go`** gains:

```go
type ResetPMEffect struct{}
func (ResetPMEffect) isEffect() {}
```

**`machine.go`** `handleIdle` prepends `ResetPMEffect{}` to the returned effects:

```go
return []Effect{ResetPMEffect{}, SpeakEffect{Text: "hi. what are we building?"}}
```

`ScriptedPM.Reset()` resets `intakeCnt` to 0. `HaikuPM.Reset()` clears `history`.

---

### 4. Session async Speak + barge-in (`internal/call/session.go`)

**`SessionDeps`** gains one field:

```go
type SessionDeps struct {
    Transcriber  Transcriber
    Speaker      Speaker
    PM           PM
    Hotkey       Hotkey
    Sidecar      *sidecar.Client
    SpeechOnsets <-chan struct{} // from vad.VAD.SpeechOnsets()
}
```

**`Session`** gains three fields:

```go
type Session struct {
    // existing …
    cancelSpeak      func()        // nil when no TTS in flight
    currentSpeakText string        // text being spoken; stored for interrupted-text context
    speakDone        chan struct{}  // goroutine signals Speak completion
    interruptedText  string        // set on barge-in; attached to next UserUtterance
}
```

**`runEffect`** for `SpeakEffect`:

```go
case SpeakEffect:
    ctx2, cancel := context.WithCancel(ctx)
    s.cancelSpeak = cancel
    s.currentSpeakText = eff.Text
    go func() {
        _ = s.deps.Speaker.Speak(ctx2, eff.Text)
        cancel()
        select { case s.speakDone <- struct{}{}: default: }
    }()
```

`speakDone` is initialized as `make(chan struct{}, 1)`.

**`Run` select loop** gains two new cases after the existing ones:

```go
case <-s.speakDone:
    s.cancelSpeak = nil
    s.currentSpeakText = ""

case <-s.deps.SpeechOnsets:
    if s.cancelSpeak != nil {
        s.interruptedText = s.currentSpeakText
        s.cancelSpeak()
        s.cancelSpeak = nil
        s.currentSpeakText = ""
    }
```

**Utterance dispatch** — when the session receives a user utterance from the Transcriber, it attaches the pending interrupted text:

```go
case text, ok := <-utterances:
    if !ok { utterances = nil; continue }
    ev := UserUtterance{Text: text, InterruptedText: s.interruptedText}
    s.interruptedText = ""
    s.handleEvent(ctx, ev)
```

`cancelSpeak`, `currentSpeakText`, `interruptedText`, and `speakDone` are only read/written from the event loop goroutine — no mutex needed.

**`runEffect`** for `ResetPMEffect`:

```go
case ResetPMEffect:
    s.deps.PM.Reset()
    s.interruptedText = ""
```

---

### 5. Haiku PM client (`internal/pm/`)

**Files:**
- `internal/pm/haiku.go` — `HaikuPM` struct implementing `call.PM`
- `internal/pm/prompts.go` — system prompt constants
- `internal/pm/haiku_test.go` — tests against a fake HTTP server

```go
type Config struct {
    APIKey              string
    Model               string  // "claude-haiku-4-5-20251001"
    ConfidenceThreshold float64 // default 0.8; escalate if confidence below this
}

type HaikuPM struct {
    client  *anthropic.Client
    cfg     Config
    history []anthropic.MessageParam // Intake conversation history; cleared by Reset()
}

func New(cfg Config) *HaikuPM
func (p *HaikuPM) Reset()
func (p *HaikuPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error)
func (p *HaikuPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error)
```

**Intake mode** — multi-turn conversation using tool_use to force structured output. Haiku may call exactly one of two tools per turn:

`ask_followup` — Haiku needs more information:
```json
{"question": "string"}
```

`complete_objective` — Haiku has enough to build the full spec:
```json
{
  "goal": "string",
  "acceptance_criteria": ["string"],
  "constraints": ["string"],
  "notes": ["string"],
  "model_hint": "sonnet|opus",
  "spoken_summary": "string"
}
```

System prompt instructs Haiku to:
- Ask one question at a time
- When `interrupted_text` is present in the user message, treat the user's reply as a direct answer to that interrupted speech
- Treat "just go", "ship it", or any clear force-start phrase as `complete_objective` immediately with whatever it has
- Classify `model_hint` as `opus` for tasks involving cross-cutting refactors, architectural changes, or subtle multi-file reasoning; `sonnet` for everything else

Each `Intake` call appends a `user` message (built from `IntakeInput.Latest` and optionally `InterruptedText`) to `history`, calls the API, appends the `assistant` response to `history`, and returns the parsed tool call.

`Reset()` sets `history = nil`.

**Router mode** — stateless, one-shot call. System prompt plus a single `user` message containing the objective, the transcript, and Claude's question. Haiku calls one of two tools:

`answer_inline`:
```json
{"answer": "string", "confidence": 0.0}
```

`escalate`:
```json
{"spoken_question": "string"}
```

If Haiku returns `answer_inline` with `confidence < cfg.ConfidenceThreshold`, the PM upgrades it to `PMRouteResult{SpokenQuestion: in.Question}` (escalate with the raw question as the spoken form). This ensures Claude never goes down a wrong path due to low-confidence inline answers.

---

### 6. Real sidecar (`sidecar/sidecar.ts`)

Replaces `stub.ts` with a real `@mariozechner/pi-coding-agent` session. The JSONL protocol to/from the Go parent is unchanged.

**Startup:** spawned by `runCallWithRealAudio` pointing at `sidecar/sidecar.ts` instead of `stub.ts`. `stub.ts` is kept — `runCallWithFakes` still uses it.

**On `start` message:**

```typescript
const session = createAgentSession({
    model: msg.objective.model,   // "claude-sonnet-4-6" or "claude-opus-4-6"
    workingDir: process.cwd(),
    tools: [askUserTool],
    auth: "subscription",         // uses local claude CLI credentials
});

const prompt = buildPrompt(msg.objective); // goal + criteria + constraints + notes
await session.run(prompt);
```

**`ask_user` tool** — custom tool registered in the pi session. When Claude calls it:
1. Generate correlation `id = crypto.randomUUID()`
2. Write `{type:"ask_user", id, question}` to stdout
3. Await `pendingReplies.get(id)` — a `Promise<string>` set up before the tool returns
4. Return the resolved answer as the tool result

On `ask_user_reply` from stdin, resolve the matching promise.

**Completion:** after `session.run()` resolves, extract a one-sentence summary from the final assistant message and emit `{type:"done", summary}`. Then `process.exit(0)`.

**On `cancel`:** call `session.abort()` and `process.exit(0)`.

**On `error` from the session:** catch the exception, emit `{type:"error", message: err.message}`, and `process.exit(1)`.

**Prompt builder** — constructs the prompt string passed to the pi session:

```
Goal: <goal>

Acceptance criteria:
- <criterion>
...

Constraints:
- <constraint>
...

Notes:
- <note>
...
```

---

### 7. Wiring (`cmd/freeman/call.go`)

In `runCallWithRealAudio`, three changes:

**Replace ScriptedPM:**
```go
haiku := pm.New(pm.Config{
    APIKey:              os.Getenv("ANTHROPIC_API_KEY"),
    Model:               conf.Freeman.PM.Model,
    ConfidenceThreshold: conf.Freeman.PM.ConfidenceThreshold,
})
```

**Pass speech onsets to SessionDeps:**
```go
session := call.NewSession(call.SessionDeps{
    Transcriber:  tr,
    Speaker:      sp,
    PM:           haiku,
    Hotkey:       hk,
    Sidecar:      sc,
    SpeechOnsets: v.SpeechOnsets(),  // new
})
```

**Change sidecar path** from `stub.ts` to `sidecar.ts`:
```go
stubPath := filepath.Join(repoRoot, "sidecar", "sidecar.ts")
```

---

## Data flow — barge-in example

1. Session is in `StateIntake`. Machine emitted `SpeakEffect{Text: "should the flag default on or off?"}`.
2. Session launches Speak goroutine; stores `cancelSpeak`, `currentSpeakText = "should the flag default on or off?"`.
3. User starts speaking mid-sentence. VAD detects speech onset → sends on `v.onsets`.
4. Session event loop receives from `SpeechOnsets`: calls `cancelSpeak()`, stores `interruptedText = "should the flag default on or off?"`, clears `cancelSpeak` and `currentSpeakText`. TTS stops.
5. Speak goroutine unblocks (context cancelled), sends to `speakDone`.
6. Session event loop drains `speakDone` (no-op: `cancelSpeak` already nil).
7. VAD accumulates the user's speech, emits `Utterance` after silence.
8. `stt.Transcriber` produces `"off"` on `Utterances()`.
9. Session constructs `UserUtterance{Text: "off", InterruptedText: "should the flag default on or off?"}`, clears `interruptedText`.
10. Machine handles `UserUtterance` → emits `CallPMIntakeEffect`.
11. Haiku receives `IntakeInput{Latest: "off", InterruptedText: "should the flag default on or off?"}` — understands this is a direct reply to its interrupted question.

---

## Error handling

| Failure | Behavior |
|---|---|
| `ANTHROPIC_API_KEY` missing or invalid | `pm.New` does not fail at construction; first `Intake` call returns an error. Session hears `SidecarError` → speaks "brain is having trouble, try again." stays in Intake. |
| Haiku API error during routing | `Route` returns error; session treats it as escalate (spoken question = Claude's raw question). |
| Haiku returns no tool call | `Intake` returns `PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}` and retries on the next utterance. |
| `confidence < threshold` on `answer_inline` | PM upgrades to escalate; spoken question = Claude's raw question. |
| pi-coding-agent session crashes | Sidecar emits `{type:"error"}`. Session speaks the error, returns to Idle. |
| Barge-in while no TTS in flight | `speechOnsets` event arrives; `cancelSpeak` is nil; no-op. Next utterance has empty `InterruptedText`. |
| Multiple rapid onsets | Channel cap-1 + non-blocking send: at most one onset queued; extras dropped. |

---

## Testing

### `internal/pm/haiku_test.go`

Uses `httptest.NewServer` standing in for `api.anthropic.com`. Four test cases:

1. **Intake → NeedsMore**: server returns tool call `ask_followup{question: "..."}`. Assert `PMIntakeResult{NeedsMore: true}`.
2. **Intake → Objective**: server returns tool call `complete_objective{...}`. Assert full `Objective` parsed correctly.
3. **Intake → "just go"**: `IntakeInput.Latest = "just go"`. Server returns `complete_objective` with partial fields. Assert result has `NeedsMore: false`.
4. **Router → answer_inline above threshold**: server returns `answer_inline{answer: "yes", confidence: 0.9}`. Assert `PMRouteResult{AnswerInline: "yes"}`.
5. **Router → answer_inline below threshold**: server returns `answer_inline{answer: "yes", confidence: 0.5}`. Assert upgraded to `PMRouteResult{SpokenQuestion: <raw question>}`.
6. **Router → escalate**: server returns `escalate{spoken_question: "..."}`. Assert `PMRouteResult{SpokenQuestion: "..."}`.
7. **Reset clears history**: call Intake twice, then Reset, then Intake again — assert history length is 1 (only the post-reset turn).

### `internal/audio/vad/vad_test.go`

Add one test: feed speech frames followed by silence to VAD. Assert `SpeechOnsets()` channel fires once on speech start, before the `Utterance` channel fires.

### `internal/call/session_test.go`

Add two tests:

1. **Barge-in cancels Speak and records interrupted text**: inject a `SpeakEffect`, then immediately send on a fake `SpeechOnsets` channel. Assert Speak is cancelled (speaker fake returns quickly). Then inject a `UserUtterance` — assert `InterruptedText` is populated. Assert `interruptedText` is cleared after the utterance.
2. **No interrupted text without barge-in**: `SpeakEffect` completes normally. Next `UserUtterance` has empty `InterruptedText`.

### `sidecar/sidecar.test.ts`

Mock `createAgentSession`. Three tests:

1. **ask_user round-trip**: mock session calls the `ask_user` tool; sidecar emits `ask_user` to stdout; test feeds `ask_user_reply` to stdin; assert tool receives the answer and session continues.
2. **cancel aborts**: send `cancel` on stdin; assert `session.abort()` called and process exits 0.
3. **done emits summary**: session resolves; assert `done` written to stdout with non-empty summary.

### No new E2E test

The `--fake-audio` path (Plan 1 fakes + `stub.ts`) continues to cover session integration in CI. A real end-to-end test against Claude is tracked in Plan 4 under `FREEMAN_E2E=1`.

---

## Configuration (additions to `config.yaml`)

No new YAML fields needed — `freeman.pm.model`, `freeman.pm.confidence_threshold`, `freeman.pm.api_key_env`, `freeman.worker.default_model`, and `freeman.worker.opus_model` are already in the schema from Plan 1. Wire `ANTHROPIC_API_KEY` (or whatever `api_key_env` names) at startup in `runCallWithRealAudio`.

---

## File map

| File | Change |
|---|---|
| `internal/audio/vad/vad.go` | Add `onsets chan struct{}`, `SpeechOnsets()`, onset send in Run goroutine |
| `internal/audio/vad/vad_test.go` | Add onset timing test |
| `internal/call/events.go` | `UserUtterance` gains `InterruptedText string` |
| `internal/call/types.go` | `IntakeInput` and `RouteInput` gain `InterruptedText string` |
| `internal/call/effects.go` | Add `ResetPMEffect` |
| `internal/call/ports.go` | `PM` gains `Reset()`; `SessionDeps` gains `SpeechOnsets` |
| `internal/call/machine.go` | `handleIdle` prepends `ResetPMEffect`; pass `InterruptedText` through |
| `internal/call/session.go` | Async Speak goroutine; barge-in select cases; interrupted text tracking |
| `internal/call/session_test.go` | Barge-in tests |
| `internal/call/fakes/fakes.go` | `ScriptedPM.Reset()` resets `intakeCnt` |
| `internal/pm/haiku.go` | New: HaikuPM implementing call.PM |
| `internal/pm/prompts.go` | New: system prompt constants |
| `internal/pm/haiku_test.go` | New: 7 tests against httptest server |
| `sidecar/sidecar.ts` | New: real pi-coding-agent sidecar |
| `cmd/freeman/call.go` | Wire HaikuPM, SpeechOnsets, sidecar.ts in runCallWithRealAudio |
