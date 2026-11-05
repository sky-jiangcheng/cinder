# Cinder for VS Code

**The local-first memory layer for AI coding agents.** Cinder keeps what your coding agents learn — why a decision was made, what gotcha you hit, what to do next — in a local, searchable store. This extension surfaces it where you work: load a project's full session context, write a structured handoff when you stop, and **ask your project's knowledge base a question** — in a chat console that remembers the conversation, without leaving VS Code.

One VSIX covers the whole fork family — **VS Code, Cursor and Windsurf**.

## What it does

| Surface | What it does |
|---|---|
| Command `Cinder: Load Session Context` | Pulls the working context for your project (tech stack, README excerpt, recent commits, open todos, the last session handoff) via `repotory_context`, sending the current workspace path so a subdirectory resolves to the repository it belongs to; shows it in the Cinder output channel |
| Command `Cinder: Record Session Handoff` | Prompts for summary / changes / next steps, then records a structured handoff (`repotory_handoff`) — the project is resolved from the workspace path — the next session, yours or any agent's, starts from it |
| Command `Cinder: Ask the Knowledge Base` | Asks `repotory_ask` and appends the answer — with its sources — to the chat console |
| Command `Cinder: Search Knowledge` | Full-text search (FTS5) across every note you captured; the workspace path is sent so notes recorded in the matched repository rank first (ranking only, nothing filtered). The hits are appended to the chat console as a **Notes** turn |
| Command `Cinder: Search Approved Knowledge Pages` | Searches the approved knowledge pages (`repotory_wiki_search`) and appends them as a **Pages** turn |
| Sidebar view "Cinder Chat" (Cinder activity bar) | The chat console — see below. The panel is a conversation: one turn per question, the old turns folded away, every detail opened in place. Its scope chip is **locked to the current project** by default; click it to search across all projects. The panel draws its **own** header row — brand plus four inline icon buttons: **Search notes**, **AI provider**, **Diagnostics** and **Desktop app**. They live inside the panel, not in the view's title bar, so the host cannot clip them (ADR-0044) |
| Status bar `$(repo) Cinder` | Click = load context; the tooltip names the resolved workspace (project / repository and its path) once resolved |
| Command `Cinder: Register MCP Server` | Runs the bundled `repotory-init --with-hook` silently in the background (no terminal): registers the MCP server for Claude Code / Cursor / VS Code / Windsurf and installs the SessionEnd hook. The installer ships inside the extension, so this works in any project — it is not limited to the Cinder repository |
| Command `Cinder: Setup & Diagnostics` | One panel showing four checks — `repotory-mcp` binary, local service, workspace resolution, MCP client config — each with the fix action next to it. Use this instead of guessing why something is not working |
| Command `Cinder: Set repotory-mcp Path` | File picker for the `repotory-mcp` executable; writes `repotory.mcpPath` so no PATH editing is needed |

## The chat console

The panel is a **conversation**, not a result list (ADR-0036). You ask a question, the answer lands in the feed with the sources it was built from, and the exchange stays readable — the older turns fold away, and the detail of any note or page opens **in place**, inside the turn that produced it. There is no second pane to squeeze into a narrow sidebar.

```
┌──────────────────────────────────────────────┐
│ Cinder                        ⌕  ⚙  ⚠  ↗   │  ← brand + four icon buttons
├──────────────────────────────────────────────┤
│ ▸ 3 earlier turns                            │  ← the folded history
│                                              │
│  Ask    why do we retry on 503?              │  ← one turn per question
│  The gateway retries…                        │
│  Sources (2) ▸                               │  ← the evidence is a footnote
│                                              │
│  Notes   deploy hook                         │
│  ┌────────────────────────────────────────┐  │
│  │ NOTE  Deploy hook                     │  │  ← a card; click to open it
│  └────────────────────────────────────────┘  │
├──────────────────────────────────────────────┤
│ ┌──────────────────────────────────────────┐ │
│ │ Ask about this project…            [➤]   │ │  ← the send key sits in the box
│ └──────────────────────────────────────────┘ │
│ [Ask][Notes][Pages]                   [🔒 Cinder] │
└──────────────────────────────────────────────┘
```

