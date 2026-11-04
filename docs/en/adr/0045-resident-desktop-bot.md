# ADR-0045: The desktop bot becomes resident — it survives the window closing, switches modules itself, and is not locked to a project

- Status: Superseded (partial, 2026-11-04) — **decisions 1 (residency) and 2 (the macOS menu bar as the status bar) are superseded by [ADR-0047](0047-resident-agent-separate-host.md)**: the desktop app goes back to "closing the window quits", and a separate project becomes an **independent copilot** (`repotory-agent`). **Decisions 3 through 7 remain in force, but their scope narrows to "the small bot inside the workbench"** (module chips / scope defaults to the whole library / what "independent" means / capability parity with the two IDE plugins / no schema or MCP tool surface change) — **the copilot inherits none of those five**: it is an independent product, not a frontend of the workbench.
- Date: 2026-11-03 (decisions 1 and 2 were superseded by ADR-0047 on 2026-11-04; the scope of decisions 3 through 7 was narrowed the same day)
- Related: [ADR-0041](0041-draggable-agent-bot-and-shared-dashboard-scope.md) (the draggable bot and the shared dashboard scope), [ADR-0038](0038-desktop-information-architecture.md) (the top level converged to five entries), [ADR-0034](0034-desktop-three-column-shell.md) (the three-column shell and the hidden macOS title bar), [ADR-0044](0044-ide-panel-windsurf-shape.md) (the Windsurf-shaped IDE panel, provider configuration and project recognition)

## Background

The user raises three things that all point at the same judgement: **the desktop's bot should not be
"a floating layer inside the app window" but a resident assistant that can exist independently of
it.**

The original wording breaks into three:

1. "the desktop app's bot should be able to be used independently of the desktop app"
2. "closing the app should leave it resident in the status bar, still showing the bot"
3. "the bot should be able to switch between the various feature modules by itself"
   plus one alignment: "basically the two IDE plugins' functionality can be about the same as the
   desktop app's bot — only the desktop bot is not restricted in the range of projects".

### State one: the bot is a child node of the window; closing the window removes it

The bot in `web/src/components/AgentBot.tsx` is a **positioned overlay inside the main window's
webview** (a draggable 52×52 ball plus a 420px panel, position kept in `localStorage`). It shares
the window's life: quit the app and the bot is gone. What the user wants is **to still be able to
use it after quitting**.

### State two: Wails v2 has no tray, and the tray library would break this repo's build gates

Wails v2.17's `options.App` has **no Tray field**, `pkg/application` has no systray, and
`pkg/menu` only builds the application menu (the macOS top bar), not a status-bar icon.

`pkg/menu` does carry a `TrayMenu` type, but it is a **dead type**: `internal/menumanager`
stores it while **no platform frontend renders it and no public API reaches it** (darwin /
windows / linux `MenuSetApplicationMenu` only handle the application menu). So "Wails v2 has
no tray" holds — the reason just has to be stated correctly: not "it was never written", but
"it was written and nobody picked it up".
The external library `github.com/energye/systray` offers `RunWithExternalLoop` (designed for exactly
"the program still has to show other UI, e.g. a webview"), but its darwin implementation
`import "C"`.

That conflict was previously recorded as "it clashes with the zero-CGO release assets", but **that
ledger was half wrong**: `CGO_ENABLED=0` applies only to the two pure-Go binaries `repotory-mcp`
and `repotory-server` (`scripts/build-release-assets.sh:52`, `release.yml:229,256`,
`Dockerfile:43`); **the desktop app itself is built by `wails build` and already carries CGO on
macOS** (WebKit). So systray does not clash with the desktop app.

