# Freeman Conversational Redesign

**Status:** approved
**Date:** 2026-04-16
**Replaces:** the narrow-intake state machine introduced in Plan 3 (`internal/call/`, `internal/pm/`, `sidecar/pm-sidecar.ts`)

## Why

Plan 3 modelled a Freeman call as a fixed pipeline of phases (Idle → Intake → AwaitingConfirm → Working → Escalating). Live testing showed users don't talk to it that way: they ask questions, change topics, want it to chat. The narrow PM keeps emitting follow-up questions or "sorry, one moment" loops because the LLM can't break out of the intake mold. Every UX bug we hit was a symptom of the same root cause — Freeman is hard-coded as a task funnel but users treat it as a conversational agent.

The redesign collapses the intake state machine and lets a single long-lived conversational LLM session decide what to do each turn. Heavy coding work still runs in a background pi-coding-agent task sidecar, but it's something the conversation reaches for via tool calls, not the only thing the call can do.

## Goals

- Freeman behaves like a voice assistant: natural turn-by-turn dialogue, project-aware on the lightweight stuff, no rigid "intake → confirm → dispatch" ceremony.
- A single background coding task can run in parallel with the conversation; its state is surfaced into chat naturally without blocking either side.
- The Go layer shrinks to a thin router. All decision-making (when to ask a follow-up, when to start a task, when to reply to a task's question) lives inside the conversation LLM.
- Voice ergonomics are preserved: short replies, no markdown, barge-in still cancels TTS.

## Non-goals (v1)

- **Multiple concurrent tasks.** Single task at a time. Multi-task is a clean follow-up once the architectural pattern lands.
- **Cross-call memory.** Each call is fresh; the conversation history dies when the call ends.
- **Always-on / ambient mode.** Freeman still starts a call on hotkey and ends on ctx cancel.
- **Acoustic echo cancellation.** Speakers-to-mic leak is mitigated by VAD/Transcriber muting during playback (already in place).
- **Whisper hallucination filtering** beyond the existing min-speech threshold.
- **Automated tests.** v1 ships with live testing only; tests get added once the architecture stabilises.
- **`/pm` vs `/eng` skill routing.** Personas are out of scope for v1; Freeman is one conversational agent.

## Architecture

```
mic → VAD → whisper → text ──▶ ConvSession (long-lived pi-coding-agent)
                                ├─ tools: start_task, reply_to_task,
                                │          cancel_task, task_status
                                ├─ system: voice agent, short, no markdown
                                ├─ static project context: README,
                                │          go.mod/package.json, dir listing
                                └─ task state injected as field on each turn
                                                │
                                                │ start_task(...)
                                                ▼
                                          TaskManager (Go)
                                                │
                                                │ spawn
                                                ▼
                                          TaskSession (existing sidecar.ts)
                                          ├─ pi-coding-agent with full tools
                                          └─ emits ask_user / done / error
                                                │
                              events ──▶ TaskState ──▶ next user_say.task_state

ConvSession assistant text stream ──▶ sentence chunker ──▶ Kokoro TTS ──▶ Speaker
```

Two cooperating pi-coding-agent processes, coordinated by a thin Go layer:

- **ConvSession** — long-lived, one per call, runs the conversational LLM (default `claude-haiku-4-5`). System prompt is voice-tuned. Static project context is injected once at startup. Has four custom tools: `start_task`, `reply_to_task`, `cancel_task`, `task_status`.
- **TaskSession** — spawned on demand by the conversation when it calls `start_task`. Same `sidecar/sidecar.ts` script we already have (Plan 3 Task 7). Untouched by this redesign. Uses pi-coding-agent's full coding tools (read, edit, write, bash, grep, find).
- **TaskManager** — Go-side single-task supervisor. Owns at most one TaskSession subprocess. Tracks task state across the lifetime of the call.

## Components

### `internal/conv/session.go` (new)

Owns the long-lived ConvSession over a `conv-sidecar.ts` subprocess. Hosts the call's event loop. Replaces `internal/call/session.go` entirely.

Event sources:
- Hotkey channel (start of call signal — only used to know when to greet)
- Transcriber utterances (whisper text)
- ConvSession messages (`assistant_say`, `tool_call`, `error`)
- TaskManager events (`running` / `needs_input` / `done` / `failed` transitions)
- Speech onsets (barge-in)
- speakDone (TTS finished)

Per-turn flow when a user utterance arrives:
1. Grab current task state from TaskManager.
2. Send `{type: "user_say", text, task_state}` to ConvSession.
3. Stream `assistant_say` chunks back; pipe through a sentence chunker into the Speaker queue.
4. Handle any `tool_call` from ConvSession by dispatching to TaskManager (start/reply/cancel) or returning current task state synchronously (`task_status`).
5. After the turn ends, loop.

Barge-in cancels in-flight Speak and drains the pending sentence queue. Interrupted text is **not** sent back as context (intentional — keeps the protocol simple; the LLM sees the user's next utterance and figures it out).

### `internal/conv/taskmgr.go` (new)

Single-task supervisor. State: `none → running → {needs_input | done | failed} → none`. Methods:

- `Start(objective Objective) error` — spawns `sidecar/sidecar.ts`, sends start message, transitions to `running`. Errors if a task is already in flight.
- `Reply(answer string) error` — forwards to task sidecar as `ask_user_reply`. Errors if not in `needs_input`.
- `Cancel() error` — sends `cancel` to task sidecar, kills subprocess, transitions to `none`.
- `Status() TaskState` — current snapshot, safe to call any time.
- `Events() <-chan TaskEvent` — internal channel for state-transition notifications (the conv session subscribes so task progress can update the next `user_say`).
- `Close() error` — shutdown on call end.

Internally wraps `internal/sidecar.Client` (the existing JSONL client). Lifecycle is owned by the conv Session.

### `internal/conv/projectctx.go` (new)

```go
func Read(cwd string) string
```

Returns ~1 KB of project context: README first ~50 lines, contents of `go.mod`/`package.json`/`pyproject.toml` if present, and a one-level directory listing. Read once at conv startup, injected into ConvSession's system prompt as a "Project context:" section.

### `sidecar/conv-sidecar.ts` (new)

Long-lived Bun subprocess. Mirrors `pm-sidecar.ts` in style but with conversational message types.

JSONL protocol:

**Inbound (Go → sidecar):**
- `{type: "init", system_prompt, project_context, model}` — once at startup, creates the pi-coding-agent session
- `{type: "user_say", id, text, task_state}` — a user turn; `task_state` is one of `{state: "none"}` / `{state: "running"}` / `{state: "needs_input", question}` / `{state: "done", summary}` / `{state: "failed", message}`
- `{type: "tool_result", id, name, result}` — result of a tool call the sidecar emitted (start_task ack, reply_to_task ack, etc.)
- `{type: "shutdown"}` — graceful close

**Outbound (sidecar → Go):**
- `{type: "ready"}` — after init succeeds
- `{type: "assistant_say", id, text}` — streamed assistant text chunks (full sentences or smaller; Go's sentence chunker handles framing)
- `{type: "tool_call", id, call_id, name, args}` — model called one of the four custom tools
- `{type: "turn_end", id}` — assistant turn finished
- `{type: "error", id?, message}` — anything went wrong

Custom tools registered with the pi-coding-agent session (via `customTools`):

- `start_task({goal, acceptance_criteria, constraints, notes, model_hint, spoken_summary})` — Go spawns the task sidecar
- `reply_to_task({answer})` — Go forwards to the task sidecar's `ask_user_reply`
- `cancel_task({})` — Go cancels the task sidecar
- `task_status({})` — returns current task state synchronously (the LLM can self-check, though normally task state is injected on each turn anyway)

The Go side's user_say handler builds the prompt as:

```
[Background task: <state-summary>]

<user text>
```

…where `state-summary` is something like `"task running"` or `"task needs you to answer: use existing client?"`. The system prompt instructs the LLM: *"If the background task line shows new information, weave it naturally into your reply."*

The system prompt for ConvSession (passed in `init`) emphasizes voice-friendly behavior:

- You are Freeman, a voice assistant on a phone call.
- Replies are spoken aloud — never use markdown, asterisks, bullets, code fences, or line breaks.
- Keep responses to one or two casual sentences unless asked for detail.
- You have a background task tool. Use it when the user asks you to build, fix, refactor, or implement something concrete. Don't use it for questions or chat.
- You may chat freely about general topics using your knowledge.
- For questions about this specific project, use the project context provided below; if you don't see what you need, say so honestly.
- The user can interrupt you mid-sentence. If they do, they meant to redirect — don't apologise, just respond to the new thing.

### `cmd/freeman/call.go`

Wiring rewritten. The audio/whisper/VAD/speaker/hotkey setup stays. The `call.NewSession(...)` call is replaced by `conv.NewSession(...)`, and the `pm.New(...)` block is removed entirely. `findRepoRoot()` stays. The `--fake-audio` path is left untouched for now (uses ScriptedPM through the old call.Session); we'll either delete or rewire it after the conv path proves out.

### Files deleted

- `internal/call/` — entire package: `machine.go`, `machine_test.go`, `effects.go`, `events.go`, `types.go`, `state.go`, `state_test.go`, `session.go`, `session_test.go`, `ports.go`, `fakes/fakes.go`, `fakes/fakes_test.go`
- `internal/pm/` — entire package: `pi.go`, `pi_test.go`, `prompts.go`
- `sidecar/pm-sidecar.ts`

`sidecar/sidecar.ts` (the task sidecar from Task 7) **stays unchanged**. `internal/sidecar/` (the JSONL client) **stays unchanged** — TaskManager uses it.

## Data flow

### Boot (call start)

1. Hotkey fires.
2. Go reads project context via `projectctx.Read(cwd)`.
3. Go spawns `conv-sidecar.ts` and sends `{type: "init", system_prompt: <chat prompt>, project_context: <blurb>, model: "claude-haiku-4-5"}`.
4. conv-sidecar creates the pi-coding-agent session with that prompt + the four custom tools, replies `{type: "ready"}`.
5. Go boots TaskManager (no task running).
6. Go sends a synthetic seed `user_say` (`text: "<call started>"`, `task_state: {state: "none"}`) so the LLM produces a greeting.
7. ConvSession streams `assistant_say` chunks; sentence chunker pipes them to Kokoro/Speaker.

### Normal user turn (no task running)

1. User speaks → VAD → whisper → text.
2. Go sends `{type: "user_say", id, text, task_state: {state: "none"}}` to ConvSession.
3. ConvSession streams `assistant_say` back. Each chunk is appended to a buffer; complete sentences are flushed to the Speaker queue. Barge-in cancels in-flight Speak and clears the queue.
4. ConvSession emits `turn_end`. Loop.

### User turn that triggers a task

1. ConvSession's response includes a `tool_call` for `start_task` with an objective.
2. Go calls `taskmgr.Start(objective)` → spawns `sidecar.ts`. State → `running`.
3. Go sends `{type: "tool_result", id, name: "start_task", result: {ok: true}}` back to ConvSession so the LLM knows the call succeeded.
4. ConvSession may also have produced assistant text in the same turn ("ok, on it") which is spoken normally.
5. Loop.

### Background task event during conversation

1. Task sidecar emits `ask_user` / `done` / `error`. TaskManager updates state.
2. Go does **not** interrupt. Just records the new state.
3. Next `user_say` carries the updated `task_state`. ConvSession LLM sees it as part of the user message context and weaves it in: *"Quick thing — the task is asking whether to use the existing auth client. What do you think?"*

### User answers a `needs_input` task question

1. User's reply goes through the normal turn flow; `task_state` still shows `needs_input`.
2. ConvSession LLM, seeing both the user's reply and the still-`needs_input` task state, calls `reply_to_task({answer: "use the existing one"})`.
3. Go calls `taskmgr.Reply(answer)` → task sidecar receives `ask_user_reply`. Task state → `running`.
4. Go sends `{type: "tool_result", id, name: "reply_to_task", result: {ok: true}}`.
5. ConvSession may also produce a brief acknowledgement assistant text spoken normally.

### Call end

1. Hotkey fires again, ctx canceled, or SIGINT.
2. Go sends `{type: "shutdown"}` to ConvSession, then closes the subprocess.
3. Go calls `taskmgr.Cancel()` if a task is in flight.
4. Speaker drains, audio context closes, process exits.

## Error handling

- **conv-sidecar process dies mid-call.** Go detects EOF on its stdout. Speaks "voice agent crashed, restarting" via direct Kokoro call (bypassing the dead session), respawns conv-sidecar with the same project context. Conversation history is lost. If respawn fails, the call ends.
- **task-sidecar process dies mid-task.** TaskManager detects EOF, transitions task state to `failed` with message "task process died". Next user turn surfaces the failure via `task_state` injection. User can ask Freeman to retry; ConvSession may call `start_task` again.
- **ConvSession returns plain text instead of calling a tool when the user clearly asked to start a task.** Mitigated by the system prompt; not hard-enforced. Users can be explicit ("just go", "do it"), and the prompt instructs the LLM to start_task on those phrases.
- **Two `start_task` calls before the first finishes.** TaskManager rejects the second; tool result returns `{ok: false, error: "task already running"}`. ConvSession surfaces the conflict ("there's already a task running, want me to cancel it first?").
- **`reply_to_task` called when task isn't in `needs_input`.** TaskManager rejects with `{ok: false, error: "no question pending"}`. ConvSession apologises naturally.
- **Barge-in mid-TTS.** VAD onset cancels in-flight Kokoro Speak via context cancellation, drains the pending sentence queue. Next user utterance proceeds normally. Interrupted assistant text is **not** passed back to the conv session in v1.
- **Long ConvSession responses.** Streamed sentence-by-sentence to Kokoro so playback starts before the full response is ready. Barge-in cancels mid-response.
- **Whisper hallucinations on silence.** Out of scope; existing VAD min-speech threshold helps.

## Spec coverage map

| Decision | Section |
|---|---|
| Discrete calls, no cross-call memory | Goals, Non-goals |
| Single background task (v1) | Non-goals, TaskManager |
| Task state injected per turn (option 3) | Architecture, Data flow |
| Unified LLM with task-control tools (option 1) | Components → ConvSession, conv-sidecar tools |
| Lightweight project awareness via static blurb (option 3) | Components → projectctx |
| State machine deleted | Components → Files deleted |
| Live testing only, no automated tests in v1 | Non-goals |
