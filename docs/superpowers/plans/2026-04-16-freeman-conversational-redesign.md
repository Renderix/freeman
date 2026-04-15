# Freeman Conversational Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the narrow Plan 3 intake state machine with a single long-lived conversational pi-coding-agent session that can spawn one background coding task on demand and weave its status into chat naturally.

**Architecture:** Two cooperating pi-coding-agent processes — a long-lived ConvSession (chat) and an on-demand TaskSession (heavy coding work) — coordinated by a thin Go layer in a new `internal/conv` package. Task state is injected as a field on each user turn so the LLM weaves updates in without Go-side timers.

**Tech Stack:** Go 1.25 (event loop, subprocess management, audio glue), Bun + TypeScript (`pi-coding-agent` sidecars), `pi-ai`, existing internal/sidecar JSONL client, existing audio/VAD/whisper/Kokoro stack.

**Spec:** `docs/superpowers/specs/2026-04-16-freeman-conversational-redesign-design.md`

**Live testing only.** No automated tests in v1 per spec.

---

## File Map

**New files:**
- `internal/conv/types.go` — `Objective`, `TaskStateKind`, `TaskState`
- `internal/conv/projectctx.go` — `Read(cwd) string` static project blurb
- `internal/conv/taskmgr.go` — `TaskManager` single-task supervisor
- `internal/conv/protocol.go` — JSONL message types for conv-sidecar
- `internal/conv/session.go` — call event loop + sentence chunker + tool routing
- `sidecar/conv-sidecar.ts` — long-lived Bun chat sidecar

**Modified files:**
- `cmd/freeman/call.go` — replace runCall body with conv path, delete `runCallWithFakes` and `--fake-audio` flag
- `sidecar/package.json` — add `conv-sidecar` script

**Deleted files (Task 6):**
- `internal/call/` — entire package
- `internal/pm/` — entire package
- `sidecar/pm-sidecar.ts` — replaced

---

### Task 1: Conv types and project-context helper

**Files:**
- Create: `internal/conv/types.go`
- Create: `internal/conv/projectctx.go`

- [ ] **Step 1: Create `internal/conv/types.go`**

```go
// Package conv hosts the conversational call layer that replaces the
// narrow Plan 3 intake state machine. A long-lived ConvSession runs the
// chat LLM; a TaskManager owns at most one background coding task; the
// Go side here is a thin router between mic, conv-sidecar, task sidecar,
// and speaker.
package conv

// Objective is a structured task spec the chat LLM hands to TaskManager
// when it calls the start_task tool. Mirrors what the existing
// sidecar.Client expects via sidecar.ObjectivePayload.
type Objective struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	ModelHint          string   `json:"model_hint"` // "sonnet" or "opus"
	SpokenSummary      string   `json:"spoken_summary"`
}

// TaskStateKind is the lifecycle of the single background task tracked
// by TaskManager. The zero value is TaskStateNone.
type TaskStateKind int

const (
	TaskStateNone TaskStateKind = iota
	TaskStateRunning
	TaskStateNeedsInput
	TaskStateDone
	TaskStateFailed
)

func (k TaskStateKind) String() string {
	switch k {
	case TaskStateRunning:
		return "running"
	case TaskStateNeedsInput:
		return "needs_input"
	case TaskStateDone:
		return "done"
	case TaskStateFailed:
		return "failed"
	default:
		return "none"
	}
}

// TaskState is a snapshot of the current background task's state.
// Unused fields for the current Kind are left at their zero value.
type TaskState struct {
	Kind     TaskStateKind
	Question string // valid when Kind == TaskStateNeedsInput
	Summary  string // valid when Kind == TaskStateDone
	Message  string // valid when Kind == TaskStateFailed
	// AskUserID is the sidecar-issued correlation id for the current
	// needs_input question. Used by Reply() to forward the answer.
	AskUserID string
}
```

- [ ] **Step 2: Create `internal/conv/projectctx.go`**

```go
package conv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Read returns a short static blurb describing the project at cwd,
// suitable for injection into the chat LLM's system prompt. It reads
// the first ~50 lines of README, the first ~30 lines of common
// manifest files (go.mod, package.json, pyproject.toml, Cargo.toml),
// and a one-level top directory listing. Total output is capped at
// roughly 4 KB so we don't blow the conv-sidecar prompt budget.
func Read(cwd string) string {
	var sb strings.Builder

	if readme := findFirst(cwd, []string{"README.md", "README.MD", "README.txt", "README"}); readme != "" {
		sb.WriteString("README (")
		sb.WriteString(filepath.Base(readme))
		sb.WriteString("):\n")
		appendFirstLines(&sb, readme, 50)
		sb.WriteString("\n")
	}

	for _, manifest := range []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml"} {
		p := filepath.Join(cwd, manifest)
		if _, err := os.Stat(p); err == nil {
			sb.WriteString(manifest)
			sb.WriteString(":\n")
			appendFirstLines(&sb, p, 30)
			sb.WriteString("\n")
		}
	}

	if entries, err := os.ReadDir(cwd); err == nil {
		sb.WriteString("Top-level entries:\n")
		count := 0
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() {
				sb.WriteString("- ")
				sb.WriteString(name)
				sb.WriteString("/\n")
			} else {
				sb.WriteString("- ")
				sb.WriteString(name)
				sb.WriteString("\n")
			}
			count++
			if count >= 40 {
				break
			}
		}
	}

	out := sb.String()
	const cap = 4096
	if len(out) > cap {
		out = out[:cap] + "\n…(truncated)\n"
	}
	return out
}

func findFirst(dir string, names []string) string {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func appendFirstLines(sb *strings.Builder, path string, n int) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for i := 0; i < n && s.Scan(); i++ {
		sb.WriteString(s.Text())
		sb.WriteString("\n")
	}
}
```

