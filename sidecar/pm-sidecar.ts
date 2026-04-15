// PM sidecar for Freeman. A long-lived Bun process that handles intake
// and route PM calls for the Go side over JSONL on stdin/stdout.
//
// Auth: uses pi-coding-agent's subscription auth from ~/.pi/agent/auth.json.
// Run `pi auth login` before first use.

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

type IntakeCmd = {
  type: "intake";
  id: string;
  user_text: string;
  interrupted_text?: string;
  model: string;
};
type RouteCmd = {
  type: "route";
  id: string;
  objective_goal: string;
  question: string;
  interrupted_text?: string;
  model: string;
};
type ResetCmd = { type: "reset" };
type InCmd = IntakeCmd | RouteCmd | ResetCmd;

type IntakeResult =
  | { needs_more: true; question: string }
  | {
      needs_more: false;
      objective: {
        goal: string;
        acceptance_criteria: string[];
        constraints: string[];
        notes: string[];
        model_hint: string;
        spoken_summary: string;
      };
    };

type RouteResult =
  | { answer_inline: string; confidence: number }
  | { spoken_question: string };

// ─── System prompts (kept in sync with internal/pm/prompts.go) ──────────────

const intakeSystemPrompt = `You are Freeman's requirements analyst. Your job is to turn a voice request into a precise engineering objective.

Rules:
- Ask exactly one follow-up question at a time if you need more information.
- When you have enough to write a complete spec, call complete_objective immediately.
- Treat "just go", "ship it", "start", or any clear force-start phrase as complete_objective immediately — use whatever you have.
- Classify model_hint as "opus" for cross-cutting refactors, architectural changes, or subtle multi-file reasoning; "sonnet" for everything else.
- The spoken_summary must be one sentence suitable for text-to-speech — no markdown, no lists.
- You MUST call exactly one tool per turn. Do not output plain text.

Context hint: when interrupted_text is present in a user message, the user was interrupting Freeman who was in the middle of saying that text. Treat the user's utterance as a direct reply to interrupted_text.`;

const routerSystemPrompt = `You are Freeman's routing assistant. A coding agent is executing a task and has asked the user a yes/no or short-answer question.

Rules:
- If you can answer the question confidently from the objective, transcript, and common sense, call answer_inline with a direct answer and your confidence (0.0-1.0).
- If you are not confident, or the question requires user judgment, call escalate with a spoken_question rephrasing the agent's question naturally for text-to-speech (one sentence, no markdown).
- Confidence below 0.8 means escalate regardless.
- You MUST call exactly one tool. Do not output plain text.

Context hint: when interrupted_text is present, the user was interrupting Freeman's speech. Factor that into your answer.`;

// ─── Tool definitions (args captured via subscribe; execute is a no-op) ─────

const askFollowupTool = defineTool({
  name: "ask_followup",
  label: "Ask Followup",
  description: "Ask the user one follow-up question to clarify their request.",
  parameters: Type.Object({
    question: Type.String({ description: "The follow-up question to ask." }),
  }),
  execute: async () => ({ content: [{ type: "text", text: "ok" }], details: {} }),
});

const completeObjectiveTool = defineTool({
  name: "complete_objective",
  label: "Complete Objective",
  description:
    "Signal that you have enough information and provide the completed engineering objective.",
  parameters: Type.Object({
    goal: Type.String(),
    acceptance_criteria: Type.Array(Type.String()),
    constraints: Type.Array(Type.String()),
    notes: Type.Array(Type.String()),
    model_hint: Type.String(),
    spoken_summary: Type.String(),
  }),
  execute: async () => ({ content: [{ type: "text", text: "ok" }], details: {} }),
});

const answerInlineTool = defineTool({
  name: "answer_inline",
  label: "Answer Inline",
  description: "Answer the agent's question directly without asking the user.",
  parameters: Type.Object({
    answer: Type.String(),
    confidence: Type.Number(),
  }),
  execute: async () => ({ content: [{ type: "text", text: "ok" }], details: {} }),
});

const escalateTool = defineTool({
  name: "escalate",
  label: "Escalate",
  description: "Ask the user this question out loud because it requires judgment.",
  parameters: Type.Object({
    spoken_question: Type.String(),
  }),
  execute: async () => ({ content: [{ type: "text", text: "ok" }], details: {} }),
});

// ─── Helpers ────────────────────────────────────────────────────────────────

function send(out: Writable, obj: Record<string, unknown>): void {
  out.write(JSON.stringify(obj) + "\n");
}

function promptIntake(userText: string, interruptedText: string | undefined): string {
  if (interruptedText) {
    return `[interrupted: ${JSON.stringify(interruptedText)}] ${userText}`;
  }
  return userText;
}

function promptRoute(
  objectiveGoal: string,
  question: string,
  interruptedText: string | undefined,
): string {
  let text = `Objective: ${objectiveGoal}\n\nQuestion: ${question}`;
  if (interruptedText) {
    text += `\n\nNote: Freeman was interrupted saying ${JSON.stringify(interruptedText)} when the agent asked this question.`;
  }
  return text;
}

function makeLoader(systemPrompt: string): DefaultResourceLoader {
  return new DefaultResourceLoader({
    systemPromptOverride: () => systemPrompt,
    appendSystemPromptOverride: () => [],
  });
}

