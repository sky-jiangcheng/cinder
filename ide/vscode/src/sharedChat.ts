import { randomUUID } from "node:crypto";
import { parseAskAnswer } from "./headless.js";
import type { Turn } from "./chatTypes.js";

export interface SharedThread {
  id: string;
  title: string;
  path: string;
  createdAt: string;
  updatedAt: string;
}

export interface SharedMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  source: string;
  replyTo: string | null;
  createdAt: string;
}

export interface SharedChatState {
  threads: SharedThread[];
  threadId: string | null;
  turns: Turn[];
  busy: boolean;
  problem: string | null;
}

interface SharedTransport {
  callTool(name: string, args: Record<string, unknown>): Promise<string>;
  workspacePath(): string | undefined;
}

export function sharedTurns(messages: SharedMessage[]): Turn[] {
  const replies = new Map(messages.filter(m => m.role === "assistant" && m.replyTo).map(m => [m.replyTo, m]));
  return messages.filter(m => m.role === "user").map(message => {
    const reply = replies.get(message.id);
    const parsed = reply ? parseAskAnswer(reply.content) : null;
    return {
      id: message.id,
      query: message.content,
      mode: "ask",
      state: reply ? "done" : "pending",
      answer: parsed ? (parsed.parsed ? parsed.answer : parsed.raw) : undefined,
      sources: parsed?.parsed ? parsed.sources : [],
      verbatim: parsed ? !parsed.parsed : false,
      scopeNote: `${message.source}${reply ? ` → ${reply.source}` : " · awaiting a reply"}`,
    };
  });
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid shared conversation response");
  return value as Record<string, unknown>;
}

function parseThread(value: unknown): SharedThread {
  const item = record(value);
  for (const key of ["id", "title", "path", "createdAt", "updatedAt"]) {
    if (typeof item[key] !== "string") throw new Error(`Invalid conversation ${key}`);
  }
  return item as unknown as SharedThread;
}

function parseMessages(value: unknown): SharedMessage[] {
  if (!Array.isArray(value)) throw new Error("Invalid conversation messages");
  return value.map(entry => {
    const item = record(entry);
    if ((item.role !== "user" && item.role !== "assistant") ||
        !["id", "content", "source", "createdAt"].every(key => typeof item[key] === "string") ||
        (item.replyTo !== null && typeof item.replyTo !== "string")) {
      throw new Error("Invalid conversation message");
    }
    return item as unknown as SharedMessage;
  });
}

export class SharedChat {
  private threads: SharedThread[] = [];
  private selected: string | null = null;
  private messages: SharedMessage[] = [];
  private busy = false;
  private problem: string | null = null;
  private replyProblems = new Map<string, string>();
  private generation = 0;
  private refreshFlight: Promise<void> | null = null;
  private visible = false;
  private disposed = false;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private pending = new Map<string, { id: string; text: string; answer?: string }>();

  constructor(private transport: SharedTransport, private post: (state: SharedChatState) => void) {}

  get active(): boolean { return this.selected !== null; }
  get threadId(): string | null { return this.selected; }

  private publish(): void {
    if (this.disposed) return;
    const problem = this.problem ?? (this.selected ? this.replyProblems.get(this.selected) ?? null : null);
    this.post({ threads: this.threads, threadId: this.selected, turns: sharedTurns(this.messages), busy: this.busy, problem });
  }

  private async call(operation: string, args: Record<string, unknown> = {}): Promise<Record<string, unknown>> {
    return record(JSON.parse(await this.transport.callTool("repotory_chat", { operation, ...args })));
  }

  setVisible(visible: boolean): void {
    this.visible = visible;
    clearTimeout(this.timer);
    if (visible && !this.disposed) void this.poll();
  }

  private async poll(): Promise<void> {
    await this.refresh();
    if (this.visible && !this.disposed) {
      clearTimeout(this.timer);
      this.timer = setTimeout(() => void this.poll(), 3000);
      this.timer.unref?.();
    }
  }