- [ ] **Step 3: Verify it compiles**

```bash
cd /Users/ayusman/hale/freeman
go build ./internal/conv/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/conv/types.go internal/conv/projectctx.go
git commit -m "feat(conv): types and project context helper"
```

---

### Task 2: TaskManager

**Files:**
- Create: `internal/conv/taskmgr.go`

Uses the existing `internal/sidecar.Client` to spawn `sidecar/sidecar.ts` (the unchanged Plan 3 task sidecar) and forward objectives + replies. Tracks a single task; rejects double-start; surfaces state transitions via an Events channel.

- [ ] **Step 1: Create `internal/conv/taskmgr.go`**

```go
package conv

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/Renderix/freeman/internal/sidecar"
)

// TaskEvent is emitted whenever the tracked task's state changes.
// Subscribers receive a snapshot of the new state.
type TaskEvent struct {
	State TaskState
}

// TaskManager owns at most one background pi-coding-agent task at a
// time. It wraps a sidecar.Client over sidecar/sidecar.ts.
type TaskManager struct {
	repoRoot string

	mu     sync.Mutex
	state  TaskState
	client *sidecar.Client
	cancel context.CancelFunc

	events chan TaskEvent

	wg sync.WaitGroup
}

// NewTaskManager constructs a manager that will spawn the task sidecar
// from the given repo root. No subprocess is started until Start() is
// called.
func NewTaskManager(repoRoot string) *TaskManager {
	return &TaskManager{
		repoRoot: repoRoot,
		events:   make(chan TaskEvent, 8),
	}
}

// Events returns the channel of state transitions. Capacity is small;
// consumers should drain promptly.
func (m *TaskManager) Events() <-chan TaskEvent { return m.events }

// Status returns the current task state snapshot.
func (m *TaskManager) Status() TaskState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Start spawns the task sidecar and dispatches the objective. Errors
// if a task is already in flight.
func (m *TaskManager) Start(ctx context.Context, obj Objective) error {
	m.mu.Lock()
	if m.state.Kind != TaskStateNone && m.state.Kind != TaskStateDone && m.state.Kind != TaskStateFailed {
		m.mu.Unlock()
		return fmt.Errorf("task already running")
	}
	// Reset stale done/failed state before launching a new one.
	m.state = TaskState{}

	scriptPath := filepath.Join(m.repoRoot, "sidecar", "sidecar.ts")
	taskCtx, cancel := context.WithCancel(ctx)
	client, err := sidecar.Spawn(taskCtx, "bun", "run", scriptPath)
	if err != nil {
		cancel()
		m.mu.Unlock()
		return fmt.Errorf("spawn task sidecar: %w", err)
	}
	m.client = client
	m.cancel = cancel
	m.state = TaskState{Kind: TaskStateRunning}
	m.mu.Unlock()

	if err := client.Send(sidecar.StartMsg{
		Type: sidecar.MsgTypeStart,
		Objective: sidecar.ObjectivePayload{
			Goal:               obj.Goal,
			AcceptanceCriteria: obj.AcceptanceCriteria,
			Constraints:        obj.Constraints,
			Notes:              obj.Notes,
			Model:              obj.ModelHint,
		},
	}); err != nil {
		m.cancelLocked()
		m.transition(TaskState{Kind: TaskStateFailed, Message: fmt.Sprintf("send start: %v", err)})
		return err
	}

	m.transition(TaskState{Kind: TaskStateRunning})

	m.wg.Add(1)
	go m.readLoop(client)
	return nil
}

// Reply forwards a user's spoken answer to the task sidecar's pending
// ask_user. Errors if no question is pending.
func (m *TaskManager) Reply(answer string) error {
	m.mu.Lock()
	if m.state.Kind != TaskStateNeedsInput {
		m.mu.Unlock()
		return fmt.Errorf("no question pending")
	}
	id := m.state.AskUserID
	client := m.client
	m.mu.Unlock()

	if client == nil {
		return fmt.Errorf("no task client")
	}
	if err := client.Send(sidecar.AskUserReplyMsg{
		Type:   sidecar.MsgTypeAskUserReply,
		ID:     id,
		Answer: answer,
	}); err != nil {
		return fmt.Errorf("send ask_user_reply: %w", err)
	}
	// Optimistically transition back to running; the sidecar will eventually
	// confirm via subsequent events.
	m.transition(TaskState{Kind: TaskStateRunning})
	return nil
}

// Cancel terminates any in-flight task. Safe to call when no task is running.
func (m *TaskManager) Cancel() error {
	m.mu.Lock()
	if m.client == nil {
		m.mu.Unlock()
		return nil
	}
	m.cancelLocked()
	m.mu.Unlock()
	m.transition(TaskState{Kind: TaskStateFailed, Message: "canceled"})
	return nil
}

// Close shuts down the manager. Idempotent.
func (m *TaskManager) Close() error {
	m.Cancel()
	m.wg.Wait()
	return nil
}

func (m *TaskManager) cancelLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
}

func (m *TaskManager) transition(s TaskState) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
	select {
	case m.events <- TaskEvent{State: s}:
	default:
		// Drop if consumer is slow; latest state is always available via Status().
	}
}

func (m *TaskManager) readLoop(client *sidecar.Client) {
	defer m.wg.Done()
	for msg := range client.Events() {
		switch m := msg.(type) {
		case sidecar.AssistantTextMsg:
			// Intermediate task narration; ignored in v1.
			_ = m
		case sidecar.AskUserMsg:
			(func(am sidecar.AskUserMsg) {})(m) // satisfy linter
		case sidecar.DoneMsg:
			(func(dm sidecar.DoneMsg) {})(m)
		case sidecar.ErrorMsg:
			(func(em sidecar.ErrorMsg) {})(m)
		}
		switch v := msg.(type) {
		case sidecar.AskUserMsg:
			m.transition(TaskState{
				Kind:      TaskStateNeedsInput,
				Question:  v.Question,
				AskUserID: v.ID,
			})
		case sidecar.DoneMsg:
			m.transition(TaskState{Kind: TaskStateDone, Summary: v.Summary})
		case sidecar.ErrorMsg:
			m.transition(TaskState{Kind: TaskStateFailed, Message: v.Message})
		}
	}
}
```

