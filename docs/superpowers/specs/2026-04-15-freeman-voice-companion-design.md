# Freeman — Voice Companion to Claude Code

**Status:** Design
**Date:** 2026-04-15
**Authors:** Ayusman + Claude (brainstorming session)

## Purpose

Freeman is a CLI voice companion that lets you build software by talking. You run `freeman call` in a project directory, press a hotkey to open a phone-call-style session, and have a natural spoken conversation with an intake agent that builds a structured task spec from your words. Once the spec is confirmed, Freeman dispatches it to Claude (Sonnet or Opus) running as a real coding agent. While Claude works, Freeman intercepts its questions: simple ones are auto-answered from the spec and context; real judgment calls are spoken aloud to you and your voice reply is piped back.

The shape is a two-tier agent system:

- **Freeman PM (Claude Haiku 4.5)** — the conversational project manager that lives between you and the worker. Handles intake, builds the objective, and routes Claude's mid-task questions.
- **Freeman Worker (Claude Sonnet 4.6 or Opus 4.6 via `pi-coding-agent`)** — the actual coding agent that reads, writes, edits, and runs bash in your repo.

The PM-worker split is the core insight: a fast, cheap, chatty Haiku sits next to the mic insulating you from low-level questions, while a heavier Sonnet/Opus handles the real code work without burning your attention.

## Non-goals (MVP)

- Menubar or daemon mode (CLI-in-directory only).
- Session memory across calls (every call starts fresh).
- Mid-session model escalation (model is fixed at dispatch time).
- Voice cloning or alternative TTS engines (Kokoro only).
- Custom voice commands beyond opening/closing the call, confirming dispatch, and force-starting.
- Multi-user or shared sessions.
- Non-macOS platforms.
- Local LLM fallback for the PM (Haiku only; local Gemma 4 or Qwen is a future path).

## User experience — one complete call

```
$ cd ~/myproject
$ freeman call
[freeman ready. press ⌥-space to start]

[user presses ⌥-space]
freeman: "Hi. What are we building?"
user: "I want to add a feature flag for the new checkout flow."
freeman: "Got it. Should the flag default on or off?"
user: "Off. And roll it out to ten percent of users."
freeman: "Any constraints I should know about?"
user: "No database changes. Tests need to pass in CI."
freeman: "OK, here's what I heard: add a feature flag for the new
          checkout flow, default off, ten percent rollout, no DB
          changes, tests passing. Should I start?"
user: "Yes."
freeman: "Starting now. I'll let you know if I need you."

[claude works for ~2 minutes via pi-coding-agent]

freeman: "Claude wants to know whether to use the existing
          LaunchDarkly client or add a new one. Which way?"
user: "Use the existing one."
freeman: "Got it."

[claude finishes]

freeman: "Done. I edited three files and ran the tests. They pass.
          Anything else?"
user: [presses ⌥-space again to hang up]
```

Key UX properties:

- **Phone-call metaphor.** Opening the call is one keypress; inside the call, turn-taking is automatic via VAD. You don't tap to speak once you're in.
- **Hard barge-in.** If Freeman is speaking and you start talking, Freeman's TTS cuts off immediately and a new user turn begins.
- **Propose-and-confirm dispatch.** Haiku always reads back the structured objective before dispatching. You can force-skip with the keyword "just go" if Haiku is over-asking.
- **Escalation only when needed.** Most of Claude's tool calls never reach you. You only hear a question when Haiku judges it a real judgment call.
- **Narration is throttled.** Freeman doesn't read every Claude message aloud. It announces major milestones ("starting," "done," optional high-level progress) and otherwise stays quiet.

## Architecture

