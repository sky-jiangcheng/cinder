// Gate B runner for the VS Code panel (ADR-0059, TODO 13.2).
//
// The fixture (scripts/shared-chat-conformance.fixture.json) is the contract;
// this runner asserts the panel's REAL code paths against it — sharedTurns'
// pairing/order and SharedChat.send's fallback refusal — not a reimplementation
// of them. A rule the fixture marks "skipped" must NOT be asserted here: the
// assert_forbidden guard below fails the run if a rule gains an expect before
// the product decision it waits on lands.
//
// Kotlin has its own runner with the same shape (ide/jetbrains/src/test/.../
// SharedChatConformanceTest.kt); the two fail independently, naming the
// surface that drifted — that is gate B's whole point.
import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { SharedChat, sharedTurns, type SharedMessage } from "../sharedChat.js";
import { ChatSession } from "../chatSession.js";

// The compiled test lives in out/__tests__/, the repo root three levels up.
// __filename, not import.meta: this build targets CommonJS.
const here = __filename;
function findFixture(): string {
  let dir = dirname(here);
  for (;;) {
    const candidate = join(dir, "..", "..", "..", "scripts", "shared-chat-conformance.fixture.json");
    try {
      readFileSync(candidate);
      return candidate;
    } catch {
      const parent = dirname(dir);
      if (parent === dir) throw new Error("shared-chat-conformance.fixture.json not found above");
      dir = parent;
    }
  }
}
const fixturePath = findFixture();
const fixture = JSON.parse(readFileSync(fixturePath, "utf8"));

function fixtureRule(id: string) {
  const found = fixture.rules.find((r: { id: string }) => r.id === id);
  if (!found) throw new Error(`rule ${id} disappeared from the fixture: the runner implements a contract that no longer exists`);
  if (found.status === "skipped" && found.assert_forbidden) {
    throw new Error(`rule ${id} is skipped (open decision: ${found.skip_reason}) but still carries an expectation — asserting it would pin today's divergence as the standard`);
  }
  return found;
}

const settled = fixture.rules.filter((r: { status: string }) => r.status === "settled");
if (settled.length === 0) throw new Error("fixture has no settled rules: gate B gates nothing");
// Skips may legitimately reach zero: an open decision that gets decided is
// promoted to settled (11.1-11.3 were closed on 2026-11-13). What must never
// happen is a rule DISAPPEARING — the rule count in contract_version is what
// makes a deletion visible, and fixtureRule() above already refuses to run
// against an id that vanished.
const statuses = new Set(fixture.rules.map((r: { status: string }) => r.status));
for (const status of statuses) {
  if (status !== "settled" && status !== "skipped") {
    throw new Error(`unknown rule status ${status}`);
  }
}

interface FixMsg {
  id: string;
  role: "user" | "assistant";
  content: string;
  source: string;
  reply_to: string | null;
  created_at: string;
}

// The fixture stores reply_to; the panel's SharedMessage type calls it replyTo.
function toShared(m: FixMsg): SharedMessage {
  return {
    id: m.id,
    role: m.role,
    content: m.content,
    source: m.source,
    replyTo: m.reply_to,
    createdAt: m.created_at,
  };
}

function turnsOf(ruleId: string): Array<{ userId: string; replyId: string | null }> {
  const r = fixtureRule(ruleId);
  const messages = (r.input.messages as FixMsg[]).map(toShared);
  // A reply id has NO reserved shape (the server's reply guard only requires a
  // user message in the same thread), so the reply is looked up by replyTo —
  // deriving it from the user id would re-implement the very assumption the
  // fixture exists to retire.
  const replyOf = new Map(messages.filter((m) => m.role === "assistant" && m.replyTo).map((m) => [m.replyTo, m.id]));
  return sharedTurns(messages).map((t) => ({ userId: t.id, replyId: replyOf.get(t.id) ?? null }));
}

function expectTurns(ruleId: string): Array<{ user_id: string; reply_id: string }> {
  return fixtureRule(ruleId).expect.turns;
}

test("fixture contract: the panel's runner reads the one file and its counts hold", () => {
  assert.equal(fixture.contract_version.core, "2.0.0");
  assert.equal(fixture.rules.length, fixture.contract_version.rules, "contract_version.rules must equal the file's rule count");
});

test("rule pairing.attach: a turn pairs by reply_to, one authority (TODO 11.3)", () => {
  const want = expectTurns("pairing.attach").map((t) => ({ userId: t.user_id, replyId: t.reply_id }));
  const got = turnsOf("pairing.attach");
  assert.deepEqual(
    got,
    want,
    "the VS Code panel's pairing diverged from the fixture — the fixture is the target, so the panel changes",
  );
});

