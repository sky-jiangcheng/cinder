// Unit tests for the conversation model (chatSession.ts).
//
// The model owns three decisions that the UI cannot make for itself:
//   - which tool a mode talks to;
//   - what the project lock does in that mode (a server-side filter, or a
//     presentation filter the panel has to admit to);
//   - what a failure looks like (a failed turn, not an empty answer).
//
// The transport is faked here: no MCP process, no service, no editor.
import { strict as assert } from "node:assert";
import { test } from "node:test";
import { ChatSession, type Transport } from "../chatSession.js";
import type { ScopeState, ViewMessage } from "../chatTypes.js";

interface FakeTransport extends Transport {
  calls: Array<{ name: string; args: Record<string, unknown> }>;
  rpcs: Array<{ method: string; args: unknown[] }>;
}

function fakeTransport(
  tools: Record<string, string>,
  project?: { id: number; name: string },
  rpcResults: Record<string, unknown> = {},
  toolFails = false,
): FakeTransport {
  const calls: Array<{ name: string; args: Record<string, unknown> }> = [];
  const rpcs: Array<{ method: string; args: unknown[] }> = [];
  return {
    calls,
    rpcs,
    async callTool(name, args) {
      calls.push({ name, args });
      if (toolFails) throw new Error("repotory-mcp is not running");
      const value = tools[name];
      if (value !== undefined) return value;
      // The workspace resolution (ADR-0062) is synthesised from the same
      // fixture the RPC bridge used to answer with, so a test that locks the
      // panel needs no canned tool text.
      if (name === "repotory_workspace_resolve" && project) {
        return JSON.stringify({ Project: { id: project.id, name: project.name }, Repository: null });
      }
      if (name === "repotory_workspace_resolve") {
        return JSON.stringify({ Project: null, Repository: null });
      }
      throw new Error(`no such tool: ${name}`);
    },
    async rpc(method, args) {
      rpcs.push({ method, args });
      if (method in rpcResults) return rpcResults[method];
      return null;
    },
    serverUrl: () => "http://127.0.0.1:18765",
    workspacePath: () => "/tmp/work",
    openExternal: async () => {},
  };
}

function collector(): { messages: ViewMessage[]; post: (m: ViewMessage) => void } {
  const messages: ViewMessage[] = [];
  return { messages, post: (m) => messages.push(m) };
}

function lastTurn(messages: ViewMessage[]) {
  const turns = messages.filter((m) => m.type === "turn");
  const last = turns[turns.length - 1];
  assert.ok(last && last.type === "turn");
  return last.turn;
}

test("ask mode calls repotory_ask and carries the parsed sources", async () => {
  const transport = fakeTransport({
    repotory_ask: 'Because the gateway retries.\n\nSources:\n[P1] note "retry" (id 3, project Foo)\n',
  });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("why 503?", "ask");
  const turn = lastTurn(messages);
  assert.equal(turn.state, "done");
  assert.equal(turn.answer, "Because the gateway retries.");
  assert.equal(turn.sources?.length, 1);
  assert.equal(transport.calls[0]?.name, "repotory_ask");
});

test("an unparseable source list shows the answer verbatim instead of losing it", async () => {
  const raw = 'Answer.\n\nSources:\n[P1] note "broken (id 3)\n';
  const transport = fakeTransport({ repotory_ask: raw });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("q", "ask");
  const turn = lastTurn(messages);
  assert.equal(turn.state, "done");
  assert.equal(turn.verbatim, true);
  assert.equal(turn.answer, raw);
  assert.equal((turn.sources ?? []).length, 0);
});

