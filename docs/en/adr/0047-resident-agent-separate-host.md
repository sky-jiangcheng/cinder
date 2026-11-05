# ADR-0047: The desktop app goes back to "closing the window quits", a separate project becomes a copilot — two products, one job each

- Status: Accepted (2026-11-04, the user first picked "add a `cmd/` inside this repo" among the three options for "a separate project", then revised it the same day to "a separate repository as an independent copilot", and marked ADR-0045 Superseded (partial))
- Date: 2026-11-04
- Supersedes: [ADR-0045](0045-resident-desktop-bot.md) (resident desktop bot — closing the window does not quit, macOS uses the menu bar as the status bar) **for its decisions 1 and 2 only** (the residency and status-bar parts)
- Retained from ADR-0045: decision 3 (the bot switches modules by itself), decision 4 (scope defaults to the whole library and is switchable), decision 5 (what "independent" means), decision 6 (capability parity with the two IDE plugins), decision 7 (no schema change, no new MCP tool) — **but their scope narrows to "the small bot inside the workbench"**, see decision 2
- Related: [ADR-0046](0046-macos-reopen-handler.md) (the macOS reopen channel — this ADR leaves its **implementation without a consumer**), [ADR-0035](0035-jetbrains-mcp-primary-channel.md) (MCP as the primary channel + HTTP for the gaps), [ADR-0015](0015-w3-minimal-compile-loop.md) (approving is a human act), [ADR-0041](0041-draggable-agent-bot-and-shared-dashboard-scope.md) (draggable bot + scope as the shared single truth)

## Background

### The trigger: one "the app won't open a window" report exposed the cost of residency