The real clash is elsewhere: **CI's `go build ./...` runs on a bare ubuntu-latest with no
GTK/WebKit headers** (`ci.yml`'s comment states "no GTK/WebKit system packages are installed here,
and none are needed"). systray's linux implementation needs cgo and the X11/GTK development
packages too, so importing it into `main.go` makes **that gate fail to compile in CI**.
Adding no dependency is the right answer here — not because of CGO, but because **that gate may not
break**.

### State three: the bot cannot switch modules, it can only ask

The bot's context is produced by `web/src/components/agent/agentContext.ts` **from the current
route** (the dashboard yields a "today's commit count" card, the knowledge page a "needs filing"
card…). In other words **the context follows the page; the bot does not navigate** — the user stays
on the dashboard and the bot can only talk about the dashboard. ADR-0041's decision ③ says "the
context follows the route", but that only covers "how to answer", never "where to answer".

The top level has exactly five entries (ADR-0038: Home / Dashboard / Knowledge / Review /
Settings), and the list already exists in `web/src/components/SideRail.tsx` as `NAV_ENTRIES` —
**the list the bot needs in order to switch modules is the one already written down**.

### State four: the bot's scope follows the page

ADR-0041's decision ③ defines the semantics as "the project page asks about **that project**,
everything else asks about **the whole library**". What the user asks for this time is "the desktop bot
is **not restricted in the range of projects**". The two do not conflict — **the difference lies in
"the default" and "can it be switched"**: on a project page the default is that project, but the
user must be able to switch to the whole library at any time, **and having switched, the page
must not drag it back**.

## Decision

**① Residency = the process does not exit + the window can be summoned again, using Wails' own
capabilities and adding no dependency.** Three fields, each with one job:

| Field | Job |
|---|---|
| `OnBeforeClose` | **intercepts the close**: hide the window on macOS (`runtime.WindowHide`), minimise it on Windows / Linux (`runtime.WindowMinimise`), and return `true` to prevent the exit — **only the "Quit Repotory" menu item really exits** |
| `options.App.Menu` | the resident application menu in the macOS menu bar (see decision ②) |
| `SingleInstanceLock` | a second launch summons the existing instance (show the window + open the bot) instead of opening a second process on the same SQLite |

**`HideWindowOnClose` is not used — this one is a correction made after reading the source.**
On macOS `options.App.HideWindowOnClose` does **not "hide the window", it hides the whole
application**: Wails v2.17's `internal/frontend/desktop/darwin/WindowDelegate.m` routes
`windowShouldClose` to `[NSApp hide:nil]`. Once the app is hidden the menu bar belongs to the
front app, so **Repotory's menu items are no longer in the menu bar** — exactly the opposite of
what decision ② promises. `runtime.WindowHide` goes to `[mainWindow orderOut:nil]` instead:
**the window is gone, the app is alive, the app is still the active app, and the menu bar stays.**
So the interception point is `OnBeforeClose`, and we decide ourselves between "hide the window"
and "minimise".

**② On macOS the menu bar IS the status bar; on Windows / Linux it is not, and that must be said
plainly.** The macOS application menu lives in the top bar and **stays there when the app is
inactive** — so "close the window → Repotory is still in the menu bar → click it and the window
comes back" is status-bar residency on macOS. The menu carries three items:
**Show Repotory** (summon the window), **Ask Repotory…** (summon the window and open the bot
panel), and **Quit Repotory** (a real exit).
Wails' own `menu.EditMenu()` and `menu.WindowMenu()` hang off the application menu as well:
**the Edit menu is not optional** — without it the macOS webview has no Cmd+C / Cmd+V.
Wails' own `menu.AppMenu()` is *not* used: its role is rendered by the system into a
**read-only standard application menu**, and `MenuItem.Append` refuses role items
(`isSubMenu()` is false), so our own entries cannot be hung inside it. macOS reads the menu
bar's **first** item as the application menu, so our own "Repotory" menu goes first; on
Windows / Linux it renders as a menu bar inside the window.
Windows / Linux **have no equivalent**: Wails v2 offers no tray and this ADR does not bring in
systray (see decision ① for why). On those two platforms closing the window **minimises** instead
of hiding, so the taskbar keeps the icon and the process stays resident — **the user can find it
back, but that is a taskbar icon, not a tray**. The manual and the settings page must say so.

**③ The bot switches modules by itself: a row of module chips at the top of the panel, where a
click = the main area switches route + the context follows.** The list **reuses `SideRail`'s
`NAV_ENTRIES`** (moved to `web/src/components/navEntries.ts`, with both sides importing it) and
**does not write a second one** — two lists will drift, and when the navigation drifts, the bot
and the rail point at different modules.
The list lives in its own file rather than being exported from `SideRail.tsx`: the rail is a
component (with a CSS module), and the bot has no reason to drag the whole rail into its
dependency graph for a piece of pure data.
The chips are the bot's **context selector**, not a second navigation: what they change is "which
part of the product the bot is answering about", and they drive the same route and the same data as
the rail.

**④ The desktop bot's default scope is the whole library, and any project can be selected.**
A **scope chip** sits next to the module chips in the bot panel (`All` / `This project` / a specific
project), defaulting to `All` — that is the literal reading of "the desktop bot is not restricted in
the range of projects". Even on a project page the default **stays `All`** (unlike ADR-0041's "the
project page asks about that project"; this ADR explicitly overrides it: the desktop side is a
global assistant and the page is only its current topic).