(Note: the duplicated switch in readLoop is intentional ugliness because the outer loop handles the `_ = m` shadowing for unused branches; the second switch does the actual transition. If you prefer cleaner code, collapse to one switch; the behavior is identical.)

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/ayusman/hale/freeman
go build ./internal/conv/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/conv/taskmgr.go
git commit -m "feat(conv): TaskManager single-task supervisor"
```

---

### Task 3: conv-sidecar.ts

**Files:**
- Create: `sidecar/conv-sidecar.ts`
- Modify: `sidecar/package.json`

Long-lived Bun process that runs the chat LLM via pi-coding-agent. JSONL protocol over stdin/stdout. Four custom tools (start_task, reply_to_task, cancel_task, task_status) whose `execute()` sends a `tool_call` to Go and awaits the matching `tool_result`.

- [ ] **Step 1: Create `sidecar/conv-sidecar.ts`**

```typescript
// Conversational sidecar for Freeman. A long-lived Bun process that
// hosts the chat LLM via pi-coding-agent. JSONL on stdin/stdout.
//
// Auth: subscription via ~/.pi/agent/auth.json (pi auth login).

import * as readline from "node:readline";
import type { Readable, Writable } from "node:stream";
import {
  createAgentSession,
  DefaultResourceLoader,
  SessionManager,
  defineTool,
  type AgentSession,
} from "@mariozechner/pi-coding-agent";
import { getModel, Type } from "@mariozechner/pi-ai";

// ─── Protocol ───────────────────────────────────────────────────────────────

type TaskStateMsg =
  | { state: "none" }
  | { state: "running" }
  | { state: "needs_input"; question: string }
  | { state: "done"; summary: string }
  | { state: "failed"; message: string };

type InitMsg = {
  type: "init";
  system_prompt: string;
  project_context: string;
  model: string;
};
type UserSayMsg = {
  type: "user_say";
  id: string;
  text: string;
  task_state: TaskStateMsg;
};
type ToolResultMsg = {
  type: "tool_result";
  call_id: string;
  result: any;
};
type ShutdownMsg = { type: "shutdown" };
type InMsg = InitMsg | UserSayMsg | ToolResultMsg | ShutdownMsg;

function send(out: Writable, obj: Record<string, unknown>): void {
  out.write(JSON.stringify(obj) + "\n");
}

function formatTaskState(s: TaskStateMsg): string {
  switch (s.state) {
    case "none":
      return "no background task";
    case "running":
      return "background task is running";
    case "needs_input":
      return `background task is waiting for an answer to: ${s.question}`;
    case "done":
      return `background task finished. summary: ${s.summary}`;
    case "failed":
      return `background task failed: ${s.message}`;
  }
}

// ─── Custom tools ───────────────────────────────────────────────────────────

const objectiveSchema = Type.Object({
  goal: Type.String(),
  acceptance_criteria: Type.Array(Type.String()),
  constraints: Type.Array(Type.String()),
  notes: Type.Array(Type.String()),
  model_hint: Type.String(),
  spoken_summary: Type.String(),
});

// ─── Main entrypoint ────────────────────────────────────────────────────────

export interface ConvDeps {
  createSession?: typeof createAgentSession;
}