On 2026-11-04 the user reported "the latest local Repotory won't open a window". The investigation
(detailed in ADR-0046's background) found that on macOS the "click the Dock icon to bring the app
back" path **had no implementation** — Wails v2's `AppDelegate` does not implement
`applicationShouldHandleReopen:hasVisibleWindows:`, and Wails hides via `[mainWindow orderOut:nil]`
(the window is taken out of the on-screen window list), which AppKit's default unhide cannot undo.

### The root cause is not the implementation but a misattribution in ADR-0045's decision 1

ADR-0045's decision 1 table says:

> `SingleInstanceLock` | a second launch wakes the running instance (shows the window + opens the bot)

On macOS that is **only half true**. Verified against the source
(`internal/frontend/desktop/darwin/single_instance.go`):

- `SingleInstanceLock`'s hand-off goes through **`NSDistributedNotificationCenter`** and only happens
  when a second process **actually starts**;
- but when the user clicks the Dock icon of a running app, LaunchServices **dispatches the reopen
  event to the existing instance** and starts no second process at all, so `SingleInstanceLock`
  **is never triggered**.

**Consequence**: ADR-0045's decision 1 believed the wake-up capability was covered, while it
covered one of three wake-up paths. The other two (the menu bar's "Show Repotory", and the
single-instance hand-off on a second launch) work; the one users reach for most (clicking the Dock
icon) was broken. **ADR-0045 never mentions "Dock", "reopen" or "clicking the icon" once — it never
considered that path at all.**

### One level deeper: the payoff of residency was never delivered

ADR-0045's background records the user's original demand:

> "The desktop app's bot should be usable independently of the desktop app"
> "Closing the desktop app should keep it in the status bar and still show the bot"

**Look at the second half: "still show the bot".** But decision 5 admits it cannot be done:

> "The bot panel is DOM inside the webview, so it always needs the host process alive"
> "Wanting 'a separate small window after closing the app' is a second native window, which
> Wails v2's single-window model cannot give; this ADR does not do it"

Verified: the assertion holds. `options.App` has no `Windows []*WebviewWindow` field, `pkg/runtime`
has no `NewWebviewWindow`, and grepping `WebviewWindow` across the whole module returns **zero
hits**.

**So a combination that spins in place emerged**: close the window → the window is `orderOut:`-ed →
**the bot (DOM inside the webview) goes invisible with it** → the process stays alive holding the
single-instance lock → the user clicks the Dock icon → **nobody receives the reopen** → the user
sees "the app won't open".

**Residency bought "the process does not exit", and charged for it "the bot is invisible while the
window is invisible, plus a zombie-like process state". What the user wanted was "the bot is still
shown". That gap is exactly what this failure felt like.**

### Two more facts found during the re-review — each one kills an original option

**First: Wails v2 cannot do a tray, and the tray is the only form of residency that actually
delivers.**

ADR-0045's decision 2 was "the macOS menu bar IS the status bar". The first draft of this ADR
still planned to follow that with "the agent process = a `cmd/repotory-agent` in this repo = another
Wails app". Verification says that road is closed:

- `options.App` has **no Tray field**; the `TrayMenu` in `pkg/menu` is a **dead type**
  (`internal/menumanager` stores it, but no platform frontend renders it and no public API reaches
  it);
- the external `energye/systray` darwin implementation needs cgo, and **CI's `go build ./...` runs on
  bare ubuntu** with no GTK/WebKit headers — systray's linux implementation needs cgo too, so
  importing it into `main.go` makes that gate **fail to compile**.

So "another resident process in the same Wails stack" ends up as: **it can stay resident, but the only
way back is "click the Dock icon again" — and that path was just proven broken.** Residency becomes a
payoff that cannot be delivered, all over again.

**Whereas Tauri v2 ships a tray**: `tauri-2.12.5/src/tray/` is a built-in module (behind the
`tray-icon` feature) — menu-bar icon, click-to-wake, background residency, all out of the box.
**That is a hard advantage of Tauri over Wails, and it is exactly the thing ADR-0045 wanted ("the
menu bar as the status bar") and could not get.**

**Second: the MCP tool surface has no approve/reject to begin with — "the workbench approves, the
copilot reads, chats and executes" is not a compromise.**

Checked against `cmd/mcp/tools_wiki.go`: the MCP surface registers only `repotory_wiki_search` and
`repotory_wiki_read`, **two pure-read tools**, and the comment puts the gate on the **write side** —
"publishing content into retrieval is a human act" (ADR-0015 decision 3). The only write tools on the
MCP surface are `repotory_notes_create` / `_update` / `_organize` and `repotory_handoff`.

**So "who may approve" was never a technical question — it is a division of labour**: approving is
**something a person does in the workbench**. A copilot running on the user's machine reads memory,
talks about the project and runs commands — **it has no business approving knowledge on the user's
behalf.**

### And one more verification: multi-process writes to the same database are not a new risk

The first draft of this ADR wrote "two processes writing SQLite at once" down as an added cost.
Verification shows it is **an existing fact**:

`cmd/mcp/main.go:27` — `repotory-mcp` **opens the database itself** (`db.InitDB(platform.GetDbPath())`),
and both IDE plugins (VS Code's `ide/vscode/src/mcpClient.ts`, JetBrains's
`ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/McpStdioClient.kt`) **spawn a `repotory-mcp`
child process over stdio**.

**So "the desktop app running + an MCP server running" already share one SQLite today.**
The copilot goes over MCP, so it **adds no writer** (it never touches the database itself; writes are
done by the MCP server on its behalf).

## Decisions

### 1. The desktop app goes back to "closing the window quits"; the residency trio is removed

`main.go` drops: the `OnBeforeClose` interception, `SingleInstanceLock`, and the menu items it built
itself. The desktop app returns to its pre-v2.29 semantics: **closing the window ends the process**,
and clicking the Dock icon starts a fresh process with a fresh window.

**Why**: the payoff of residency ("the process does not exit") **cannot be turned into user-visible
value** in this form, while its costs (the zombie process state, single-instance lock contention,
having to implement the wake-up path yourself) are all paid by the user.
**Keeping a payoff that cannot be delivered is worse than not keeping it.**

**The Edit and Window role menus stay** — Wails' darwin frontend **always** calls
`SetAsApplicationMenu`, and with no menu at all the bar would be empty, so macOS webviews have no
Cmd+C / Cmd+V. **The first item must be `menu.AppMenu()`** (macOS renders the first item as the
application menu and replaces its title with the app name), otherwise the whole Edit group collapses
into the application menu.

### 2. The desktop app's small bot stays, explicitly positioned as "part of the workbench"

The 52px draggable ball **continues to exist** (ADR-0041's draggable bot is unaffected). Its
positioning is **one view of the workbench**: it asks "what can this page answer", and the answers
come from the data that page already holds (exact, instant, no AI endpoint needed). Its scope shares
the **same single truth** as the dashboard.

**It no longer promises "still usable after you close the app" — it is a floating layer inside the
app.** The two IDE plugins are the same (their bots always depended on the editor).

**Two things it explicitly does not promise**: it is not an approval entry point (approval belongs to
the review page, which is a main surface of the workbench); and it is not a resident assistant (that
is the copilot's job).

### 3. The separate project `repotory-agent` is positioned as an **independent copilot**, not a
second identity for the workbench

The separate repository (formerly `repotory-agent-shell`, renamed as part of this ADR) is **its own
product**, not "a desktop bot that escaped the window". Its relation to the workbench is **two
products** — not master and servant, not two frontends of one product.

**What it is**: the user picks a local model (Ollama first, llama.cpp / MLX as pluggable backends),
chats about the current project, reads project context, runs commands within a whitelist, and pulls
memory back from Repotory.

**What it is not**:

- **not a second frontend of the workbench** — it shares none of the workbench's React frontend and
  does not reuse `AgentBot.tsx`;
- **not an approver** — ADR-0015's gate sits on the write side of the MCP surface by construction, so
  going over MCP puts the copilot outside it automatically;
- **not the workbench's lifeline** — the workbench can close and it keeps running; it can close and the
  workbench keeps working.

**Stack and form**: Tauri v2 + Rust backend + TypeScript frontend. **There is exactly one reason for
choosing Tauri, and it is a hard one**: it ships a tray (decision 5) and Wails v2 does not (first
verification). **Tauri uses the system WebView, not Swift/AppKit native controls** — it is "an
independent process + a real tray + a real distribution channel", **not a native UI framework**.

**Everything the user is allowed to have a preference about** — the model backend, whether it stays
resident, what the interface looks like — is explicitly left to the user.

### 4. The memory channel is MCP; it never touches the database directly

The copilot reads Repotory **only through MCP tools** (`repotory_context` / `repotory_notes_*` /
`repotory_projects_*` / `repotory_wiki_*` / `repotory_handoff` / `repotory_scan`), **never connecting
to SQLite directly and never copying the retrieval logic**.

**Why**: MCP is Repotory's public protocol (ADR-0009's thin-client discipline, ADR-0035's primary
channel), and both IDE plugins already walk that road. **Going over MCP makes the copilot the same
kind of client as the IDE plugins**, and parity is guaranteed by the protocol; talking to the database
directly would mean redoing connection management, migrations and lock contention — and it would drift.

**This also dissolves the ADR-0015 gate conflict**: the workbench (the desktop app) has every binding
in `internal/app`, so approval, merging and scope promotion all live there; the copilot goes over
MCP, whose tool surface has **no approve/reject at all** — it cannot cross that line even if it wanted
to. **The division of labour is not maintained by discipline; it is cut into the tool surface.**

**The MCP server opens the database itself** (`cmd/mcp/main.go:27`) — so the copilot adds no writer,
and "the desktop app + an MCP server writing the same SQLite" is **the state that already runs today**
(both IDE plugins do exactly that).

### 5. Waking and residency belong to the copilot's own tray; the desktop app no longer needs the
reopen hook

When the copilot stays resident it uses a **Tauri tray icon** (the `tray-icon` feature) as its
wake-up entry — an off-the-shelf Tauri capability, and exactly the thing ADR-0045 wanted and Wails
could not provide.

**The desktop app no longer has a state where "the window is hidden while the process lives"**, so
ADR-0046's reopen hook **loses its only consumer**: it solved "a hidden Wails window cannot be woken",
and the desktop app no longer hides; the copilot is Tauri and does not travel Wails' `orderOut:` road.

**Disposition**: `internal/platform/darwinreopen/` is **deleted from the repo**, and ADR-0046 is marked
**Superseded**. **Why**: the repo gains Objective-C source for the first time (`.m` + cgo + a
dependency on Wails' internal class name `AppDelegate`), and keeping **verified-correct code with no
consumer** has an ongoing cost (the cgo build surface, the fragility that a Wails upgrade invalidates)
for a problem that no longer exists.

**That code never entered the git history** — it lived only in ADR-0046's working tree and was deleted
by this ADR before it was ever committed. So there is **nothing to take back from the history**
(`git log -- internal/platform/darwinreopen/` is empty): **what remains is ADR-0046's analysis**.
If a Wails-side residency need ever appears, that analysis saves a round of investigation, but **the
code has to be written again** — that is the real trade-off between "keeping the code" (a cgo build
surface, plus the fragility that a Wails upgrade invalidates) and "keeping the conclusions" (saving one
investigation). **This ADR takes the latter.**

**ADR-0046's analysis conclusions all still hold** (Wails v2 has no reopen channel, `orderOut:` cannot
be undone, `@encode(BOOL)` has to be generated per architecture) — that part is knowledge, not code.

### 6. ADR-0045 is marked Superseded (partial), but its scope narrows

ADR-0045's **decisions 1 and 2** (residency + the menu bar as status bar) are superseded by this
ADR; **decisions 3 through 7 remain in force, but only for "the small bot inside the workbench"** —
that is: the module chips (`NAV_ENTRIES` as the single truth), scope defaulting to the whole library
and being switchable, "independent" meaning "not depending on the current page", capability parity
with the two IDE plugins, and no schema or MCP tool surface change.

**The copilot inherits none of those five**: it is an independent product, not a frontend of the
workbench. It has its own model backend and its own interface; it needs no module chips and none of
the workbench's scope semantics.

### 7. No schema change, no new MCP tool surface

Neither direction adds an MCP tool: the copilot **consumes** the existing tool surface, it does not
extend it. `internal/app`'s bindings are unchanged, the MCP tool registry is unchanged (the tool count
pinned by `TestToolsRegistered` is unchanged), and the schema is unchanged.

**The only exception is the copilot's own MCP client implementation** — in the other repository, not
in this one.

## Consequences

- **Positive**:
  - **The desktop app returns to a predictable form**: close = quit, same as v2.28 and earlier.
    Failures like "the app won't open a window" **disappear at the root** — there is no longer a
    "process alive but no window" zombie state.
  - **The workbench and the copilot each get their own job**: the workbench owns "browse, edit,
    approve, govern", the copilot owns "read, chat, execute". **They do not overlap, do not compete,
    and neither has to make room for the other.**
  - **The hard tray requirement is met**: the copilot uses a Tauri tray, so "close the workbench and
    an assistant is still there" goes from "impossible" to "possible".
  - **Approval stays with the human** — cut into the tool surface, not maintained by discipline.
  - **ADR-0046's analysis is not wasted**: the conclusion "Wails v2 has no reopen channel" saves the
    next person who wants residency in Wails a whole investigation.
- **Costs (accepted)**:
  - **Two products to maintain**: two UIs, two builds, two release channels. **They share no frontend
    component** — that is the cost of being independent products, and also the delivery of being
    independent. (The first draft planned to share `AgentBot.tsx` between them to avoid drift; once
    the positioning split, that stopped being possible, because they do not answer the same
    questions.)
  - **The copilot's capabilities are a subset of the MCP tool surface**: no approval, no scope
    promotion, no note filing. It can do less than the workbench — **that is the division of labour,
    not a defect.**
  - **Version coupling has to be handled explicitly**: the MCP tool set the copilot consumes evolves
    with Repotory. **Without a capability negotiation, "which version pairs with which version" is an
    implicit constraint** and has to be written into the copilot's README and installer (not just kept
    in someone's head).
  - **ADR-0046's implementation leaves the repo**: that Objective-C injection **never entered the
    git history** (`git log -- internal/platform/darwinreopen/` is empty) — if a Wails-side residency
    need ever appears it **has to be written again**; only ADR-0046's analysis survives.
  - **The copilot's tray is in place, but "residency" has not become a product form yet**: the tray
    is implemented in the separate repo (`src-tauri/src/tray.rs` — left click wakes the window, right
    click opens the Show / Quit menu). **But the tray is a wake-up entry for a process that is
    already running**, not a "hide the window and keep an icon" kind of residency — closing the
    window still ends the app (the same reading as decision 1). **A global shortcut and an
    always-visible window are not built yet**; the full shape of the wake-up interaction still waits
    for user feedback (see "Not decided").
- **What cannot be pretended away**:
  - **"Native" is an ambiguous word here**: Tauri uses the system WebView, not Swift/AppKit native
    controls. The copilot is "an independent process + a real tray + a real distribution channel",
    **not a native UI framework**.
  - **Whether the copilot can really run on its own depends on Repotory being installed**: it needs
    the `repotory-mcp` binary to exist (that is its memory source). **Without Repotory installed there
    is no memory** — the first screen has to say so plainly rather than pretend.
  - **The two products share one SQLite**, so write latency is real (but as noted above, that is the
    existing state, not an addition).

## Not decided (explicitly out of scope here)

- **The copilot's distribution channel**: a separate download? A Homebrew cask? Shipped together
  with the workbench?
- **The copilot's wake-up interaction**: a tray icon / a global shortcut / an always-visible window?
- **The copilot's version compatibility strategy**: capability negotiation, or a semantic-version
  constraint?
- **Whether the copilot should perform writes at all**: this ADR fixes "it goes over MCP, with one
  set of write tools", but "should the copilot write notes" is a product decision, not a technical one.

## Addendum (2026-11-05)

**Naming, unified**: the product name is **Repotory Agent** everywhere it faces users (the separate
repo `repotory-agent`); "copilot" is a **role word** only, disambiguated at its first occurrence
(**not** GitHub Copilot). **Every "copilot" in this ADR reads that way** — it means the product in the
separate repository. This repo's README and documentation home now each carry a one-line statement of
the relation in both directions (workbench vs assistant, approval lives in Repotory).

**Where the four "not decided" items above stand now** (details in the separate repo's `docs/product.md`):

| Left open back then | Settled as |
|---|---|
| The version compatibility strategy | **Settled (2026-11-05)**: **self-evident at runtime, warn in plain words** — catch the `serverInfo.version` in the `initialize` response (the server was reporting it all along; the client threw the whole response away), show "Repotory vX.Y.Z · N tools" on the first screen, warn when it is below the floor and **never hard-refuse**; **no real capability handshake** (that solves "a tool was removed or renamed", not "a tool was added" — and the added case is already covered by the dynamic fetch) |
| Whether the copilot should perform writes at all | **Settled (2026-11-05)**: **layered** — `handoff` / `scan` are written automatically; **durable knowledge (`notes_create` / `notes_update` / `notes_organize`) needs a human confirmation first** (a note is retrievable the moment it is written, so the agent could write into the very memory it is about to read next; ADR-0015's own words describe exactly that shape) |
| The distribution channel | **Still open** — Phase 6 of the separate repo |
| The wake-up interaction | **Still open** — global shortcut / always-visible window, waiting for user feedback |

**One more settled behaviour (what happens when the memory layer is unavailable)**: **the chat
continues and a banner says so out loud** — a failed context assembly must reach the user, and **a
silent fallback is never allowed** (silence means the model answers with no basis while the user
cannot tell that this turn had none).

## Addendum II (2026-11-05): no repo split, boundaries inside the repo

**The question**: this repository holds both the **workbench** (the Wails desktop app) and the
**services** (the headless HTTP server and the MCP server) — should everything except the server be
split out?

**The answer: no split.** The key fact is that **the server is not the core of this repository**: what
is actually shared is `internal/service` + `internal/db`, and the server is only **one of three doors**
onto it (the desktop Wails app, the headless HTTP API, the MCP stdio server); **the desktop app and
the MCP server both open the same SQLite directly and call the service directly — neither goes through
the HTTP server**. Splitting the repository would only turn "three doors onto one implementation" into
cross-repository release coordination.

**Why there is nothing to gain**: **the IDE plugins and the CLI were already pure HTTP clients**
(`ide/vscode` is TypeScript, `ide/jetbrains` is Kotlin, `scripts/repotory-init` is node) with **zero
coupling to the Go code** — they **are already split out**, they merely still live in the same
repository. The coupling that does exist is **at the Go module level**: one `go.mod`, one version number
(`wails.json` is the SSOT), and one release that produces the desktop bundle, the server bundle, the MCP
binary, the VSIX and the JetBrains plugin together. After a split, **schema migrations would have to be
released across repositories** (the desktop app would have to wait for a core release before it could
consume a new migration) — pure overhead for a single maintainer.

**Boundaries first — the first cut landed (2026-11-05)**: `internal/app` used to import the Wails
runtime for two side effects only (`EventsEmit`, `BrowserOpenURL`), and `cmd/server` links
`internal/app` too — so **the whole webview frontend stack (13 packages) was dragged into a binary that
never opens a window**. Both side effects are now **injected** (`app.WithEmitter` / `app.WithOpener`),
the Wails implementations moved back to the desktop entry point (`main.go`), and `internal/app` no longer
imports Wails. **The behaviour is unchanged word for word**: with no emitter, events are dropped (the
same outcome the old nil-ctx check produced); with no opener, `OpenBrowserURL` still returns the same
"browser open unavailable outside the desktop runtime". **The guard**:
`cmd/server/wails_free_test.go` walks the **module's own import graph** starting at `cmd/server` and
asserts that the whole dependency chain contains no `github.com/wailsapp/*` package; it also carries a
**reverse control** (walking from the repository root must find the desktop's own Wails import) so the
guard cannot go green by walking nothing at all.

**There are only two conditions that would trigger a split**: ① a third party needs to consume the core
**as a Go library** (rather than over MCP / HTTP); ② the desktop app's release cadence must be decoupled
from schema migrations. **"The repository looks too big" is not a reason** — that is a mood, not a
constraint.

## Landing checklist

| Surface | File / action |
|---|---|
| Desktop app drops residency | `main.go` (remove `OnBeforeClose` / `SingleInstanceLock` / the self-built menu items; **keep AppMenu + EditMenu + WindowMenu**) |
| Delete the reopen implementation | the whole `internal/platform/darwinreopen/` directory (**note: it never entered the git history — see decision 5**) |
| ADRs | this one (ADR-0047) + ADR-0046's status line to Superseded + ADR-0045's status line gets its scope narrowing + both indexes |
| Frontend: delete the dead subscription | `listenAskBot` in `web/src/api/transport.ts` is **deleted outright** (not re-commented) — `app.ask-bot`'s only sender was the menu item that decision 1 removed, so **the repo has no sender left**; the subscription and `openPanelRef` in `web/src/components/AgentBot.tsx` go with it |
| Rename the separate repo | GitHub repo `repotory-agent-shell` → `repotory-agent`; the local directory, the Rust package and lib names, `tauri.conf.json`'s `productName` / `identifier`, every documentation reference |
| Copilot positioning | the separate repo's `docs/product.md` / `docs/roadmap.md` / `README` / `AGENTS.md` rewritten as "an independent copilot" |
| Copilot gates | add `.github/workflows/` to the separate repo (docs mirror + frontend build + `cargo check` / `clippy` / `test`) |
| Copilot icons | replace the default Tauri icons via the main repo's generator (three brand domains: the OS app domain uses the squircle) |
| MCP tool copy | the hardcoded tool list in the separate repo's `src-tauri/src/memory_layer.rs` **has already drifted** (7 real tools missing) — switch to fetching it dynamically |
| The copilot's tray | the separate repo's `src-tauri/src/tray.rs` (**in place**): left click wakes the window, right click opens Show / Quit; `shell_status` exposes `tray.installed`, and the first screen carries a "wake-up entry" card |
| schema / MCP tool surface | **untouched** |

## Addendum III (2026-11-05): that "version" is the contract version, not the release version

The first-screen wording written in the addendum above — "**Repotory vX.Y.Z · N tools**" — needs one
correction. [ADR-0048](0048-core-contract-version.md) defines the MCP `initialize` response's
`serverInfo.version` as the **memory layer's contract version** (core version), not the release version.

So the first screen shows "**memory layer vX.Y.Z · N tools**" — and that X.Y.Z answers
**"did the tool surface change"**, not "is the build new". The release version is still reachable
(`/health`'s `version` key and the startup log), but it has **no answer** to "what may I rely on":
the contract surface rarely moves and the build surface moves constantly, so a 2.30.0 cannot say
whether the tool surface changed.

**The original text is kept as history.**

## Addendum IV (2026-11-05): the failure was the old build — all three Dock paths verified working now, plus a guard

**Trigger**: the user reported that "the previous Repotory could not be quit from the Dock and could not
show the main window". Those two symptoms are exactly the two signatures of the ADR-0045 resident build,
so the first thing to check was **which version is actually installed on the machine**.

**What was found**: `/Applications/Repotory.app` is **v2.29.0** (released 2026-11-04), and its binary
still carries the string `com.repotory.desktop` — the `UniqueId` of ADR-0045 decision 1's
`SingleInstanceLock`. **So the machine was running the resident build.** The commit that removed the
residency (`5f205b7`, decision 1 of this ADR) only reached a release in **v2.30.0**:
`git merge-base --is-ancestor 5f205b7 v2.30.0` is true, **and false for v2.29.0**.
**The conclusion was not "the code was never fixed" — it is "the installed copy predates the fix".**

**Why the old build produced exactly those two symptoms** (mechanism verified line by line, not guessed):

- **"Cannot be quit"**: when `OnBeforeClose` returns `true`, Wails' `frontend.Quit()` takes the branch
  `if !f.frontendOptions.OnBeforeClose(f.ctx) { f.mainWindow.Quit() }` — returning true means
  **it never quits**. The Dock's "Quit" and Cmd+Q both arrive through
  `applicationShouldTerminate:` → `processMessage("Q")` → that same `frontend.Quit()`,
  **so the same interception covers them**. The resident build did have a way out — its own
  "Quit Repotory" menu item, which sets `quitting` first and is therefore let through —
  but the route the user tried (Dock → Quit) is the system one, and that one was blocked.
- **"Cannot show the main window"**: closing called `wailsruntime.WindowHide(ctx)`, and Wails' hide
  does `orderOut:` — the window is **removed from the screen list**, not merely covered.
  Clicking the Dock icon dispatches a reopen event, and **Wails v2 has no
  `applicationShouldHandleReopen`** (the ADR-0046 finding, still true) → **nobody receives it**.

**Verified on the current build (all three paths pass — measured, not reasoned about)**: the desktop app
was built from the current code, packaged as an `.app` bundle, run with an isolated `HOME` (via
`LSEnvironment`), and then exercised with the **user's real gestures** (System Events clicking the Dock
icon, clicking items in the Dock's right-click menu):

| Gesture | Result |
|---|---|
| Click the window's red close button | **process exits** (`pgrep` finds nothing) |
| Dock right-click menu → "Quit" | **process exits** |
| Dock menu "Hide" → click the Dock icon | **the main window comes back** (same screen region captured before and after: after hiding it shows the window behind, after the click it shows the app UI) |
| `osascript`'s `quit` | the process exits, but AppleScript reports **-128 "user cancelled"** — that is the echo of Wails' `applicationShouldTerminate:` returning `NSTerminateCancel`, **not a failure** (the process exits anyway) |

**Swapped in on this machine**: the v2.29.0 bundle that was installed has been replaced with
**v2.31.1** — taken from the release's `repotory-darwin-arm64.dmg`, **byte-for-byte matching
`SHA256SUMS`** (`c788c6ff…53c`). The bundle inside the DMG is called `Cinder.app` (the **display-layer
rename**), while the **identifier layer is untouched**: the executable inside the bundle is still called
`repotory`, and so is the data directory. **The data directory stayed exactly where it was** —
**no `cinder` directory grew** under `~/Library/Application Support/repotory`, `repotory.db` opens
normally and its content reads back normally (MiddlePlatform / CBiPay / ADR-0021 are all on screen),
**the schema did not change and neither did the contract version** (`CoreVersion` stays `1.0.0`).
**Those three paths were exercised once more on the bundle actually installed** (not only on the test
bundle): red button → process exits, Dock right-click "Quit" → process exits, Dock "Hide" then click the
Dock icon → the main window comes back. The old bundle was not deleted — it was moved to
`/tmp/Repotory.app-v2.29.0.bak` in case it is needed.

**New guard**: `TestDesktopAppMustNotBecomeResident` in `main_test.go` — it parses `main.go` with
`go/parser` (not a text grep: a commented-out line must not decide whether the gate is red) and asserts
that `options.App` carries **neither** `OnBeforeClose` **nor** `SingleInstanceLock`, that `mac.Options`
carries **no** `HideWindowOnClose`, and that **no** `runtime.WindowHide` / `runtime.WindowMinimise` call
is made. **Negative cases verified**: putting each of the three fields back turns the test red once,
and restoring it turns it green again. A **reverse control** comes with it: if the `options.App` literal
cannot be found the test goes red, so the gate cannot pass by looking at nothing.

**Why the guard is worth adding**: the residency route **looks like a reasonable feature**
("keep the bot warm while the user is in the browser"), and its consequence (cannot quit, cannot be
brought back) is precisely the failure the user reported. **Decision 1 relied on one manual review
session; nothing stopped it from being "helpfully added back".**

**Explicitly not done**: **the ADR-0046 reopen hook is not coming back**. Decision 5's reasoning still
holds — the desktop app no longer has a "window hidden while the process lives" state, so the reopen
hook has **no consumer**; adding it would mean inviting back cgo + Objective-C injection code that is
"verified correct but unused". **(The thing that genuinely needs residency-based wake-up is the separate
repo's copilot, and that one uses Tauri's tray, not Wails' `orderOut:` route.)**

**Disposal**: `Repotory.app` on the user's machine is upgraded to v2.31.1. **The data directory is not
touched** (the identifier layer is frozen — `~/Library/Application Support/repotory` stays as it is),
**the schema does not change, and the contract version does not change**.

## Roadmap

1. Rename the separate repo + rewrite its positioning + add gates + icons (make the copilot a repo that
   "builds and can prove itself" first).
2. Land the copilot's MCP client (`initialize` + `tools/list` + `tools/call`), and turn that drifted
   tool list into a dynamic fetch on the way.
3. Land the Tauri tray (**in place**) + residency, delivering "close the workbench and the assistant
   is still there". **The remaining residency forms** (global shortcut / always-visible window) wait
   for feedback.
4. The copilot's form and distribution channel are decided by user feedback, not preset.
