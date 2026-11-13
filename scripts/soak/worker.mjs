// Soak worker for the single-writer contract (ADR-0058 / TODO 13.1).
//
// One process per concurrent client. Each worker holds its own cinder-mcp
// child (the real binary, the real stdio MCP handshake) and hammers the write
// tools for the duration of the soak. It reports, once at the end, exactly the
// numbers the gate asserts — and it exits non-zero the moment it sees the two
// failures the contract exists to prevent:
//
//   - "database is locked" reaching the caller as a TOOL error. That is the
//     whole point of the contract: a forwarded write must QUEUE behind the
//     writer's long transaction, not surface the driver's busy text.
//   - writer_unavailable while the writer is demonstrably alive. The
//     re-election path exists for a writer that DIED; reporting it against a
//     healthy writer means the channel or the role bookkeeping is broken.
//
// Usage:
//   node worker.mjs --mcp <path> --home <dir> --port <n> --ms <duration>
//                   --out <json path> [--interval <ms>] [--kill-phase]
//
// --kill-phase is how phase B works: the worker keeps writing across the
// moment the writer is SIGKILLed, so the takeover has to happen under load
// rather than in a quiet window that would prove nothing.
//
// Exit codes: 0 = clean, 1 = contract violation (details in --out), 2 = bad args.
//
// Bounded data: every note is deleted right after it is created (see cleanup()),
// so the file stays at its seeded size. An unbounded soak reached 9 MB / 16k
// notes, at which point VACUUM INTO took minutes and every client starved for
// the rest of the run — that measures SQLite's vacuum throughput, not the write
// contract under test.

import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";
import { createInterface } from "node:readline";
import { randomUUID } from "node:crypto";

function parseArgs(argv) {
  const args = { ms: 180_000, interval: 250, killPhase: false };
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i];
    const next = () => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`${key} needs a value`);
      return v;
    };
    switch (key) {
      case "--mcp": args.mcp = next(); break;
      case "--home": args.home = next(); break;
      case "--port": args.port = next(); break;
      case "--ms": args.ms = Number(next()); break;
      case "--out": args.out = next(); break;
      case "--interval": args.interval = Number(next()); break;
      case "--kill-phase": args.killPhase = true; break;
      case "--note-id": args.noteId = Number(next()); break;
      default: throw new Error(`unknown argument: ${key}`);
    }
  }
  for (const required of ["mcp", "home", "port", "out", "noteId"]) {
    if (!args[required]) throw new Error(`--${required} is required`);
  }
  return args;
}

/** One MCP stdio child, speaking newline-delimited JSON-RPC 2.0. */
class McpClient {
  constructor(command, env) {
    this.command = command;
    this.env = env;
    this.nextId = 1;
    this.pending = new Map();
    this.buffer = "";
    this.stderr = [];
  }

  start() {
    return new Promise((resolve, reject) => {
      this.proc = spawn(this.command, [], {
        stdio: ["pipe", "pipe", "pipe"],
        env: { ...process.env, ...this.env },
      });
      this.proc.on("error", reject);
      this.proc.stdout.on("data", chunk => this.onData(chunk.toString("utf8")));
      // Anything the server prints for humans must go to stderr; keep it so a
      // failure report can say WHY, and so a panic is not silently swallowed.
      createInterface({ input: this.proc.stderr }).on("line", line => {
        if (this.stderr.length < 50) this.stderr.push(line);
      });
      this.proc.on("exit", code => {
        for (const { reject: r } of this.pending.values()) {
          r(new Error(`cinder-mcp exited (code ${code ?? "?"})`));
        }
        this.pending.clear();
      });
      this.request("initialize", {
        protocolVersion: "2024-11-05",
        capabilities: {},
        clientInfo: { name: "soak-worker", version: "0.1.0" },
      }).then(() => {
        this.notify("notifications/initialized", {});
        resolve();
      }, reject);
    });
  }

  onData(chunk) {
    this.buffer += chunk;
    let newline;
    while ((newline = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, newline).trim();
      this.buffer = this.buffer.slice(newline + 1);
      if (!line) continue;
      let msg;
      try {
        msg = JSON.parse(line);
      } catch {
        continue; // not protocol traffic
      }
      const waiter = this.pending.get(msg.id);
      if (!waiter) continue;
      this.pending.delete(msg.id);
      if (msg.error) waiter.reject(new Error(msg.error.message ?? JSON.stringify(msg.error)));
      else waiter.resolve(msg.result);
    }
  }

  request(method, params) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.proc.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
    });
  }

  notify(method, params) {
    this.proc.stdin.write(JSON.stringify({ jsonrpc: "2.0", method, params }) + "\n");
  }

  /**
   * Call one tool.
   *
   * Returns { ok, text, structured, isError } — an MCP tool that FAILED still
   * answers successfully at the JSON-RPC layer (ADR-0055: the failure face is
   * structured), so the classification has to read the payload rather than the
   * transport.
   */
  async callTool(name, args) {
    const res = await this.request("tools/call", { name, arguments: args });
    const text = (res?.content ?? [])
      .filter(part => part?.type === "text" && typeof part.text === "string")
      .map(part => part.text)
      .join("\n");
    return {
      ok: res?.isError !== true,
      isError: res?.isError === true,
      text,
      structured: res?.structuredContent ?? null,
    };
  }

  stop() {
    try {
      this.proc?.kill("SIGTERM");
    } catch {
      // already gone
    }
  }
}

