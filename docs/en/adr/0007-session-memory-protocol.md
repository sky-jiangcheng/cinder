# ADR-0007: Session Memory Protocol (context / handoff Dual Tools)

- Status: Accepted
- Date: 2026-09-28
- Related: [ADR-0006](0006-scope-freeze.md) (core loop first), [ADR-0005](0005-service-layer.md) (service layer unification)

## Background

ADR-0006 defined the core loop as "Discover → Understand → Record → Retrieve → AI use", but the two ends of the loop are broken from an agent's perspective:

1. **Session start**: after receiving a new task, an agent must chain 3-4 calls itself (`projects_list` → `notes_search` → `notes_read`) to assemble project context; most agents skip this step entirely and start with zero context.
2. **Session end**: what the agent learned (decisions, gotchas, next steps) vanishes with the session. `notes_create` exists but carries no protocol — the agent does not know when to write or what structure to write, and humans cannot predict the output format.

For the "AI project memory layer" narrative to hold, memory must happen **automatically at session boundaries** rather than relying on users remembering to save.

## Decision

Three new MCP tools form the session memory protocol (cold start included):

### `repotory_context` (session start)

A single call returns a Markdown document of the project's full context:

- Tech stack / README summary / language shares / dependencies / contributors / activity (from the `repo_meta` mining cache; when uncached, background async mining runs and is declared in the document)
- Recent commits (live git log)
- Open todos
- Highly relevant knowledge notes; **notes tagged `handoff` sort first** (they record how the previous session ended)

Project resolution protocol (`ResolveProject`): `project_id` exact resolution → `project_name` fuzzy match on name/path → auto-resolution when called with no arguments and exactly one project exists. On multiple matches, a project directory (with ID table) is returned for the agent to choose; the context of a wrongly guessed project is never injected.

