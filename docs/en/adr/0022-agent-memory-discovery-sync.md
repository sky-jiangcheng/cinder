# ADR-0022: Auto-discovery and scheduled sync of agent memory — source type visible to the user

- Status: Accepted (**promoted by user review on 2026-10-23** — the 2026-10-22 phase-by-phase verification had already closed every implementation item, and the full `go test ./...` suite ran green again at promotion time). The 2026-10-22 verification record follows (it found the actual implementation boundary differs from what the previous status line stated, and was rewritten to match the measurement): **P1 Wiring** ✅ landed (`internal/service/plugin.go:115-116` registers both CodeBuddy sources, eight sources in total; 5 tests in `internal/importers/codebuddy`); **P2 Discovery probing** ✅ landed (the `plugin.Prober` interface + `plugin.ProbeResult`, `internal/core/plugin/plugin.go:29-41`; `runtime.safeProbe` with panic recovery, `internal/core/plugin/runtime/runtime.go:151`; `SourceStatus` extended to `{name, plugin, detected, enabled, detail, type, last_run, last_error}`; all seven sources implement `Probe()`, memsrc being the built-in test source and thus not applicable); **P3 Scheduled sync** ✅ landed (`internal/service/knowledge_sync.go` ticker whose first tick lands one interval after startup, `knowledge_sync_interval_minutes` defaulting to 240 minutes, per-source `sync_source_<name>` opt-ins, claude on by default and the rest off; **the per-source `lastRun`/`lastError` were added**, in the runtime layer's `SourceStatus`, so a manual import and a scheduled round share one state); **P4 Source type and UI** ✅ landed (the derived mapping `SourceTypeFor` (`runtime.go:48`, now exported as the cross-layer single source of truth) + the four `SourceType` constants + three pinning tests in `source_type_test.go` + the settings-page type chip, detection status dot, detail, per-source sync switch, per-source sync time and error + the `settings.sourceTypes.*` i18n in both locales; **the note card now shows the label by type with a type-specific tone** — the new shared component `web/src/components/SourceTypeBadge.tsx` (the backend sends `source_type`; the frontend never re-derives it)); **P5 Verification and wrap-up** ✅ **landed** (`go test ./internal/...` green; **the golden-file regression is established** — ADR-0011 decision 5 asks for "golden-file samples per source" and that now holds across all seven sources under `internal/importers/` (claude/codex/codebuddy/opencode/openclaw/hermes/cursor, 28 sample files in total; the two SQLite-backed sources use a `.sql` dump, with the shared helpers in `internal/importers/imptest`); **the GUI smoke walkthrough is done** — the hermes chain ran end to end in an isolated environment (isolated HOME + a read-only mount of the real `~/.hermes/state.db`): probe → import → idempotent re-run → scheduled sync → settings-page display, see `outputs/screenshots/2026-10-22-adr0022-closure/`). **Nothing is left on the implementation side**: the three P4/P3 deviations are all closed and the golden-file form is closed as well; the design as a whole was promoted to Accepted by user review on 2026-10-23)
- Date: 2026-10-14
- Related: [ADR-0011](0011-multi-agent-memory-importers.md) (multi-agent memory importers), [ADR-0014](0014-llm-wiki-knowledge-compiler.md) (LLM Wiki knowledge compiler), [ADR-0015](0015-w3-minimal-compile-loop.md) (W3 minimal compile loop), [ADR-0007](0007-session-memory-protocol.md) (session memory protocol), [ADR-0009](0009-ide-presence.md) (IDE presence), TODO session-memory roadmap M2/M8

## Background

User request (target environment is a **macOS host machine**, the host application is a Wails desktop app; agent memory directories differ by OS, discovery paths are OS-specific, this ADR uses macOS as the reference): the local machine has multiple agent tools with built-in conversational memory — CodeBuddy, an **IDEA/VS Code/JetBrains plugin** (session transcripts land in `~/Library/Application Support/CodeBuddyExtension/Data/...` and curated memory in `~/.codebuddy/memery/*.md`), plus **command-line agents** such as Claude Code / Codex / OpenCode / OpenClaw / Hermes / Cursor. The user wants the application to do four things:

1. **Automatically discover** conversational memory from these agent tools on the local machine;
2. **Periodically sync** that memory;
3. **Organize** the synced content into the application's own knowledge base;
4. Make the **source type of each knowledge item visible to the user** (e.g. external import / project extraction / user conversation).

The 2026-10-14 inventory shows that points 3 and 4 already have groundwork; points 1 and 2 are pure gaps:

- **Import pipeline is ready (ADR-0011)**: `plugin.KnowledgeImporter` produces `[]plugin.ImportDoc{ProjectID,Title,Content,Tags,Kind,Source}`, registered via `Runtime.RegisterSource(/Manual)` (`internal/core/plugin/runtime/runtime.go`); `TriggerImport → upsertDoc` idempotently upserts into `project_notes` keyed on `(project_id, source, title)` (`source TEXT DEFAULT 'manual'`). Six sources are registered: `claude` (AUTO, the only auto-import on startup) and `codex/opencode/openclaw/hermes/cursor` (MANUAL, default off, explicit trigger only). `internal/service/plugin.go:89 registerBuiltinImporters` is the single registration point.
- **CodeBuddy importer is written but not wired in, not committed**: `internal/importers/codebuddy/codebuddy.go` (621 lines, untracked by git) implements two sources — `codebuddy` (session transcripts) + `codebuddy-memory` (curated memory, `codebuddy_project` target-project config) — but `plugin.go` has no import or registration for it; the comment still says "six builtin sources." This is the only half-finished operation in the current repository.
- **No auto-discovery**: source availability depends on the user knowing the paths; `SourceStatus.Enabled` is currently just a derived value of `lastErr == nil`, not a detection of "is this agent installed locally and is the memory directory readable." After installing CodeBuddy, the settings page will not spontaneously surface the source.
- **No scheduled sync**: only the startup auto-import (claude only) and manual button presses in settings; the service layer already has a `time.NewTicker` convention (`startEmbedDrainer` 5 s, `startWikiLintTicker` 6 h, page drain, compile poll — all in `internal/service/`), but knowledge import has no periodic channel.
- **Source annotation is just a raw string, no classification, nearly invisible**: `project_notes.source` stores the importer name (claude/codex/…/manual); the front-end only draws a badge for `source==='claude'` at `NoteSection.tsx:274` — all other sources, `manual`, and `handoff` (session handoff) are not shown; there is no "external import / project extraction / user conversation" classification concept and no source-filter entry point. The scan pipeline only produces `repo_meta` (README excerpt + stats, fed into LLM context), not knowledge notes, so "project extraction" is currently an empty category that needs to be reserved.

## Decisions

1. **Discovery = each source implements `Probe()`, register-and-probe**. Add `Probe() ProbeResult` to `plugin.KnowledgeImporter` (`Detected bool` + `Detail`: root path, matched file count, error summary); `Runtime` runs a read-only probe on every source at `RegisterSource` time; `SourceStatus` is extended to `{name, plugin, detected, enabled, detail}` (`enabled` keeps its original meaning: whether the last import succeeded). Probing follows the **hard-scoping discipline** from openclaw/hermes: check only whether at least one memory file exists inside the source's declared root directory (e.g. `~/.codebuddy/...`, `~/.claude/...`); never read outside the parent/sibling/subdirectory. The settings-page source list changes from "list the registry" to "what was actually detected on this machine."

2. **CodeBuddy wiring is the first new source (first-step item)**. Commit `codebuddy.go` into the repo; add `RegisterSourceManual(codebuddy.SourceName, codebuddy.New(s.db))` and `RegisterSourceManual(codebuddy.MemorySourceName, codebuddy.NewMemory(s.db))` to `registerBuiltinImporters` (keep MANUAL, session transcripts are privacy-sensitive, ADR-0011 decision 4 defaults off); add `codebuddy_project` to the "global memory source target project" input group alongside `openclaw_project`/`hermes_project` on the settings page; update the "six" comments in `plugin.go`/`runtime.go` to "eight." Cost: about one commit + ten lines of registration; fastest way to clear the "unfinished operation."

3. **Scheduled sync = service-layer sync ticker + per-source persistent switch, default off**. Add `internal/service/knowledge_sync.go`: `startKnowledgeSyncTicker` (follows the `time.NewTicker` convention, **first tick deferred by one period** to avoid full import on startup, same as `startWikiLintTicker`). Parameters:
   - Period config `knowledge_sync_interval_minutes` (default `240`, i.e. 4 hours);
   - Per-source switch `sync_source_<name>` (`'1'`/`'0'`, default `'0'` off; `claude` keeps AUTO semantics with default `'1'`) — **privacy default-off** extends ADR-0011 decision 4 ("every source defaults off, explicitly turned on") to the periodic channel;
   - Each sync round = `TriggerImport` sequentially for all `sync_source_*='1'` sources (`importMu` already serializes; mutually exclusive with the settings-page manual import, no duplication);
   - Results go through the existing `emitImportEvent → import.completed` toast, and `SourceStatus` carries `lastRun`/`lastError` for the settings page to display the last sync time and error.

4. **Source type = server-side derived classification**. Add an enum (no new DB column; `project_notes.source` already exists; classification is display/filtering semantics derived from the stored value):

   | Source type | Meaning (user-visible label) | Mapped source values |
   |---|---|---|
   | `agent-import` | External import (agent memory) | `claude, codex, opencode, openclaw, hermes, cursor, codebuddy, codebuddy-memory` |
   | `user-conversation` | User conversation (session handoff) | `handoff` (existing handoff-tagged notes), future structured conversation imports |
   | `project-extract` | Project extraction (from project/repo) | Reserved empty category; current scan produces `repo_meta` only, not notes; future README/code summaries compiled into notes fall here |
   | `manual` | Manual (user-written/edited) | `manual` and user-created notes |

   Implemented as a single `SourceType(source string) (type, label)` function in `internal/service` (or `internal/core`); unknown sources fall back to displaying the raw string with type `manual`; the front-end receives `{source, source_type, label}` through existing bindings; i18n labels go through react-i18next.

5. **Source type visible to user = three-layer UI**:
   - **Note card**: generalize the existing claude-only badge in `NoteSection.tsx` (`badgeSource`) into a "source badge" — every source displays its type label (e.g. "CodeBuddy import", "Session handoff", "Manual") + type color, alongside the existing kind badge;
   - **Settings page source list**: enhance each row in `PluginsTab.tsx` to: detection status (detected/undetected + root path) → source type → scheduled sync switch → import now → last sync time; the global memory source target-project input gains `codebuddy_project` per decision 2;
   - **Knowledge browse/search**: the note list and search results reuse the same source-badge component; the user can answer "where does this knowledge come from" on any screen that shows knowledge.

6. **Organizing into the own knowledge base = keep the two-layer pipeline, no new storage**. Import → `project_notes` (existing upsertDoc) → M6 wiki compilation (ADR-0014/0015) produces wiki pages (`page.source` inherits the note's source); source type is visible at both the notes and pages layers. This ADR adds no new tables, does not rebuild indexes, and does not touch the `embed-drainer`/vector search (new notes from sync naturally enter search via the `note_embed_dirty` queue).

## Rationale

- **Reuse over new-build**: discovery (Probe) = one read-only function per source; sync = one ticker + a set of config keys; source type = one derived function + badge generalization. All falls within the ADR-0011 pipeline and the service-layer ticker convention; no schema changes, no layering freeze violation (ADR-0006).
- **Privacy default-off continues the existing discipline**: scheduled sync is a high-sensitivity capability ("periodically reading agent private memory directories"); per-source explicit enablement is consistent with ADR-0011 decision 4, avoiding "open the app and all my agent conversations get imported."
- **Source classification via derived mapping rather than schema migration**: the `source` string is already a versioned fact (v23 and earlier); classification is display semantics; a single derived function keeps server and front-end consistent; when filtering/statistics by type becomes a frequent path, it can be materialized as a column (see open questions).
- **CodeBuddy first-step is natural**: the code is written, only wiring is missing; its dual-source shape (session transcripts + curated memory) pilots both "discovery" and "scheduled sync" forms, and directly clears the repository's only unfinished operation.

## Consequences

- **Positive**: the settings page goes from "I know which sources exist" to "the app tells me what's installed on this machine"; no hand-typed paths needed after installing a new agent; scheduled sync keeps agent memory flowing into the knowledge base incrementally and into semantic search via the existing embed-drainer; source type is visible in three places (notes, pages, settings), satisfying the first-class question "where did this knowledge come from."
- **Negative**: probing at registration adds one read-only I/O pass (one stat + a few file listings per source, acceptable); scheduled sync consumes periodic I/O/CPU proportional to the number of enabled sources, controlled by the interval config and per-source switches; source classification is a manual enum — new sources must register their mapping or they fall into `manual`/raw-string fallback.
- **Open questions**: ① Provide a "global one-click enable all source syncs" — leaning no, per-source switches better fit the privacy default-off; ② Add source-type filter and statistics to search/lists — wait for real demand signal; ③ Materialize `source_type` as a column (schema v24) — revisit when type-based filtering becomes a high-frequency path; ④ Backoff retry on sync failure — keep `TriggerImport` single-shot semantics + `lastErr` display for now, observe on real hardware.

## Phased delivery plan (for review)

- **P1 Wiring (first-step)**: `git add internal/importers/codebuddy/codebuddy.go`; register both sources in `plugin.go` + update comments; update `runtime.go` comments; add `codebuddy_project` to the `PluginsTab` target-project group. Verify: `go build ./...`, `go test ./internal/importers/codebuddy/ ./internal/core/plugin/runtime/`; run one CodeBuddy dual-source import on real hardware (requires CodeBuddy data present locally).
- **P2 Discovery (Probe)**: add the `Probe` contract to runtime and `SourceStatus.detected/detail`; implement read-only probes for seven sources (claude/codex/opencode/openclaw/hermes/cursor/codebuddy×2) — most check the root directory + at least one sample file, reusing each source's existing golden-file samples; display detection status on the settings page.
- **P3 Scheduled sync**: `knowledge_sync.go` ticker + `knowledge_sync_interval_minutes` + per-source `sync_source_<name>` + settings-page switch and lastRun display.
- **P4 Source type and UI**: `SourceType` derived function + tests (pin the mapping table above + unknown fallback); generalize the NoteSection source badge; enhance settings-page per-source type/switch info; i18n labels.
- **P5 Verification and wrap-up**: golden-file regression maintained (ADR-0011 decision 5); `go test ./internal/...`; GUI smoke test (source list discovery → enable scheduled sync for a source → note badge → settings lastRun); this ADR's promotion and implementation-correction notes are back-filled per project convention after delivery.

## Implementation correction record (phase-by-phase verification, 2026-10-22)

The previous status line claimed "P2/P4/P5 have not started". Verifying each phase against the code showed **two of them already landed and one partially landed**. Method: read the implementation and the tests phase by phase, never the index line — an ADR status line lags behind the code, and the index line lags behind the body.

### P2 Discovery probing — was written as "not started", is in fact fully landed

| Decision element | Landing site | Status |
|---|---|---|
| `Probe() ProbeResult` contract | `internal/core/plugin/plugin.go:29-41` (`ProbeResult{Detected, Detail}` + the optional-capability interface `Prober`) | ✅ |
| Probe on registration | `internal/core/plugin/runtime/runtime.go:151` `safeProbe` (with panic recovery, so one broken probe cannot take down the settings page) | ✅ |
| `SourceStatus` extension | `runtime.go:63-70`, `{name, plugin, detected, enabled, detail, type}` | ✅ |
| Per-source probes | claude / codex / opencode / openclaw / hermes / cursor / codebuddy all implement `Probe()`; memsrc is the built-in test source and does not apply | ✅ 7/8 |
| Hard scoping discipline | each source stats only its own declared root (hermes touches only `memories/*.md` and `state.db`, never `.env` / `mcp-tokens/`) | ✅ |

### P4 Source type and UI — largely landed, with a boundary that differs from the decision text

Landed: the derived mapping `SourceTypeFor` (`runtime.go:48`) + the four `SourceType` constants (`agent-import` / `user-conversation` / `project-extract` / `manual`) + three tests in `source_type_test.go` pinning the eight-source mapping and the unknown fallback + the settings-page type chip, detection status dot, detail, per-source sync switch, and preview/import-now buttons + the `settings.sourceTypes.*` i18n in both locales.

**Two deviations, both closed on 2026-10-22**:

1. **The `label` of decision 4 is not derived separately → kept as an i18n responsibility.** The decision text asks for a single `SourceType(source string) (type, label)` function returning both; the actual implementation leaves the wording in the frontend — the settings page resolves the text from `type` through `settings.sourceTypes.<type>`. This is a **better division of labour** (the wording does not travel with the server binary), so it does not match the decision text and is recorded here.
2. **Decision 5's "note card shows the label by type, with a type-specific tone" → landed.** The `sourceLabel` in `NoteSection.tsx` used to build its badge from the capitalized source string (`Codex`, `Opencode`), a different vocabulary from the settings page's "Agent 记忆 / 会话记录" — the user could not tell from a card which category it belonged to. How it closed:
   - The backend adds a **derived field** `source_type` to `domain.Note` (no new DB column — decision 4 already ruled that the classification is presentation semantics), filled by the service layer on the note-return path. The mapping function went from package-private to the exported `SourceTypeFor`, becoming a cross-layer **single source of truth** (the frontend never re-derives it, so the two cannot drift).
   - The frontend adds a shared component `web/src/components/SourceTypeBadge.tsx`: label by type (through i18n) with a type tone (agent-import=info / user-conversation=accent / project-extract=success), the raw source name demoted to a tooltip (the user still sees whether it was claude or codex), and no badge at all for `manual` (a hand-written note has no provenance to mark).
   - The component ships 5 tests pinning four contracts: `manual` renders no badge / an unknown type renders no badge (no guessing) / the type decides the tone / the raw name appears only in the tooltip.

### P3 Scheduled sync — largely landed, the lastRun/lastError granularity now filled in

Landed: all of `internal/service/knowledge_sync.go` (ticker whose first tick lands one interval after startup, `knowledge_sync_interval_minutes` defaulting to 240 minutes, per-source `sync_source_<name>` opt-ins, claude on by default and the rest off, `importMu` serializing against manual imports, one failing source not aborting the others) + 5 tests.

**The original deviation (global granularity) closed on 2026-10-22**: decision 3 asks to "carry `lastRun`/`lastError` on `SourceStatus` for the settings page to show the last sync time and error", and originally only a global ticker-level `KnowledgeSyncStatus` existed (describing the round as a whole, `knowledge_sync.go:143-146`), which cannot answer "which source failed". How it closed: the per-source state lives in the **runtime layer** (`sourceEntry.lastRun` + `SourceStatus.last_run/last_error`), not in the service layer — because the manual import, the scheduled round and the startup auto-import **all go through `TriggerImport`**, so recording it in the runtime is the one place that sees all three paths; a second copy in the service layer would be a second truth. The settings page now shows each source's own last sync time and error, alongside the global round line.

### P5 Verification and wrap-up — all three requirements met

| Requirement | Actual | Verdict |
|---|---|---|
| golden-file regression (ADR-0011 decision 5) | **all seven sources now have a `testdata/` directory** under `internal/importers/` (28 sample files in total): the inline fixtures of claude / codex / codebuddy / opencode / openclaw / hermes / cursor were all migrated into sample files | ✅ established |
| `go test ./internal/...` | green | ✅ |
| GUI smoke walkthrough (source list discovery → scheduled sync → note badge → settings lastRun) | the full hermes chain ran in an isolated environment (isolated HOME + a read-only mount of the real `~/.hermes/state.db`) — see the table below | ✅ |

**GUI smoke walkthrough, measured 2026-10-22**:

| Step | Result |
|---|---|
| Probe | `detected=true`, detail "~/.hermes/state.db：发现会话库" |
| First import | `created=1, updated=0, skipped=0` |
| Re-run idempotency | `updated=1`, note count unchanged |
| Imported content | the first question and the last reply are both preserved whole |
| Target project | without `hermes_project` → lands in "Uncategorized" (the established semantics); with `hermes_project=lushan` → lands in project_id=41 |
| Scheduled sync | `sync_source_hermes=1` with a 1-minute interval → `last_run` filled, `last_error` empty, note count unchanged (idempotent) |
| Settings UI | the "Plugins" tab, hermes row: "Data found" + an "Agent 记忆" chip + the sync switch reading "Syncing" + the detail "~/.hermes/state.db：发现会话库" |

Screenshots: `outputs/screenshots/2026-10-22-adr0022-closure/`.

**The golden-file row was once filed as "form not met"**: the original intent of ADR-0011 decision 5 is "each source pins its parse result against fixed sample files", so that a drift in the source format raises an alarm immediately. What existed then was 66 tests using inline string samples — which pins the same parse results and alarms on the same drift. **The capability is equivalent**; the difference is only whether the samples live beside the code and whether a drift shows up as a cleaner diff. So it was filed as closure work rather than a defect.

**Closed on 2026-10-22**: all seven sources migrated their inline fixtures into `testdata/` sample files, 28 files in total:

| Source | Sample shape |
|---|---|
| claude | `testdata/memory/{project.md,notes.txt}` + `testdata/oversize/project.md` (the oversized file is still generated in code — a 1MB blob of 'x' has no business in the repo) |
| codex | `testdata/rollout.jsonl` (with a non-JSON line) + `testdata/rollout-envelope.jsonl` (the real 2026-07 shape) |
| codebuddy | `testdata/session/{index.json,messages/*.json}` + `testdata/fork/{index.json,messages/*.json}` |
| opencode | `testdata/{session.json,object-summary.json,notjson.json,empty.json}` |
| openclaw | `testdata/workspace/{SOUL.md,USER.md,notes.txt,sub/nested.md}` + `testdata/vault-roots.md` |
| hermes | `testdata/memories/{MEMORY.md,USER.md,notmd.txt}` + `testdata/sibling/{leak.md,.env}` + `testdata/state.sql` |
| cursor | `testdata/state.sql` + `testdata/workspaceStorage/<32-hex>/workspace.json` ×2 |

The two SQLite-backed sources (hermes `state.db`, cursor `state.vscdb`) use a **`.sql` dump** rather than a binary `.db` file: a binary blob shows nothing in a diff when the format drifts, while a dump reads line by line — the schema and the rows *are* the definition of the real format. That is why `internal/importers/imptest` gained two shared helpers — `CopySample` (a file or a whole directory tree) and `ExecScript` (one Exec, many statements); they are shared across packages so the same copy of the logic cannot drift in seven places.

The samples also **keep the negative cases**: every source's sample directory holds files that *must* be ignored (openclaw's `sub/nested.md`, hermes's `sibling/leak.md` and `sibling/.env`, cursor's unknown workspace, codex's non-JSON line), so the allowlist and the fault tolerance are pinned by the samples too — not just the "it parsed some content" half.

### Closure list (by priority)

1. ~~**P4 decision 5, note side**: the note card should classify by `SourceType` (label through i18n, tone by type), matching the settings-page vocabulary~~ — **closed (2026-10-22)**: the backend sends `source_type` + the frontend has the shared `SourceTypeBadge` component + 5 tests;
2. ~~**P3 lastRun/lastError granularity**: add per-source `last_run` / `last_error` to `SourceStatus`~~ — **closed (2026-10-22)**: recorded in the runtime layer, displayed per source on the settings page;
3. ~~**P5 golden-file**: give each source a `testdata/` directory and migrate the inline fixtures into sample files~~ — **closed (2026-10-22)**: all seven sources covered, 28 sample files, the two SQLite sources as `.sql` dumps;
4. ~~**P5 GUI smoke walkthrough**: four steps — source list detection → enable scheduled sync for a source → note badge → settings lastRun~~ — **closed (2026-10-22)**: the full hermes chain ran in an isolated environment, see the table and the screenshots above.

All four closure items are closed, so "the source type is visible to the user" truly closes the loop. The ADR stays Proposed — the design itself is still up for review — but **nothing is left on the implementation side**: P1–P5 now match what this ADR describes, and the three deviations of P3/P4 were corrected against the measurements and recorded. (**Note 2026-10-23**: this was the pre-promotion closing statement; the design was promoted to Accepted by user review the same day — the status line is authoritative.)

## Addendum (2026-11-23)

Pending item 2 (source-type filtering on search/lists) was closed by [ADR-0072](0072-search-transparency-recall-provenance.md): SearchHit carries an optional `source_type` and the knowledge search surface filters client-side via chips; pending item 3 (materialized column) stays deferred with its trigger unchanged.