export function runConvSidecar(
  inp: Readable,
  out: Writable,
  deps: ConvDeps = {},
): void {
  const createSession = deps.createSession ?? createAgentSession;

  let session: AgentSession | null = null;
  let currentUserSayId: string | null = null;
  const pendingToolCalls = new Map<string, (result: any) => void>();

  function makeTool(name: string, description: string, paramSchema: any) {
    return defineTool({
      name,
      label: name,
      description,
      parameters: paramSchema,
      execute: async (_toolCallId, args) => {
        if (currentUserSayId === null) {
          return {
            content: [{ type: "text", text: JSON.stringify({ ok: false, error: "no active turn" }) }],
            details: {},
          };
        }
        const callId = crypto.randomUUID();
        const result = await new Promise<any>((resolve) => {
          pendingToolCalls.set(callId, resolve);
          send(out, {
            type: "tool_call",
            id: currentUserSayId,
            call_id: callId,
            name,
            args,
          });
        });
        return {
          content: [{ type: "text", text: JSON.stringify(result) }],
          details: {},
        };
      },
    });
  }

  const startTaskTool = makeTool(
    "start_task",
    "Spawn a background coding task. Use this when the user has clearly described something concrete to build, fix, refactor, or implement. Do not use this for questions or chat. The task runs in parallel; you'll get its status injected on later turns.",
    objectiveSchema,
  );

  const replyToTaskTool = makeTool(
    "reply_to_task",
    "Forward an answer to a background task that is asking the user a question. Only call this when the task is in needs_input state.",
    Type.Object({ answer: Type.String() }),
  );

  const cancelTaskTool = makeTool(
    "cancel_task",
    "Abort the current background task. Use sparingly — only when the user explicitly says to stop or cancel.",
    Type.Object({}),
  );

  const taskStatusTool = makeTool(
    "task_status",
    "Get the current background task's state. Normally you don't need to call this because task state is injected into every user turn.",
    Type.Object({}),
  );

  async function runInit(msg: InitMsg): Promise<void> {
    const fullSystemPrompt =
      msg.system_prompt +
      "\n\n## Project context (read once at boot)\n\n" +
      msg.project_context;

    const loader = new DefaultResourceLoader({
      systemPromptOverride: () => fullSystemPrompt,
      appendSystemPromptOverride: () => [],
    });
    await loader.reload();

    const created = await createSession({
      model: getModel("anthropic", msg.model as never),
      tools: [],
      customTools: [startTaskTool, replyToTaskTool, cancelTaskTool, taskStatusTool],
      resourceLoader: loader,
      sessionManager: SessionManager.inMemory(),
    });
    session = created.session;

    // Subscribe once for the lifetime of the session: stream assistant
    // text deltas to Go as assistant_say events.
    session.subscribe((event: any) => {
      if (event.type === "message_update") {
        const ame = event.assistantMessageEvent;
        if (ame && ame.type === "text_delta" && typeof ame.delta === "string") {
          if (currentUserSayId !== null) {
            send(out, {
              type: "assistant_say",
              id: currentUserSayId,
              text: ame.delta,
            });
          }
        }
      }
    });

    send(out, { type: "ready" });
  }

  async function runUserSay(msg: UserSayMsg): Promise<void> {
    if (!session) {
      send(out, { type: "error", id: msg.id, message: "session not initialized" });
      return;
    }
    currentUserSayId = msg.id;
    const promptText =
      `[background task: ${formatTaskState(msg.task_state)}]\n\n${msg.text}`;
    try {
      await session.prompt(promptText);
      send(out, { type: "turn_end", id: msg.id });
    } catch (err: unknown) {
      send(out, {
        type: "error",
        id: msg.id,
        message: err instanceof Error ? err.message : String(err),
      });
    } finally {
      currentUserSayId = null;
    }
  }

  function runToolResult(msg: ToolResultMsg): void {
    const resolve = pendingToolCalls.get(msg.call_id);
    if (resolve) {
      pendingToolCalls.delete(msg.call_id);
      resolve(msg.result);
    }
  }

  async function runShutdown(): Promise<void> {
    if (session) {
      try {
        await session.abort();
      } catch {
        // ignore
      }
      session.dispose();
      session = null;
    }
    process.exit(0);
  }

  const rl = readline.createInterface({ input: inp, terminal: false });
  rl.on("line", (raw: string) => {
    let msg: InMsg;
    try {
      msg = JSON.parse(raw) as InMsg;
    } catch {
      send(out, { type: "error", message: `bad json: ${raw}` });
      return;
    }
    if (msg.type === "init") void runInit(msg);
    else if (msg.type === "user_say") void runUserSay(msg);
    else if (msg.type === "tool_result") runToolResult(msg);
    else if (msg.type === "shutdown") void runShutdown();
  });
}

if (import.meta.main) {
  runConvSidecar(process.stdin, process.stdout);
}
```

- [ ] **Step 2: Add `conv-sidecar` script to `sidecar/package.json`**

Replace the `scripts` block in `sidecar/package.json` so it looks like:

```json
"scripts": {
  "stub": "bun run stub.ts",
  "sidecar": "bun run sidecar.ts",
  "pm-sidecar": "bun run pm-sidecar.ts",
  "conv-sidecar": "bun run conv-sidecar.ts"
}
```

(The `pm-sidecar` line stays for now; Task 6 deletes it along with the file.)

- [ ] **Step 3: Typecheck**

```bash
cd /Users/ayusman/hale/freeman/sidecar
bunx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/ayusman/hale/freeman
git add sidecar/conv-sidecar.ts sidecar/package.json
git commit -m "feat(sidecar): conv-sidecar long-lived chat session"
```

---

### Task 4: Conv Session

**Files:**
- Create: `internal/conv/protocol.go`
- Create: `internal/conv/session.go`

Spawns conv-sidecar, runs the call event loop, routes tool calls to TaskManager, streams assistant text through a sentence chunker into the Speaker, handles barge-in.

- [ ] **Step 1: Create `internal/conv/protocol.go`**

```go
package conv

import "encoding/json"