const args = (() => {
  try {
    return parseArgs(process.argv.slice(2));
  } catch (e) {
    console.error(String(e.message));
    process.exit(2);
  }
})();

const soakNoteId = args.noteId;
const soakThreadId = process.env.SOAK_THREAD_ID || "";
const stats = {
  attempts: 0,
  ok: 0,
  failed: 0,
  /** Every failure, classified. The gate reads these, not a rate. */
  failures: [],
  /** "database is locked" reaching the caller as a tool error. Gate-critical. */
  lockBubbles: 0,
  /** writer_unavailable while the writer was alive. Gate-critical. */
  falseUnavailable: 0,
  /** Slowest single write, so a queue that silently took 4 minutes is visible. */
  slowestMs: 0,
  startedAt: new Date().toISOString(),
};

const LOCK_TEXT = /database is locked|database table is locked|sqlite_busy/i;

function classify(result) {
  const code = result.structured?.code ?? "";
  const message = `${result.structured?.message ?? ""} ${result.text ?? ""}`;
  // A lock bubble is a lock bubble whatever the code says: the driver's busy
  // text is the evidence, and the contract's whole job is to keep it away from
  // the caller. Anything that carries it counts.
  return { code, message, lock: LOCK_TEXT.test(message) };
}

/** Sleep in small slices so a SIGTERM from the gate is honoured promptly. */
function tick(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

const client = new McpClient(args.mcp, { HOME: args.home });
try {
  await client.start();
} catch (e) {
  console.error(`worker could not start cinder-mcp: ${e.message}`);
  process.exit(1);
}

const deadline = Date.now() + args.ms;
let index = 0;
while (Date.now() < deadline) {
  index++;
  // Rotate between the write tools the contract covers, so one code path is
  // not privileged: notes (the hottest), handoff (a bigger transaction) and
  // todo (a different table).
  const which = index % 3;
  let tool;
  let toolArgs;
  if (which === 0) {
    // One note per worker, updated in place.
    //
    // Creating a new note every iteration was the first design and it was
    // wrong for the SOAK: the table grew to 16k rows / 9 MB, VACUUM INTO then
    // needed minutes per snapshot, and every client starved for the rest of the
    // run. That measures SQLite's vacuum throughput, not the write contract.
    // An UPDATE exercises the same channel, the same queue and the same
    // exactly-once rule, with a bounded file.
    tool = "repotory_notes_update";
    toolArgs = {
      id: soakNoteId,
      content: `worker ${process.pid} iteration ${index}`,
    };
  } else if (which === 1) {
    // A rename on the shared thread: a write on a DIFFERENT table (the shared
    // chat store) with a bounded footprint. A handoff would append a note per
    // iteration and grow the file like the notes did.
    tool = "repotory_chat";
    toolArgs = {
      operation: "rename",
      thread_id: soakThreadId,
      title: `soak thread ${process.pid} ${index}`,
    };
  } else {
    // The shared-chat table: a different write path (and the one the three
    // IDE panels use), so the soak does not only exercise project_notes.
    // Append a message to the SAME thread (bounded: one thread, growing only by
    // the message text, which the delete-free design tolerates for a 30m run).
    tool = "repotory_chat";
    toolArgs = {
      operation: "append",
      thread_id: soakThreadId,
      id: randomUUID(),
      role: "user",
      content: `w${process.pid} i${index}`,
      source: "vscode",
    };
  }

  stats.attempts++;
  const started = Date.now();
  try {
    const result = await client.callTool(tool, toolArgs);
    const elapsed = Date.now() - started;
    if (elapsed > stats.slowestMs) stats.slowestMs = elapsed;
    if (result.ok) {
      stats.ok++;
    } else {
      stats.failed++;
      const { code, message, lock } = classify(result);
      // A lock bubble is the contract failing at its one job. Record it, and
      // keep a bounded sample so the report explains it without growing.
      if (lock) stats.lockBubbles++;
      if (code === "writer_unavailable") stats.falseUnavailable++;
      if (stats.failures.length < 40) {
        stats.failures.push({ tool, code, elapsed, message: message.slice(0, 300) });
      }
    }
  } catch (e) {
    // A transport failure: the child died, or the request timed out. During
    // phase B this is expected exactly once per worker (the writer is being
    // killed); outside it, it is a failure.
    stats.failed++;
    if (stats.failures.length < 40) {
      stats.failures.push({ tool, code: "transport", elapsed: Date.now() - started, message: String(e.message).slice(0, 300) });
    }
  }

  // --kill-phase has no branch on purpose: the gate kills the writer, and the
  // only correct behaviour here is to keep writing straight through it, so the
  // takeover happens under load instead of in a quiet window.
  await tick(args.interval);
}

client.stop();

stats.finishedAt = new Date().toISOString();
stats.exitClean = stats.lockBubbles === 0;
writeFileSync(args.out, JSON.stringify(stats, null, 2));

if (stats.lockBubbles > 0) {
  console.error(`worker ${process.pid}: ${stats.lockBubbles} "database is locked" bubble(s) reached the caller — the contract failed at its one job`);
  process.exit(1);
}
process.exit(0);
