import { describe, test, expect, mock } from "bun:test";
import { PassThrough } from "node:stream";
import { runSidecar, type SidecarDeps } from "./sidecar";

// ─── Helpers ─────────────────────────────────────────────────────────────────

function makePipes(): { stdin: PassThrough; stdout: PassThrough } {
  return { stdin: new PassThrough(), stdout: new PassThrough() };
}

function writeJSON(stream: PassThrough, obj: unknown): void {
  stream.write(JSON.stringify(obj) + "\n");
}

function readJSONLines(stream: PassThrough): AsyncGenerator<any> {
  let buf = "";
  const queue: any[] = [];
  let resolver: ((v: IteratorResult<any>) => void) | null = null;

  stream.on("data", (chunk: Buffer) => {
    buf += chunk.toString();
    let idx: number;
    while ((idx = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, idx).trim();
      buf = buf.slice(idx + 1);
      if (!line) continue;
      const parsed = JSON.parse(line);
      if (resolver) {
        resolver({ value: parsed, done: false });
        resolver = null;
      } else {
        queue.push(parsed);
      }
    }
  });

  return (async function* () {
    while (true) {
      if (queue.length > 0) {
        yield queue.shift()!;
        continue;
      }
      yield await new Promise<any>((resolve) => {
        resolver = (v) => resolve(v.value);
      });
    }
  })();
}

type FakeSession = {
  subscribe: ReturnType<typeof mock>;
  prompt: ReturnType<typeof mock>;
  abort: ReturnType<typeof mock>;
  _emit: (event: any) => void;
};

function makeFakeSession(): FakeSession {
  const listeners: Array<(ev: any) => void> = [];
  const session: FakeSession = {
    subscribe: mock((l: (ev: any) => void) => {
      listeners.push(l);
      return () => {
        const i = listeners.indexOf(l);
        if (i >= 0) listeners.splice(i, 1);
      };
    }),
    prompt: mock(async (_text: string) => {}),
    abort: mock(async () => {}),
    _emit: (ev) => listeners.forEach((l) => l(ev)),
  };
  return session;
}

const startMsg = {
  type: "start",
  objective: {
    goal: "add feature flag",
    acceptance_criteria: ["flag defaults off"],
    constraints: [],
    notes: [],
    model: "claude-sonnet-4-6",
  },
};

function fakeAssistantMessage(text: string) {
  return {
    role: "assistant",
    content: [{ type: "text", text }],
    api: "anthropic-messages",
    provider: "anthropic",
    model: "claude-sonnet-4-6",
    usage: {
      input: 0,
      output: 0,
      cacheRead: 0,
      cacheWrite: 0,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    },
    stopReason: "stop",
    timestamp: Date.now(),
  };
}

// ─── Tests ───────────────────────────────────────────────────────────────────

describe("sidecar", () => {
  test("done emits summary after session completes", async () => {
    const session = makeFakeSession();
    session.prompt.mockImplementation(async () => {
      session._emit({
        type: "agent_end",
        messages: [fakeAssistantMessage("I edited 2 files. All tests pass.")],
      });
    });

    const exitCodes: number[] = [];
    const deps: SidecarDeps = {
      createSession: mock(async () => ({
        session: session as any,
        extensionsResult: { extensions: [], errors: [] } as any,
      })) as any,
      exit: (code) => exitCodes.push(code),
    };

    const { stdin, stdout } = makePipes();
    const events = readJSONLines(stdout);

    runSidecar(stdin, stdout, deps);
    writeJSON(stdin, startMsg);

    const done = (await events.next()).value;
    expect(done.type).toBe("done");
    expect(done.summary).toBe("I edited 2 files");
    expect(exitCodes).toEqual([0]);
  });

  test("cancel calls abort and exits cleanly", async () => {
    const session = makeFakeSession();
    // prompt never resolves — simulates in-flight session
    session.prompt.mockImplementation(() => new Promise<void>(() => {}));

    const exitCodes: number[] = [];
    const deps: SidecarDeps = {
      createSession: mock(async () => ({
        session: session as any,
        extensionsResult: { extensions: [], errors: [] } as any,
      })) as any,
      exit: (code) => exitCodes.push(code),
    };

    const { stdin, stdout } = makePipes();
    runSidecar(stdin, stdout, deps);

    writeJSON(stdin, startMsg);
    // wait a tick for createSession to resolve and session to be stored
    await new Promise((r) => setTimeout(r, 10));

    writeJSON(stdin, { type: "cancel" });
    await new Promise((r) => setTimeout(r, 10));

    expect(session.abort).toHaveBeenCalled();
    expect(exitCodes).toEqual([0]);
  });

  test("session error emits error message and exits non-zero", async () => {
    const exitCodes: number[] = [];
    const deps: SidecarDeps = {
      createSession: mock(async () => {
        throw new Error("no api key");
      }) as any,
      exit: (code) => exitCodes.push(code),
    };

    const { stdin, stdout } = makePipes();
    const events = readJSONLines(stdout);

    runSidecar(stdin, stdout, deps);
    writeJSON(stdin, startMsg);

    const err = (await events.next()).value;
    expect(err.type).toBe("error");
    expect(err.message).toBe("no api key");
    expect(exitCodes).toEqual([1]);
  });
});
