// Gate C (ADR-0059): the panel works end to end against REAL binaries.
//
// Gate B pinned the behaviour rules with fakes on both sides. This file is the
// other half of the question: does the panel's own conversation model, driven
// through its own `Transport`, actually reach a real `cinder-mcp` process and a
// real `cinder-server` over HTTP — and complete the three journeys a user
// makes?
//
//   1. search  — a query returns hits the panel can render;
//   2. read    — opening a card loads its whole body in place, over MCP; the
//                pane has no second window to fall back on;
//   3. ask     — a question comes back as an answer with its source list.
//
// No editor, no webview, no display: the model is plain TypeScript (ADR-0036
// put it behind a Transport port for exactly this), the binaries are real, and
// the AI endpoint is a local stub so the ask journey runs the whole tool loop
// without a model or a network.
//
// Run: npm run compile && node --test out/__tests__/e2e-gate-c.test.js
// Requires CINDER_E2E_MCP / CINDER_E2E_SERVER to point at built binaries
// (scripts/e2e-gate-c.sh builds them and exports both).

import { spawn, type ChildProcess } from "node:child_process";
import { createServer, type Server } from "node:http";
import { after, before, test } from "node:test";
import assert from "node:assert/strict";

import { ChatSession } from "../chatSession.js";
import type { ExtMessage, HitCard, Turn, ViewMessage } from "../chatTypes.js";
import { McpStdioClient } from "../mcpClient.js";
import type { Transport } from "../chatSession.js";

const MCP_BIN = process.env.CINDER_E2E_MCP ?? "";
const SERVER_BIN = process.env.CINDER_E2E_SERVER ?? "";
const HOME_DIR = process.env.CINDER_E2E_HOME ?? "";

/** The port the fake model listens on. */
const AI_PORT = 19899;

let server: ChildProcess | null = null;
let serverUrl = "";
let mcp: McpStdioClient | null = null;
let ai: Server | null = null;
/** The seeded note's id, read back from the write. */
let noteId = 0;

/** Everything the panel posted, so a turn can be read back. */
const posted: ViewMessage[] = [];

function requireEnv(): void {
  if (!MCP_BIN || !SERVER_BIN || !HOME_DIR) {
    throw new Error(
      "gate C needs CINDER_E2E_MCP, CINDER_E2E_SERVER and CINDER_E2E_HOME — run scripts/e2e-gate-c.sh",
    );
  }
}

/** Wait until the server answers /health, or fail with its stderr. */
async function waitHealthy(url: string, timeoutMs = 20_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr = "";
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url + "/health");
      if (res.ok) return;
      lastErr = `HTTP ${res.status}`;
    } catch (e) {
      lastErr = String(e);
    }
    await new Promise(r => setTimeout(r, 150));
  }
  throw new Error(`cinder-server never became healthy at ${url}: ${lastErr}`);
}

/**
 * Start a server on `port`.
 *
 * No `--web-dist`: the panels no longer link into the web UI, so the API is all
 * the gate needs (the panel's own read path is MCP).
 */
function startServer(port: number): ChildProcess {
  return spawn(SERVER_BIN, ["-port", String(port)], {
    env: { ...process.env, HOME: HOME_DIR },
    stdio: ["ignore", "pipe", "pipe"],
  });
}

async function rpc(method: string, args: unknown[]): Promise<unknown> {
  const res = await fetch(serverUrl + "/api/rpc", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ method, args }),
  });
  const data = (await res.json()) as { result?: unknown; error?: string };
  if (data.error) throw new Error(`${method}: ${data.error}`);
  return data.result ?? null;
}

/** A fake OpenAI-compatible endpoint: enough for the ask tool loop. */
function startFakeModel(): Promise<Server> {
  return new Promise(resolve => {
    const srv = createServer((req, res) => {
      if (req.method !== "POST") {
        const body = JSON.stringify({ data: [{ id: "e2e-model", object: "model" }] });
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(body);
        return;
      }
      req.resume();
      req.on("end", () => {
        const body = JSON.stringify({
          id: "e2e",
          object: "chat.completion",
          choices: [{
            index: 0,
            message: {
              role: "assistant",
              content:
                "The gateway retries a 503 because the upstream pod may still be rolling out; " +
                "the retry lives in pay-gateway, not in the client.",
            },
            finish_reason: "stop",
          }],
          usage: {},
        });
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(body);
      });
    });
    srv.listen(AI_PORT, "127.0.0.1", () => resolve(srv));
  });
}

