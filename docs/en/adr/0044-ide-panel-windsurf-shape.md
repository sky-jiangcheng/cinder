# ADR-0044: The IDE panel becomes Windsurf-shaped — the action strip moves into the panel, plus AI provider configuration and project recognition

- Status: Accepted (2026-11-03, the user supplied a Windsurf reference screenshot and made each point)
- Date: 2026-11-03
- Related: [ADR-0036](0036-ide-chat-console.md) (the panel is a conversation console; the two IDEs are isomorphic), [ADR-0035](0035-jetbrains-mcp-primary-channel.md) (MCP as the primary channel, HTTP for the gaps), [ADR-0033](0033-mcp-ask-evidence-answer.md) (the evidence-answer contract of `repotory_ask`), [ADR-0009](0009-ide-presence.md) (thin-client discipline), [ADR-0045](0045-resident-desktop-bot.md) (the resident desktop bot and module switching)

## Background

The user put Windsurf's Cascade panel screenshot next to Repotory's VS Code panel screenshot and
asked for four things: **reshape the panel after Windsurf**, **fix the button in the red box that
gets cut off**, **simplify the copy and the buttons**, and **add external AI provider configuration
so the plugin's conversation ability catches up with the desktop app's bot**. Plus one functional
ask: **when the plugin cannot recognise the current project, it should be able to recognise and set
one instead of disabling the picker.**

Taken apart, these are four independent gaps — and only the first one is purely visual.

### Gap one: the button in the red box was not "badly laid out", it was cut off by the host

The red box circles the third icon in the VS Code sidebar's title bar (sliders = `repotory.diagnose`).
It hugs the panel's right edge, shows only half of itself and carries a focus box because the root
cause lives in `ide/vscode/package.json`:

```json
"menus": { "view/title": [
  { "command": "repotory.search",      "when": "view == repotory.notesSearch", "group": "navigation" },
  { "command": "repotory.openDesktop", "when": "view == repotory.notesSearch", "group": "navigation" },
  { "command": "repotory.diagnose",    "when": "view == repotory.notesSearch", "group": "navigation" }
]}
```

VS Code renders the `navigation` group at the **far right** of the view's title bar, and this view
is a webview whose width the user drags; three icons crowd into that bar and the third crosses the
panel's right edge and is cropped by the host. **The plugin's webview cannot draw that strip** — it
belongs to the host's chrome, and the plugin can only register commands there. In other words: as
long as the actions stay in `view/title`, **no CSS can save them**; the only thing that can is
**moving those actions out of the host's chrome and into the strip the panel draws itself**.

And Windsurf's panel does exactly that: every action sits in its own title row on the right, and
nothing is mounted on the host's title bar. **The reference object hands over the fix itself.**

### Gap two: the plugin's conversation ability is one tier below the desktop bot

The desktop bot has full external AI provider configuration (the settings page's "AI" tab:
`ai_chat_base_url` / `ai_chat_model` / `ai_chat_api_key`, an OpenAI-compatible `/chat/completions`,
with the actual call in `internal/service/ai.go`). The plugin side has **no entry point at all**:
`repotory_ask` does read the same server-side configuration, but **the plugin cannot tell the server
"the endpoint is this one"** — the user can only configure it on the desktop's settings page, and
only then can the plugin answer questions.

Hence the odd state: **the desktop can configure it, the plugin cannot.** The request's "give it the
conversation ability the desktop app's bot has" lands here.

