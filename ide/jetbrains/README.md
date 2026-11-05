# Cinder for JetBrains

**The local-first memory layer for AI coding agents.** Cinder keeps what your coding agents learn — why a decision was made, what gotcha you hit, what to do next — in a local, searchable store. This plugin puts it where you work: a **Cinder** tool window that is a **conversation console** — you ask a question, read the answer with its sources, and browse what is already there.

One build covers the whole IntelliJ family — it declares `com.intellij.modules.platform`, so IntelliJ IDEA, PyCharm, WebStorm, GoLand and the rest all install the same zip.

## What it does

The panel is a **conversation console**, not a result list (ADR-0036). One question goes in, one turn comes out, and the turns stay in the feed — a result that replaces the previous one cannot be read as a sequence, and Cinder's value is in the sequence.

| Surface | What it does |
|---|---|
| Tool window **Cinder** | The chat console. Five regions: a header (brand + channel state), an **announcement bar** (one line, dismissible — it replaced the old status line), the **conversation feed** (the only scroll area), the **folded history** (older turns fold into one row) and the **input area** at the bottom (always there) |
| Brand header | The mark and the wordmark, plus the channel state (a pill: `online` / `offline` / `checking…`), the **service actions** — **Refresh** and **Start local service** — and the **panel actions**: **Search notes**, **AI provider**, **Diagnostics** and **Desktop app**. Clicking the pill re-checks the channel |
| Conversation feed | One turn per question: the mode it was asked in (`ASK` / `NOTES` / `PAGES`), the question, and the answer or the cards it produced. The newest **2** turns stay open; everything older folds into `▸ N earlier turns` and opens on click |
| **Ask** mode (default) | `repotory_ask` answers with prose plus a `Sources:` list. The answer is the body; the sources are its footnote, folded behind `Sources (N) ▸`. A source list that cannot be parsed is shown **verbatim** — the raw text — so nothing is ever swallowed |
| **Notes** mode | `repotory_notes_search` answers with cards (notes / todos). Clicking a title opens the item **in place**, inside the turn that produced it — there is no separate detail pane |
| **Pages** mode | `repotory_wiki_search` answers with the approved knowledge pages, opened in place the same way |
| Card actions | **Open in Cinder** (deep link into the web UI), **Copy** (title + body), and for notes **Edit** / **New note here** / **AI Tidy** — see *Maintaining notes without leaving the IDE* below |
| Scope chip | The panel is **locked to the current project** by default. The chip shows `🔒 <project>`; clicking it unlocks the panel back to cross-project. A workspace that resolves to no project offers the candidate projects to pick from instead of a dead end — see *When the folder is not in the knowledge base yet* |
| **Tools → Cinder** | Opens the tool window (also on its title bar) |
| **Settings → Tools → Cinder** | The **`repotory-mcp`** binary (the plugin's data channel) with a **Check MCP channel** diagnostic that lists every place it looked, plus the optional server address, server binary path, a **Test connection** button that tells "API and web UI both reachable" from "API-only, the link would 404", and **Start local service** for a one-click launch |

The layout is the same console the VS Code extension ships, so the two IDE surfaces feel like one product (ADR-0036 decision 8): same regions, same wording, same fold rules.

### The project lock, per mode

The lock is a **default, not a restriction** — the chip opens and the panel goes back to cross-project. What it does per mode differs, and the panel says which is which:

| Mode | What the lock does |
|---|---|
| **Ask** | The project id goes to `repotory_ask` — the server really filters |
| **Pages** | The project id goes to `repotory_wiki_search` — the server really filters (that project's pages **plus** the global ones) |
| **Notes** | `repotory_notes_search` has **no project parameter** (`path` only re-ranks), so the panel filters the hits itself and says so: `Locked to <project>: K of N matches shown. The notes tool has no project filter, so the panel filtered the list.` |

A project that resolves to no project, or a service that is not running, leaves the panel unlocked **and says why** — the panel never pretends to be scoped.

## The external AI provider

The **AI provider** action in the panel header opens a dialog that configures the model the panel talks to: **Base URL**, **API key** and **Model**, plus **List models** (ask the endpoint what it serves) and **Test** (send one minimal completion and show what came back). Save writes it to the Cinder service.

Three things worth knowing:

- **It is the server's configuration, not the plugin's.** The plugin reads and writes the same `ai_chat_*` settings the desktop app uses (Settings → AI). Two copies would drift, and the key belongs to the service anyway — so **there is no AI key in the plugin's settings**, and what you configure in JetBrains is the same endpoint when you open the panel in VS Code.
- **An empty key means "leave the stored key alone."** The service masks a stored secret, so the dialog never sees it; saving with the key field empty keeps what is already stored instead of deleting it.
- **The panel works without one.** Ask falls back to the service's own answer path when no provider is configured; the dialog says so instead of failing silently.

> **A deliberate difference from the VS Code panel:** there, the provider form is a **sheet inside the webview**; Swing has no inline form surface, so the JetBrains plugin opens a `DialogWrapper` dialog. The fields, the buttons and the status wording are the same on both sides — only the carrier differs (ADR-0044).

## When the folder is not in the knowledge base yet

If the workspace folder resolves to no project, the scope chip stops being a dead end: it offers the candidate projects the service knows about. Pick one and the plugin files this folder under it — a real binding in the knowledge base, not a per-window setting — and the next resolution hits it directly. The picker is the *only* place a project is chosen by hand; everything else is resolved from the path.

If there are no candidates either (the service is unreachable, or nothing matches), the chip says so and the panel searches across all projects — it never pretends to be scoped to something.

## Requirements

- **IntelliJ IDEA 2024.2 or newer** (any IntelliJ-family IDE). The plugin runs on the JRE the IDE ships with (JBR 21 on 2024.2+), **independently of your project's JDK**. A project that compiles with **JDK 8** is fully supported — the plugin never loads your project's JDK, never compiles your code, and never needs Java 8 APIs. Building the plugin from source is the only Java-21 task (see Development).
- A **`repotory-mcp`** binary — this is the plugin's **data channel**: searching, reading, editing, creating and organizing all go through it over MCP stdio, exactly like the VS Code extension. The plugin looks for the binary in this order — **Settings | Tools | Cinder → MCP binary**, `REPOTORY_MCP_BIN`, `PATH`, then the usual Homebrew / Scoop / `~/.local/bin` directories. It ships with the release (`install.sh`, Homebrew and Scoop all carry it) and the plugin **never downloads it**: if no binary is found, the tool window offers the file picker and the release page instead.
- A **`repotory-server`** binary — **optional**. The server is no longer a prerequisite: the plugin talks to your knowledge base through `repotory-mcp`, which opens the same local SQLite database the desktop app uses. The server is only needed for the two things that still speak HTTP: **deep links** into the web UI, and **moving a note between projects**. The plugin can start a local one with one click; it looks for the binary in this order — **Settings | Tools | Cinder → Server binary**, `REPOTORY_SERVER_BIN`, `PATH`, then the usual Homebrew / Scoop / `~/.local/bin` directories.

Everything is loopback-only by design, so there is nothing to authenticate and nothing that leaves your machine.

### Starting the server yourself (optional)

The one-click start is just a shortcut for:

```bash
./repotory-server --port 18765 --web-dist <bundle>/web
```

`--web-dist` matters for result links. Without it the server is API-only: the API answers, but every result link 404s — and the plugin says exactly that instead of dropping you on a dead page. The release bundle's `web/` directory is the **server** build of the web assets (base `/`); the desktop `web/dist` (base `./`) is not interchangeable. The plugin does not embed the SPA itself (one rendering of the knowledge UI, the desktop app's).

## First launch

1. Open the **Cinder** tool window from the right-hand tool window bar, or **Tools | Cinder**.
2. Pick a mode — **Ask** (default), **Notes** or **Pages** — type a question and press **Enter** (Shift+Enter breaks the line). The turn appears in the feed; asking does **not** need a server — the plugin talks to `repotory-mcp`, which reads your local knowledge base directly.
3. The panel locks itself to the project your IDE project belongs to. The chip next to the modes says `🔒 <project>`; click it to search across all projects, click it again to lock back.
4. If the plugin cannot find `repotory-mcp`, the announcement bar says so and offers **Locate…** (file picker) or **Open settings**. Point it at the binary and the next question works.
5. **Open in Cinder** (on an opened card) jumps into the Cinder web UI, and that one DOES need a local server that serves the UI. The plugin starts one for you with **Start local service**: it looks for `repotory-server` (see Requirements), starts it on the configured port with its bundled `web/` directory and waits for `/health`.
6. Prefer to check things by hand? **Settings | Tools | Cinder** has the MCP binary, the address, the server binary path, **Check MCP channel**, **Test connection** and **Start local service**:
   - *repotory-mcp found at … (via …).* → the data channel is ready.
   - *Connected: … — API and web UI both reachable.* → deep links work.
   - *Connected: …, but API-only: "Open in Cinder" would 404.* → the running server has no web UI; restart it with `--web-dist` or use the release bundle.
   - *No response from …* → nothing is listening; press **Start local service**.

If you click **Open in Cinder** while the service is API-only, you do not land on a bare 404: the plugin says so and offers **Open Anyway** / **Copy URL** / **Cancel**.

## Maintaining notes without leaving the IDE

The console is also the editor. Open a card in the feed (click its title) and the card's actions appear: **Edit**, **New note here** and **AI Tidy** — the same three maintenance actions the desktop app's note list and the VS Code extension ship.

| Action | What it does |
|---|---|
| **Edit** | Opens the note in a dialog: title, kind, tags, 项目 (project, in a dropdown pre-set to the note's own project) and the Markdown body, with a live preview under the textarea. **Save** writes the fields through the `repotory_notes_update` MCP tool and then the project change through `MoveNote` (the one write the MCP surface deliberately keeps human-only, so it goes over the local HTTP bridge and needs the server). A failed save keeps your draft and says why; if only the move failed, the announcement bar says exactly that — the fields are saved. After the save the card is re-read, so the feed shows what was actually written |
| **New note here** | Creates a note under the **same project** as the card — the dialog opens empty, pre-set to that project |
| **AI Tidy** | Asks the configured AI to restructure the note: same facts, cleaner sections, a table-of-contents index when the note grew sections, and a mermaid flowchart when the content is really a process. The result is a **proposal** — it opens in the same dialog under an "AI tidied draft — review, then save." banner, so machine-generated wording is never saved silently; your save (or your edits first) is what lands it |

> **A deliberate difference from the VS Code console:** there, the editor opens *inside* the card. Swing has no inline editing surface, so the JetBrains plugin opens the shared `NoteEditorDialog` instead — a platform modal form with a live preview. The gate is the same: the AI writes a proposal, the human saves it.


The tidy proposal is generated server-side by the shared service layer (`OrganizeNote`, twin tool `repotory_notes_organize`) and writes nothing by itself — the human save is the gate, exactly as in the desktop app. The AI endpoint is configured once — in the desktop app (Settings → AI) or from the panel's **AI provider** dialog, which writes the same setting. Either way it lives in the knowledge base, so the headless service this plugin talks to reads the same one. In the dialog's preview a mermaid block shows as labelled source code — the same note renders it as a graphic in the desktop app.

## Settings

| Setting | Default | Description |
|---|---|---|
| MCP binary | empty (auto-detect) | Path to `repotory-mcp` — the plugin's data channel; empty means `REPOTORY_MCP_BIN`, then `PATH`, then common install directories |
| Server address | `http://127.0.0.1:18765` | Cinder headless service URL (deep links and moving notes) |
| Server binary | empty (auto-detect) | Path to `repotory-server`; empty means `REPOTORY_SERVER_BIN`, then `PATH`, then common install directories |

All three values are stored in the IDE's own properties store (`repotory.xml`), not in Cinder's database — a client must not write into the service's storage. **The AI provider is not here either**: it lives in the service's configuration and is edited from the panel (see *The external AI provider*), so there is nothing to keep in sync between the IDE and the desktop app.

## Scope

The console talks to three knowledge faces, and every answer comes from the shared service layer:

| Mode | Tool | What it produces |
|---|---|---|
| **Ask** | `repotory_ask` | An answer with a `Sources:` list (ADR-0033). The sources are parsed back out of the tool's text; a line that does not match the contract makes the whole answer show **verbatim** instead of a half-list |
| **Notes** | `repotory_notes_search` | Cards for notes and todos — the same FTS5 (bm25, with a `LIKE` fallback) ranking the desktop app and the VS Code extension see, computed by the shared service layer. The plugin reimplements no ranking, no snippet extraction and no gating; it renders what the service returns. Opening a card reads the note in full through `repotory_notes_read`, so the card and an agent's `notes_read` can never disagree about a note's content |
| **Pages** | `repotory_wiki_search` | Cards for the approved knowledge pages (ADR-0032) — only approved pages ever appear, because the service refuses anything a human has not reviewed |

**Approved wiki pages are searchable too** (ADR-0032), and the project lock is applied per mode as described above: server-side for Ask and Pages, presentation-side for Notes (with the count the panel filtered away spelled out).

One difference from the desktop app is worth knowing: the MCP surface **cannot clear a title** (an empty value means "keep what is there"), so a title you empty in the editor is kept as it was — the announcement bar says so instead of reporting a success that did not do what you asked. Notes tagged `handoff` are the session protocol's records and are not editable from the IDE either; write a new handoff instead.

**Not in the MVP:** session capture, and one-click MCP registration (JetBrains-side MCP client configuration proved too unstable to ship; MCP registration stays with `repotory-init` for now).

## Install

**From the JetBrains Marketplace** (2026-10-22): **Settings | Plugins | Marketplace**, search for **Cinder**, install, restart the IDE. Every released version is published there with the same version number as the desktop app.

**From disk** (offline / pre-release builds): download the zip attached to a GitHub Release and use **Settings | Plugins | ⚙ | Install Plugin from Disk…**, then restart.

The plugin is a thin client — every answer comes from the same `internal/service` layer as the Cinder Desktop app, read from your local SQLite knowledge base. Nothing leaves your machine.

## Compatibility

| | |
|---|---|
| `sinceBuild` | `242` — IntelliJ 2024.2 and newer |
| `untilBuild` | none — no upper bound, so 2025.x and later install normally |
| Built and verified against | IntelliJ IDEA CE 2025.2 |
| Project JDK | irrelevant — JDK 8, 11, 17 or 21 projects all work; the plugin runs on the IDE's bundled JBR |

The build target (2025.2) is newer than the declared floor (242) on purpose: the plugin uses only platform APIs that exist in 242 (tool window, settings page, Swing, `java.net`), and `verifyPlugin` is what proves that. Older builds declared `untilBuild = "243.*"`, which made the plugin refuse to load on every IDE after 2024.3 — including the 2025.2 it was built for. That cap is gone.

## Development

There is **no Gradle wrapper** in this directory — use a local Gradle (this plugin is developed and verified with **Gradle 8.10.2**) and **JDK 21**:

```bash
cd ide/jetbrains
gradle buildPlugin     # → build/distributions/repotory-jetbrains-<version>.zip
```

The build resolves the IntelliJ Platform 2025.2 distribution on first run (about 1.2 GB into the Gradle cache). To also run the Plugin Verifier against IDEA CE 2025.2:

```bash
gradle verifyPlugin    # reports API misuse and binary-compatibility problems
```

The plugin's own unit tests run with `gradle test` — they cover the MCP stdio client (against a fake server), the payload parsers (one case per tool mapping), the binary lookup, the **ask-answer parser** (the source list read back out of the tool's text, including the verbatim fallback) and the **conversation model** (which tool a mode talks to, what the project lock does in that mode, and what a failure looks like). The last two are plain JVM tests with no IntelliJ API in them — the model talks to a transport *port*, so a rule that only a running IDE could check is a rule nobody checks. CI runs `gradle buildPlugin` **and** `gradle test` on every push and pull request (`jetbrains-plugin-test` in [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml)), so neither a Kotlin breakage nor a broken tool mapping can sit on `master` unnoticed. `verifyPlugin` is heavier (it downloads a full IDE) and is run locally: `gradle verifyPlugin`.

### Publishing to the JetBrains Marketplace

`release.yml` publishes the tagged build with `gradle publishPlugin` (`publish-jetbrains-marketplace` job). It needs one secret and optionally three signing secrets:

| Secret | Required | What it is |
|---|---|---|
| `JETBRAINS_MARKETPLACE_TOKEN` | yes | Permanent token from **https://plugins.jetbrains.com/author/me/tokens** |
| `JETBRAINS_CERTIFICATE_CHAIN` | no | PEM certificate chain (Marketplace "verified vendor" badge) |
| `JETBRAINS_PRIVATE_KEY` | no | PEM private key |
| `JETBRAINS_PRIVATE_KEY_PASSWORD` | no | Password of that key |

Without the token the job skips with a warning and the zip stays on the GitHub Release, so a release never fails on a missing credential. Without the signing secrets the upload is unsigned — Marketplace accepts it; only the badge is missing.

To publish by hand (same artifacts, same token):

```bash
JETBRAINS_MARKETPLACE_TOKEN=… gradle publishPlugin
```

The first upload of a brand-new plugin has to be done once from the vendor account (the Marketplace creates the plugin page and assigns its numeric public URL); every later version can come from CI. The version is never passed as an argument — `build.gradle.kts` already carries the release version stamped by `scripts/bump-version.sh`, so the Marketplace listing, the GitHub zip and the desktop app share one number.

Architecture and scope: [ADR-0027](https://github.com/sky-jiangcheng/repotory/blob/master/docs/en/adr/0027-jetbrains-plugin.md), the panel's conversation form: [ADR-0036](https://github.com/sky-jiangcheng/repotory/blob/master/docs/en/adr/0036-ide-chat-console.md), and the channel split: [ADR-0035](https://github.com/sky-jiangcheng/repotory/blob/master/docs/en/adr/0035-jetbrains-mcp-primary-channel.md). The plugin is a thin client — its data goes over MCP stdio to `repotory-mcp`, and the two things that still speak HTTP (`MoveNote` and the web-UI probe) speak to the loopback-only headless service over `GET /health` and `POST /api/rpc`. Zero business logic lives here.