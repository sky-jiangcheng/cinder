# ADR-0040: Orientation for the unscoped ask, and handoff records as evidence

- Status: Accepted (2026-11-02 — the user picked each of the three options explicitly)
- Date: 2026-11-02
- Related: [ADR-0014](0014-llm-wiki-knowledge-compiler.md) (layered context L3→L0 and the evidence layer), [ADR-0031](0031-global-page-scope.md) (**L3 gets no automatic fallback** — this ADR does not change that), [ADR-0015](0015-w3-minimal-compile-loop.md) (the compile gate), [ADR-0033](0033-ai-chat-endpoint.md), [ADR-0007](0007-session-memory-protocol.md) (the session-memory protocol), [ADR-0025](0025-backup-recovery.md)

## Background

It starts with a user question: **"We configured an external AI — why can't our Agent read the local projects at all?"** Tracing it down turns up three **mutually independent** breaks — the first two live in the code, the third in the distribution state. All three have to be present for the symptom to look the way it does.

### Break #1: under the default scope, the whole context layer disappears

The scope dropdown of the ask panel defaults to "all knowledge and all projects", which is `projectID = 0` on the backend. `evidenceMessages` in `internal/service/wiki_evidence.go` wraps the entire context assembly in `if projectID > 0`:

```go
if projectID > 0 {
    lay := s.BuildLayeredMemory(projectID, "", LayerBudget{}, false)
    ...
} else if ev != "" {
    messages = append(messages, chatMessage{Role: "system", Content: ev})
}
```

So under the default scope the model receives "the retrieved pages/notes + the question" and **no orientation at all**. It does not know which projects the user has, it does not know the shape of the library, and it can only try to answer from the few text fragments it retrieved.

Measured (v2.24.0 live library, 31 projects): `LayeredProjectContext(0, "")` → **chars=0, blocks=[]**; `LayeredProjectContext(124, "")` → 1000 characters (Project / Path / Repositories / Languages / Top contributors). One interface, two scopes, an order of magnitude apart.

And **L3 (`layerGlobalProfile` in `context_layers.go`) does not depend on projectID at all** — in `BuildLayeredMemory` it sits **outside** the `if projectID > 0`. A condition that has nothing to do with it keeps it out.

### Break #2: handoff records have no AI entry point

A handoff record is a note carrying the `handoff` tag (`IsHandoffNote` in `context.go`). The only entry point that lets an agent see one is MCP's `repotory_context` (`cmd/mcp/tools_context.go` → `BuildProjectContext` in `context.go`), which renders the **latest handoff in full** and collapses older ones into a one-line timeline. `context.go` says it plainly: "a truncated exit record is worse than useless — the next session would act on half a summary".

The AI ask path does not go through that. `GatherEvidence` (`wiki_evidence.go`) retrieves **wiki pages + notes** (FTS / semantic) and has **no channel by time or by type**. Meanwhile handoff titles are English (`Session Handoff 2026-10-26 18:34`), so a Chinese question — "最近有哪些会话交接记录" — **does not match** in FTS.

Measured: the Chinese question hits 4 ADR notes and **0 handoff records**; swapping the query word for the English `handoff` hits 3 Session Handoffs. **This is not a retrieval weakness — the channel simply does not exist.**

### Break #3: the agent inside the IDE still runs an old binary

`~/.claude.json` registers `command = "repotory-mcp"` (resolved through PATH, no absolute path). `/usr/local/bin/repotory-mcp` on PATH is **v2.11.1** and actually exposes 13 tools (measured via tools/list); `/usr/local/bin/repotory-server` is v2.18.0. The current source is v2.24.0 / 18 tools — missing `repotory_ai_audit`, `repotory_ai_review_rules`, `repotory_notes_organize`, `repotory_wiki_read` and `repotory_wiki_search`.