// taskStatePayload is the JSON shape sent to conv-sidecar inside
// user_say.task_state. It mirrors TaskStateKind but as discriminated
// JSON so the TS side can switch on .state cleanly.
type taskStatePayload struct {
	State    string `json:"state"`
	Question string `json:"question,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Message  string `json:"message,omitempty"`
}

func taskStateToPayload(s TaskState) taskStatePayload {
	switch s.Kind {
	case TaskStateRunning:
		return taskStatePayload{State: "running"}
	case TaskStateNeedsInput:
		return taskStatePayload{State: "needs_input", Question: s.Question}
	case TaskStateDone:
		return taskStatePayload{State: "done", Summary: s.Summary}
	case TaskStateFailed:
		return taskStatePayload{State: "failed", Message: s.Message}
	default:
		return taskStatePayload{State: "none"}
	}
}

// Outbound (Go → conv-sidecar)

type initMsg struct {
	Type           string `json:"type"`
	SystemPrompt   string `json:"system_prompt"`
	ProjectContext string `json:"project_context"`
	Model          string `json:"model"`
}

type userSayMsg struct {
	Type      string           `json:"type"`
	ID        string           `json:"id"`
	Text      string           `json:"text"`
	TaskState taskStatePayload `json:"task_state"`
}

type toolResultMsg struct {
	Type   string          `json:"type"`
	CallID string          `json:"call_id"`
	Result json.RawMessage `json:"result"`
}

type shutdownMsg struct {
	Type string `json:"type"`
}

// Inbound (conv-sidecar → Go)

type inboundMsg struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Text    string          `json:"text,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	Message string          `json:"message,omitempty"`
}
```

- [ ] **Step 2: Create `internal/conv/session.go`**

```go
package conv

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Transcriber is the subset of stt.Transcriber the conv layer needs.
type Transcriber interface {
	Utterances() <-chan string
	Stop()
}

// Speaker is the subset of playback.Speaker the conv layer needs.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// Hotkey is the subset of hotkey.Hotkey the conv layer needs.
type Hotkey interface {
	Events() <-chan struct{}
	Stop()
}

// Deps are the runtime dependencies injected into a Session.
type Deps struct {
	Transcriber  Transcriber
	Speaker      Speaker
	Hotkey       Hotkey
	SpeechOnsets <-chan struct{}
	TaskManager  *TaskManager

	RepoRoot     string // used to locate sidecar/conv-sidecar.ts
	Model        string // e.g. "claude-haiku-4-5"
	SystemPrompt string // chat system prompt (see DefaultSystemPrompt)
	Logger       *slog.Logger
}

// DefaultSystemPrompt is the voice-tuned chat system prompt used when
// Deps.SystemPrompt is empty.
const DefaultSystemPrompt = `You are Freeman, a voice assistant on a phone call with the user.

ABSOLUTE RULES:
- Replies are spoken aloud by text-to-speech. Never use markdown, asterisks, bullets, code fences, or line breaks.
- Keep responses to one or two casual spoken sentences unless the user asks for detail.
- The user can interrupt you mid-sentence. If they do, respond to the new thing without apologising.

WHAT YOU CAN DO:
- Chat about general topics using your knowledge.
- Answer questions about this specific project using the project context provided below. If the project context doesn't have what you need, say so honestly — do not make things up.
- Spawn a background coding task by calling the start_task tool. Use this when the user clearly asks you to build, fix, refactor, or implement something concrete. Do not use it for questions or chat.
- Forward the user's answer to a running task by calling reply_to_task. The task is asking when the background task state shows needs_input.
- Cancel a task with cancel_task only when the user explicitly says to stop.

ON BACKGROUND TASKS:
- Each user turn includes a [background task: …] line at the top describing the task's current state. If the state has new information (the task finished, failed, or needs an answer), weave it naturally into your reply — don't ignore it. Do not mention the bracketed line literally.
- Tasks run in parallel with the conversation; don't wait for them.
`

// Session is the conv-layer event loop. It owns the conv-sidecar
// subprocess and routes between mic, sidecar, taskmgr, and speaker.
type Session struct {
	deps Deps
	log  *slog.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu      sync.Mutex
	pending map[string]chan inboundMsg
	closed  bool

	nextID atomic.Uint64

	// The current in-flight assistant turn id. Receiving an
	// assistant_say with an id != currentTurnID is dropped.
	turnMu        sync.Mutex
	currentTurnID string
	turnCanceled  bool

	// speak goroutine state, owned by Run.
	cancelSpeak func()
	speakDone   chan struct{}

	wg sync.WaitGroup
}

