# ADR-0035: Channel layering for the JetBrains plugin — MCP as the primary channel, HTTP filling the gaps

- Status: Accepted (**2026-10-30 — settled by the project owner after two rounds of grilling**: ① the target channel is MCP-primary with HTTP filling the gaps; ② deep links are best-effort and `repotory-server` stays optional)
- Date: 2026-10-30
- Related: [ADR-0009](0009-ide-presence.md) (this ADR **amends the wording** of its decision 1), [ADR-0027](0027-jetbrains-plugin.md) (this ADR **amends the standing** of its decisions 1 and 4), [ADR-0007](0007-session-memory-protocol.md) (the MCP protocol and its tool surface), [ADR-0017](0017-one-kernel-two-entry-points.md) (one kernel, two entry points), [ADR-0032](0032-approved-wiki-page-visibility.md) (the precedent of MCP twins), the VS Code extension (`ide/vscode`, the isomorphism reference)
- Starting schema: v28 (**this ADR changes no schema**)

## Background

This ADR answers one specific usability question: **why can't the JetBrains plugin be as immediately usable as the VS Code extension?** The answer is not a single bug — it is three independently true facts that stack.

### Fact one: the JetBrains plugin turned "is the service running?" into "does the plugin work at all?"

`ide/jetbrains/` shipped 7 Kotlin source files, and **every** data access goes through `RepotoryClient.rpc()` to `POST {serverUrl}/api/rpc` ([RepotoryClient.kt:302-320](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/RepotoryClient.kt#L302-L320)). The bindings it uses are `SearchAll` (notes + todos), `SearchWikiPages`, `GetNote`, `GetWikiPage`, `ListProjects`, `OrganizeNote`, `UpdateNoteFull`, `CreateNoteWithMeta`, `MoveNote`.

So **searching, reading a note, writing a note, re-filing a note all require something to answer on `127.0.0.1:18765`**. With no service up, the tool window cannot even issue one search (the `offline = true` branch of `RepotoryClient.search`, [RepotoryClient.kt:246-254](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/RepotoryClient.kt#L246-L254)).

**And installing the desktop app does not fix this**: the desktop app is a Wails process and listens on no port (ADR-0001 decision M1) — `repotory-server` is the thing that listens on 18765, and `scripts/install.sh` installs `repotory` + `repotory-mcp`, **not `repotory-server`**.

### Fact two: the VS Code extension has been "MCP-primary with HTTP filling the gaps" all along — it is the isomorphism reference

The real shape of the VS Code side (its call sites, not its documentation):

| Capability | Which channel the VS Code extension uses | Note |
|---|---|---|
| Search, note read/write/create, organize, wiki read/write, project list | **MCP stdio** (`repotory-mcp`) | 21 `callTool(...)` sites ([extension.ts:346-670](file:///workspace/ide/vscode/src/extension.ts#L346-L670)) |
| `MoveNote` (re-file a note into another project) | **HTTP** `/api/rpc` | `moveNoteViaRpc` ([extension.ts:700-708](file:///workspace/ide/vscode/src/extension.ts#L700-L708)) |
| `ResolveWorkspacePath` (workspace resolution) | **HTTP** `/api/rpc` | `refreshScope` ([extension.ts:267-270](file:///workspace/ide/vscode/src/extension.ts#L267-L270)) |
| Deep link (open the web UI in a browser) | **HTTP** | `openHit` ([extension.ts:527-537](file:///workspace/ide/vscode/src/extension.ts#L527-L537)) |

The reason VS Code sends `MoveNote` over HTTP is written into the code comment: **"re-filing knowledge is a human decision an agent must not make"** ([extension.ts:695-698](file:///workspace/ide/vscode/src/extension.ts#L695-L698)) — that capability **deliberately** never enters MCP. And when the service is unavailable it **degrades** rather than aborting: "Fields saved, but moving the note failed" ([extension.ts:660-665](file:///workspace/ide/vscode/src/extension.ts#L660-L665)).

**Conclusion: the VS Code extension's real shape is exactly "MCP primary with HTTP filling the gaps".** The JetBrains plugin's problem is not that it picked the wrong transport — it is that **it turned two HTTP dependencies that VS Code can degrade away from into its only path**.

### Fact three: the phrase "MCP is the only execution interface" has drifted

The repository's canonical definition is "MCP is the only **AI** execution interface" ([docs/features/ai-integration.md:117](file:///workspace/docs/features/ai-integration.md#L117), [docs/product/2026-08-24-scope-reduction.md:274](file:///workspace/docs/product/2026-08-24-scope-reduction.md#L274)) — the qualifier is **"AI"**, meaning the agent side.

But [ADR-0009 decision 1](file:///workspace/docs/adr/0009-ide-presence.md#L20) writes "MCP remains the only execution interface" and in the same sentence says "every new IDE asset … reaches the shared service layer through headless HTTP **or** MCP stdio"; [ADR-0027 decision 1](file:///workspace/docs/adr/0027-jetbrains-plugin.md#L21) goes further: it says both "the plugin … reaches the shared service layer through headless HTTP" **and** "no new execution interface is added (MCP remains the only execution interface)" — **self-contradictory in one sentence**.

There are two readings:

- **Narrow**: "the only execution interface" means the agent-side **execution** channel. A human-facing UI calls a **binding surface**, not an execution interface. Under this reading JetBrains using HTTP is not a violation — which is plainly what ADR-0027's author assumed.
- **Broad**: every channel by which anything outside the desktop app reaches Repotory capabilities counts as an execution interface, and MCP is the only one. Under this reading ADR-0027 decision 1 violates itself.

**This ADR takes the narrow reading and pins the qualifier down** — otherwise none of the trade-offs below would share a common language. Note that ADR-0009 decision 1 **already authorises** both channels in terms, so "giving JetBrains an MCP channel" is not a new interface; it is a return to an authorised shape.

### Fact four: deep links hard-depend on `repotory-server`, which has never had any distribution channel

The plugin's "open the selected item" opens the SPA in a browser, and the web UI only exists inside `repotory-server --web-dist`. When the server runs without `web/`, the link lands on a 404 and the plugin can only warn: "Repotory is running API-only — this link would land on a 404" ([RepotoryToolWindow.kt:1060-1066](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/RepotoryToolWindow.kt#L1060-L1066)).

The `repotory-server` asset does ship in releases (`repotory-server-<target>.tar.gz`, binary + `web/`), but **no installer ever installs it**: not `install.sh`, and Homebrew / Scoop carry only `repotory` and `repotory-mcp` ([packaging/README.md:8-18](file:///workspace/packaging/README.md#L8-L18)).

That leaves a closed loop: the "Consumers" column of [packaging/README.md:16-18](file:///workspace/packaging/README.md#L16-L18) lists `repotory-server`'s consumer as **"JetBrains plugin one-click start"** — its only distribution path is the plugin itself, while boundary ① of ADR-0027 decision 4 explicitly forbids the plugin from downloading a binary.

**The VS Code side never had this hole, because it never promised deep links would work** — its `openHit` just calls `openExternal` on a URL that may not resolve, with no pre-check.

### Fact five: the MCP tool surface and the HTTP binding surface share a source, but two real differences exist

MCP tools and HTTP bindings call the **same service layer**, so for most capabilities "switch transport" is not "switch capability":

| The plugin's existing binding | MCP twin | Same source? |
|---|---|---|
| `SearchAll` (notes + todos) | `repotory_notes_search` | ✅ |
| `SearchWikiPages` (scope 0, across projects) | `repotory_wiki_search` (omit `project_id` / `path` to search across projects) | ✅ |
| `GetNote` | `repotory_notes_read` | ✅ |
| `GetWikiPage` | `repotory_wiki_read` | ✅ **the same `service.WikiPageDetail`** — `out_links` / `in_links` / `source_note_ids` are all present ([wiki_browse.go:61-64](file:///workspace/internal/service/wiki_browse.go#L61-L64), [tools_wiki.go:112](file:///workspace/cmd/mcp/tools_wiki.go#L112)) — so the plugin detail pane's "Links / Backlinks / source notes" sections do not lose anything |
| `ListProjects` | `repotory_projects_list` | ✅ |
| `OrganizeNote` | `repotory_notes_organize` | ✅ |
| `UpdateNoteFull` (has `pinned`) | `repotory_notes_update` (**no `pinned`**) | ⚠️ **difference one** |
| `CreateNoteWithMeta` | `repotory_notes_create` (`project_id` / `title` / `content` / `category` / `tags`) | ✅ isomorphic parameters |
| `MoveNote` | **no twin** (zero hits in `cmd/mcp`) | ⚠️ **difference two** (VS Code also uses HTTP here — reason in fact two) |

**Difference one (`pinned`)**: `db.UpdateNoteFull(db, noteID, content, title, tags, kind, pinned)` takes `pinned`, MCP's `repotory_notes_update` does not. The plugin's note editor does write it (`pinned = draft.pinned`, [NoteEditorDialog.kt:67](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/NoteEditorDialog.kt#L67)). **Not closing it is a functional regression.**

**Difference two (handoff notes are not editable)**: `repotory_notes_update` treats notes tagged `handoff` as append-only (they are the session-memory protocol's exit records and must not be rewritten, [main.go:297-302](file:///workspace/cmd/mcp/main.go#L297-L302)); HTTP's `UpdateNoteFull` has no such guard. So after the move to MCP, **handoff notes will not be editable in either IDE** — and they are not editable in VS Code today either. That is a narrowing, and a narrowing in the right direction.

## Decision

### Decision 1: pin the term down — "execution interface" means the AI side; human-facing UIs call the binding surface

**"MCP is the only execution interface" is read as "MCP is the only AI execution interface".** Human-facing surfaces (desktop app, both IDE plugins, the browser) call the **binding surface**, and both channels are legitimate.

This ADR accordingly **amends the wording** of [ADR-0009 decision 1](file:///workspace/docs/adr/0009-ide-presence.md#L20) and [ADR-0027 decision 1](file:///workspace/docs/adr/0027-jetbrains-plugin.md#L21) by adding the "AI" qualifier. ADR-0009 decision 1's substantive authorisation ("through headless HTTP **or** MCP stdio") is unchanged.

### Decision 2: the JetBrains plugin moves to an **MCP primary channel with HTTP filling the gaps** — isomorphic with the VS Code extension

The capability routing table — **eight over MCP, two staying on HTTP**:

| Capability | Channel | Which |
|---|---|---|
| Search (notes + todos) | MCP | `repotory_notes_search` |
| Search (approved wiki pages) | MCP | `repotory_wiki_search` (omit `project_id` / `path`, keeping the plugin's existing cross-project scope) |
| Read a note | MCP | `repotory_notes_read` |
| Read a wiki page | MCP | `repotory_wiki_read` |
| Project list | MCP | `repotory_projects_list` |
| Create a note | MCP | `repotory_notes_create` |
| Edit a note (content / title / tags / kind / pinned) | MCP | `repotory_notes_update` (pinned parameter in decision 3) |
| Tidy the knowledge base | MCP | `repotory_notes_organize` |
| **Re-file a note into another project** | **HTTP** | `MoveNote` — a human-only decision, **not in MCP** (same reason as the VS Code side) |
| **Service presence / web-UI probe, deep link** | **HTTP** | raw HTTP paths, not bindings |

### Decision 3: add an optional `pinned` parameter to `repotory_notes_update`

Otherwise the migration is a **functional regression** (the plugin can set pinned today).

The implementation must obey that tool's existing **partial-update discipline**: omitting a field keeps its current value. `db.UpdateNoteMeta` replaces `title` / `tags` / `kind` / `pinned` wholesale, so the tool must read the note first and merge (the existing implementation already works that way, [main.go:287-295](file:///workspace/cmd/mcp/main.go#L287-L295) — that comment records a real incident where passing only `category` once wiped the title and tags and unpinned the note). **`pinned` must join that merge as an optional boolean; omission must never unpin a note.**

This is an **addition** to the agent-facing surface (an optional parameter on an existing tool — no new tool, no change to existing semantics).

### Decision 4: accept that handoff notes are not editable in either IDE

After the move to MCP, an IDE cannot edit a `handoff` note. **This narrowing is deliberate** and matches VS Code today: a handoff is the session-memory protocol's exit record, and correcting it means writing a new one via `repotory_handoff`. When the plugin meets such a note it must point at that flow — it must not fail silently.

### Decision 5: degradation semantics — with no service the plugin is **mostly usable**, and it must say which part is not

| Capability | With no `repotory-server` |
|---|---|
| Search / read / create / edit / organize / project list | ✅ **works as usual** (over MCP) |
| Re-file a note into another project | ⚠️ unavailable — **the save succeeded but the filing did not**, and the message must say so (mirroring VS Code's "Fields saved, but moving the note failed") |
| Deep link | ⚠️ unavailable — say plainly that it needs the local service |

**"The save succeeded but the filing did not" must not be phrased as "the save failed"**: the content really is in the store, and conflating the two makes people rewrite it.

### Decision 6: deep links become **best-effort**; no new `repotory-server` distribution channel is created for them

A deep link is an **enhancement**, not a first-class capability. Both plugins take the same posture:

- The JetBrains side stays as it is (probe `servesWebUi`, otherwise explain and offer a way to get one);
- **The VS Code side gains the same pre-check** — today it calls `openExternal` directly on a URL that may not resolve, with no explanation (this ADR closes that experience gap as a side effect).

`repotory-server` **keeps being "an optional enhancement the user obtains themselves"**. This ADR creates **no** install.sh / Homebrew / Scoop channel. The reason: the VS Code extension has shipped for its whole life with no `repotory-server` — deep links are not a usability precondition for any client, and making them one is an over-promise unique to the JetBrains side.

**Consequential correction**: the "Consumers" column of [packaging/README.md:16-18](file:///workspace/packaging/README.md#L16-L18) lists `repotory-server`'s consumer as "JetBrains plugin one-click start", which contradicts this decision (the plugin neither downloads nor has anything to point at). It should read "obtained manually (deep-link enhancement)".

### Decision 7: `repotory-mcp` binary resolution mirrors the VS Code side

The plugin resolves the binary in the order **settings → `REPOTORY_MCP_BIN` environment variable → `PATH` → common install directories** (Homebrew / Scoop / `~/.local/bin` included), and when it cannot resolve one it offers a "choose executable file" entry in the settings page plus a diagnostic of the same shape as the VS Code side (the paths tried and why each failed).

**The plugin never downloads a binary** (carrying over boundary ① of ADR-0027 decision 4 — it simply no longer applies to `repotory-server`).

### Decision 8: no schema change, no new binding, no new MCP tool

This ADR swaps a transport and adds one optional parameter to an existing tool. It **adds no `internal/app` binding** (so the binding list in `docs/api/reference.md` is untouched) and **adds no MCP tool** (so the tool count pinned by `TestToolsRegistered` is unchanged).

## Consequences

- **Positive**: the JetBrains plugin reaches parity with the VS Code extension — install `repotory-mcp` (it has **all three** channels: install.sh / Homebrew / Scoop) and you can search, read, edit and create notes; the service stops being a precondition for using the plugin. The two IDE distribution faces are now isomorphic (the same MCP tool surface, the same degradation semantics). "Desktop app installed but the plugin is dead" stops happening.
- **Negative**: the plugin needs a Kotlin MCP stdio client (`initialize` handshake + `tools/call` + JSON result parsing). **Note that most MCP tools return structured JSON** via `makeJSONResult` ([results.go:20](file:///workspace/cmd/mcp/results.go#L20)), which is cleaner than the VS Code side's parsing of `repotory_notes_search`'s text output — but `repotory_notes_search` / `repotory_wiki_search` return text and each needs its own parser.
- **Negative (agent-facing)**: adding `pinned` to `repotory_notes_update` touches the tool surface agents see. `docs/api/reference.md` (both language versions) and `docs/features/ai-integration.md` (tool descriptions) must be synchronised. It is an **addition** and changes no existing semantics.
- **Negative (behavioural narrowing)**: handoff notes are not editable in an IDE (decision 4, matching VS Code today); re-filing a note is unavailable without the service (decision 5, and the content that was saved is not lost).
- **Neutral**: the desktop app still listens on no port; `repotory-server` is still a separate binary, still shipped in releases, and still has **no distribution channel**.
- **Open**: ① how `repotory-server` should be distributed, if at all — an independent to-do, not decided here; ② whether `MoveNote` should enter MCP — **explicitly no** (human-only); a change of product stance needs its own ADR; ③ the VS Code side's `servesWebUi` pre-check is a consequential item of decision 6 and lands in the same batch as the plugin work.

## Landing checklist (implementation notes, not an implementation plan)

1. `ide/jetbrains/`: add the MCP stdio client; move the eight data calls in `RepotoryClient` onto MCP; keep `MoveNote` and the web-UI probe on HTTP; word the degradation messages per decision 5.
2. `cmd/mcp`: add optional `pinned` to `repotory_notes_update` (partial-update merge); sync `docs/api/reference.md` (both languages) and `docs/features/ai-integration.md`.
3. `ide/vscode/`: add the `servesWebUi` pre-check to `openHit`.
4. `packaging/README.md`: change `repotory-server`'s consumer to "obtained manually (deep-link enhancement)".
5. `docs/adr/0009`, `docs/adr/0027`: add the "AI" qualifier (decision 1).

## Corrections found while landing this (2026-11-01, implementation record)

**Not one decision of this ADR changed.** What follows are two FACTUAL discrepancies found by checking the code while implementing — recorded here, per this repository's "annotate, do not rewrite" practice, because implementing the text as written would have produced the wrong code.

**Correction one: the two search tools return JSON, not text.**

The "Consequences" section says "`repotory_notes_search` / `repotory_wiki_search` return text, so each needs its own parser". Checking `cmd/mcp` one by one shows **this does not hold**: both go through `makeJSONResult` and return structured JSON ([main.go:135](file:///workspace/cmd/mcp/main.go#L135), [tools_wiki.go:87](file:///workspace/cmd/mcp/tools_wiki.go#L87)). **Text appears only on the error path** (`makeTextResult`: `query is required` / `error: …` / `note not found: …` / `refusing to update note N: …`).

So there are no "two text parsers" — there is **one JSON parser plus an error branch**: text that does not parse as JSON means the tool refused, and it must **fail loudly** rather than be read as an empty result. An empty result is the one answer a user cannot tell apart from "nothing matched", which is worse than an error. While landing this, that rule was pinned as a regression case per tool mapping (`ide/jetbrains/src/test/kotlin/dev/repotory/jetbrains/McpPayloadsTest.kt`).

**Correction two: what needs syncing is the MCP tool catalog, not `docs/api/reference.md`.**

Both the landing checklist (item 2) and the "Consequences" section say "sync `docs/api/reference.md` (both language versions)". Checking it: that file documents the **binding layer** (the `internal/app` binding list), where `UpdateNoteMeta(noteID, title, tags, kind, pinned)` **already carries `pinned`**. The MCP tool surface is not in that file at all, and decision 8 explicitly adds no binding. So that file **needs no change here**. What does need changing:

- the MCP tool catalog in `docs/features/ai-integration.md` (both languages) — that is where the tool parameters live;
- the shipped skill's tool-signature references: `SKILL.md`, `skill/repotory/references/mcp-tools.md`, `skill/repotory/SKILL.md` (the latter claims its signatures are "taken from the actual `tools/list` response", so a new parameter makes them stale — that is drift).

**Landing record (2026-11-01)**: ① `cmd/mcp`'s `repotory_notes_update` gained an optional boolean `pinned` (omission keeps the current value; `false` and omission are different requests, told apart by whether the argument is PRESENT); ② `ide/jetbrains/` gained `McpStdioClient.kt` (protocol), `McpPayloads.kt` (parsers), `McpBinaryResolver.kt` (binary lookup) and `RepotoryMcp.kt` (the eight capabilities); the eight data calls moved onto MCP, `MoveNote` and the web-UI probe stayed on HTTP; ③ the settings page gained an "MCP binary" field and a "Check MCP channel" diagnostic; ④ the VS Code side's `openHit` gained the `servesWebUi` pre-check; ⑤ the plugin gained 27 JVM unit tests, now run in CI (`gradle test`).