/** The Transport the extension host would hand the session — real channels. */
function realTransport(): Transport {
  return {
    callTool: (name, args) => {
      if (!mcp) throw new Error("mcp client is not running");
      return mcp.callTool(name, args);
    },
    rpc: (method, args) => rpc(method, args),
    serverUrl: () => serverUrl,
    workspacePath: () => undefined,
  };
}

function lastTurn(): Turn {
  const turns = posted.filter(m => m.type === "turn");
  const last = turns[turns.length - 1];
  assert.ok(last && last.type === "turn", "no turn was posted");
  return last.turn;
}

before(async () => {
  requireEnv();

  ai = await startFakeModel();

  // ONE server, API-only: seeding goes through its RPC bridge, the panel's own
  // read path is MCP, and nothing links into the web UI any more.
  const port = 19872;
  server = startServer(port);
  serverUrl = `http://127.0.0.1:${port}`;
  server.stderr?.on("data", () => {});
  await waitHealthy(serverUrl);

  // Seed the knowledge the journeys read. Through the product's own RPC
  // bridge, so this is the same path the desktop uses.
  const created = (await rpc("CreateNoteWithMeta", [
    1,
    "Retry policy on 503",
    "Why do we retry on 503? The gateway retries because the upstream pod may still be rolling out. See pay-gateway.",
    "retry,gateway",
    "knowledge",
    "manual",
  ])) as { id: number };
  // The id comes from the row that was actually written: hard-coding it made
  // the gate depend on a pristine database, so a re-run against the same HOME
  // failed for the wrong reason.
  noteId = created.id;
  await rpc("UpdateConfig", ["ai_chat_base_url", `http://127.0.0.1:${AI_PORT}/v1`]);
  await rpc("UpdateConfig", ["ai_chat_model", "e2e-model"]);

  // Isolated HOME: the gate must never read or write the user's real Cinder
  // database. Without this the child inherits the test process's HOME and the
  // assertions silently run against whatever the user has.
  mcp = new McpStdioClient({ command: MCP_BIN, env: { HOME: HOME_DIR } }, () => {});
  await mcp.start();
});

after(async () => {
  mcp?.stop();
  server?.kill();
  ai?.close();
});

test("journey 1: the panel searches the real MCP server and renders hits", async () => {
  const session = new ChatSession(realTransport(), m => posted.push(m));
  await session.run("retry 503", "notes");

  const turn = lastTurn();
  assert.equal(turn.state, "done", `turn failed: ${turn.error ?? ""}`);
  const cards = turn.cards ?? [];
  assert.ok(cards.length >= 1, "no hits came back from the real MCP server");
  const first = cards[0]!;
  assert.match(first.title ?? "", /Retry policy/i);
  assert.ok((first.snippet ?? "").length > 0, "a hit without a snippet cannot be rendered usefully");
  session.dispose();
});

test("journey 2: a card opens in place and its whole body arrives over MCP", async () => {
  const session = new ChatSession(realTransport(), m => posted.push(m));
  await session.run("retry 503", "notes");
  const card = (lastTurn().cards ?? [])[0] as HitCard;
  assert.ok(card);

  // The panel has no second window: the card is opened where it sits, and the
  // body comes back inside the same turn. This journey used to assert a deep
  // link into the web UI — the link is gone because the body was always here.
  await session.handle({
    type: "expandCard",
    turnId: lastTurn().id,
    cardType: card.type,
    cardId: card.id,
  } as ExtMessage);

  const opened = (lastTurn().cards ?? []).find(c => c.type === card.type && c.id === card.id);
  assert.equal(opened?.detail?.source, "note", `the card body did not arrive: ${JSON.stringify(opened?.detail)}`);
  assert.equal(opened?.detail?.id, noteId, "the card opened a different note");
  assert.match(opened?.detail?.body ?? "", /pay-gateway/, "the body is not the note the search returned");
  session.dispose();
});

// The note's body contains the question verbatim, and that is deliberate: the
// evidence channel matches STRICTLY (no OR relaxation — a coincidence rendered
// as "sources for this answer" is a false citation). A fixture whose body only
// shares a few words with the question would pass yesterday and fail the moment
// the strict path stops being bypassed.
test("journey 3: a question comes back as an answer with its sources", async () => {
  const session = new ChatSession(realTransport(), m => posted.push(m));
  await session.run("why do we retry on 503?", "ask");

  const turn = lastTurn();
  assert.equal(turn.state, "done", `ask failed: ${turn.error ?? ""}`);
  assert.match(turn.answer ?? "", /rolling out|pay-gateway/i, "the answer did not come from the model stub");
  assert.ok((turn.sources ?? []).length >= 1, "an evidence-backed answer must carry its sources");
  assert.match((turn.sources ?? [])[0]?.title ?? "", /Retry policy/i);
  session.dispose();
});