- **Four modes.** **Ask** (the default) calls `repotory_ask` and gives an *answer* with its sources — not a list of titles. **Notes** calls `repotory_notes_search`, **Pages** calls `repotory_wiki_search`. The mode switch sits in the composer, next to the scope chip.
- **Locked to this project.** The chip shows `🔒 <project>`: the panel asks about the project your workspace belongs to. Click the chip to unlock it and search across all projects — the lock is a default, not a restriction. For **Ask** and **Pages** the project is a real server-side filter; **Notes** has no project parameter in the tool surface, so the panel filters the returned hits itself and says how many of them belong to the locked project (the count is never dressed up as "everything").
- **The history folds.** Only the two most recent turns stay open; older ones collapse into one row (`▸ N earlier turns`). Every answer's source list collapses into `Sources (N)`. The fold state is explicit and sticks — a collapsed turn stays collapsed while the next turn arrives.
- **Details open in place.** Clicking a card fetches the full body over `repotory_notes_read` / `repotory_wiki_read` and opens it inside the card, with **Open in Cinder**, **Copy**, and — for notes — **Edit**, **New note here** and **AI Tidy**. **Open in Cinder** deep-links the item into the Cinder web UI (`/project/<id>?noteId=<id>` for a note, `?tab=todos` for a todo, `/wiki/<id>` for a page); it is best-effort — the extension asks the local service whether it actually serves the web UI first, and says so instead of dropping you on a 404 when it does not.
- **Status and errors live in an announcement bar** at the top of the feed, not in a status line at the bottom — dismissible, and the bottom belongs to the composer.

The panel follows your editor theme — every surface is a VS Code theme color — so it looks native in light and dark alike.

## The external AI provider

The **AI provider** icon in the panel header opens a sheet that configures the model the panel talks to: **Base URL**, **API key** and **Model**, plus **List models** (ask the endpoint what it serves) and **Test** (send one minimal completion and show what came back). Save writes it to the Cinder service.

Three things worth knowing:

- **It is the server's configuration, not the extension's.** The extension reads and writes the same `ai_chat_*` settings the desktop app uses (Settings → AI). Two copies would drift, and the key belongs to the service anyway — so **there is no AI key in the extension's settings**, and what you configure in VS Code is the same endpoint when you open the panel in JetBrains.
- **An empty key means "leave the stored key alone."** The service masks a stored secret, so the sheet never sees it; saving with the key field empty keeps what is already stored instead of deleting it.
- **The panel works without one.** Ask falls back to the service's own answer path when no provider is configured; the sheet says so instead of failing silently.

## When the folder is not in the knowledge base yet

If the workspace folder resolves to no project, the scope chip stops being a dead end: it reads **Choose project** and opens a picker listing the candidate projects the service knows about (ADR-0044). Pick one and the extension files this folder under it — a real binding in the knowledge base, not a per-window setting — and the next resolution hits it directly. The picker is the *only* place a project is chosen by hand; everything else is resolved from the path.

If there are no candidates either (the service is unreachable, or nothing matches), the chip says so and the panel searches across all projects — it never pretends to be scoped to something.

## Maintaining notes without leaving the IDE

The console is also the editor. An opened note carries three maintenance actions, and the desktop app's note list carries the same ones:

| Action | What it does |
|---|---|
| **Edit** | Opens the note in the card's editor: title, kind, tags, project (via a dropdown) and the Markdown body, with a live preview under the textarea. **Save** writes the fields through `repotory_notes_update` and then the project change through `MoveNote` (the one write the MCP surface deliberately keeps human-only, so it goes over the same local HTTP bridge the JetBrains plugin uses). A failed save keeps your draft and says why |
| **New note here** | Creates a note under the **same project** as the one you are reading — the editor opens empty, pre-set to that project. After creating, the new note joins the turn as a card of its own and opens in full |
| **AI Tidy** | Asks the configured AI to restructure the note: same facts, cleaner sections, a table-of-contents index when the note grew sections, and a mermaid flowchart when the content is really a process. The result is a **proposal** — it opens in the same editor with a "review, then save" banner, so machine-generated wording is never saved silently; your save (or your edits first) is what lands it. Requires the AI endpoint configured in the desktop app (Settings → AI) |

The tidy proposal is generated server-side by the shared service layer (`OrganizeNote`, twin tool `repotory_notes_organize`) and writes nothing by itself — the human save is the gate, exactly as in the desktop app.

## Keyboard shortcuts

| Shortcut | Action |
|---|---|
| `Ctrl+Alt+R` `C` (`Ctrl+Cmd+R` `C` on macOS) | Load Session Context |
| `Ctrl+Alt+R` `H` (`Ctrl+Cmd+R` `H` on macOS) | Record Session Handoff |
| `Ctrl+Alt+R` `S` (`Ctrl+Cmd+R` `S` on macOS) | Search Knowledge |

All commands remain in the Command Palette; rebind them in *Preferences → Keyboard Shortcuts* if the defaults collide with yours. The panel's own header carries the four icon buttons instead — the view's title bar stays empty, which is what keeps them from being clipped by the host.

