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
