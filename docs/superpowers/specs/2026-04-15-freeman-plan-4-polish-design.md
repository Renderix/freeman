# Freeman — Plan 4 (Polish) Design

**Status:** Placeholder
**Date:** 2026-04-15
**Builds on:** Plan 3 (Intelligence)

## Goal

Add observability and offline PM tuning tools to Freeman: transcript logging so every call is recorded to JSONL, and a `freeman replay` command to re-run a saved transcript through an updated PM prompt without burning a real call.

## Scope

### 1. Transcript logging (`--log-transcript`)

Every call writes a JSONL file to `.freeman/transcripts/YYYY-MM-DD-HHMMSS.jsonl`. Each line is one event with a timestamp:

```json
{"ts": "2026-04-15T14:23:00Z", "type": "user_utterance", "text": "build a feature flag"}
{"ts": "2026-04-15T14:23:01Z", "type": "pm_question", "text": "should it default on or off?"}
{"ts": "2026-04-15T14:23:03Z", "type": "user_utterance", "text": "off", "interrupted_text": "should it default on or off?"}
{"ts": "2026-04-15T14:23:04Z", "type": "objective", "goal": "...", "model_hint": "sonnet"}
{"ts": "2026-04-15T14:23:10Z", "type": "ask_user", "question": "use existing client?"}
{"ts": "2026-04-15T14:23:10Z", "type": "route_decision", "action": "answer_inline", "answer": "yes", "confidence": 0.92}
{"ts": "2026-04-15T14:23:45Z", "type": "done", "summary": "edited 3 files, tests pass"}
```

Logging is always on when using real audio (not behind a flag). The transcript dir is configurable via `freeman.logging.transcript_dir` in `config.yaml` (default `./.freeman/transcripts`).

### 2. `freeman replay <transcript>`

Re-plays a saved transcript through the PM with the current prompts. Useful for tuning PM system prompts offline:

```
$ freeman replay .freeman/transcripts/2026-04-15-142300.jsonl
```

Feeds each `user_utterance` event (with its `interrupted_text` if present) to the real Haiku PM, printing PM responses to stdout. Does not play audio, does not dispatch a sidecar. Exits when the transcript ends or when `done` is reached.

Useful for:
- Checking whether updated prompts produce a better Objective from the same utterances
- Verifying the router makes better inline/escalate decisions on real historical questions

### 3. End-to-end smoke test (`FREEMAN_E2E=1`)

A single integration test gated behind `FREEMAN_E2E=1` that runs a real call against Claude in a throwaway temp dir. Inputs a canned transcript via stdin (`--fake-audio` mode), asserts a file was written. Intended for local pre-release checks, not CI.

## Non-goals for Plan 4

- Voice-level replay (re-synthesizing TTS from transcript).
- Transcript search or indexing.
- Sharing or exporting transcripts.
- Automatic PM prompt improvement (that's a future research project).

## Open design questions

- Should `freeman replay` use the real Anthropic API or a local model for cost-free iteration? Defer to design time.
- Should transcript dir be per-project (`./.freeman/`) or global (`~/.freeman/`)? Lean toward per-project for isolation.
