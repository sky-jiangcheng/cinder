// Minimal MCP stdio client — just enough for repotory-mcp: initialize
// handshake + tools/call. Speaks newline-delimited JSON-RPC 2.0 over stdio,
// which is the MCP stdio transport. Thin-client rule (ADR-0009): this file
// evolves only with the MCP wire contract, never with Cinder internals.
import { spawn, type ChildProcess } from "node:child_process";

export interface McpOptions {
  command: string;
  args?: string[];
  /**
   * Extra environment for the child.
   *
   * Production leaves this unset: the MCP server must inherit the user's
   * environment so it finds the same Cinder data directory the app does.
   * The end-to-end gate (scripts/e2e-gate-c.sh) sets a temp HOME instead,
   * because a gate that writes into the user's real database is not a gate.
   */
  env?: Record<string, string>;
}

interface Pending {
  resolve: (value: unknown) => void;
  reject: (err: Error) => void;
}

// Tool calls (repotory_context, handoff writes) are second-scale against the
// local service; 60s is generous headroom while still bounding a hang.
const REQUEST_TIMEOUT_MS = 60_000;

export class McpStdioClient {
  private proc: ChildProcess | null = null;
  private buffer = "";
  private nextId = 1;
  private readonly pending = new Map<number, Pending>();

  constructor(
    private readonly opts: McpOptions,
    private readonly onStderr: (line: string) => void = () => {},
  ) {}

  async start(): Promise<void> {
    const proc = spawn(this.opts.command, this.opts.args ?? [], {
      stdio: ["pipe", "pipe", "pipe"],
      env: this.opts.env ? { ...process.env, ...this.opts.env } : process.env,
    });
    this.proc = proc;
    proc.stdout?.on("data", (chunk: Buffer) => this.onData(chunk.toString("utf8")));
    proc.stderr?.on("data", (chunk: Buffer) =>
      chunk.toString("utf8").split(/\r?\n/).filter(Boolean).forEach(this.onStderr),
    );
    proc.on("exit", (code) => {
      this.proc = null;
      this.rejectAll(new Error(`repotory-mcp exited (code ${code ?? "?"})`));
    });
    await this.request("initialize", {
      protocolVersion: "2024-11-05",
      capabilities: {},
      clientInfo: { name: "repotory-vscode", version: "0.1.0" },
    });
    this.notify("notifications/initialized", {});
  }

  stop(): void {
    this.proc?.kill();
    this.proc = null;
    this.rejectAll(new Error("repotory-mcp client stopped"));
  }

  /** Call a tool and return its text content concatenated. */
  async callTool(name: string, args: Record<string, unknown> = {}): Promise<string> {
    const res = (await this.request("tools/call", { name, arguments: args })) as {
      content?: Array<{ type?: string; text?: string }>;
    };
    const content = res?.content;
    if (Array.isArray(content)) {
      return content
        .filter((part) => part?.type === "text" && typeof part.text === "string")
        .map((part) => part.text)
        .join("\n");
    }
    return JSON.stringify(res);
  }

  private request(method: string, params: unknown): Promise<unknown> {
    const proc = this.proc;
    if (!proc?.stdin) {
      return Promise.reject(new Error("repotory-mcp is not running"));
    }
    const id = this.nextId++;
    const body = JSON.stringify({ jsonrpc: "2.0", id, method, params });
    return new Promise((resolve, reject) => {
      // Bound every request: a wedged or half-dead server process used to
      // leave the promise (and the withProgress spinner) pending forever.
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`repotory-mcp request timed out: ${method}`));
      }, REQUEST_TIMEOUT_MS);
      timer.unref?.();
      this.pending.set(id, {
        resolve: (v: unknown) => { clearTimeout(timer); resolve(v); },
        reject: (e: Error) => { clearTimeout(timer); reject(e); },
      });
      proc.stdin?.write(body + "\n");
    });
  }

  private notify(method: string, params: unknown): void {
    this.proc?.stdin?.write(JSON.stringify({ jsonrpc: "2.0", method, params }) + "\n");
  }

  private onData(chunk: string): void {
    this.buffer += chunk;
    for (;;) {
      const nl = this.buffer.indexOf("\n");
      if (nl < 0) return;
      const line = this.buffer.slice(0, nl).trim();
      this.buffer = this.buffer.slice(nl + 1);
      if (line) this.dispatch(line);
    }
  }

  private dispatch(line: string): void {
    let msg: {
      id?: number;
      error?: { message?: string };
      result?: unknown;
    };
    try {
      msg = JSON.parse(line);
    } catch {
      return;
    }
    if (typeof msg.id === "number" && this.pending.has(msg.id)) {
      const pending = this.pending.get(msg.id);
      this.pending.delete(msg.id);
      if (!pending) return;
      if (msg.error) {
        pending.reject(new Error(msg.error.message ?? "MCP error"));
      } else {
        pending.resolve(msg.result);
      }
    }
    // Server-initiated notifications/requests are ignored by this skeleton.
  }

  private rejectAll(err: Error): void {
    for (const pending of this.pending.values()) pending.reject(err);
    this.pending.clear();
  }
}
