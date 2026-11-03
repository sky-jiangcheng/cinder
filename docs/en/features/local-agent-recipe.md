---
title: Local Agent Recipe (Fully Offline)
order: 8
---

# Local Agent Recipe: a Memory Layer for a Fully Offline Model

Repotory's MCP server is a **stdio process**: it does not care which model or which client is on the other end, and it never touches the network. So "fully offline local agent + Repotory" has always worked in principle — what was missing was one page of wiring instructions and one real-machine verification. This page supplies both.

The one-line version: **the model runs on your machine, the knowledge base runs on your machine, and the two talk over MCP through a pipe — not a single outbound request anywhere in the loop.**

## Who does what

| Role | Does | Does not |
|------|------|----------|
| Repotory (`repotory-mcp`) | Stores context, retrieves, writes handoff notes | Provide a model, hold API keys, go online |
| Local model (ollama / LM Studio …) | Reasons, decides which tool to call | Persist state (the model itself has no memory) |
| MCP client (Claude Code / Cursor / opencode …) | Connects the two: injects tools, forwards calls | Interpret semantics |

Conversation history is gone the moment the model process exits; **anything remembered across sessions can only come from Repotory**. That is why this data layer is irreplaceable when you are offline.

## Three steps to wire it up

### 1. Get the MCP binary

Release assets include `repotory-mcp-<platform>`; to build from source:

```bash
go build -o /usr/local/bin/repotory-mcp ./cmd/mcp
```

### 2. Start a local OpenAI-compatible endpoint

ollama (default `http://127.0.0.1:11434`):

```bash
ollama serve
ollama pull qwen2.5-coder:7b     # needs a model that supports tool calling
```

LM Studio: load a model in the app, then start the local server (default `http://127.0.0.1:1234`, or `lms server start`).

Tool-calling ability is a hard requirement. A 3B-class model can complete a single-step tool call, but goes off the rails as soon as a task needs more than one step; **the recipe works with a small model for a demo, but real use starts around 7B**.

### 3. Register the MCP server in your client

Claude Code:

```bash
claude mcp add repotory -- /usr/local/bin/repotory-mcp
```

Cursor / VS Code / Claude Code all use the same shape for project-level config:

```json
{
  "mcpServers": {
    "repotory": { "command": "/usr/local/bin/repotory-mcp", "args": [] }
  }
}
```

opencode:

```bash
opencode mcp add repotory -- /usr/local/bin/repotory-mcp
```

opencode prefixes tool names with the server name, so you will see tools like `repotory_repotory_context` and `repotory_repotory_handoff`. To get something shorter, register the server under a different name (e.g. `memory`, giving `memory_repotory_context`).

**Any** client that can launch a stdio MCP server works with the same command line; the protocol is plain JSON-RPC, with no private extension.

## Two calls tie the sessions together

Once installed, only two actions matter; everything else is ordinary read/write:

1. **Start of work**: `repotory_context` (optionally with `path` pointing at the current working directory). One call returns the tech stack, README excerpts, recent commits, todos, related notes — with **the latest handoff note rendered in full at the top**.
2. **End of work**: `repotory_handoff` (`summary` required; `changes` / `decisions` / `gotchas` / `next_steps` optional). It is stored with the `handoff` tag, and a later `notes_update` refuses to overwrite it.

The prompt you give the client can literally be these two lines:

```text
Before starting work, call repotory_context (path = the current repo directory) and read the returned context.
Before finishing work, call repotory_handoff and record what you did, what tripped you up, and what comes next.
```

If you would rather not rely on the model's cooperation, there are two routes, and both are product features now ([ADR-0043](../adr/0043-handoff-instructions-in-agent-instruction-files.md)):

- **The Claude Code SessionEnd hook** (see the "Automatic handoff at session end" section of [AI Integration](ai-integration.md)) — it fires when the session ends, **in that client only**.
- **Write the two lines above into the project's `AGENTS.md` and `CLAUDE.md`** — that is what `repotory-init --with-instructions`, or that checkbox in "Connect AI clients", does; any agent that reads those files (opencode / Codex / Cursor…) follows them.

The two coexist: the hook catches "forgot to wrap up", the instruction file catches "does not use Claude Code at all". The instruction file turns "write it down" from something that must happen into something that probably happens — the model reads it, obeying is not guaranteed.

## Verified on a real machine (2026-10-29)

Environment: macOS arm64; ollama `qwen2.5:3b` (1.9 GB, local); `repotory-mcp` v2.19.0; an **isolated HOME** (`HOME=/tmp/repotory-verify/home`) with one demo Git repository. The live library was never opened, and its sha256 was identical before and after.

| Step | Result |
|------|--------|
| `repotory_scan` (cold start) | 1 repository / 1 project, `success: true` |
| **Session 1** (new process) | 18 MCP tools entered the model's tool schema → the model emitted `repotory_handoff` → stored as `note_id: 1` |
| **Session 2** (another new process, no shared conversation history) | the model emitted `repotory_context` → the returned context **opened with session 1's handoff note** → the model repeated session 1's gotcha verbatim |
| Packet capture (recording proxy in front of ollama) | `POST /v1/chat/completions`, `tools: 18`, destination `127.0.0.1` |

Session 2's model answer, verbatim:

```text
The gotcha recorded in the previous session was that the `/health` endpoint
returns a `200` status code even when the account balance is negative.
```

**No step in this chain needs the network**: the model is local, the library is local, and the two are joined by a stdio pipe.

## Known limitation: opencode 2.0.29 does not hand MCP tools to the model

The first client tried in this verification was opencode v2.0.29, and it **connects to the MCP server but never uses it**:

- the log plainly reads `mcp connected server=repotory tools=18`;
- yet the requests sent to the model carry only opencode's own 12 tools — **none of the 18 repotory tools**;
- switching models (ollama qwen2.5:3b / qwen2.5-coder:7b, LM Studio 27B), switching config shapes (`mcp.servers` / `mcp` / project-level / global), switching MCP servers (a separate control server with just 1 tool) — **none of it helped**; the control server's tool did not get through either.

So this is client-side behavior, unrelated to the model or to `repotory-mcp`. opencode's own docs offer only one hint for "tool calling doesn't work" (raise ollama's `num_ctx` to 16k–32k); raising the context made no difference in this reproduction.

Until opencode fixes it, connect Repotory through Claude Code / Cursor / any other MCP client — or verify the chain yourself with the minimal driver below.

## The minimal driver used for recording

The two sessions in the table were run by a ~170-line recording script (full listing in `outputs/process-logs/2026-10-29-本地Agent-MCP真机验证.md`): it does three things — connect to `repotory-mcp` over stdio, convert the 18 tools verbatim into OpenAI's `tools` array, and translate the model's `tool_calls` into MCP calls.

**It is not a product to ship; it is a recording tool that spells out what a client is supposed to do.** For real use, use an off-the-shelf client. The script's purpose is diagnosis: when you cannot tell whether "the client isn't wired up" or "the server is broken", thirty lines of code can push the chain through and localize the problem to the client side.

## Boundaries

- **Repotory does not provide a model** and does not pick one for you. Offline is the sum of two things: the model on your machine + the library on your machine.
- **It does not guarantee the model will call tools on its own.** Small models need hard instructions ("call repotory_context first"); putting that into the client prompt or a hook is the reliable approach.
- **This is a single-user local library.** Cross-device and multi-user collaboration are out of scope (see [Data & Backup](../data-management.md)).
- Semantic search (optional) hits an embedding endpoint — **if you want to stay fully offline, use a local ollama embedding** (`go run ./cmd/vector-init` already recommends local Ollama by default); do not point it at a remote address.
