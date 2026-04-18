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

type ToolActivityEntry = {
  tool: string;
  path?: string;
  command?: string;
  ok: boolean;
};

type TaskStateMsg =
  | { state: "none" }
  | { state: "running"; activity_log?: ToolActivityEntry[] }
  | { state: "needs_input"; question: string; activity_log?: ToolActivityEntry[] }
  | { state: "done"; summary: string; activity_log?: ToolActivityEntry[] }
  | { state: "failed"; message: string; activity_log?: ToolActivityEntry[] };

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
type TaskUpdateMsg = {
  type: "task_update";
  id: string;
  task_state: TaskStateMsg;
};
type ShutdownMsg = { type: "shutdown" };
type InMsg = InitMsg | UserSayMsg | ToolResultMsg | TaskUpdateMsg | ShutdownMsg;

function send(out: Writable, obj: Record<string, unknown>): void {
  out.write(JSON.stringify(obj) + "\n");
}

function formatTaskState(s: TaskStateMsg): string {
  let base: string;
  switch (s.state) {
    case "none":
      return "no background task";
    case "running":
      base = "background task is running";
      break;
    case "needs_input":
      base = `background task is waiting for an answer to: ${s.question}`;
      break;
    case "done":
      base = `background task finished. summary: ${s.summary}`;
      break;
    case "failed":
      base = `background task failed: ${s.message}`;
      break;
  }

  const log = "activity_log" in s ? s.activity_log : undefined;
  if (log && log.length > 0) {
    const lines = log.map((a) => {
      const target = a.path || a.command || "";
      const status = a.ok ? "ok" : "FAILED";
      return `  ${a.tool} ${target} [${status}]`;
    });
    base += "\nactivity:\n" + lines.join("\n");
  } else if (s.state === "done" || s.state === "failed") {
    base += "\nactivity: no tool calls were recorded for this task";
  }
  return base;
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
  let unsubscribeAssistant: (() => void) | null = null;
  let currentUserSayId: string | null = null;
  const pendingToolCalls = new Map<string, (result: any) => void>();

  function makeTool(name: string, description: string, paramSchema: any) {
    return defineTool({
      name,
      label: name,
      description,
      parameters: paramSchema,
      execute: async (_toolCallId, args) => {
        const turnId = currentUserSayId;
        if (turnId === null) {
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
            id: turnId,
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

    // IMPORTANT: agentsFilesOverride must return an empty array. Without
    // it pi-coding-agent auto-discovers CLAUDE.md / AGENTS.md from the
    // working directory and appends them to the system prompt as
    // "Project Context". In a coding repo that file is a full markdown
    // style guide for a coding assistant — the model treats it as
    // authoritative and starts answering in bullets, bold, and emojis,
    // which is the opposite of what a voice TTS reply needs. We already
    // pass our own curated project context inline above, so we don't
    // need the library's discovery.
    const loader = new DefaultResourceLoader({
      systemPromptOverride: () => fullSystemPrompt,
      appendSystemPromptOverride: () => [],
      agentsFilesOverride: () => ({ agentsFiles: [] }),
    });
    await loader.reload();

    unsubscribeAssistant?.();
    unsubscribeAssistant = null;

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
    unsubscribeAssistant = session.subscribe((event: any) => {
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

  async function runTaskUpdate(msg: TaskUpdateMsg): Promise<void> {
    if (!session) return;
    const id = msg.id;
    currentUserSayId = id;
    const promptText = `[background task: ${formatTaskState(msg.task_state)}]`;
    try {
      await session.prompt(promptText);
      send(out, { type: "turn_end", id });
    } catch (err: unknown) {
      send(out, {
        type: "error",
        id,
        message: err instanceof Error ? err.message : String(err),
      });
    } finally {
      currentUserSayId = null;
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
    else if (msg.type === "task_update") void runTaskUpdate(msg);
    else if (msg.type === "shutdown") void runShutdown();
  });
}

if (import.meta.main) {
  runConvSidecar(process.stdin, process.stdout);
}