test("rule pairing.order: turns render in the server's read order, never re-sorted (TODO 11.3)", () => {
  const want = expectTurns("pairing.order").map((t) => ({ userId: t.user_id, replyId: t.reply_id }));
  const got = turnsOf("pairing.order");
  assert.deepEqual(got, want, "createdAt sorting across writer clocks inverts real pairs; the server's seq order is the only order two processes agree on");
});

test("rule fallback.not_persisted: the keyword fallback never lands as an answer (TODO 11.2)", async () => {
  const fb = fixtureRule("fallback.not_persisted");
  const messages = new Map<string, Array<{ id: string; role: "user" | "assistant"; content: string; source: string; replyTo: string | null; createdAt: string }>>();
  const appended: Array<{ role: string; content: string }> = [];
  const transport = {
    workspacePath: () => "/tmp/project",
    async callTool(name: string, args: Record<string, unknown>): Promise<string> {
      if (name !== "repotory_chat" && name !== "repotory_ask") throw new Error(`unexpected tool ${name}`);
      if (name === "repotory_ask") return fb.input.ask_tool_result as string;
      const threadId = String(args.thread_id ?? "");
      if (args.operation === "list") return JSON.stringify({ threads: [{ id: "t1", title: "T", path: "", createdAt: "c", updatedAt: "u" }] });
      if (args.operation === "create") {
        messages.set(String(args.id), []);
        return JSON.stringify({ thread: { id: String(args.id), title: String(args.title), path: "", createdAt: "c", updatedAt: "u" } });
      }
      if (args.operation === "read") return JSON.stringify({ thread: { id: threadId, title: "T", path: "", createdAt: "c", updatedAt: "u" }, messages: messages.get(threadId) ?? [] });
      if (args.operation === "append") {
        const list = messages.get(threadId) ?? [];
        const existing = list.find((m) => m.id === args.id);
        if (existing) return JSON.stringify({ message: existing });
        const message = {
          id: String(args.id),
          role: args.role as "user" | "assistant",
          content: String(args.content),
          source: String(args.source),
          replyTo: args.reply_to ? String(args.reply_to) : null,
          createdAt: "now",
        };
        list.push(message);
        messages.set(threadId, list);
        appended.push({ role: message.role, content: message.content });
        return JSON.stringify({ message });
      }
      throw new Error(`unknown operation ${String(args.operation)}`);
    },
  };
  const lastState: { problem: string | null } = { problem: null };
  const chat = new SharedChat(transport as never, (s) => { lastState.problem = s.problem; });
  await chat.create("Fallback rule");
  await chat.select("t1");
  // The shared ask refuses the fallback instead of storing it as an answer.
  const sent = await chat.send("Why this design?", async () => fb.input.ask_tool_result as string);
  assert.equal(sent, false, "a keyword-fallback text is not an answer: SharedChat.send must report the ask as not-sent");
  assert.deepEqual(
    appended.filter((a) => a.role === "assistant"),
    [],
    "the fallback text must never be appended to the shared thread as an assistant message",
  );
  assert.equal(
    messages.get("t1")?.some((m) => m.role === "user" && m.content === "Why this design?"),
    true,
    "the user's question is a real session event and stays",
  );
  assert.ok(lastState.problem, "the panel must surface the refusal locally, not silently");
  chat.dispose();
});

test("rule fallback.not_persisted.zero_hit: the zero-hit branch obeys the same rule", async () => {
  const zero = fixtureRule("fallback.not_persisted.zero_hit");
  const appended: string[] = [];
  const transport = {
    workspacePath: () => undefined,
    async callTool(name: string, args: Record<string, unknown>): Promise<string> {
      if (name === "repotory_ask") return zero.input.ask_tool_result as string;
      const threadId = String(args.thread_id ?? "");
      if (args.operation === "list") return JSON.stringify({ threads: [] });
      if (args.operation === "read") return JSON.stringify({ thread: null, messages: [] });
      if (args.operation === "append") {
        appended.push(`${args.role}:${String(args.content).slice(0, 40)}`);
        return JSON.stringify({ message: { id: String(args.id), role: args.role, content: String(args.content), source: String(args.source), replyTo: args.reply_to ?? null, createdAt: "now" } });
      }
      return JSON.stringify({ thread: { id: String(args.id ?? "x"), title: "t", path: "", createdAt: "c", updatedAt: "u" } });
    },
  };
  const chat = new SharedChat(transport as never, () => {});
  await chat.create("Zero hit");
  await chat.select("t1");
  await chat.send("nothing matches this", async () => zero.input.ask_tool_result as string);
  assert.deepEqual(appended.filter((entry) => entry.startsWith("assistant:")), [], "the zero-hit wording has no 'No AI endpoint' prefix — a prefix check alone would store it");
  chat.dispose();
});