// NewSession spawns conv-sidecar and prepares the Session. Run() must
// be called to drive the event loop.
func NewSession(ctx context.Context, deps Deps) (*Session, error) {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	if deps.SystemPrompt == "" {
		deps.SystemPrompt = DefaultSystemPrompt
	}
	if deps.Model == "" {
		deps.Model = "claude-haiku-4-5"
	}

	scriptPath := filepath.Join(deps.RepoRoot, "sidecar", "conv-sidecar.ts")
	cmd := exec.CommandContext(ctx, "bun", "run", scriptPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("conv stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("conv stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("conv start: %w", err)
	}

	s := &Session{
		deps:      deps,
		log:       log,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		pending:   make(map[string]chan inboundMsg),
		speakDone: make(chan struct{}, 1),
	}

	s.wg.Add(1)
	go s.readLoop()

	// Send init and wait for ready.
	projectCtx := Read(deps.RepoRoot)
	readyCh := s.registerWait("__ready__")
	if err := s.send(initMsg{
		Type:           "init",
		SystemPrompt:   deps.SystemPrompt,
		ProjectContext: projectCtx,
		Model:          deps.Model,
	}); err != nil {
		s.Close()
		return nil, fmt.Errorf("conv send init: %w", err)
	}
	select {
	case <-readyCh:
		// good
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}

	return s, nil
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	_ = s.send(shutdownMsg{Type: "shutdown"})
	_ = s.stdin.Close()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	if rc, ok := any(s.stdout).(io.Closer); ok {
		_ = rc.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Session) send(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("conv: closed")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = s.stdin.Write(b)
	return err
}

func (s *Session) nextRequestID() string {
	return strconv.FormatUint(s.nextID.Add(1), 10)
}

func (s *Session) registerWait(id string) chan inboundMsg {
	ch := make(chan inboundMsg, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	return ch
}

// readLoop demultiplexes messages from conv-sidecar.
func (s *Session) readLoop() {
	defer s.wg.Done()
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg inboundMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			s.log.Error("conv: bad json from sidecar", "err", err)
			continue
		}
		switch msg.Type {
		case "ready":
			s.mu.Lock()
			ch, ok := s.pending["__ready__"]
			if ok {
				delete(s.pending, "__ready__")
			}
			s.mu.Unlock()
			if ok {
				ch <- msg
			}
		case "assistant_say":
			s.handleAssistantSay(msg)
		case "tool_call":
			s.handleToolCall(msg)
		case "turn_end":
			s.handleTurnEnd(msg)
		case "error":
			s.log.Error("conv sidecar error", "msg", msg.Message)
		}
	}
}

// Run drives the call until ctx is canceled. Spawns goroutines for
// streaming TTS and background task event delivery.
func (s *Session) Run(ctx context.Context) error {
	utterances := s.deps.Transcriber.Utterances()
	hotkeys := s.deps.Hotkey.Events()
	taskEvents := s.deps.TaskManager.Events()
	speechOnsets := s.deps.SpeechOnsets
	if speechOnsets == nil {
		speechOnsets = make(chan struct{})
	}

	// Latest task state observed via TaskManager.Events; used when assembling
	// each user_say so the LLM sees current state in its prompt.
	currentTaskState := s.deps.TaskManager.Status()

	// On hotkey, send a synthetic seed user_say so the LLM produces a greeting.
	greet := func() {
		s.dispatchUserSay(ctx, "<call started>", currentTaskState)
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-hotkeys:
			s.log.Info("hotkey pressed")
			greet()

		case text, ok := <-utterances:
			if !ok {
				utterances = nil
				continue
			}
			s.log.Info("heard", "text", text)
			s.dispatchUserSay(ctx, text, currentTaskState)

		case ev, ok := <-taskEvents:
			if !ok {
				taskEvents = nil
				continue
			}
			currentTaskState = ev.State
			s.log.Info("task state", "kind", ev.State.Kind.String())

		case <-s.speakDone:
			s.cancelSpeak = nil

		case <-speechOnsets:
			if s.cancelSpeak != nil {
				s.log.Info("barge-in")
				s.cancelSpeak()
				s.cancelSpeak = nil
				s.markTurnCanceled()
			}
		}
	}
}

func (s *Session) dispatchUserSay(ctx context.Context, text string, state TaskState) {
	id := s.nextRequestID()
	s.setTurn(id)
	payload := userSayMsg{
		Type:      "user_say",
		ID:        id,
		Text:      text,
		TaskState: taskStateToPayload(state),
	}
	if err := s.send(payload); err != nil {
		s.log.Error("conv send user_say", "err", err)
	}
}

func (s *Session) setTurn(id string) {
	s.turnMu.Lock()
	s.currentTurnID = id
	s.turnCanceled = false
	s.turnMu.Unlock()
}

func (s *Session) markTurnCanceled() {
	s.turnMu.Lock()
	s.turnCanceled = true
	s.turnMu.Unlock()
}

func (s *Session) turnActive(id string) bool {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return id == s.currentTurnID && !s.turnCanceled
}

// assistantBuf accumulates streaming text; we flush on sentence boundary
// or turn end. Owned by handleAssistantSay/handleTurnEnd which run on
// the readLoop goroutine.
var sentenceTerminators = ".!?"

type assistantBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *assistantBuffer) appendAndFlush(chunk string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(chunk)
	full := b.buf.String()

	var out []string
	last := 0
	for i := 0; i < len(full); i++ {
		c := full[i]
		if strings.IndexByte(sentenceTerminators, c) >= 0 {
			seg := strings.TrimSpace(full[last : i+1])
			if seg != "" {
				out = append(out, seg)
			}
			last = i + 1
		}
	}
	if last > 0 {
		rem := full[last:]
		b.buf.Reset()
		b.buf.WriteString(rem)
	}
	return out
}

func (b *assistantBuffer) drain() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := strings.TrimSpace(b.buf.String())
	b.buf.Reset()
	return out
}

// One buffer per session; reset on each turn start.
var globalAssistantBuf = &assistantBuffer{}

func (s *Session) handleAssistantSay(msg inboundMsg) {
	if !s.turnActive(msg.ID) {
		return
	}
	sentences := globalAssistantBuf.appendAndFlush(msg.Text)
	for _, sent := range sentences {
		s.speakSentence(sent)
	}
}

func (s *Session) handleTurnEnd(msg inboundMsg) {
	if !s.turnActive(msg.ID) {
		globalAssistantBuf.drain()
		return
	}
	if rest := globalAssistantBuf.drain(); rest != "" {
		s.speakSentence(rest)
	}
}

// speakSentence enqueues a sentence to the Speaker. Speaker.Speak is
// blocking, so we run it in its own goroutine. cancelSpeak is updated
// by the goroutine via the Run loop (speakDone channel).
//
// In v1 we accept a small race: if barge-in fires between speakSentence
// goroutines, the second one may still run. Live testing will tell us
// whether this is acceptable.
func (s *Session) speakSentence(text string) {
	s.log.Info("speaking", "text", text)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelSpeak = cancel
	go func() {
		_ = s.deps.Speaker.Speak(ctx, text)
		cancel()
		select {
		case s.speakDone <- struct{}{}:
		default:
		}
	}()
}