```
┌────────────────────── Freeman (Go binary) ────────────────────────┐
│                                                                    │
│  ┌───────────┐     ┌────────────┐     ┌────────────┐              │
│  │   Input   │────▶│   Session  │────▶│   Output   │              │
│  │whisper.cpp│     │   state    │     │   Kokoro   │              │
│  │   VAD     │     │  machine   │     │    TTS     │              │
│  └─────▲─────┘     └──────┬─────┘     └────────────┘              │
│        │                  │                                        │
│        │           ┌──────┴─────┐                                   │
│        │           │  PM Brain  │ ─── Anthropic API ──▶ Haiku 4.5 │
│        │           │   (Haiku)  │     (intake + router)            │
│        │           └──────┬─────┘                                   │
│        │                  │                                        │
│        │                  │ stdin/stdout JSONL                     │
│        │                  ▼                                        │
│        │        ┌───────────────────┐                               │
│        │        │  Sidecar (TSX)    │                               │
│        │        │  Node + pi-coding │                               │
│        │        │    -agent SDK     │                               │
│        │        │  + ask_user tool  │                               │
│        │        └─────────┬─────────┘                               │
│        │                  │ Claude Pro/Max auth                    │
│        │                  ▼                                        │
│        │             Claude Sonnet 4.6 / Opus 4.6                   │
│        │             (read/write/edit/bash tools)                   │
└────────┴───────────────────────────────────────────────────────────┘
```

## Components

### 1. Input (Go)
- Captures mic via CoreAudio / PortAudio binding.
- Streams 16 kHz mono PCM frames to whisper.cpp (CGO or subprocess against a running `whisper-server`).
- Runs voice activity detection for endpointing (silence threshold + hangover).
- Emits events to the session: `UserSpeechStart`, `UserTranscriptPartial{text}`, `UserTranscriptFinal{text}`.
- During TTS playback, Input still runs — a detected `UserSpeechStart` triggers hard barge-in.

### 2. Session state machine (Go)
Single source of truth for the call. States:

```
Idle ──hotkey──▶ Intake ──confirm──▶ Dispatching ──▶ Working
                   ▲                                    │
                   └──────barge-in/denial───────────────┘
                                                       │
                                                  ask_user
                                                       ▼
                                                  Escalating
                                                       │
                                                   user reply
                                                       ▼
                                                    Working
                                                       │
                                                    done/err
                                                       ▼
                                                  Reporting ──▶ Idle
```

- `Idle`: mic closed, sidecar not running, waiting for hotkey.
- `Intake`: PM (Haiku) is conversing with user, building the objective. State holds running transcript + partial objective.
- `Dispatching`: PM has emitted a complete objective; user confirmed (or force-started). Sidecar is being spawned, `session.prompt(objective)` is about to be called.
- `Working`: sidecar is running Claude. Freeman is mostly quiet. Listens for `ask_user` events from sidecar and barge-in from user.
- `Escalating`: `ask_user` routed as "escalate". Freeman is speaking the question and waiting for user voice reply.
- `Reporting`: sidecar has emitted `done`. Freeman speaks a summary. Returns to Idle when done speaking (or on user barge-in to start a new turn).

All transitions are logged to the call transcript (see Testing).

### 3. PM Brain (Haiku client, Go)
Thin wrapper around the Anthropic API with two modes:

**Intake mode.** Maintains a multi-turn conversation with structured-output forcing. The system prompt instructs Haiku to:
- Chat naturally, one question at a time.
- Build an internal `Objective` schema incrementally.
- Emit the completed `Objective` along with a human-readable summary when it believes it has enough.
- Classify `model_hint` as `"sonnet"` or `"opus"` based on apparent task complexity (subtle cross-cutting refactor → opus; isolated bug fix or CRUD → sonnet).
- Respect a force-start keyword ("just go") by emitting the objective immediately with whatever it has.

```ts
type Objective = {
  goal: string;                   // one-sentence goal
  acceptance_criteria: string[];  // bullets, concrete
  constraints: string[];          // e.g. "no DB changes", "tests must pass"
  notes: string[];                // anything else worth passing along
  model_hint: "sonnet" | "opus";
  spoken_summary: string;         // what to read back to user
};
```

**Router mode.** Called once per `ask_user` tool call from the sidecar. Inputs: full call transcript + objective + Claude's question. Output:

```ts
type RouteDecision =
  | { action: "answer_inline", answer: string, confidence: number }
  | { action: "escalate", spoken_question: string };
```

The router system prompt instructs Haiku to lean toward escalation when genuinely uncertain — Freeman's UX suffers much more from Claude going down a wrong path than from an extra spoken question. A `confidence` threshold (configurable, default 0.8) on `answer_inline` forces escalation below the bar even if Haiku picks `answer_inline`.

