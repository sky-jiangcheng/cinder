import { strict as assert } from "node:assert";
import { test } from "node:test";
import { SharedChat, sharedTurns, type SharedChatState, type SharedMessage, type SharedThread } from "../sharedChat.js";
import { consoleHtml } from "../chatConsoleHtml.js";
import { Script } from "node:vm";

function fixture() {
  const threads: SharedThread[] = [];
  const messages = new Map<string, SharedMessage[]>();
  const calls: Array<Record<string, unknown>> = [];
  const transport = {
    workspacePath: () => "/tmp/project",
    async callTool(name: string, args: Record<string, unknown>): Promise<string> {
      assert.equal(name, "repotory_chat");
      calls.push(args);
      const threadId = String(args.thread_id ?? "");
      if (args.operation === "list") return JSON.stringify({ threads });
      if (args.operation === "create") {
        const thread: SharedThread = { id: String(args.id), title: String(args.title), path: String(args.path), createdAt: "2026-11-05", updatedAt: "2026-11-05" };
        threads.push(thread);
        messages.set(thread.id, []);
        return JSON.stringify({ thread });
      }
      if (args.operation === "read") return JSON.stringify({ thread: threads.find(t => t.id === threadId), messages: messages.get(threadId) });
      if (args.operation === "append") {
        const list = messages.get(threadId)!;
        let message = list.find(m => m.id === args.id);
        if (!message) {
          message = { id: String(args.id), role: args.role as "user" | "assistant", content: String(args.content), source: String(args.source), replyTo: args.reply_to ? String(args.reply_to) : null, createdAt: "2026-11-05" };
          list.push(message);
        }
        return JSON.stringify({ message });
      }
      throw new Error("unknown operation");
    },
  };
  const states: SharedChatState[] = [];
  const chat = new SharedChat(transport, state => states.push(state));
  return { chat, transport, threads, messages, calls, states, state: () => states.at(-1)! };
}

test("two clients see the same topics and paired messages", async () => {
  const f = fixture();
  await f.chat.create("Architecture");
  const otherStates: SharedChatState[] = [];
  const other = new SharedChat(f.transport, s => otherStates.push(s));
  await other.refresh();
  await other.select(f.threads[0]!.id);
  await f.chat.send("Why this design?", async () => "A shared store avoids divergent histories.");
  await other.refresh();
  assert.equal(otherStates.at(-1)?.turns[0]?.answer, "A shared store avoids divergent histories.");
  assert.equal(f.state().turns[0]?.state, "done");
  f.chat.dispose(); other.dispose();
});

test("replies pair by replyTo even when completions arrive out of order", () => {
  const msg = (id: string, role: "user" | "assistant", replyTo: string | null): SharedMessage => ({ id, role, replyTo, content: id, source: "warden", createdAt: "now" });
  const turns = sharedTurns([msg("q1", "user", null), msg("q2", "user", null), msg("a2", "assistant", "q2"), msg("a1", "assistant", "q1")]);
  assert.deepEqual(turns.map(t => t.answer), ["a1", "a2"]);
});

test("switching topics during a request cannot leak its reply", async () => {
  const f = fixture();
  await f.chat.create("One");
  const first = f.threads[0]!.id;
  await f.chat.create("Two");
  const second = f.threads[1]!.id;
  await f.chat.select(first);
  let release!: (answer: string) => void;
  let started!: () => void;
  const ready = new Promise<void>(resolve => { started = resolve; });
  const request = f.chat.send("Question one", () => { started(); return new Promise(resolve => { release = resolve; }); });
  await ready;
  await f.chat.select(second);
  release("Answer one");
  await request;
  assert.equal(f.state().threadId, second);
  assert.deepEqual(f.state().turns, []);
  await f.chat.select(first);
  assert.equal(f.state().turns[0]?.answer, "Answer one");
  f.chat.dispose();
});

test("provider failure leaves the question and never writes a fake answer", async () => {
  const f = fixture();
  await f.chat.create("Failure");
  assert.equal(await f.chat.send("hello", async () => { throw new Error("offline"); }), false);
  assert.equal(f.messages.get(f.threads[0]!.id)?.length, 1);
  assert.match(f.state().problem!, /offline/);
  assert.equal(await f.chat.send("hello", async () => "recovered"), true);
  assert.equal(f.messages.get(f.threads[0]!.id)?.length, 2);
  f.chat.dispose();
});