> **Protocol addition (2026-10-13): the path tier.** The protocol above gained one preceding input while all three
> original steps remain. `repotory_context` and `repotory_handoff` now accept an optional `path` (the IDE workspace
> folder or the agent's cwd), arbitrated by `ResolveWorkspace`: **an explicit `project_id` / `project_name` always
> wins**, and `path` is consulted only when the name or id lookup could not decide a project. The ordering is not a
> stylistic preference — letting the project a caller named be overwritten by whatever directory it happens to be
> standing in is exactly the "inject the wrong project's context" this criterion forbids, wearing a smarter face.
>
> The fallback was needed because step 2 above matches in the direction "the project's root_path contains the
> query", so a **subdirectory** of a multi-repository project (`…/CBiPay/pay-common`) hits none of the three steps,
> while the `repositories` table — which does record that directory — was never consulted. The path tier fills that
> gap in four tiers (repository exact → deepest repository ancestor → project exact → deepest project ancestor; see
> `internal/service/workspace.go` and its tests, landed with ADR-0021), and reports what it matched
> (`repository-exact` / `project-ancestor` / `unresolved` …), which turns "never guess" from a statement of intent
> into a checkable fact.

### `repotory_handoff` (session end)

A structured handoff protocol. Inputs: `summary` (required) + `changes` / `decisions` / `gotchas` / `next_steps` (arrays, at least one non-empty) + `agent` (writer identity). Rendered into a fixed Markdown template and persisted (`kind=knowledge`, `source=mcp`, tags automatically include `handoff`).

The fixed template is deliberate: the heading structure is a contract between the writer and all future readers (humans + agents), and `repotory_context` relies on the `handoff` tag to sort it to the top.

### `repotory_scan` (cold start)

The session memory protocol presumes projects already exist in the database. Only the desktop app could previously perform "seed scan roots + scan", making the documented claim "MCP requires no desktop app" empty talk — a pure-MCP installation's `repotory_context` would only return "no projects". To fix this, scan capability moves down into the service layer and is exposed as a third tool:

- `Service.EnsureDefaultScanRoots`: on first run, seeds the platform-default scan roots (`scan_roots_seeded` marker; existing scan roots are left untouched);
- `Service.ScanNow`: synchronously runs the full scan pipeline and returns repo/project counts (`TriggerScan` is kept for the desktop's background async scenario; both share the `scanning` mutex);
- `repotory_scan`: calls both, compressing the first-run funnel from "install → scan → star → refresh → use" to "install → `repotory_scan` → `repotory_context`".

The desktop first-launch flow also switches to reusing `Service.EnsureDefaultScanRoots`, keeping behavior identical on both ends.

## Rationale

- **Usable with zero arguments**: a single-project installation gets full context straight from `repotory_context()`, reducing the cost of "loading memory" to one call.
- **Cross-agent**: handoffs land in local SQLite rather than any agent-private memory format; a handoff written by Claude Code reads directly in Cursor.
- **Service layer sharing**: all three tools are implemented in `internal/service` (`context.go` / `handoff.go` / `scan.go`), and the desktop reuses the same implementation (ADR-0005 layering).
- **No guessing on multiple matches**: wrong context is more dangerous than no context; on ambiguity, a directory is returned for the agent to choose again.

## Future Directions (open)

- Automatic session capture: parse `~/.claude/projects/*/*.jsonl` to generate session summaries (zero manual involvement; privacy and size to be evaluated)
- Multi-source memory import: memory formats of Cursor, Codex, and OpenCode
- Semantic retrieval: local embeddings (evaluate sqlite-vec) to complement FTS5's literal-match blind spot
- Claude Code hook integration documentation: SessionEnd hook triggering `repotory_handoff` automatically

## Terminology: `handoff` means a persisted session handoff record (2026-11-02)

This section **annotates** the decisions above; it rewrites none of them. It is triggered by a survey of VS Code's session/memory features (VS Code 1.144 official docs, 2026-10-28), where the same word carries a different meaning — without this clarification, users will read Repotory's handoff as the IDE's same-named button.

**1. In this protocol, `handoff` means a persisted session handoff record.** One knowledge note written when a session ends (`kind=knowledge`, `source=mcp`, tags include `handoff`), stored in the project's knowledge base; the next session reads it back via `repotory_context` — **the latest one rendered in full, older ones collapsed into a one-line timeline**. It is a **stored record**, and the agent that reads it need not be the one that wrote it.

**2. It is not "handing a live conversation over between harnesses or execution environments".** That is the meaning of VS Code's built-in "Hand off a session" (VS Code 1.144, official docs 2026-10-28): switch the harness, execution environment, or agent role and carry the full conversation history along — a **runtime continuation**. The two **do not compete** — the latter is a runtime continuation inside a single IDE, the former a cross-agent, protocol-neutral, account-free, entirely local persisted record; they are different things.

**3. The protocol name and the MCP tool name `repotory_handoff` stay unchanged**, and so does the note title template `Session Handoff <time>`. The reason is protocol stability: renaming would break every existing agent integration and the Claude Code SessionEnd auto-capture hook that depends on it (`.claude/hooks/repotory-handoff.sh`, written by `scripts/repotory-init`). **The ambiguity lives in descriptions and product wording, so it is fixed there** — this section is where that choice lands.

**4. Where it landed** (all changed in the same round):

- MCP tool descriptions: `repotory_handoff` in `cmd/mcp/tools_context.go` (it writes a persisted record, not a live handover) and `repotory_context` (the previous round's handoff record — latest one in full, older ones as a one-line timeline)
- MCP refusal wording: the handoff overwrite refusal in `cmd/mcp/main.go` (now says "session handoff record")
- VS Code panel wording: the command title, input prompts, output channel and status-bar tooltip in `ide/vscode/`
- Desktop wording: see the corresponding frontend copy round

## Amendment record (2026-11-02, terminology clarification)

The decisions in this ADR are kept as written; this round only appends a clarification and changes none of them:

- **A "Terminology" section is appended** (above) — it pins `handoff` in this protocol to "a persisted session handoff record" and states that it shares a name with VS Code's "Hand off a session" (a runtime switch of harness / execution environment / role that carries the full conversation history) without sharing its meaning, and that the two do not compete.
- **The protocol name, the MCP tool name `repotory_handoff`, and the note title template `Session Handoff <time>` are not renamed** — renaming would break existing agent integrations and the Claude Code SessionEnd auto-capture hook; the ambiguity is fixed in descriptions, not in identifiers.
- **The disambiguation lands in descriptions and product wording**: MCP tool descriptions, the MCP refusal wording, and the VS Code panel wording (command title / input prompts / output channel / status-bar tooltip). The rendering logic and templates in `internal/service/handoff.go` and `internal/service/context.go` stay **untouched**.