### 4. Sidecar (TSX)
Small Node.js process spawned by Freeman using `createAgentSession` from `@mariozechner/pi-coding-agent`. Responsibilities:

- Read JSONL commands from stdin: `start`, `ask_user_reply`, `cancel`.
- Write JSONL events to stdout: `assistant_text`, `tool_call`, `ask_user`, `done`, `error`.
- Register a custom `ask_user` tool in the pi session that:
  1. Writes `{type: "ask_user", question}` to stdout.
  2. Awaits a matching `{type: "ask_user_reply", answer}` on stdin (correlation via an id).
  3. Returns the answer as the tool result.
- Pass the chosen model (`sonnet` or `opus`) through to the pi session config.
- Use Claude Pro/Max subscription auth via pi (no separate API key for the worker).
- On `cancel`, call `session.abort()` and exit cleanly.

The TSX source lives at `sidecar/sidecar.ts` in the Freeman repo. Freeman builds/runs it via `node` (or a bundled executable) depending on packaging.

### 5. Output (Kokoro, existing)
The current Freeman TTS pipeline stays. It gains one new capability:

- **Cancelable playback.** A `CancelSpeak()` method the session state machine calls on barge-in. It interrupts the current utterance cleanly (drains the audio buffer, stops the ONNX worker, returns).

No other changes to Kokoro. The session treats it as `Speak(text) → WAV frames out` with a cancel signal.

### 6. Hotkey daemon (macOS)
A minimal in-process hotkey listener (via `CGEventTap` or an existing Go binding) for ⌥-space. The CLI invocation `freeman call` stays running in the foreground with a blocking event loop; the hotkey listener is a goroutine that posts events to the session state machine.

## Data flow — one complete call

1. User runs `freeman call` in project dir. Process starts, hotkey listener armed, Idle.
2. User presses ⌥-space. Session → Intake. Input opens mic. Freeman speaks a greeting via Kokoro.
3. User speaks. whisper.cpp emits `UserTranscriptFinal`. Session forwards to PM Intake mode.
4. PM responds with either a follow-up question (spoken via Kokoro) or a completed Objective + spoken summary.
5. PM reads back the summary and asks "Should I start?" Session waits for a yes/no.
   - **Yes / "just go"**: Session → Dispatching. Sidecar is spawned with the chosen model. `session.prompt(objective.goal + acceptance criteria + constraints + notes)` called.
   - **No / additions**: Session stays in Intake, PM continues the conversation.
6. Session → Working. Sidecar starts streaming events.
7. When Claude needs input, it calls `ask_user("..."))`. Sidecar writes `{type:"ask_user", id, question}` to stdout. Session reads it, calls PM Router mode with the full context.
   - **answer_inline**: Session writes `{type:"ask_user_reply", id, answer}` to sidecar. Claude continues.
   - **escalate**: Session → Escalating. Freeman speaks the question via Kokoro. Input captures user's voice reply. Session writes the answer to sidecar. Session → Working.
8. Sidecar emits `{type:"done", summary}`. Session → Reporting. Freeman speaks the summary via Kokoro.
9. Session → Idle. Call remains alive (process still running, sidecar terminated). User can start another turn with ⌥-space or Ctrl-C to exit.

## Error handling

| Failure | Behavior |
|---|---|
| whisper transcribes empty / garbled | Freeman says "sorry, didn't catch that" and re-opens mic. |
| Sidecar crashes mid-session | Freeman says "lost the coding agent, restarting" and respawns. Objective is cached; new session resumes from the same prompt. Caveat: any unflushed edits are lost; that's on the worker model to re-do. |
| Anthropic API error (Haiku) during intake | Freeman says "brain is having trouble, try again." Session stays in Intake. |
| Anthropic API error (Haiku) during routing | Router degrades to "always escalate" — every `ask_user` becomes a spoken question until Haiku recovers. |
| Anthropic API error inside pi session | Sidecar emits `{type:"error", message}`. Freeman speaks the error. Session → Reporting. |
| Barge-in during Kokoro playback | Session calls Kokoro.CancelSpeak(), cancels in-flight PM call, returns to Intake or Escalating depending on prior state. |
| Hotkey press during Working | Treated as hangup. Session sends `{type:"cancel"}` to sidecar, waits for exit, speaks "canceled," returns to Idle. |
| Hotkey press during Idle | Opens a new call (enters Intake). |
| Mic permission denied | Freeman prints a clear error and exits. |