test("an uncertain assistant write retries with the same id and no second model call", async () => {
  const f = fixture();
  await f.chat.create("Retry");
  const original = f.transport.callTool;
  let fail = true;
  f.transport.callTool = async (name, args) => {
    const result = await original(name, args);
    if (args.operation === "append" && args.role === "assistant" && fail) { fail = false; throw new Error("reply lost after save"); }
    return result;
  };
  let modelCalls = 0;
  const answer = async () => { modelCalls++; return "Saved once"; };
  assert.equal(await f.chat.send("hello", answer), false);
  assert.equal(await f.chat.send("hello", answer), true);
  assert.equal(modelCalls, 1);
  assert.equal(f.messages.get(f.threads[0]!.id)?.length, 2);
  f.chat.dispose();
});

test("refresh reports malformed data without throwing or discarding known history", async () => {
  const f = fixture();
  await f.chat.create("Known");
  await f.chat.send("q", async () => "a");
  f.transport.callTool = async () => '{"threads":[{"id":"bad"}]}';
  await f.chat.refresh();
  assert.match(f.state().problem!, /Not synced/);
  assert.equal(f.state().turns[0]?.answer, "a");
  f.chat.dispose();
});

test("duplicate submissions while busy start only one generation", async () => {
  const f = fixture();
  await f.chat.create("Busy");
  const first = f.chat.send("q", async () => "a");
  assert.equal(await f.chat.send("q", async () => { throw new Error("must not run"); }), false);
  assert.equal(await first, true);
  assert.equal(f.messages.get(f.threads[0]!.id)?.length, 2);
  f.chat.dispose();
});

test("dispose prevents late publications and overlapping refreshes share a request", async () => {
  const f = fixture();
  const one = f.chat.refresh();
  assert.equal(f.chat.refresh(), one);
  f.chat.dispose();
  await one;
  assert.equal(f.states.length, 0);
  assert.equal(f.calls.length, 1);
  await f.chat.refresh();
  assert.equal(f.calls.length, 1);
});

test("a poll that started before our own append cannot paint over the question", async () => {
  const f = fixture();
  await f.chat.create("Architecture");
  const threadId = f.threads[0]!.id;

  // The wire snapshot is taken when the read is issued, not when it resolves:
  // that is what makes a pre-append response possible at all.
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let holdOne = false;
  const slowTransport = {
    workspacePath: () => "/tmp/project",
    async callTool(name: string, args: Record<string, unknown>) {
      const answer = await f.transport.callTool(name, args);
      if (args.operation === "read" && holdOne) { holdOne = false; await gate; }
      return answer;
    },
  };
  const states: SharedChatState[] = [];
  const panel = new SharedChat(slowTransport, state => states.push(state));
  await panel.refresh();
  await panel.select(threadId);

  holdOne = true;
  const tick = () => new Promise(resolve => setImmediate(resolve));
  const stalePoll = panel.refresh();
  await tick(); // the read is issued and parked; its snapshot has no new message
  // The question is appended while that read is still in flight, and the
  // answer then fails — so the transcript the panel ends on was written by
  // the stale read unless the write path refuses it.
  const sending = panel.send("Why this design?", async () => { throw new Error("provider down"); });
  await tick(); // the user message has landed on the store
  release();
  const sent = await sending;
  await stalePoll;
  panel.dispose();

  assert.equal(sent, false);
  assert.equal(states.at(-1)?.turns.at(-1)?.query, "Why this design?");
  // Three reads: the parked one plus a fresh read after each of our appends.
  // Returning the in-flight stale poll instead would leave this at two — the
  // write path would believe it had synced when it had only waited.
  assert.ok(f.calls.filter(c => c.operation === "read").length >= 3);
});

test("the webview script compiles and supports IME-safe submit and shared topics", () => {
  const html = consoleHtml("test-nonce");
  const script = html.match(/<script nonce="test-nonce">([\s\S]*?)<\/script>/)?.[1];
  assert.ok(script);
  assert.doesNotThrow(() => new Script(script));
  assert.match(html, /ev\.isComposing/);
  assert.match(html, /Conversation topic/);
  assert.match(html, /type: 'sharedSelect'/);
});
