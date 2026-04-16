// Real sidecar for Freeman. Uses @mariozechner/pi-coding-agent to run a
// Claude session for the given objective. Communicates with the Go parent
// via JSONL on stdin/stdout. The protocol matches stub.ts.

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
import type { AgentMessage } from "@mariozechner/pi-agent-core";

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

function extractFinalText(messages: AgentMessage[]): string {
  for (let i = messages.length - 1; i >= 0; i--) {
    const msg = messages[i];
    if (msg && msg.role === "assistant") {
      const parts: string[] = [];
      for (const c of msg.content) {
        if ((c as { type: string }).type === "text") {
          parts.push((c as { text: string }).text);
        }
      }
      if (parts.length > 0) return parts.join(" ").trim();
    }
  }
  return "done";
}

/**
 * runSidecar wires the JSONL protocol to a pi-coding-agent session.
 * Exported for testing; main() calls it with process.stdin/stdout.
 * The `deps` arg lets tests inject a fake createAgentSession/exit.
 */
export interface SidecarDeps {
  createSession?: typeof createAgentSession;
  exit?: (code: number) => void;
}

export function runSidecar(
  inp: Readable,
  out: Writable,
  deps: SidecarDeps = {},
): void {
  const createSession = deps.createSession ?? createAgentSession;
  const exit = deps.exit ?? ((code: number) => process.exit(code));

  const pendingReplies = new Map<string, (answer: string) => void>();
  let currentSession: AgentSession | null = null;
  let started = false;

  const askUserTool = defineTool({
    name: "ask_user",
    label: "Ask User",
    description:
      "Ask the user a clarifying question and wait for their spoken reply.",
    parameters: Type.Object({
      question: Type.String({ description: "The question to ask the user." }),
    }),
    execute: async (_toolCallId, params) => {
      const id = crypto.randomUUID();
      const answer = await new Promise<string>((resolve) => {
        pendingReplies.set(id, resolve);
        send(out, { type: "ask_user", id, question: params.question });
      });
      return {
        content: [{ type: "text", text: answer }],
        details: {},
      };
    },
  });

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
      if (started) return;
      started = true;
      void runSession(msg);
    } else if (msg.type === "ask_user_reply") {
      const resolve = pendingReplies.get(msg.id);
      if (resolve) {
        pendingReplies.delete(msg.id);
        resolve(msg.answer);
      }
    } else if (msg.type === "cancel") {
      void (async () => {
        if (currentSession) {
          try {
            await currentSession.abort();
          } catch {
            // ignore abort errors
          }
        }
        exit(0);
      })();
    }
  });

  // Emit debug diagnostics only when SIDECAR_DEBUG=1 is set.
  // Without this guard these lines bleed directly into Freeman's stderr
  // (the Go parent sets cmd.Stderr = os.Stderr) and appear as untagged
  // "trading log lines" when the task summary contains domain terminology.
  const debug = process.env.SIDECAR_DEBUG === "1";

  async function runSession(msg: StartMsg): Promise<void> {
    try {
      if (debug) console.error("[sidecar] runSession starting, model=%s", msg.objective.model);
      const model = getModel("anthropic", msg.objective.model as never);
      const loader = new DefaultResourceLoader({
        systemPromptOverride: () => buildPrompt(msg.objective),
        appendSystemPromptOverride: () => [],
      });
      await loader.reload();
      if (debug) console.error("[sidecar] creating agent session");
      const { session } = await createSession({
        model,
        customTools: [askUserTool],
        resourceLoader: loader,
        sessionManager: SessionManager.inMemory(),
      });
      currentSession = session;
      if (debug) console.error("[sidecar] session created, prompting");

      let finalMessages: AgentMessage[] = [];
      const pendingArgs = new Map<string, any>();
      const unsubscribe = session.subscribe((event) => {
        if (event.type === "agent_end") {
          finalMessages = event.messages;
        } else if (event.type === "tool_execution_start") {
          pendingArgs.set(event.toolCallId, event.args);
        } else if (event.type === "tool_execution_end") {
          const args = pendingArgs.get(event.toolCallId) ?? {};
          pendingArgs.delete(event.toolCallId);
          const entry = extractActivity({
            toolName: event.toolName,
            args,
            isError: event.isError,
          });
          if (entry) {
            send(out, { type: "tool_activity", ...entry });
          }
        }
      });

      try {
        await session.prompt(buildPrompt(msg.objective));
      } finally {
        unsubscribe();
      }

      const summary = extractFinalText(finalMessages);
      if (debug) console.error("[sidecar] done, summary=%s", summary.slice(0, 120));
      send(out, { type: "done", summary });
      exit(0);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      // Always emit errors — they indicate a real failure, not routine progress.
      console.error("[sidecar] error: %s", message);
      send(out, { type: "error", message });
      exit(1);
    }
  }
}

function extractActivity(event: {
  toolName: string;
  args: any;
  isError: boolean;
}): { tool: string; path: string; command: string; ok: boolean } | null {
  const { toolName, args, isError } = event;
  switch (toolName) {
    case "edit":
    case "write":
    case "read":
      return { tool: toolName, path: args?.file_path ?? args?.path ?? "", command: "", ok: !isError };
    case "bash":
      return { tool: "bash", path: "", command: (args?.command ?? "").slice(0, 80), ok: !isError };
    case "grep":
    case "find":
    case "ls":
      return { tool: toolName, path: args?.path ?? args?.directory ?? "", command: "", ok: !isError };
    default:
      return null;
  }
}

if (import.meta.main) {
  runSidecar(process.stdin, process.stdout);
}
