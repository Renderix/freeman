// Stub sidecar for Plan 1. Reads JSONL from stdin, writes JSONL to stdout.
// On start: emits assistant_text, then ask_user, waits for reply, emits done.

import * as readline from "node:readline";

type StartMsg = { type: "start"; objective: unknown };
type AskUserReplyMsg = { type: "ask_user_reply"; id: string; answer: string };
type CancelMsg = { type: "cancel" };
type InMsg = StartMsg | AskUserReplyMsg | CancelMsg;

function send(obj: Record<string, unknown>): void {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

async function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

const rl = readline.createInterface({ input: process.stdin });
const pendingReplies = new Map<string, (answer: string) => void>();
let canceled = false;

rl.on("line", (raw: string) => {
  let msg: InMsg;
  try {
    msg = JSON.parse(raw) as InMsg;
  } catch {
    send({ type: "error", message: `bad json: ${raw}` });
    return;
  }
  if (msg.type === "start") {
    void runSession();
  } else if (msg.type === "ask_user_reply") {
    const cb = pendingReplies.get(msg.id);
    if (cb) {
      pendingReplies.delete(msg.id);
      cb(msg.answer);
    }
  } else if (msg.type === "cancel") {
    canceled = true;
    process.exit(0);
  }
});

async function runSession(): Promise<void> {
  send({ type: "assistant_text", text: "thinking..." });
  await sleep(200);
  if (canceled) return;

  const id = "q-" + Date.now();
  const askPromise = new Promise<string>((resolve) => {
    pendingReplies.set(id, resolve);
  });
  send({ type: "ask_user", id, question: "should i use the existing client?" });
  const answer = await askPromise;
  if (canceled) return;

  send({ type: "assistant_text", text: `got answer: ${answer}. proceeding.` });
  await sleep(200);
  if (canceled) return;

  send({ type: "done", summary: "stub edited 0 files and made coffee" });
  process.exit(0);
}
