// Seed the rows the soak writes into, through the real MCP binary.
//
// Why this is a separate script and not a pipe of printf into cinder-mcp: a
// pipe closes stdin as soon as the last line is written, the MCP server reads
// EOF, starts shutting down, and CANCELS the tool call that has not answered
// yet. The symptom is `write chat.create cancelled: context canceled` — which
// looks like a concurrency bug and is actually the seeder hanging up.
//
// This keeps the child alive until its answer arrives, then exits.
//
// Usage:
//   node seed.mjs --mcp <path> --home <dir> [--out <file>]
//
// It prints a JSON object with the created ids:
//   {"note_id": 3, "thread_id": "..."}

import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";
import { createInterface } from "node:readline";
import { randomUUID } from "node:crypto";

function arg(name, fallback = "") {
  const i = process.argv.indexOf(name);
  return i >= 0 && process.argv[i + 1] ? process.argv[i + 1] : fallback;
}

const mcpPath = arg("--mcp");
const home = arg("--home");
const out = arg("--out");
if (!mcpPath || !home) {
  console.error("usage: seed.mjs --mcp <path> --home <dir> [--out <file>]");
  process.exit(2);
}

const child = spawn(mcpPath, [], {
  stdio: ["pipe", "pipe", "pipe"],
  env: { ...process.env, HOME: home },
});

const pending = new Map();
let nextId = 1;
let buffer = "";

createInterface({ input: child.stdout }).on("line", line => {
  let msg;
  try {
    msg = JSON.parse(line);
  } catch {
    return;
  }
  const waiter = pending.get(msg.id);
  if (!waiter) return;
  pending.delete(msg.id);
  if (msg.error) waiter.reject(new Error(msg.error.message ?? JSON.stringify(msg.error)));
  else waiter.resolve(msg.result);
});

const stderr = [];
createInterface({ input: child.stderr }).on("line", line => {
  if (stderr.length < 20) stderr.push(line);
});

function request(method, params) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
  });
}

function tool(name, args) {
  return request("tools/call", { name, arguments: args });
}

function textOf(res) {
  return (res?.content ?? [])
    .filter(part => part?.type === "text" && typeof part.text === "string")
    .map(part => part.text)
    .join("\n");
}

function fail(what, res) {
  console.error(`${what} failed: ${textOf(res) || JSON.stringify(res)}`);
  if (stderr.length) console.error("mcp stderr:\n  " + stderr.join("\n  "));
  process.exit(1);
}

await request("initialize", {
  protocolVersion: "2024-11-05",
  capabilities: {},
  clientInfo: { name: "soak-seed", version: "0.1.0" },
});
child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized", params: {} }) + "\n");

// The one note every notes write updates in place. Creating one per iteration
// was the first design and it grew the file to 9 MB in 30 minutes, at which
// point VACUUM INTO needed minutes and the clients starved — measuring SQLite,
// not the contract.
const noteRes = await tool("repotory_notes_create", {
  project_id: 1,
  title: "soak note",
  content: "seed body",
  category: "knowledge",
});
if (noteRes?.isError) fail("notes_create", noteRes);
let noteId = 0;
try {
  noteId = JSON.parse(textOf(noteRes)).id;
} catch {
  fail("notes_create (unparseable result)", noteRes);
}

// The one shared thread every chat write appends to / renames.
const threadId = randomUUID();
const threadRes = await tool("repotory_chat", {
  operation: "create",
  id: threadId,
  title: "soak thread",
});
if (threadRes?.isError) fail("chat create", threadRes);

const result = { note_id: noteId, thread_id: threadId };
const json = JSON.stringify(result);
if (out) writeFileSync(out, json);
console.log(json);

child.stdin.end();
child.kill("SIGTERM");
