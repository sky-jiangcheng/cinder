// Gate C (ADR-0059): the panel works end to end against REAL binaries.
//
// Gate B pinned the behaviour rules with fakes on both sides. This file is the
// other half of the question: does the panel's own conversation model, driven
// through its own `Transport`, actually reach a real `cinder-mcp` process and a
// real `cinder-server` over HTTP — and complete the three journeys a user
// makes?
//
//   1. search  — a query returns hits the panel can render;
//   2. deep link — a hit opens the SPA route, and a server WITHOUT `--web-dist`
//                  is refused with the panel's own explanation instead of a
//                  dead 404 (ADR-0035 decision 6);
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
const WEB_DIST = process.env.CINDER_E2E_WEB_DIST ?? "";
const HOME_DIR = process.env.CINDER_E2E_HOME ?? "";

/** The port the fake model listens on. */
const AI_PORT = 19899;

let server: ChildProcess | null = null;
let serverUrl = "";
let mcp: McpStdioClient | null = null;
let ai: Server | null = null;
/** The seeded note's id, read back from the write. */
let noteId = 0;

/** What the panel asked the host to open. */
const opened: string[] = [];
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

function startServer(port: number, withWebDist: boolean): ChildProcess {
  const args = ["-port", String(port)];
  if (withWebDist && WEB_DIST) args.push("--web-dist", WEB_DIST);
  return spawn(SERVER_BIN, args, {
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
    openExternal: async (url: string) => {
      opened.push(url);
    },
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

  // ONE server, started WITH the web UI: the deep-link journey needs a server
  // that serves the SPA (the other branch is covered by the second journey
  // below, which points the panel at an API-only port).
  const port = 19872;
  server = startServer(port, true);
  serverUrl = `http://127.0.0.1:${port}`;
  server.stderr?.on("data", () => {});
  await waitHealthy(serverUrl);

  // Seed the knowledge the journeys read. Through the product's own RPC
  // bridge, so this is the same path the desktop uses.
  const created = (await rpc("CreateNoteWithMeta", [
    1,
    "Retry policy on 503",
    "The gateway retries a 503 because the upstream pod may still be rolling out. See pay-gateway.",
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

test("journey 2: a deep link opens the SPA route, and an API-only server is refused out loud", async () => {
  const session = new ChatSession(realTransport(), m => posted.push(m));
  await session.run("retry 503", "notes");
  const card = (lastTurn().cards ?? [])[0] as HitCard;
  assert.ok(card);

  // (a) The UI-serving server: the route reaches openExternal.
  opened.length = 0;
  await session.handle({ type: "open", card } as ExtMessage);
  assert.equal(opened.length, 1, "the deep link never reached the host");
  assert.equal(
    opened[0],
    `http://127.0.0.1:19872/project/1?noteId=${noteId}`,
    "the deep link must point at the row the search actually returned",
  );

  // (b) An API-only server: the panel must SAY so, not open a 404.
  const apiOnly = startServer(19873, false);
  apiOnly.stderr?.on("data", () => {});
  try {
    await waitHealthy("http://127.0.0.1:19873");
    const apiUrl = "http://127.0.0.1:19873";
    // Point the same session at the API-only port for this call.
    const transport = realTransport();
    const apiSession = new ChatSession(
      { ...transport, serverUrl: () => apiUrl, openExternal: async u => void opened.push(u) },
      m => posted.push(m),
    );
    await apiSession.run("retry 503", "notes");
    const apiCard = (lastTurn().cards ?? [])[0] as HitCard;
    assert.ok(apiCard);
    opened.length = 0;
    await apiSession.handle({ type: "open", card: apiCard } as ExtMessage);
    assert.equal(opened.length, 0, "an API-only server must not be opened as if it served the UI");
    const warns = posted.filter(m => m.type === "announce" && m.level === "warn");
    assert.ok(warns.length >= 1, "the refusal must be announced, not silent");
    apiSession.dispose();
  } finally {
    apiOnly.kill();
  }
  session.dispose();
});

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