## Configuration

`config.yaml` gains new sections while keeping the existing TTS config:

```yaml
# existing TTS config preserved
server:
  port: 17000  # now unused in CLI mode but kept for compat
models:
  path: ./models
voice:
  default: af_heart
  speed: 1.0

# new
freeman:
  pm:
    model: claude-haiku-4-5
    confidence_threshold: 0.8
    api_key_env: ANTHROPIC_API_KEY
  worker:
    default_model: claude-sonnet-4-6
    opus_model: claude-opus-4-6
    auth: subscription   # "subscription" | "api_key"
  stt:
    model: whisper-large-v3-turbo
    model_path: ./models/whisper/ggml-large-v3-turbo.bin
    vad:
      silence_ms: 800
      min_speech_ms: 300
  hotkey: option+space
  logging:
    transcript_dir: ./.freeman/transcripts
```

## Testing strategy

**Unit tests (Go)**
- Session state machine: table-driven transition tests covering every state × event pair, including error and barge-in paths.
- PM router decision table: fixtures of `(objective, transcript, ask_user_question) → expected action`. Runs against a mocked Haiku client returning canned responses.
- Sidecar JSONL protocol: handshake, ask_user round-trip, cancel, error.
- Config loading and validation.

**Unit tests (TSX sidecar)**
- Custom `ask_user` tool correctly proxies to stdin/stdout.
- `session.abort()` wired to cancel handling.
- Model selection respects start payload.

**Integration tests**
- Full loop with a **stub sidecar** (Go test spawns a fake node process or uses a Go-native stub) that emits canned events. Fake whisper (text-in) and fake Kokoro (text-out). Verifies end-to-end state machine behavior without real models.
- Single **smoke test** against real Claude in a throwaway project dir, gated behind `FREEMAN_E2E=1` env var so CI doesn't burn tokens. Runs one canned intake → dispatch → simple edit → done cycle and asserts a file was written.

**Manual / dogfood**
- `--log-transcript` flag writes every call to JSONL at `.freeman/transcripts/YYYY-MM-DD-HHMMSS.jsonl` with all events (user utterances, PM decisions, sidecar events, TTS playback).
- A `freeman replay <transcript>` command re-plays a transcript through the PM with a potentially updated prompt, to let you tune PM prompts offline without burning real calls.

## Open questions (resolved — listed for record)

- **Interaction model.** Settled on phone-call metaphor: hotkey to enter, VAD inside, hard barge-in, hotkey to hang up.
- **How Freeman drives Claude.** Settled on `pi-coding-agent` TSX sidecar via SDK mode with a custom `ask_user` tool. Rejected: headless `claude -p` CLI (no question-intercept path), raw Anthropic API + DIY tools (reimplements pi).
- **PM brain.** Settled on Claude Haiku 4.5 via API. Considered Gemma 4 E4B (excellent for its size, purpose-built for agentic workflows) but rejected for MVP because (a) current Ollama+Apple Silicon bugs affect tool calling, (b) Haiku's judgment quality genuinely matters for the routing role, and (c) cost is pennies/day for realistic usage. Local LLM fallback is a post-MVP feature behind a `--local` flag.
- **STT.** Settled on whisper.cpp local (large-v3-turbo). Rejected Deepgram/AssemblyAI for privacy + matching the local-ecosystem instinct; rejected macOS native for accuracy.
- **TTS.** Kept existing Kokoro pipeline. Considered OuteTTS via llama.cpp (same ecosystem as whisper.cpp, clean dependency story) and Orpheus (more natural voice) as post-MVP upgrades. Kokoro is already integrated, still the speed champion, and "mechanical voice" is not a blocker for MVP.
- **Worker model selection.** Haiku decides Sonnet vs Opus during intake based on task complexity; fixed at dispatch time for MVP. Mid-session escalation is a v2 feature.
- **Intake → dispatch trigger.** Propose-and-confirm with "just go" force-start escape hatch. Rejected pure autonomous dispatch (too risky) and pure user-triggered (objective may be ill-formed).
- **Form factor.** CLI-in-directory (`freeman call`) for MVP. Menubar / daemon / global hotkey is v2.