  dispose(): void {
    this.disposed = true;
    this.visible = false;
    this.generation++;
    clearTimeout(this.timer);
  }

  refresh(): Promise<void> {
    if (this.disposed) return Promise.resolve();
    if (this.refreshFlight) return this.refreshFlight;
    this.refreshFlight = this.readState().finally(() => { this.refreshFlight = null; });
    return this.refreshFlight;
  }

  /** Re-read after a write of ours, dropping any read that started before it.
   *  `refresh()` hands out the in-flight poll, and a poll that began before
   *  the append carries a snapshot without the message just written — its
   *  generation check would still pass, and the question the user just sent
   *  would disappear until the next tick. */
  private async syncAfterWrite(): Promise<void> {
    this.generation++;
    await this.refreshFlight;
    await this.refresh();
  }

  private async readState(): Promise<void> {
    const generation = this.generation;
    const selected = this.selected;
    try {
      const payload = await this.call("list");
      if (!Array.isArray(payload.threads)) throw new Error("Invalid conversation list");
      const threads = payload.threads.map(parseThread);
      let messages: SharedMessage[] = [];
      if (selected) {
        const result = await this.call("read", { thread_id: selected });
        const thread = parseThread(result.thread);
        if (thread.id !== selected) throw new Error("Conversation response does not match the selected topic");
        messages = parseMessages(result.messages);
        if (!threads.some(item => item.id === selected)) threads.push(thread);
      }
      if (generation !== this.generation || this.disposed) return;
      this.threads = threads;
      this.messages = messages;
      this.problem = null;
    } catch (error) {
      if (generation !== this.generation || this.disposed) return;
      this.problem = `Not synced — ${error instanceof Error ? error.message : String(error)}. Check or update the Cinder MCP service.`;
    }
    this.publish();
  }

  async select(id: string | null): Promise<void> {
    if (id && !this.threads.some(thread => thread.id === id)) return;
    this.generation++;
    this.selected = id;
    this.messages = [];
    this.problem = null;
    this.publish();
    await this.refreshFlight;
    await this.refresh();
  }

  async create(title: string): Promise<void> {
    if (this.busy || !title.trim()) return;
    this.busy = true;
    this.publish();
    try {
      const payload = await this.call("create", { id: randomUUID(), title: title.trim(), path: this.transport.workspacePath() ?? "" });
      const thread = parseThread(payload.thread);
      this.threads = [thread, ...this.threads.filter(item => item.id !== thread.id)];
      await this.select(thread.id);
    } catch (error) {
      this.problem = `Could not create conversation: ${error instanceof Error ? error.message : String(error)}`;
    } finally {
      this.busy = false;
      this.publish();
    }
  }

  async send(query: string, answer: (query: string) => Promise<string>): Promise<boolean> {
    const threadId = this.selected;
    const text = query.trim();
    if (!threadId || !text || this.busy) return false;
    this.busy = true;
    this.problem = null;
    this.publish();
    const key = JSON.stringify([threadId, text]);
    const request = this.pending.get(key) ?? { id: randomUUID(), text };
    this.pending.set(key, request);
    const { id } = request;
    try {
      await this.call("append", { thread_id: threadId, id, role: "user", content: text, source: "vscode" });
      await this.syncAfterWrite();
      const content = request.answer ?? await answer(text);
      if (!content.trim()) throw new Error("The service returned an empty answer");
      request.answer = content;
      await this.call("append", { thread_id: threadId, id: `${id}-reply`, role: "assistant", content, source: "vscode", reply_to: id });
      this.pending.delete(key);
      this.replyProblems.delete(threadId);
      await this.syncAfterWrite();
      return true;
    } catch (error) {
      // The pending request survives on purpose: the same question must reuse
      // the same message id (and the same already-generated answer) on retry.
      this.replyProblems.set(threadId, `Reply not completed: ${error instanceof Error ? error.message : String(error)}`);
      while (this.pending.size > 50) this.pending.delete(this.pending.keys().next().value!);
      return false;
    } finally {
      this.busy = false;
      this.publish();
    }
  }
}