Thin-client discipline (ADR-0009) says **retrieval and ranking do not descend into the client**;
it never said **only the desktop may change the configuration**. And the `/api/rpc` bridge already
exposes `GetConfig` / `UpdateConfig` / `ListAIModels` / `TestAIChat` (they are marked
`SurfaceDesktopOnly` in `surface.go`, and that mark means "**no MCP twin**", not "unreachable from
the HTTP bridge" — `surface.go`'s comment spells out the difference at the top). So the correct way
to give the plugin a provider configuration is **not to rewrite an LLM client**, but to **read and
write the server's one configuration**.

### Gap three: when the project cannot be recognised, the user is locked out

Path → project resolution is a four-tier match (`internal/service/workspace.go`,
`ResolveProjectByPath`: path-exact repository → deepest ancestor repository → path-exact project →
deepest ancestor project). On a miss, the plugin panel sets the scope chip to `disabled`, labels it
`No project`, and the announcement bar tells the user "this folder is not in the knowledge base
yet, the panel searches across all projects" — **and then there is no next step**.

But the server has already paved the road: `ProjectResolution.Candidates` carries the candidate
project list, and `db.UpsertRepositoryTx(tx, path, projectID)` already exists — it is the very step
the scanner uses to register a repository folder under a project. **What is missing is only a "let
the user choose" action.**

### Gap four: the copy and the buttons talk about the product, not about actions

The current empty state is four paragraphs of marketing copy ("Repotory keeps what your coding
agents learn — why a decision was made, what gotcha you hit, what to do next — in a local,
searchable store…") plus a keyboard hint. The panel title row also carries the slogan
`The local-first memory layer for AI coding agents`. Windsurf's screenshot has, in the corresponding
places: one welcome line + a `Learn More` button + a `DID YOU KNOW` tip card + an `Advanced` link at
the bottom. **It talks about "what you can do now" and "where to go next", not "what I am".**

## Decision

**① The panel's action strip moves into the row the panel draws itself; `view/title` mounts
nothing.** The three `view/title` registrations are deleted from the VS Code side; the **commands
stay** (the command palette and the keybinding still reach them), they are simply no longer drawn by
the host. The JetBrains side already uses its own Swing header and is unaffected, but **its icon set
must match the VS Code side**.

**② The header is brand + a row of icon buttons on the right, drawn as inline SVG.**
On the left: the brand mark and the wordmark (the slogan line is dropped — Windsurf's title row
carries only a title). On the right: four icons, ordered by Windsurf's visual weight:
**search** (switch to Notes mode), **settings** (the AI provider panel), **diagnostics**
(environment diagnostics), **desktop** (open the Repotory desktop app).
The icons are inlined into the webview's HTML (the same technique as the brand mark) and **do not
depend on a `media/` resource root** — the webview has no `localResourceRoots` configured, so
referencing an external file 404s, and a 404 icon shows up in VS Code as **an empty box**, which is
harder to diagnose than a missing button.

**③ The bottom is one input box plus a tool row; the send key lives inside the input box.**
The send key sits at the input's bottom-right (Windsurf's shape: the ⏎ key inside the box). Under
the input box is the tool row: **the mode group** (Ask / Notes / Pages) on the left, **the project
chip** on the right. Both are chip-shaped (Windsurf's bottom row is a chip row too).
**The mode group and the project chip sit at opposite ends** with whitespace between — Windsurf's
bottom row has three evenly spaced chips, whereas Repotory's mode group has three members and the
project chip has one, so the two ends hold better than crowding them together.

**④ The copy only talks about actions.** The empty state goes from four paragraphs of marketing to
**one question plus three tappable examples** (the examples are the real entries into each mode).
The slogan line in the title row is deleted outright.
**The criterion**: every sentence on the panel either describes "what the state is now" or tells the
user "what happens if you click this"; sentences that do neither are deleted.

**⑤ The plugin's AI provider configuration reads and writes the server's `ai_chat_*`; no second
configuration is created.** The plugin's "AI settings" panel reads `GetConfig`, writes
`UpdateConfig`, pulls the model list with `ListAIModels` and probes connectivity with `TestAIChat`.
**The key values exist in exactly one place — the server side.** The plugin does not keep the
endpoint or the key in its own `globalState` or in VS Code settings. Reason: two copies will drift
(the user changes the key on the desktop, the plugin keeps using the old one), and the key
**belongs to the server** in the first place (the desktop's `ai_chat_api_key` lives in the `config`
table too). The VS Code `contributes.configuration` **gains no AI key** — that would be a third
source of truth.

**Where the form lands on each side, and where the difference is allowed to stop.**
Capability and wording correspond one to one; **only the carrier differs**: on VS Code it is a
**sheet inside the panel** (a layer drawn in the webview, dismissed by the overlay or a second
click); on JetBrains it is a **`DialogWrapper` dialog** (`ProviderDialog.kt`, summoned from the
panel's header action strip). The reason is rendering capability, not capability itself: the only
place a JetBrains tool window can host a form is a Swing dialog, and spreading the provider
configuration across the settings page would put it too far from "click the icon and configure it".
**The measure**: the fields, buttons and status wording must be word-for-word identical on both
sides; the difference is allowed only in "how this form is drawn" — the same ruler as ADR-0036's
decision 8. **Both sides read the same server-side configuration** — so what is set up in VS Code
is the same endpoint when it is opened in JetBrains.

**⑥ Project recognition = candidate projects plus one binding action.**
When the workspace misses, the panel no longer disables the chip; it shows "choose a project",
and clicking it lists the `Candidates` returned by `ResolveWorkspacePath` (plus an "all projects"
entry). After the user picks one, the plugin calls the new binding
`BindWorkspaceToProject(path, projectID)`: it upserts the workspace folder into the `repositories`
table by path and points it at that project (reusing the very `db.UpsertRepositoryTx` the scanner
uses), so **the next resolution hits repository-exact** and the panel locks onto the project on its
own. **The binding is a write, so it is a desktop-only binding** (same species as `MoveNote`:
deciding which project knowledge belongs to is a human decision) and does not enter the MCP tool
surface.

**⑦ No new MCP tool, no schema change, no change to the `repotory_ask` output contract.**
The tool count pinned by `TestToolsRegistered` does not move; the plugin's four provider bindings
and one binding binding are desktop-only capabilities reachable over the HTTP bridge.
The two IDE panels stay isomorphic (ADR-0036 decision 8 still holds): **the same structure, the same
wording**, and differences are allowed only in the layer that draws Swing as HTML — JetBrains'
`JEditorPane` speaks HTML 3.2 with no flexbox and no CSS variables, so its header icon row is drawn
with Swing components and its tool row with an HTML table, **but wording and function correspond
one-to-one**.

## Consequences

- **Positive**:
  - The cropped button in the red box **disappears at the root** — not by widening something with
    CSS, but because there is no host-drawn button in that place any more.
  - The capability gap between the plugin and the desktop bot shrinks from "can you configure an
    endpoint" to "where do you configure it".
  - When the workspace resolves to no project, the user gets an **executable next step** (pick one)
    for the first time, instead of a read-only announcement.
  - The panel's copy stops pitching the product — pitching belongs on the store page and in the
    manual; the panel talks about actions.
- **Cost (accepted)**:
  - **The panel's own action strip takes width.** Four icons plus the brand mark crowd a narrow
    sidebar (< 260px), so the brand mark hides below that width and the icon buttons stay at a fixed
    22px without shrinking.
  - **The AI settings panel is new UI inside the plugin**, and both plugins (VS Code / JetBrains)
    have to draw it, with different rendering abilities — the known cost of ADR-0036 decision 8,
    accepted here as it stands.
  - **`BindWorkspaceToProject` is a write binding.** It writes the `repositories` table, not
    knowledge content, but it does change the database; it is therefore desktop-only (no MCP twin)
    and the reason is written into `surface.go` (that table's completeness is enforced by
    `surface_test.go`).
  - **The plugin's provider configuration needs the local service running** (`/api/rpc` is an HTTP
    bridge). A working MCP channel does not imply a running HTTP bridge — the settings panel must
    **say so honestly** rather than offer a form that is bound to fail.
- **What this does not solve**: the plugin's conversation ability is still bound by the server's
  configuration. The plugin can configure the endpoint, but it has **no local vector store, no
  knowledge compilation and no review queue** — the desktop bot leads on "how knowledge grows", and
  this ADR only levels "how you ask".

## Implementation checklist

| Surface | File |
|---|---|
| Panel HTML / CSS / JS | `ide/vscode/src/chatConsoleHtml.ts` (header icon row, tool row, AI settings panel, project candidates) |
| Message contract | `ide/vscode/src/chatTypes.ts` |
| Session model | `ide/vscode/src/chatSession.ts` (provider read/write, candidate projects, binding) |
| Host wiring | `ide/vscode/src/extension.ts`, `ide/vscode/src/chatConsole.ts` |
| Manifest | `ide/vscode/package.json` (`view/title` removed; the commands and their icons stay) |
| JetBrains isomorphism | `ide/jetbrains/.../ChatConsoleHtml.kt`, `ChatModel.kt`, `ChatSession.kt`, `RepotoryToolWindow.kt` (header action strip, scope chip) |
| JetBrains provider form | `ide/jetbrains/.../ProviderDialog.kt` (`DialogWrapper`), `DiagnosticsDialog.kt` |
| Service | `internal/service/workspace.go` (`BindRepositoryToProject`) |
| Bindings | `internal/app/bindings.go` (`BindWorkspaceToProject`), `internal/app/surface.go` (classification and reason) |