test("rule scope.path: a shared ask carries no scope (ADR-0061 / TODO 11.1)", async () => {
  const rule = fixtureRule("scope.path");
  assert.deepEqual(rule.expect.ask_args_keys_exclude, ["path", "project_id"]);

  // The ASK ARGS are decided in ChatSession, not in SharedChat: SharedChat.send
  // takes an answer callback and hands the question over, and ChatSession is
  // what calls `repotory_ask`. Testing the wrong layer would pass while the
  // panel sent a path, so this drives the real session.
  const askArgs: Record<string, unknown>[] = [];
  const transport = {
    workspacePath: () => "/tmp/some-workspace",
    async callTool(name: string, args: Record<string, unknown>): Promise<string> {
      if (name === "repotory_ask") {
        askArgs.push({ ...args });
        return "An answer.\n\nSources:\n[N1] note \"n\" (id 1, project P)\n";
      }
      if (args.operation === "append") {
        return JSON.stringify({ message: { id: String(args.id), role: args.role, content: String(args.content), source: String(args.source), replyTo: args.reply_to ?? null, createdAt: "now" } });
      }
      if (args.operation === "read") {
        return JSON.stringify({ thread: { id: "t1", title: "Scoped?", path: "/tmp/some-workspace", createdAt: "c", updatedAt: "u" }, messages: [] });
      }
      if (args.operation === "list") return JSON.stringify({ threads: [{ id: "t1", title: "Scoped?", path: "/tmp/some-workspace", createdAt: "c", updatedAt: "u" }] });
      return JSON.stringify({ thread: { id: String(args.id ?? "t1"), title: "t", path: "/tmp/some-workspace", createdAt: "c", updatedAt: "u" } });
    },
    async rpc() { return null; },
    serverUrl: () => "http://127.0.0.1:18765",
    openExternal: async () => {},
  };
  const session = new ChatSession(transport as never, () => {});
  await session.run("why?", "ask");

  assert.equal(askArgs.length, 1, "the ask must have run exactly once");
  for (const forbidden of rule.expect.ask_args_keys_exclude as string[]) {
    assert.equal(
      Object.prototype.hasOwnProperty.call(askArgs[0], forbidden),
      false,
      `the ask sent "${forbidden}": a shared thread belongs to the person, so the workspace must not scope the answer (the thread's path is provenance, not scope)`,
    );
  }
  session.dispose();
});

test("rule pairing.orphan_reply: a reply with no question is shown, marked, never dropped", async () => {
  const rule = fixtureRule("pairing.orphan_reply");
  assert.equal(rule.expect.orphan_rendered, true);
  assert.equal(rule.expect.orphan_marked, true);
  assert.equal(rule.expect.orphan_not_dropped, true);

  const messages: SharedMessage[] = [
    { id: "u1", role: "user", content: "paired question", source: "vscode", replyTo: null, createdAt: "2026-11-13T00:00:01Z" },
    { id: "u1-reply", role: "assistant", content: "paired answer", source: "vscode", replyTo: "u1", createdAt: "2026-11-13T00:00:02Z" },
    // replyTo names a user message that is NOT in this history.
    { id: "ghost-reply", role: "assistant", content: "answer with no question", source: "warden", replyTo: "u-gone", createdAt: "2026-11-13T00:00:03Z" },
  ];
  const turns = sharedTurns(messages);

  assert.equal(turns.length, 2, "the orphan must be its own turn, not swallowed");
  const paired = turns.find(t => t.id === "u1");
  assert.ok(paired && paired.answer === "paired answer");
  const orphan = turns.find(t => t.id === "ghost-reply");
  assert.ok(orphan, "the orphan reply was dropped: the other panel keeps it, so the same history would differ");
  assert.equal(orphan.orphan, true, "the orphan must be MARKED, not passed off as an answer to a visible question");
  assert.equal(orphan.query, "", "an orphan has no question; the panel labels it instead of inventing one");
  assert.equal(orphan.answer, "answer with no question");
});

test("rule pairing.multiple_replies: one question, one answer — the last reply wins", async () => {
  const rule = fixtureRule("pairing.multiple_replies");
  assert.equal(rule.expect.reply_winner, "last_in_read_order");
  assert.equal(rule.expect.earlier_replies_rendered, false);

  const messages: SharedMessage[] = [
    { id: "u1", role: "user", content: "q", source: "vscode", replyTo: null, createdAt: "2026-11-13T00:00:01Z" },
    { id: "u1-reply", role: "assistant", content: "stale answer", source: "vscode", replyTo: "u1", createdAt: "2026-11-13T00:00:02Z" },
    // A third-party writer appended a second answer under a NEW id.
    { id: "u1-reply-2", role: "assistant", content: "final answer", source: "warden", replyTo: "u1", createdAt: "2026-11-13T00:00:03Z" },
  ];
  const turns = sharedTurns(messages);

  assert.equal(turns.length, 1, "one question must stay one turn");
  const only = turns[0]!;
  assert.equal(only.answer, "final answer", "the LAST reply in read order must win");
  assert.notEqual(only.answer, "stale answer", "the stale duplicate must not be rendered");
});