## Requirements

- The `repotory-mcp` binary — install it via Homebrew, Scoop or the GitHub Releases page. It does **not** have to be on your PATH: the extension resolves it in this order and stops at the first hit — `repotory.mcpPath`, `REPOTORY_MCP_BIN`, the extension's own storage, `PATH`, then the usual Homebrew / Scoop / `~/.local/bin` directories. If none matches, **"Cinder: Set repotory-mcp Path"** opens a file picker. The desktop app is optional; the MCP server alone is enough.
- Optional: a running `repotory server`. The chat console works without it over `repotory-mcp` alone — but the **project lock needs it**: the panel asks the read-only `ResolveWorkspacePath` binding which project your folder belongs to, and without the service there is no project to lock to, so the panel says so and searches across all projects instead (set `repotory.serverUrl` if you changed the port).

## First launch

On first activation the extension opens **"Cinder: Setup & Diagnostics"** instead of a wall of instructions: it shows whether the binary was found (and from where), whether the local service answers, what the current folder resolves to, and whether the MCP client config exists. Each row carries its fix — locate the binary, open the download page, re-run registration, or open the folder.

To wire the MCP server into your AI clients, run **"Cinder: Register MCP Server"**. It executes the bundled `repotory-init --with-hook` in the extension host (Electron-as-node), so no terminal opens; if that fails, the output channel has the full log and a "run it manually" fallback.

## Settings

| Setting | Default | Description |
|---|---|---|
| `repotory.serverUrl` | `http://127.0.0.1:18765` | Cinder headless service URL |
| `repotory.mcpPath` | `""` (auto-detect) | Explicit path to the `repotory-mcp` executable; empty means auto-detect. Pointing at a missing/unexecutable file is reported by the diagnostics panel instead of silently falling back |

That is the whole surface. No project id to configure: the extension sends the workspace path and the service resolves it into a project and a repository, so there is no setting to keep in sync with your knowledge base. **The AI provider is not here either** — it lives in the service's configuration and is edited from the panel (see *The external AI provider*), so there is nothing to keep in sync between the editor and the desktop app.

## Workspace resolution

The extension no longer needs a hand-typed project id. Every command sends the current workspace folder's absolute path (`repotory_context`, `repotory_handoff`, `repotory_notes_search`), and the backend's four-tier path resolver (`project → repository`, most specific first) works out which project — and which repository inside it — the folder belongs to. The matching rules live server-side on purpose: the MCP tools resolve the same path, and a second implementation in the extension would let the sidebar and an agent's `repotory_context` disagree about which knowledge a session gets. On load and whenever the workspace folders change, the extension asks the read-only `ResolveWorkspacePath` binding what the folder is and shows the answer: the console's scope chip locks to `<project>` and the status-bar tooltip names the resolved workspace (or reports that the folder is `not in the knowledge base yet` when nothing matches).

When the headless service is not reachable, the console's announcement bar says so (it does not guess) and the scope chip reads `No project` — the panel searches across all projects rather than pretending to be scoped. The commands themselves still work over `repotory-mcp`, and any path-based resolution there is done by the same server-side code.

When the service **is** reachable but the folder matches nothing, the chip offers the candidate projects instead of a dead end, and filing the folder under one is a real write to the knowledge base — see *When the folder is not in the knowledge base yet*.

## Install

**Marketplace**: search "Cinder Memory" in the Extensions view, or:

```bash
code --install-extension repotory-vscode.vsix
```

One VSIX works in VS Code, Cursor and Windsurf (use the fork's own CLI: `cursor --install-extension …`, `windsurf --install-extension …`).

The extension is a thin client — every answer comes from the same `internal/service` layer as the Cinder Desktop app, read from your local SQLite knowledge base. Nothing leaves your machine.

## Development

```bash
cd ide/vscode
npm install
npm run compile
npm test        # unit tests for the parsers (ask answer, project lock)
# open this folder in VS Code and "Run Extension" (F5), or package:
npx @vscode/vsce package --no-dependencies   # → repotory-vscode-<version>.vsix
```

Architecture and scope: [ADR-0009](https://github.com/sky-jiangcheng/cinder/blob/master/docs/en/adr/0009-ide-presence.md) for the client discipline, [ADR-0036](https://github.com/sky-jiangcheng/cinder/blob/master/docs/en/adr/0036-ide-chat-console.md) for the chat console's shape. The extension calls `repotory-mcp` over stdio and the loopback-only headless service; zero business logic lives here.
