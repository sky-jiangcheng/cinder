# ADR-0027: JetBrains plugin kickoff — the second IDE distribution surface under the thin-client discipline

Status: **Accepted (2026-10-22)** — on the day of drafting the 4 open questions were ruled under the owner's delegation (see "Ruling on the open questions"); scheduling is a separate matter
Date: 2026-10-22
Related: [ADR-0009](0009-ide-presence.md) (this ADR revises its Decision 4: JetBrains moves from "deferred" to "kickoff"), [ADR-0007](0007-session-memory-protocol.md) (MCP as the single execution interface), [ADR-0006](0006-scope-freeze.md) (distribution surfaces are not platformization), [ADR-0017](0017-one-kernel-two-entry-points.md) (the headless API is the plugin's consumption surface), VS Code extension (`ide/vscode`, the first IDE distribution surface)

## Background

[ADR-0009](0009-ide-presence.md) Decision 4 deferred the JetBrains plugin: "an independent Kotlin/Gradle codebase doubles the maintenance surface; do not kick off or write code until a real demand signal (issues/stars) arrives and a dedicated ADR argues the case". The "issue/star magnitude" in that threshold is a **proxy metric for the public-distribution scenario** — estimating real demand from the volume of stranger users' signals, to avoid maintaining a second tech stack for a user base that does not yet exist.

On 2026-10-22 the project owner gave the demand signal directly and ruled to kick off. The proxy metric loses meaning here: the owner is themself a daily JetBrains IDE user, so the demand signal arrives in its most direct form (an explicit instruction from the owner), and the risk of "maintaining code for users who do not exist" is void — maintainer and user are the same person. ADR-0009's substantive reason for deferring (the doubled maintenance surface) still holds, but its precondition (demand uncertainty) is gone; this ADR is the execution of its legacy clause "once a real demand signal arrives, argue the case in a dedicated ADR".

Capability-side facts (each verified):

- The headless server (`cmd/server`, default `127.0.0.1:18765`, loopback-only) exposes, via the generic `/api/rpc` bridge, **exactly the same** binding surface as the desktop Wails app, plus REST endpoints for the dsh plugin / agents — every capability the plugin needs (search, notes, wiki, evidence) is already available, and `/api/rpc` is validated daily by the web frontend's real load.
- The MCP server (`repotory-mcp`) can be registered into JetBrains AI Assistant / the built-in MCP client (ADR-0009's background already confirmed native support), but that serves the agent flow only; the **human** still gets zero native UI in the IDE — command palette, tool windows, status bar: Repotory is absent. That is the visibility blind spot ADR-0009 set out to fix, continuing onto the second IDE family.
- The VS Code extension (`ide/vscode`) has landed and validated the thin-shell pattern: palette commands, status bar, sidebar search — all zero-logic-duplication through the shared service layer. The JetBrains plugin is the second reuse of that pattern, not a new pattern to explore — the design risk was already digested by the first IDE surface.

## Decision

1. **Kick off, inheriting the thin-client discipline**: the plugin is a thin shell — it calls the shared service layer over headless HTTP (primarily `/api/rpc`) with zero logic duplication; **a human-facing UI calls the binding surface, not the execution interface** (MCP stays the single **AI** execution interface, ADR-0007; the qualifier is pinned down by [ADR-0035](0035-jetbrains-mcp-primary-channel.md) decision 1); the plugin evolves only with the external API, never with internal implementation. Any sub-proposal violating this discipline is out of the MVP. (**The channel shape was changed by ADR-0035 decision 2 to "MCP primary with HTTP filling the gaps"** — this decision describes the shape as it stood when the MVP was coded.)
2. **MVP scope (porting the VS Code extension pattern)**:
   - ① Knowledge-base search tool window: FTS/hybrid search over notes + todos (via the existing `SearchAll` binding; semantic and graph-aware retrieval follow the `semantic_search` / `wiki_graph_search` gates naturally — the plugin carries no gate logic and re-implements no ranking);
   - ② Result jump: open the Repotory SPA in the system browser, located at that result (same-origin server address; deep-link routes follow the frontend as-is, supplemented during the MVP with minimal route parameters where missing; no full UI embedding inside the plugin);
   - ③ Settings page: server address (default `http://127.0.0.1:18765`) + connection health check + an optional `repotory-server` binary path (empty = auto-detect);
   - ④ One-click MCP registration (reusing `repotory-init`'s registration approach, writing the JetBrains-side MCP client configuration) — if the IDE configuration surface proves too unstable during implementation, degrade to documentation guidance without blocking the MVP.
3. **Technical form**: Kotlin + Gradle (IntelliJ Platform Plugin); an independent codebase. The doubled maintenance surface predicted by ADR-0009 is hereby **knowingly accepted**, with its boundary pressed down to the API layer by the thin-client discipline.

## Ruling on the open questions (2026-10-22, delegated by the owner)

1. **Target IDE family and version baseline → IntelliJ IDEA CE first, platform-level compatibility, baseline 242 (2024.2+)**. Measured on the owner's machine: the daily JetBrains IDE is IntelliJ IDEA CE (`/Applications/IntelliJ IDEA CE.app` = 2025.2.6.1, support dir `IdeaIC2025.2`, plus an `IntelliJIdea2024.3-backup`; no GoLand anywhere — the original question's "e.g. GoLand" premise does not hold). The plugin declares `com.intellij.modules.platform`: every API the MVP needs (tool window / settings page / browser jump / HTTP) is pure platform API, so one build installs into the whole IntelliJ family (including the PyCharm CE on this machine) with no per-product fork. `sinceBuild=242` covers the owner's recent 2024.3 and current 2025.2 with margin; development and acceptance run on IDEA CE 2025.2; the baseline is re-checked before any public distribution.
2. **Distribution → JetBrains Marketplace plus the GitHub Release zip** (2026-10-22 revision of the original "private-first" ruling): the plugin is published to the JetBrains Marketplace, using `gradle publishPlugin` on every product tag with the same version number; the zip stays a GitHub Release asset for offline / air-gapped manual installs. Signing is optional (a missing secret produces an unsigned upload, which Marketplace accepts); the first upload must be done once by the owner from a vendor account and passes Marketplace review.
3. **Session capture → out of the MVP**: strictly align with the VS Code extension's validated capability set; terminal/capture integration waits for a real pain point (original recommendation adopted).
4. **Server lifecycle → reuse first, with one-click start of a local instance** (2026-10-22 usability revision, replacing the original "connect-only"): by default the plugin reuses whatever answers on `serverUrl`; when nothing answers, it may start a **local** `repotory-server`. Four boundaries, all required: ① it only uses a binary the user already has (setting → `REPOTORY_SERVER_BIN` → `PATH` → common install directories) — no download, no build, no package-manager proxy; ② it only starts for a loopback address and refuses remote ones; ③ it never starts while `/health` answers, so it never races an existing instance for the port; ④ on IDE exit it stops only the child it spawned, never a user-started instance. Distribution accordingly gains `repotory-server-<platform>` assets (binary + `web/`; the plugin passes `--web-dist` itself), without which "one-click start" would be an empty promise for end users. The lifecycle item ADR-0009 left behind is hereby settled on the JetBrains side; the VS Code extension stays on MCP + on-demand headless connections and is not widened this round.

## Consequences

- Positive: the JetBrains family gains visible in-IDE presence (tool window / settings), completing the second distribution shelf; the thin-client discipline keeps the maintenance surface limited to API-layer evolution; the search backend directly benefits from the lane-2 production pipeline (`semantic_search` + graph-aware evidence enabled 2026-10-22), so the plugin inherits the full-pipeline caliber at zero extra cost.
- Negative: +1 independent Kotlin/Gradle codebase (the doubled maintenance surface — ADR-0009's original reason for deferring, knowingly accepted by the owner given an explicit demand signal); one build covers only JetBrains, with no VSIX-style fork-family amortization.
- Neutral: no contact with the scope freeze (`repotory` binary name / data directory / MCP prefix `repotory_*` unchanged; the plugin is a distribution surface, not platformization — ADR-0006's reconciliation clause continues to apply).
- Legacy: Marketplace listing materials (description / icon / change notes) were filled in with the 2026-10-22 publishing setup, and signing runs off CI secrets (optional). Two external steps remain outside this codebase — the owner creating a vendor account and doing the first upload, and Marketplace reviewing the first version — tracked in `TODO.md`.

## Implementation correction record (verified layer by layer while the MVP landed, 2026-10-22)

The MVP has landed in `ide/jetbrains/` (Kotlin + Gradle, 8 source files); both `buildPlugin` and `verifyPlugin` (against IDEA CE 2025.2) pass. What follows records what the implementation measured and the corrections it forced on the decision text.

### Decision 2①'s "wiki pages" search — measured, both ends are missing and the middle was never connected; the decision text is corrected

Decision 2① originally read "FTS/hybrid search over notes + wiki pages (via the existing search bindings)". Verifying layer by layer while writing the code, the real state is **three segments, each with its own status**:

| Layer | State of wiki-page search |
|---|---|
| db layer | ✅ **complete** — `db.SearchWikiPages` (`internal/db/wiki_fts.go:91`): the `wiki_pages_fts` FTS5 trigram index + bm25(3.0, 1.0) ranking + an **approved-only** filter (ADR-0015's core rule: a page no human has reviewed must never enter retrieval) + the same `escapeFTS` / `escapeFTSOR` relaxation as the notes path (a whole Chinese sentence expands into trigrams). Landed with ADR-0014 W2, with tests (`wiki_compile_test.go` / `wiki_community_test.go`) |
| service / app binding layer | ❌ **not exposed** — `SearchAll` (`internal/db/search.go:205`) covers notes + todos only; the app bindings' retrieval set is `SearchProjects` / `SearchNotes` / `SearchAll`, with **no wiki-page search entry**. The note in ADR-0014 — "search is the first thing a consumer needs, and W2 is that consumer" — W2 landed long ago, and **the consumer never came** |
| frontend | ❌ **no browsing surface** — no component in `web/src` renders the content of an approved wiki page (`Review.tsx` reads `p.content` in exactly two places: the pending review queue and the rejected tombstones); neither `/knowledge` nor `/review` accepts page parameters, and the route table (`web/src/App.tsx:324-333`) has no wiki-page entry. The real database holds **27 approved pages that are entirely invisible in the UI** |

The VS Code extension (the first IDE distribution surface, whose pattern ADR-0027 requires the MVP to port) goes through `repotory_notes_search` and **also searches notes only**.

**Ruling: decision 2① is corrected to "notes + todos" (via the existing `SearchAll` binding), and wiki-page search becomes a separate todo.** Three reasons:

1. **The thin-client discipline requires the plugin to duplicate no logic.** For the plugin to search wiki pages, the service layer needs an entry point — and none exists. Writing the FTS query in the plugin (even by copying those few dozen db-layer lines) **is** logic duplication and violates Decision 1 directly.
2. **Adding the backend binding alone does not close the loop.** Even if the service/app layer exposed `SearchWikiPages`, clicking a wiki hit in the plugin would land **nowhere** — there is no browsing surface and no addressable route for an approved page. Decision 2② asks to "open the Repotory SPA in the system browser, located at that result"; with no landing spot, that cannot be honored. The real gap is **"approved wiki pages are invisible to the user"** — an independent product gap (27 pages invisible in the UI), not a plugin problem, and closing it is a new feature rather than this ADR's wrap-up.
3. **The porting principle comes first.** Decision 2's title is literally "MVP scope (**porting the VS Code extension pattern**)", and the VS Code side's capability set is notes. Aligning the MVP with its pattern beats shipping something outside that pattern in half-working form (findable but not reachable) inside the MVP.

**Wiki-page search and its browsing surface are therefore filed as a separate todo in `TODO.md`**, to be specced in an ADR before any work: it needs a browsing UI and an addressable route for approved pages, a `SearchWikiPages` binding, and both IDE distribution surfaces consuming them.

### Decision 2②'s deep-link parameters — taken from the frontend as-is, verified

The plugin's first draft guessed `?note=` and `?page=`. The frontend's actual truth is `?noteId=` (`web/src/pages/ProjectDetail.tsx:58` reads `searchParams.get('noteId')`) and `?tab=todos` for todos (`web/src/App.tsx:323-337` route table). Corrected to match, and "deep-link routes follow the frontend as-is" is not read as license to invent parameters — the only reason a deep link exists is to **land the user where they can see something**.

### Four real defects the compile verification exposed

Compile verification (`buildPlugin` + `verifyPlugin` against IDEA CE 2025.2) surfaced four genuine problems in the first-draft code, all fixed:

| Defect | Symptom | Fix |
|---|---|---|
| `IntelliJPlatformType` not imported | Gradle script compilation failure | added `import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType` |
| Kotlin compiler older than the platform's metadata | a wall of "Module was compiled with an incompatible version of Kotlin (binary 2.2.0, expected 2.0.0)" plus an internal compiler error | Kotlin plugin 2.0.21 → **2.2.0** (a compiler must never be older than the metadata it reads) |
| `RepotoryPanel` implemented `Disposable` but was used as a `JPanel` | "Unresolved reference" on `layout` / `add` | made it a `JPanel` + `Disposable` (a tool window needs a Swing component, and it must be releasable with its content) |
| wrong `getToolWindow` entry point | "Unresolved reference 'getToolWindow'" | the correct entry is `ToolWindowManagerEx.getInstanceEx(project).getToolWindow(id)` (package `com.intellij.openapi.wm.ex`; `WindowManager` has no project-taking overload) |

Also fixed: `plugin.xml` had no `<description>` — the Plugin Verifier ruled it an "Invalid plugin descriptor" (`<description>` is a required property of a valid plugin). It is now present.

`verifyPlugin` reports **no compatibility problem** against IDEA CE 2025.2. The `sinceBuild=242` being lower than the build target 252 is deliberate (Decision 1 requires covering 2024.2+); the `build.gradle.kts` comment spells out that "the build target is the newest IDE, while the declared floor is the lowest version the plugin promises to support" — the plugin uses only platform APIs that exist in 242 (tool window, settings page, Swing, `java.net`), which is exactly what the Verifier is there to prove.

## Usability revision (2026-10-22, after real user feedback)

The owner reported two problems after using the plugin: ① unclear whether it works when the project uses JDK 8; ② the plugin only worked after starting the service in a terminal — pure GUI operation was impossible. The first is a documentation gap; the second is a product gap in Decision 4 — "connect-only" left the hardest step to the user. This round changes both.

### `untilBuild = "243.*"` was a real defect, removed

The first build declared `sinceBuild=242` + `untilBuild="243.*"`, intending "cover 2024.2 through 2024.3". The actual semantics are an **upper bound**: every IDE from 2025.1 onward — including the 2025.2 build target itself — is judged incompatible and refuses to load the plugin. The `build.gradle.kts` comment even said "untilBuild 243.* so the plugin stays installable on 2024.2", which is backwards. It now declares only `sinceBuild=242`, with no upper bound. That is defensible: the plugin uses only platform APIs present in 242, and if a future platform breaks one of them, the Plugin Verifier — not a hard-coded year — is what finds out.

Checking the packaged artifact before publishing exposed a second layer of the same trap: **removing `untilBuild = "243.*"` is not enough**. When the property is left untouched, the IntelliJ Platform Gradle Plugin's `patchPluginXml` fills in `<idea-version until-build="<build target>.*"/>` — this repo actually produced `until-build="252.*"` — silently re-capping the plugin at the IDE it was built against. Only an explicit `untilBuild = provider { null }` omits it; with that in place, `plugin.xml` inside the built zip contains just `<idea-version since-build="242" />`.

### Java 8: unrelated to the project JDK, now documented

The plugin runs on the IDE's bundled JBR (Java 21 on 2024.2+); it does not load the project JDK, compile user code, or depend on Java 8 APIs. **A JDK 8 project can therefore use the plugin normally.** The only Java 21 requirement is building the plugin from source (`jvmToolchain(21)`), which never affects end users. Both the Requirements and Compatibility sections of `ide/jetbrains/README.md` now say so explicitly.

### One-click local service start (implementation of the revised Decision 4)

New `RepotoryServer.kt` (an application-level service):

- Resolution order: the `Server binary` setting → `REPOTORY_SERVER_BIN` → `PATH` → common Homebrew / Scoop / `~/.local/bin` directories; when nothing is found it offers "Set Path… / Open Download Page / Cancel" and **downloads or builds nothing**.
- Launch: `repotory-server --port <port from serverUrl>`; when `web/index.html` sits next to the binary, it appends `--web-dist <dir>/web` automatically (the release bundle's layout), so deep links work out of the box.
- Safety boundaries: ping first and reuse any answering instance; refuse non-loopback addresses; stop only the child it spawned (`Disposer` on the application disposes it when the IDE exits).
- Entry points: a **Start local service** button appears beside the status line when a search fails at the transport layer (not on 4xx/5xx), and the settings page has the same button; a successful start re-runs the current query.

Distribution follows: `release.yml` gains a `Build headless server bundle` step that builds `repotory-server` plus the `npm run build:server` assets as `web/`, packaged per platform as `repotory-server-<target>.tar.gz` / `.zip`; `packaging/README.md` lists the new assets. Without it, one-click start would remain an empty promise for end users.

### Not changed this round

- One-click MCP registration stays out of the JetBrains MVP (Decision 2④'s degradation clause still applies; `repotory-init` only prints manual guidance).
- The VS Code extension is not widened to start a headless service — its MCP path already works standalone, and this round gave it multi-source binary resolution, silent registration and a diagnostics panel.

## JetBrains Marketplace publishing (2026-10-22, revising ruling 2)

The owner explicitly required the IDEA plugin to be published to the plugin store, so ruling 2 moves from "private-first" to "Marketplace plus the GitHub Release zip". Code and release changes:

| Surface | Change |
|---|---|
| Build | `build.gradle.kts` gains `publishing { token = providers.environmentVariable("JETBRAINS_MARKETPLACE_TOKEN") }`; `gradle publishPlugin` uploads the `buildPlugin` artifact. The three signing environment variables (`CERTIFICATE_CHAIN` / `PRIVATE_KEY` / `PRIVATE_KEY_PASSWORD`) configure `signing` **only when all three exist**; otherwise the upload is unsigned (Marketplace accepts it; only the "verified vendor" badge is missing) |
| Metadata | `pluginConfiguration.changeNotes` points at `CHANGELOG.md` (no second release narrative, ADR-0017 criterion 3); new `META-INF/pluginIcon.svg` / `pluginIcon_dark.svg` (40×40, the same R glyph re-fitted) |
| CI | `release.yml` gains `publish-jetbrains-marketplace`: `needs: create-release`, tag-only. When `JETBRAINS_MARKETPLACE_TOKEN` is missing it emits `::warning::` and skips — the zip still ships on the GitHub Release, so a release never fails on a missing credential |
| Version | No version argument is passed to `publishPlugin`: `build.gradle.kts` is stamped from `wails.json` by `scripts/bump-version.sh`, so the Marketplace listing, the GitHub zip and the desktop app share one number |

**Two steps remain manual** (outside the codebase): ① the owner performs the **first** upload from a vendor account — Marketplace then creates the plugin page and assigns the numeric public URL, after which CI can publish every later version; ② Marketplace review of the first version. The plugin ID `dev.repotory.jetbrains` is immutable after the first upload, hence finalized before it.

**Artifact check**: after `buildPlugin`, the jar's `plugin.xml` inside the zip was inspected — `<idea-version since-build="242" />` (no `until-build`), `<change-notes>` and both `pluginIcon*.svg` present; `gradle verifyPlugin` remains Compatible.

## Amendment record (2026-10-30, [ADR-0035](0035-jetbrains-mcp-primary-channel.md))

Three decisions in this ADR are amended by a later ADR; the original text is kept:

- **Decision 1's "only execution interface" gains the qualifier "AI"** — a human-facing UI calls the **binding surface**, not the execution interface. ADR-0035 decision 1 pins that meaning down: otherwise "the plugin goes through HTTP" and "no new execution interface is added" land in the same sentence, contradicting each other.
- **Decision 1's "primarily over headless HTTP" is no longer the end state** — ADR-0035 decision 2 changes the plugin to "MCP primary with HTTP filling the gaps", isomorphic with the VS Code extension: eight data capabilities go over MCP, while `MoveNote` (human-only, never in MCP) plus the service presence probe and deep links stay on HTTP.
- **Decision 4's "one-click start" is demoted in standing** — ADR-0035 decision 6 makes deep links and one-click start **best-effort enhancements**; `repotory-server` stays "an optional enhancement the user obtains themselves", and **no install.sh / Homebrew / Scoop channel is created for it**. The asset still ships as decided (decision 4's "publish a `repotory-server-<platform>` asset" is satisfied); what is no longer claimed is that "the only distribution path = the plugin's one-click start". The settings page and tool window keep their **Start local service** button, but its role changes from "a precondition for using the plugin" to "the entry point for the gap-filling and deep-link enhancements".

## Amendment record (2026-11-02, [ADR-0036](0036-ide-chat-console.md))

The decisions in this ADR are kept as written. ADR-0036 **amends the FORM of the tool window in decision 2①**:

- **Decision 2①'s "knowledge search tool window" becomes a "chat console tool window"**. The tool window is no longer "a search box + a result list + a detail pane" but a conversation feed: one question and its answer is a turn, the old turns fold away, and details open in place inside the turn that produced them (ADR-0036 decisions 1 / 3 / 7). **The tool window stays where it is; what it holds changes.**
- **The tool window can answer for the first time**: `repotory_ask` (ADR-0033) is wired, and the answer carries its source list — the retrieval surface existed long ago, the answering surface arrives with this round.
- **The scope goes from global to "locked to the current project" by default**: the chip shows `🔒 <project>` and clicking it unlocks back to cross-project (ADR-0036 decision 2). The Ask and Pages modes are **really filtered by the server**; the Notes mode has no project parameter in the tool surface, so the lock is a **presentation filter** in the plugin — and the panel says how many hits it dropped instead of pretending the count is everything.
- **Isomorphic with the VS Code side** (ADR-0036 decision 8): the same structure, the same wording, the same folding rules. Swing's rendering limits (`HTMLEditorKit` understands only part of CSS and cannot draw a collapse arrow) affect only the "how to draw Swing as HTML" layer, not the visible semantics.