test("the tool's text error becomes a failed turn, not an answer", async () => {
  const transport = fakeTransport({ repotory_ask: "error: the AI endpoint refused the request" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("q", "ask");
  const turn = lastTurn(messages);
  assert.equal(turn.state, "error");
  assert.equal(turn.error, "the AI endpoint refused the request");
});

test("a thrown tool error becomes a failed turn", async () => {
  const transport = fakeTransport({});
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("q", "ask");
  const turn = lastTurn(messages);
  assert.equal(turn.state, "error");
  assert.ok(turn.error);
});

test("notes mode filters by the locked project and says how many were dropped", async () => {
  const hits = JSON.stringify([
    { type: "note", id: 1, project_id: 7, title: "mine", snippet: "s" },
    { type: "note", id: 2, project_id: 9, title: "other", snippet: "s" },
    { type: "note", id: 3, project_id: 7, title: "mine too", snippet: "s" },
  ]);
  const transport = fakeTransport({ repotory_notes_search: hits }, { id: 7, name: "Cinder" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  await session.run("q", "notes");
  const turn = lastTurn(messages);
  assert.equal(turn.state, "done");
  assert.equal(turn.cards?.length, 2);
  assert.ok(turn.scopeNote?.includes("2 of 3"));
  assert.ok(turn.scopeNote?.includes("Cinder"));
});

test("notes mode without a lock keeps every hit and says nothing about scope", async () => {
  const hits = JSON.stringify([{ type: "note", id: 1, project_id: 9, title: "other", snippet: "s" }]);
  const transport = fakeTransport({ repotory_notes_search: hits });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("q", "notes");
  const turn = lastTurn(messages);
  assert.equal(turn.cards?.length, 1);
  assert.equal(turn.scopeNote, undefined);
});

test("pages mode passes the project id to the tool — the filter is server-side", async () => {
  const transport = fakeTransport(
    { repotory_wiki_search: '[{"id":1,"slug":"s","title":"P","kind":"concept","project_id":7}]' },
    { id: 7, name: "Cinder" },
  );
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  await session.run("q", "pages");
  const call = transport.calls.find((c) => c.name === "repotory_wiki_search");
  assert.equal(call?.args.project_id, 7);
  const turn = lastTurn(messages);
  assert.equal(turn.cards?.length, 1);
});

test("ask mode passes the project id to the tool too", async () => {
  const transport = fakeTransport({ repotory_ask: "no evidence" }, { id: 7, name: "Cinder" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  await session.run("q", "ask");
  const call = transport.calls.find((c) => c.name === "repotory_ask");
  assert.equal(call?.args.project_id, 7);
});

test("unlocking drops the project id from the calls", async () => {
  const transport = fakeTransport({ repotory_ask: "no evidence" }, { id: 7, name: "Cinder" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  await session.handle({ type: "scopeToggle" });
  const scope: ScopeState = session.currentScope();
  assert.equal(scope.locked, false);
  await session.run("q", "ask");
  const call = transport.calls.find((c) => c.name === "repotory_ask");
  assert.equal(call?.args.project_id, undefined);
});

test("a workspace that resolves to no project leaves the panel unlocked", async () => {
  const transport = fakeTransport({ repotory_ask: "no evidence" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  const scope = session.currentScope();
  assert.equal(scope.projectId, 0);
  assert.equal(scope.locked, false);
});

test("turns accumulate — the panel is a history, not a result slot", async () => {
  const transport = fakeTransport({ repotory_ask: "one" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("first", "ask");
  await session.run("second", "ask");
  const turns = messages.filter((m) => m.type === "turn");
  assert.ok(turns.length >= 4);
  const ids = new Set(
    turns.map((m) => (m.type === "turn" ? m.turn.id : "")),
  );
  assert.equal(ids.size, 2);
});

test("an empty answer says so instead of showing nothing", async () => {
  const transport = fakeTransport({ repotory_ask: "" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("q", "ask");
  const turn = lastTurn(messages);
  assert.equal(turn.state, "done");
  assert.equal(turn.answer, "The service returned an empty answer.");
});

test("a todo card shows its excerpt, not the note that shares its id", async () => {
  // Notes and todos are separate id sequences (ADR-0032): a turn can hold a
  // note 12 and a todo 12. Reading the todo by id would answer about the
  // note, so the todo keeps its excerpt and asks for nothing.
  const hits = JSON.stringify([
    { type: "note", id: 12, project_id: 7, title: "the note", snippet: "note excerpt" },
    { type: "todo", id: 12, project_id: 7, title: "the todo", snippet: "todo excerpt" },
  ]);
  const transport = fakeTransport({
    repotory_notes_search: hits,
    repotory_notes_read: '{"id":12,"project_id":7,"title":"the note","content":"the note body"}',
  });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("q", "notes");
  await session.handle({ type: "expandCard", turnId: "turn-1", cardType: "todo", cardId: 12 });
  const turn = lastTurn(messages);
  const todo = turn.cards?.find((c) => c.type === "todo");
  assert.equal(todo?.detail?.source, "snippet");
  assert.equal(todo?.detail?.body, "todo excerpt");
  // The note with the same id was never read on the todo's behalf.
  assert.equal(transport.calls.some((c) => c.name === "repotory_notes_read"), false);
});

test("a note card with the same id as a todo still reads the note", async () => {
  const hits = JSON.stringify([
    { type: "note", id: 12, project_id: 7, title: "the note", snippet: "note excerpt" },
    { type: "todo", id: 12, project_id: 7, title: "the todo", snippet: "todo excerpt" },
  ]);
  const transport = fakeTransport({
    repotory_notes_search: hits,
    repotory_notes_read: '{"id":12,"project_id":7,"title":"the note","content":"the note body"}',
  });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.run("q", "notes");
  await session.handle({ type: "expandCard", turnId: "turn-1", cardType: "note", cardId: 12 });
  const turn = lastTurn(messages);
  const note = turn.cards?.find((c) => c.type === "note");
  assert.equal(note?.detail?.source, "note");
  assert.equal(note?.detail?.body, "the note body");
});

// ------------------------------------------------------- project recognition
//
// The workspace resolving to no project used to be a dead end: the chip was
// disabled and the reader was told to go scan the project somewhere else.
// The candidates ARE the next step (ADR-0044 decision 6).

test("an unresolved workspace sends the candidate projects to the panel", async () => {
  const resolution = JSON.stringify({
    Project: null,
    Repository: null,
    Candidates: [
      { id: 12, name: "CBiPay" },
      { id: 19, name: "CBiPay-Other" },
    ],
  });
  const transport = fakeTransport({ repotory_workspace_resolve: resolution });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  const sent = messages.filter((m) => m.type === "projectCandidates");
  assert.equal(sent.length, 1);
  assert.deepEqual(
    sent[0] && sent[0].type === "projectCandidates" ? sent[0].candidates : [],
    [
      { id: 12, name: "CBiPay" },
      { id: 19, name: "CBiPay-Other" },
    ],
  );
});

test("an unreachable MCP channel says so instead of pretending the panel is scoped", async () => {
  const transport = fakeTransport({}, undefined, {}, true);
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  assert.equal(session.currentScope().projectId, 0);
  // ADR-0062: the project lock runs over MCP, so the banner names the MCP
  // channel — not a local service that is no longer the lock's transport.
  const warned = [...messages].reverse().find((m) => m.type === "announce");
  assert.ok(warned && warned.type === "announce");
  assert.equal(warned.level, "warn");
  assert.equal(
    warned.message,
    "The Cinder MCP channel is not reachable — the project lock needs it, so the panel searches across all projects.",
  );
});

test("the project lock resolves through the MCP tool, not the HTTP bridge", async () => {
  const transport = fakeTransport({}, { id: 7, name: "Cinder" });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.refreshScope();
  const call = transport.calls.find((c) => c.name === "repotory_workspace_resolve");
  assert.ok(call, "the workspace must resolve through the MCP tool");
  assert.deepEqual(call.args, { path: "/tmp/work" });
  assert.equal(session.currentScope().projectId, 7);
  assert.equal(session.currentScope().locked, true);
  assert.ok(messages.some((m) => m.type === "scope"));
});

test("picking a project files the workspace under it and resolves again", async () => {
  const resolution = JSON.stringify({
    Project: null,
    Repository: null,
    Candidates: [{ id: 12, name: "CBiPay" }],
  });
  const transport = fakeTransport({ repotory_workspace_resolve: resolution });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.handle({ type: "pickProject", projectId: 12 });
  const bind = transport.rpcs.find((r) => r.method === "BindWorkspaceToProject");
  assert.ok(bind, "the workspace must be bound through the binding");
  assert.deepEqual(bind.args, ["/tmp/work", 12]);
  // The scope is re-resolved after the binding, so the panel locks itself.
  assert.ok(transport.calls.some((c) => c.name === "repotory_workspace_resolve"));
  assert.ok(messages.some((m) => m.type === "scope"));
});

// -------------------------------------------------- the AI provider (ADR-0063)
//
// The provider settings live in the service and are read/written over MCP
// since the config pair became catalogue-covered: an IDE-only user (no
// desktop, no server) can now configure an endpoint.

test("the provider settings are read over the MCP config tool", async () => {
  const get = JSON.stringify({
    config: { ai_chat_base_url: "http://127.0.0.1:1234/v1", ai_chat_model: "qwen3", ai_chat_api_key: "********" },
    scan_roots: [],
  });
  const transport = fakeTransport({ repotory_config_get: get });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.handle({ type: "providerLoad" });
  const call = transport.calls.find((c) => c.name === "repotory_config_get");
  assert.ok(call, "the provider settings must be read over the MCP tool");
  const state = messages.find((m) => m.type === "providerState");
  assert.ok(state && state.type === "providerState");
  assert.equal(state.baseUrl, "http://127.0.0.1:1234/v1");
  assert.equal(state.model, "qwen3");
  assert.equal(state.hasKey, true);
});

test("saving the provider writes each key over the MCP config tool", async () => {
  const transport = fakeTransport({
    "repotory_config_get": JSON.stringify({
      config: { ai_chat_base_url: "http://127.0.0.1:1234/v1", ai_chat_model: "qwen3" },
      scan_roots: [],
    }),
    "repotory_config_update": JSON.stringify({ key: "ai_chat_base_url", value: "http://h/v1" }),
  });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.handle({
    type: "providerSave",
    baseUrl: "http://h/v1",
    model: "qwen3",
    apiKey: "",
  });
  const writes = transport.calls.filter((c) => c.name === "repotory_config_update");
  assert.deepEqual(
    writes.map((w) => [w.args.key, w.args.value]),
    [
      ["ai_chat_base_url", "http://h/v1"],
      ["ai_chat_model", "qwen3"],
    ],
  );
  const result = messages.find((m) => m.type === "providerResult");
  assert.ok(result && result.type === "providerResult");
  assert.equal(result.ok, true);
});

test("a rejected config write surfaces as a failed save, not a success", async () => {
  const transport = fakeTransport({
    "repotory_config_update": "error: unknown config key: nope",
  });
  const { messages, post } = collector();
  const session = new ChatSession(transport, post);
  await session.handle({ type: "providerSave", baseUrl: "http://h/v1", model: "m", apiKey: "" });
  const result = messages.find((m) => m.type === "providerResult");
  assert.ok(result && result.type === "providerResult");
  assert.equal(result.ok, false);
  assert.match(result.message, /unknown config key/);
});