// ─── Session runner — exported for testing ──────────────────────────────────

export interface PMDeps {
  createSession?: typeof createAgentSession;
}

export function runPMSidecar(
  inp: Readable,
  out: Writable,
  deps: PMDeps = {},
): void {
  const createSession = deps.createSession ?? createAgentSession;

  // Long-lived intake session; reset on "reset" command or first intake.
  let intakeSession: AgentSession | null = null;
  let intakeModel: string | null = null;

  async function ensureIntakeSession(model: string): Promise<AgentSession> {
    if (intakeSession && intakeModel === model) {
      return intakeSession;
    }
    if (intakeSession) {
      intakeSession.dispose();
      intakeSession = null;
    }
    const loader = makeLoader(intakeSystemPrompt);
    await loader.reload();
    const { session } = await createSession({
      model: getModel("anthropic", model as never),
      tools: [],
      customTools: [askFollowupTool, completeObjectiveTool],
      resourceLoader: loader,
      sessionManager: SessionManager.inMemory(),
    });
    intakeSession = session;
    intakeModel = model;
    return session;
  }

  async function runIntake(cmd: IntakeCmd): Promise<void> {
    try {
      const session = await ensureIntakeSession(cmd.model);
      const ref: { value: IntakeResult | null } = { value: null };
      const unsubscribe = session.subscribe((event: any) => {
        if (event.type === "tool_execution_start") {
          if (event.toolName === "ask_followup") {
            ref.value = { needs_more: true, question: event.args.question };
          } else if (event.toolName === "complete_objective") {
            const a = event.args;
            ref.value = {
              needs_more: false,
              objective: {
                goal: a.goal,
                acceptance_criteria: a.acceptance_criteria ?? [],
                constraints: a.constraints ?? [],
                notes: a.notes ?? [],
                model_hint: a.model_hint ?? "sonnet",
                spoken_summary: a.spoken_summary ?? "",
              },
            };
          }
        }
      });

      try {
        await session.prompt(promptIntake(cmd.user_text, cmd.interrupted_text));
      } finally {
        unsubscribe();
      }

      const r = ref.value;
      if (!r) {
        send(out, {
          type: "intake_result",
          id: cmd.id,
          needs_more: true,
          question: "sorry, one moment.",
        });
        return;
      }

      if (r.needs_more) {
        send(out, {
          type: "intake_result",
          id: cmd.id,
          needs_more: true,
          question: r.question,
        });
      } else {
        send(out, {
          type: "intake_result",
          id: cmd.id,
          needs_more: false,
          objective: r.objective,
        });
      }
    } catch (err: unknown) {
      send(out, {
        type: "error",
        id: cmd.id,
        message: err instanceof Error ? err.message : String(err),
      });
    }
  }

  async function runRoute(cmd: RouteCmd): Promise<void> {
    try {
      const loader = makeLoader(routerSystemPrompt);
      await loader.reload();
      const { session } = await createSession({
        model: getModel("anthropic", cmd.model as never),
        tools: [],
        customTools: [answerInlineTool, escalateTool],
        resourceLoader: loader,
        sessionManager: SessionManager.inMemory(),
      });

      const ref: { value: RouteResult | null } = { value: null };
      const unsubscribe = session.subscribe((event: any) => {
        if (event.type === "tool_execution_start") {
          if (event.toolName === "answer_inline") {
            ref.value = {
              answer_inline: event.args.answer,
              confidence: event.args.confidence,
            };
          } else if (event.toolName === "escalate") {
            ref.value = { spoken_question: event.args.spoken_question };
          }
        }
      });

      try {
        await session.prompt(
          promptRoute(cmd.objective_goal, cmd.question, cmd.interrupted_text),
        );
      } finally {
        unsubscribe();
        session.dispose();
      }

      const r = ref.value;
      if (!r) {
        send(out, {
          type: "route_result",
          id: cmd.id,
          spoken_question: cmd.question,
        });
        return;
      }

      if ("answer_inline" in r) {
        send(out, {
          type: "route_result",
          id: cmd.id,
          answer_inline: r.answer_inline,
          confidence: r.confidence,
        });
      } else {
        send(out, {
          type: "route_result",
          id: cmd.id,
          spoken_question: r.spoken_question,
        });
      }
    } catch (err: unknown) {
      send(out, {
        type: "error",
        id: cmd.id,
        message: err instanceof Error ? err.message : String(err),
      });
    }
  }

  function runReset(): void {
    if (intakeSession) {
      intakeSession.dispose();
      intakeSession = null;
      intakeModel = null;
    }
  }

  const rl = readline.createInterface({ input: inp, terminal: false });
  rl.on("line", (raw: string) => {
    let cmd: InCmd;
    try {
      cmd = JSON.parse(raw) as InCmd;
    } catch {
      send(out, { type: "error", message: `bad json: ${raw}` });
      return;
    }
    if (cmd.type === "intake") {
      void runIntake(cmd);
    } else if (cmd.type === "route") {
      void runRoute(cmd);
    } else if (cmd.type === "reset") {
      runReset();
    }
  });
}

// Entry point when run directly.
if (import.meta.main) {
  runPMSidecar(process.stdin, process.stdout);
}