func (s *Session) handleToolCall(msg inboundMsg) {
	go func() {
		result := s.runTool(msg.Name, msg.Args)
		raw, _ := json.Marshal(result)
		_ = s.send(toolResultMsg{
			Type:   "tool_result",
			CallID: msg.CallID,
			Result: raw,
		})
	}()
}

type toolOk struct {
	Ok bool `json:"ok"`
}
type toolErr struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
}

func (s *Session) runTool(name string, args json.RawMessage) any {
	switch name {
	case "start_task":
		var obj Objective
		if err := json.Unmarshal(args, &obj); err != nil {
			return toolErr{Ok: false, Error: fmt.Sprintf("bad args: %v", err)}
		}
		if err := s.deps.TaskManager.Start(context.Background(), obj); err != nil {
			return toolErr{Ok: false, Error: err.Error()}
		}
		return toolOk{Ok: true}

	case "reply_to_task":
		var a struct {
			Answer string `json:"answer"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return toolErr{Ok: false, Error: fmt.Sprintf("bad args: %v", err)}
		}
		if err := s.deps.TaskManager.Reply(a.Answer); err != nil {
			return toolErr{Ok: false, Error: err.Error()}
		}
		return toolOk{Ok: true}

	case "cancel_task":
		if err := s.deps.TaskManager.Cancel(); err != nil {
			return toolErr{Ok: false, Error: err.Error()}
		}
		return toolOk{Ok: true}

	case "task_status":
		st := s.deps.TaskManager.Status()
		return taskStateToPayload(st)

	default:
		return toolErr{Ok: false, Error: "unknown tool: " + name}
	}
}
```

- [ ] **Step 3: Verify it compiles**

```bash
cd /Users/ayusman/hale/freeman
go build ./internal/conv/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/conv/protocol.go internal/conv/session.go
git commit -m "feat(conv): event loop, tool routing, sentence chunker"
```

---

### Task 5: Wire conv into call.go and remove --fake-audio

**Files:**
- Modify: `cmd/freeman/call.go`

The `--fake-audio` path is removed entirely (it depended on the now-defunct call.Session and ScriptedPM). All remaining code goes through the new conv path.

- [ ] **Step 1: Replace `cmd/freeman/call.go`**

Read the existing file first to confirm structure:

```bash
cat cmd/freeman/call.go
```

Then replace its full content with:

```go
package main

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
	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/conv"
	"github.com/Renderix/freeman/internal/engine"
	"github.com/spf13/cobra"
)

var callCmd = &cobra.Command{
	Use:   "call",
	Short: "Start a Freeman voice call",
	Long: `Start a Freeman voice call. Uses real audio hardware (mic + speakers),
Whisper for STT, Kokoro for TTS, and a long-lived pi-coding-agent
chat session that can spawn background coding tasks on demand.

Requires pi-coding-agent subscription auth: run scripts/pi_login.sh
once before first use.`,
	RunE: runCall,
}

func runCall(cmd *cobra.Command, args []string) error {
	conf := config.LoadConfig(configFile)

	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
	defer func() {
		if err := mgr.Stop(); err != nil {
			logger.Error("whisper-server stop", "err", err)
		}
	}()

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

	// 6. STT Transcriber.
	client := stt.NewClient(mgr.BaseURL(), 10*time.Second)
	tr := stt.NewTranscriber(client, uttCh, 16000)
	tr.Run(ctx)
	defer tr.Stop()

	// 7. Speaker, muting VAD + Transcriber together.
	muter := &audio.MultiMuter{Muters: []audio.Muter{v, tr}}
	sp, err := playback.Open(actx, playback.Config{
		DeviceID: conf.Freeman.Audio.OutputDevice,
		ChunkMs:  50,
		Voice:    conf.TTS.DefaultVoice,
		Speed:    conf.TTS.DefaultSpeed,
	}, eng, muter)
	if err != nil {
		return fmt.Errorf("playback open: %w", err)
	}
	defer sp.Close()

	// 8. Hotkey.
	hk, err := hotkey.Open(hotkey.Config{
		Mode: conf.Freeman.Hotkey.Mode,
		Key:  conf.Freeman.Hotkey.Key,
	})
	if err != nil {
		return fmt.Errorf("hotkey open: %w", err)
	}
	defer hk.Stop()

	// 9. TaskManager (no task running yet).
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	taskMgr := conv.NewTaskManager(repoRoot)
	defer taskMgr.Close()

	// 10. Conv session.
	convSession, err := conv.NewSession(ctx, conv.Deps{
		Transcriber:  tr,
		Speaker:      sp,
		Hotkey:       hk,
		SpeechOnsets: v.SpeechOnsets(),
		TaskManager:  taskMgr,
		RepoRoot:     repoRoot,
		Model:        conf.Freeman.PM.Model,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("conv session: %w", err)
	}
	defer convSession.Close()

	fmt.Fprintln(os.Stderr, "freeman: ready")
	return convSession.Run(ctx)
}

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

- [ ] **Step 2: Verify the package compiles (will fail if the old call/pm packages have other importers)**

```bash
cd /Users/ayusman/hale/freeman
go build ./cmd/freeman/...
```

Expected: success. If there are unresolved imports of `internal/call` or `internal/pm` from elsewhere in cmd/freeman, find and remove them. (As of this plan there should be none.)

- [ ] **Step 3: Build the binary**

```bash
go build -o freeman ./cmd/freeman
```

Expected: success.

- [ ] **Step 4: Commit (the rest of the codebase still has internal/call and internal/pm — that's Task 6)**

```bash
git add cmd/freeman/call.go
git commit -m "feat(cmd): wire conv session, remove --fake-audio path"
```

---

### Task 6: Delete old packages

**Files:**
- Delete: `internal/call/` (entire directory)
- Delete: `internal/pm/` (entire directory)
- Delete: `sidecar/pm-sidecar.ts`
- Modify: `sidecar/package.json` (remove pm-sidecar script)

- [ ] **Step 1: Confirm nothing imports them**

```bash
cd /Users/ayusman/hale/freeman
grep -r "Renderix/freeman/internal/call" --include="*.go" .
grep -r "Renderix/freeman/internal/pm" --include="*.go" .
```

Expected: no matches. If anything matches, fix that file before deleting.

- [ ] **Step 2: Delete the directories and pm-sidecar.ts**

```bash
rm -rf internal/call internal/pm sidecar/pm-sidecar.ts
```

- [ ] **Step 3: Remove the pm-sidecar script from `sidecar/package.json`**

The scripts block should now read:

```json
"scripts": {
  "stub": "bun run stub.ts",
  "sidecar": "bun run sidecar.ts",
  "conv-sidecar": "bun run conv-sidecar.ts"
}
```

- [ ] **Step 4: Verify Go builds clean**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Verify TS typechecks clean**

```bash
cd sidecar
bunx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Rebuild the freeman binary**

```bash
cd /Users/ayusman/hale/freeman
go build -o freeman ./cmd/freeman
```

Expected: success.

- [ ] **Step 7: Commit**

```bash
git add -A internal/call internal/pm sidecar/pm-sidecar.ts sidecar/package.json
git commit -m "chore: delete narrow intake state machine and PM layer"
```

---

### Task 7: Live smoke test

No code changes. Manual verification only.

- [ ] **Step 1: Confirm pi auth is set up**

```bash
cat ~/.pi/agent/auth.json
```

Expected: a JSON file with at least an `anthropic` entry. If missing, run `./scripts/pi_login.sh`, do `/login` in the TUI, and `/quit`.

- [ ] **Step 2: Run the binary**

```bash
cd /Users/ayusman/hale/freeman
./freeman call
```

Expected stderr:
```
time=… level=INFO msg="audio: context ready"
freeman: warming up whisper…
freeman: ready
```

- [ ] **Step 3: Press Enter (the TTY hotkey) to start a call**

Expected: a `hotkey pressed` log followed by an `assistant_say` stream and TTS playback of a casual greeting like "hey, what's up?"

- [ ] **Step 4: Smoke test scenarios**

Try each of the following and confirm Freeman behaves correctly. Note any failures with the stderr lines that surrounded them.

1. **General chat:** "What's the weather like in Tokyo right now?" — Freeman should respond from world knowledge in one or two sentences. No tool call.
2. **Project question:** "What is this project about?" — Freeman should answer briefly using the injected README/go.mod context. No tool call.
3. **Out-of-context project question:** "What's in internal/audio/playback?" — Freeman should honestly say the project context doesn't include that level of detail and offer to spin up a task. No tool call yet.
4. **Explicit task start:** "Add a debug flag to the call command" — Freeman should call `start_task` and acknowledge ("ok, starting that"). The task sidecar should spawn (you'll see no obvious stderr for it because the task is independent, but state will eventually transition).
5. **Mid-task chat:** while the task is running, ask "What's the moon made of?" — Freeman should chat about it normally. The next user turn should optionally reference task state if it changed.
6. **Task needs input:** when the task asks a question, Freeman should weave it into its next response on its own (because the next turn's `task_state` carries the question).
7. **Reply to task:** answer the task's question. Freeman should call `reply_to_task` and acknowledge. Task continues.
8. **Cancel task:** "actually cancel that". Freeman should call `cancel_task` and confirm.
9. **Barge-in:** while Freeman is speaking a long answer, start talking. TTS should cancel and Freeman should respond to your new utterance.

- [ ] **Step 5: Exit cleanly**

Press Ctrl-C. Expected: clean shutdown, no zombie subprocesses.

```bash
ps aux | grep -E "bun run sidecar|conv-sidecar|whisper-server" | grep -v grep
```

Expected: no output (all child processes exited).

- [ ] **Step 6: Report results**

Note any scenario above that didn't work as expected, paste the relevant stderr lines, and stop here. Iteration on prompts/code/tools happens after this plan completes.

---

## Spec Coverage Map

| Spec section | Task |
|---|---|
| ConvSession (long-lived chat) | Task 4 (session.go) |
| TaskManager (single-task supervisor) | Task 2 |
| Project context blurb | Task 1 |
| conv-sidecar.ts JSONL protocol | Task 3 |
| Custom tools: start_task, reply_to_task, cancel_task, task_status | Task 3 (TS), Task 4 (Go routing) |
| Task state injected per turn | Task 4 (dispatchUserSay), Task 3 (formatTaskState) |
| Voice-tuned system prompt | Task 4 (DefaultSystemPrompt) |
| Sentence chunker for streaming TTS | Task 4 (assistantBuffer) |
| Barge-in cancels in-flight Speak and discards remaining stream | Task 4 (markTurnCanceled, turnActive) |
| Wire into runCall | Task 5 |
| Delete old call/pm/pm-sidecar | Task 6 |
| Live smoke test | Task 7 |
| --fake-audio path removed | Task 5 |