What is overridden is **only that last sentence of ADR-0041's decision ③** ("the project page
asks about that project"), **not its quick-answer table**: `agentContext` splits the two
things apart — **the quick answers follow the route** (a project page offers "project
overview / recent commits / tech stack", which are that page's own answers), while **the
free-form question follows the scope chip**. Picking a project moves both sides to that
project; staying on "the whole library" leaves a project page offering its own cards while the
question is asked about the whole library.

**⑤ "Independent of the desktop app" = not dependent on the main window's current page, not
detached from the process.** The bot panel is DOM inside a webview and will always need the host
process alive; **being summonable after the window closes and able to keep working under any
module IS the usable kind of "independent"**. A genuinely detached resident service is another
product form (`repotory-server` already is one, but it has no UI) — this ADR does not blur the two.

**⑥ The two IDE plugins and the desktop bot are aligned in capability; the only difference is
scope.** The aligned parts are already landed in ADR-0044: panel structure, question answering,
provider configuration, project recognition, writing and tidying notes.
**The only difference**: the IDE plugins lock onto the current workspace's project (the default in
an editor), while the desktop bot defaults to the whole library and can switch. **No other
difference is allowed** — "the plugin is the cut-down version" is not a product position, it is an
unfinished job.

**⑦ No schema change, no new MCP tool, no change to any decision in ADR-0038/0039/0041, no change
to the `ai_chat_*` config keys, no change to the brand's three domains.** The menu items are
**process chrome**, not knowledge capabilities, so they do not enter the MCP tool surface.

## Consequences

- **Positive**:
  - Repotory is still there after the window is closed; on macOS one click in the menu bar brings
    it back, and "Ask Repotory…" summons the bot panel directly.
  - The bot can **lead** for the first time: no need to click the rail first — it knows which
    modules exist and can take you there.
  - The capability gap between the desktop bot and the IDE plugins collapses to one item (scope);
    the two sides talk about the same set of things.
- **Cost (accepted)**:
  - **No tray on Windows / Linux.** That is Wails v2's capability boundary, not laziness here;
    writing it in the manual beats pretending there is a tray.
  - **"Closing the window does not exit" changes what users expect**: closing used to mean "done",
    now it means "still running". There must be an explicit "Quit Repotory" entry (menu item plus
    the taskbar context menu), otherwise users will think the process did not die cleanly.
  - **The bot gains two rows of controls** (module chips + scope chip). The bot is already an
    overlay on the desktop, and two more chip rows make it heavier — so the chip row **appears only
    while the panel is open**, and the collapsed state is still that 52px ball.
  - **The bot's default scope changes from "a project page = that project" to "always the whole
    library"** — an **explicit override** of ADR-0041's decision ③. The cost: on a project page,
    asking "what gotchas did this project hit recently" needs the user to switch the scope
    themselves — **the chip at the top of the bot panel shows the current scope**, so nothing is
    pretended.
- **What this does not solve**: the bot cannot leave the process (decision ⑤). A truly separate
  always-on window would be a second native window, which Wails v2's single-window model cannot
  provide; this ADR does not attempt it.

## Implementation checklist

| Surface | File |
|---|---|
| Residency and menu | `main.go` (`OnBeforeClose` / `SingleInstanceLock` / `Menu`) |
| Menu → frontend | a Wails event (`runtime.EventsEmit`, event name `app.ask-bot`) → `listenAskBot` in `web/src/api/transport.ts` (**deleted along with ADR-0047 decision 1**: the menu item and its only sender went away, leaving the frontend subscription dead code**) |
| Module switching | `web/src/components/navEntries.ts` (the module list), `web/src/components/SideRail.tsx`, `web/src/components/AgentBot.tsx` (module chips) |
| Scope | `web/src/components/AgentBot.tsx`, `web/src/components/agent/agentContext.ts` |
| Bilingual strings | `web/src/locales/{zh-CN,en}/common.json` (**both changed together**) |