**This one is not the cause of "can't read the local projects"** — the MCP path reads the local library and has nothing to do with the AI endpoint; ADR-0017's "one kernel, two entry points" holds here. But it leaves the agent inside the IDE five tools short, and the IDE is exactly where a user is most likely to assume "the Agent is reading my projects".

### One more thing has to be said up front: L3 is empty right now

Of the 45 wiki pages in the live library, **0 are global pages** (`project_id IS NULL`). ADR-0031 gave a human the action "promote a page to global scope" (the review page's "page scope" area), and nobody used it. ADR-0015 states outright that "**Producing L3 automatically is exactly the model writes into the store with no gate**", so L3 gets **no automatic fallback** — and neither does this ADR.

Which means the "restore L3" half **produces nothing at all** on the current library. What actually delivers "the Agent can read the local projects" is the other half: **the whole-library project catalog**. That has to be written into the decisions, otherwise they read as "L3 is now alive".

## Decisions

### 1. Orientation even without a scope: L3 plus the whole-library project catalog

**"Every layer has its scope."** L2 answers "what is in this scope": when the scope is one project, that layer's body is that project's scenario; when the scope is the whole library, the same layer's body becomes the library's catalog.

- `MemoryLayer` gains `LayerProjectCatalog = "L2-catalog"`.
- `BuildLayeredMemory`: `projectID > 0` → the L2 project scenario (**unchanged**); `projectID == 0` → the L2 project catalog. The two are **mutually exclusive**; they never stack.
- `evidenceMessages` drops the block-level gate and becomes "always assemble, give whatever came back". When `projectID == 0` and the orientation layers come back empty, it still gives just the evidence block — **removing the condition must not turn into "no system message at all"**.
- **The catalog is deliberately coarse**: project name / root path / repository count / note count. Tech stack, README excerpt and contributors stay **project-scoped only** (the existing `aiProjectBaseContext` semantics); the catalog is orientation, not detail, and does not build a deep profile per project.
- **A project with neither repositories nor notes does not enter the catalog**: it is an empty shell row, says nothing about the library and would only spend budget. An empty library therefore still assembles **zero blocks** (the existing "an empty store produces no context" assertion keeps holding).
- **Ordering**: note count descending (knowledge density first), then project name. A project with many repositories but no notes sorts later, because it contributes less to "asking about knowledge".
- **Its own budget**: the catalog gets its own character cap (`LayerBudget.CatalogChars`, default 2400), not shared with the L2 project scenario — this repository's standing principle is "budgets are per layer, because a shared cap lets a chatty layer crowd out the layer that gives the model its bearings". Over budget, the existing shape applies: **trim rather than mute the whole layer**, and report `omitted` honestly on the block.

**The catalog is not L3.** L3 is "human-approved wiki pages belonging to no project", and ADR-0031 explicitly refuses an automatic fallback. The catalog is the **fact** of "which projects this library holds", aggregated from the projects / repositories / project_notes tables — not content the model wrote into the store. Their sources and their gates differ, and conflating them would hollow out ADR-0031's gate.

**L3's current state goes into the docs, not into an automatic fallback**: 0 global pages out of 45. To actually use L3, a page has to be promoted to global scope in the review page. That is a user action; no code can stand in for it.

### 2. Handoff records enter the evidence layer

- `GatherEvidence` gains a third candidate source: **the most recent handoff records**. It reads through the existing `db.ListRecentHandoffNotes` (the same `handoff` tag predicate + `ORDER BY updated_at DESC`) and **does not write a second tag-matching rule** — two copies of that predicate will eventually diverge.
- **How many**: at most 3 — the **latest one in full** (ceiling 1200 characters, a new constant `handoffEvidenceRunes`), the 2 before it collapsed into a **one-line timeline** (reusing `oneLineSummary`, capped in line with `contextHandoffTimelineLen`). The shape matches `BuildProjectContext` — **the latest one must survive complete, the older ones are history and only need to be recognisable as which session they were**. That shape is already pinned by abeval's "first-position hit rate" metric.
- **Why the ceiling is 1200 and not `contextHandoffFullLen`'s 4000**: the whole evidence budget is 4000 characters, so one item allowed 4000 would crowd out the other eleven. A handoff record is a **whole session's record** and its body is far longer than an ordinary note's, so it gets its own ceiling — but **depth is still arbitrated by the overall evidence budget** (L0 admits at a short excerpt, L1 deepens within the total budget, the same two-phase mechanism as every other piece of evidence).
- **Its L0 depth is separate too**: an ordinary note's 140 characters applied to a handoff leaves a Markdown heading and half a clause — not a readable summary — so handoff records are admitted at 400 characters.
- **Scope**: `projectID > 0` → that project's handoffs; `projectID == 0` → the most recent across the library.
- **It is evidence, not context**: it goes through the same budget competition and the same citation refs (notes use the `N` prefix), so the frontend's source list can list it and "file as page" can cite it. **No new evidence type** (`type` stays `note`, `kind` records `handoff`) — that would split the frontend source list into two renderers, which is a different piece of work (the user explicitly declined that option).
- **Position**: handoff records lead the **note candidates** (the same reason `BuildProjectContext` puts handoffs first: they are the previous session's current state), taking at most 3 note slots; the rest still belong to the search hits. They **do not take a slot of their own** — leading the list only lets the budget pick them in order.
- **Deduplication**: the same note can be hit both by FTS and by the handoff channel. Deduplicate by note id — a duplicated evidence item inflates the budget's coverage claim.
- **A handoff record is user data** (it may quote repository content) and is rendered as data, never as an instruction — the existing `Evidence.Render()` stance.

### 3. Upgrade the IDE-side binaries

- `/usr/local/bin/repotory-mcp` and `/usr/local/bin/repotory-server` are upgraded to **v2.24.0**.
- **Back up first** to `~/Documents/repotory-rescue/bin-backup-<date>/`, then overwrite. This overwrites system-level files on the user's machine — a **change outside the repository**: not versioned, not covered by CI.
- Verification: after the upgrade the MCP tools/list count goes from 13 to 18.

### 4. What does not change

- **No schema change** (no new table, no new column) → `ExpectedSchemaVersion` stays 28.
- **No new MCP tool, no new `internal/app` binding.**
- **The shape of `BuildProjectContext` is unchanged** — the MCP path (`repotory_context`) stays word for word.
- **The existing page / note retrieval semantics are unchanged** — FTS / semantic recall works exactly as before; this **adds** one candidate source.
- **ADR-0031's "no automatic fallback for L3" is unchanged.**
- **None of ADR-0038 / ADR-0039's decisions change.**
- **The frontend does not change**: the ask panel's UI and scope dropdown stay as they are. The source list will show handoff records as additional entries, but their type is still `note`.

## Consequences

- **Under the default scope, the ask can now answer "which projects do I have" and "what does this library look like" for the first time** — but honestly: the catalog is **coarse** (name / path / two counts), and tech stack and README excerpt remain project-scoped. A user who asks "what tech stack is this?" without picking a project still gets an incomplete answer — that is a trade-off, not an oversight.
- **L3 is still empty.** To actually use the cross-project profile, a page has to be promoted to global scope in the review page. That goes into the docs (`docs/features/knowledge.md`, both languages) rather than into an automatic fallback.
- **An answer may now cite the conclusion of a session.** A handoff record is the product of the previous session and **may already be stale** — the model is told "these are data", but it cannot be told "these are certainly true". That is the inherent cost of putting "the previous round's judgement" into "evidence". Mitigation: the latest one is given in full (it is most likely still valid), older ones collapse to one line.
- **The evidence budget has one more competitor**: handoff records lead the note candidates and take at most 3 note slots; the rest still belong to the search hits. They **do not reserve a slot of their own** — leading the list only lets the budget pick them in order.
- **The IDE binary will drift again.** This ADR records the fact; it does not add an automatic sync mechanism — that belongs on the distribution side, and touching system-level files needs explicit user consent anyway.

## Landing checklist

1. `internal/service/context_layers.go`: add `LayerProjectCatalog`; assemble the catalog block when `projectID == 0` in `BuildLayeredMemory`; add `layerProjectCatalog`; add `CatalogChars` to `LayerBudget`.
2. `internal/db/repositories.go`: add a per-project repository-count aggregate (one SQL query, no N+1).
3. `internal/service/wiki_evidence.go`: `evidenceMessages` drops the block-level gate; `GatherEvidence` gains the handoff candidate source and deduplication.
4. Regression tests: the catalog layer (catalog present / absent / trimmed over budget), non-empty context under the unscoped scope, handoffs entering the evidence (correct scope / deduplicated / citable).
5. `docs/features/knowledge.md`, both languages: the layer table gains L2-catalog; write down the fact that "L3 is currently empty".
6. One row each in `docs/adr/index.md` and `docs/en/adr/index.md`.
7. A CHANGELOG entry.

## Revision record

- 2026-11-02: first draft, accepted as-is (the user picked each of the three options explicitly). The details of decisions 1 and 2 were then filled in to match the implementation word for word before landing (three handoffs, a 1200 ceiling, a 400-character L0 depth, empty projects excluded from the catalog), and the index summary was rewritten accordingly — **completing a draft before it lands is not rewriting a decision already in effect**.

## Landing record (2026-11-02)

**Measured on the live library** (v2.24.0, 31 projects / 136 notes / 4 handoff records / 45 wiki pages / 140 repositories):

- **Context under the default scope**: before the fix `LayeredProjectContext(0, "")` → **chars=0, blocks=[]** (not one character); after it → **chars=2341**, one L2-catalog block, **items=29 / omitted=2** (2 of the 31 projects dropped by the 2400-character cap, the rest all listed). The project-scoped path is unchanged: `LayeredProjectContext(124, "")` → 1000 characters (Project / Path / Repositories / Languages / Top contributors).
- **Handoff records reach the evidence**: the Chinese question "最近有哪些会话交接记录与重点事项？" used to retrieve **4 ADR notes and 0 handoff records**; it now retrieves **7 — N1/N2/N3, three `kind=handoff` records, lead the set, followed by N4–N7, the ADR notes**. The model **cited N1/N2/N3 directly** and reported what those sessions actually did (the v2.15.0 release wrap-up, the Sprint 0/1 audit push, 20 results from a full pick-a-hole audit), instead of answering off-target.
- **The catalog reaches the prompt**: asked "我有哪些项目？分别是什么技术栈？", the model **listed all 29 projects with their paths** and cited `[L2-catalog]`; for the projects whose path gives no tech stack away it **marked them "unknown" itself** — the "the catalog is deliberately coarse, tech stack stays project-scoped" boundary shows up honestly in a real answer.

**Regression** (backend): three new cases in `context_layers_test.go` — "the unscoped scope assembles a catalog block with the project name, path and both counts", "a project scope never has both the catalog and the scenario", "over budget the catalog is trimmed rather than muted, and the omitted count is reported"; four new cases in `wiki_evidence_test.go` — "the unscoped ask carries orientation (not an empty system message)", "the handoff reaches the evidence set, is citable and carries its content", "the scope is respected (another project's handoff does not leak)", "the same record does not appear twice". The full backend `go test ./...` is green.

**Docs**: `docs/features/knowledge.md` (layer table gains L2-catalog, the two L2s are mutually exclusive, the catalog is not L3, L3 is still empty most of the time) and `docs/features/ai-integration.md` (handoff records ride their own channel, "the whole library" is a scope too, an answer may cite a session conclusion that has gone stale) — both languages. One row each in the ADR index, both languages.
