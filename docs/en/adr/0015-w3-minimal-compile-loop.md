# ADR-0015: W3 minimal compile loop — the LLM writes pending pages only, and humans decide what enters search

- Status: Accepted (schema v18 and the compiler are implemented and contract-tested; a real local-model run was reviewed page by page; the review tab and async dispatch landed via [ADR-0016](0016-async-batch-jobs.md). Promotion criteria 1-5 are met.)
- Date: 2026-10-08
- **Note (2026-11-24, [ADR-0073](0073-approval-gate-relaxed-optional-auto.md))**: Decisions 3 ("only a human can trigger") and 6 ("no scheduler") are narrowed **on the review chain** to "human by default, optional auto-approval" — v2.43.0 added three config keys, all off by default. The `wiki_compile` constraints on the compilation chain (no scheduler / no auto_import / no MCP tool) **remain verbatim in force**. The original text is preserved; read Decisions 3/6 together with ADR-0073.
- Relates to: [ADR-0014](0014-llm-wiki-knowledge-compiler.md) (this realizes its Decisions 4 and 5a), [ADR-0012](0012-semantic-search.md), [ADR-0011](0011-multi-agent-memory-importers.md), [ADR-0007](0007-session-memory-protocol.md), TODO **M6-W3**; prerequisites W1 (schema v16), W1b (v17 page index) and W4 (lint) have landed. (Chinese original: `docs/adr/0015-*.md`.)

## Context

W1 gave structure, W2 gave the consumer-side evidence path, W4 gave the anti-corruption checks. What is missing is ADR-0014 Decision 4: **compilation at ingest** — letting a model read a source and integrate it into the page layer.

Why this needs a decision of its own rather than just executing ADR-0014: building W1/W2 surfaced a fact that the direction document did not know — **"review before indexing" is not expressible in the current data model.**

1. `wiki_pages` has only `id/slug/title/kind/project_id/content/created_at/updated_at`. No status column, no author column.
2. Neither `SearchWikiPages` nor `ListWikiPages` filters on status, so a page is searchable, listed in `index.md` and fed into AskAI evidence **the moment it exists**.

Shipping the compiler first and the review state afterwards would put unreviewed model output on the evidence path — inviting into the main flow precisely what ADR-0014 rejects (hallucinations compiled into apparently established knowledge). **So the status column and the search filter are one atomic change with the compiler, not a follow-up.**

## Decision (MVP scope, seven items)

### 1. schema v18 adds two columns and makes them retrieval predicates — before the compiler exists

```
wiki_pages.status  TEXT NOT NULL DEFAULT 'approved'
                   CHECK (status IN ('pending','approved','rejected'))
wiki_pages.source  TEXT NOT NULL DEFAULT 'manual'
                   -- manual | ai | wiki-compile | import:<source>
```

- The default must be `approved`: every existing page was human-initiated (written in the app, or filed via W2's "File as page" button) and is already trusted. Defaulting to `pending` would silently evict the user's existing knowledge from search on upgrade.
- Once migrated, `SearchWikiPages` and `SearchWikiPagesMatch` (the two FTS queries) filter `p.status = 'approved'`, and `ListPendingWikiPages` serves the review surface. **Order matters**: the filter must exist before the compiler has somewhere to write.
>
> **One narrowing made during implementation**: the plan also had `ListWikiPages` filter on approved, which in practice would have broken **both the W1b export bypass and W4 lint** — pending pages are precisely what the export must surface for review and what lint must check (the `no-source` rule exists to police compiler output). Landed behaviour is therefore: **retrieval and evidence paths filter approved; inventory paths (export, lint, review queue) keep every status and carry `status`/`source` explicitly.** "What exists" and "what may answer a question" are different questions, and this line reflects the shipped behaviour rather than the original wording.
- Reversibility follows W1's bar: columns are added rather than tables, so `DROP COLUMN` restores the v17 shape; the migration test asserts that pages which existed before the upgrade are still retrievable after it — otherwise "default approved" is a comment that quietly fails.

### 2. Compile unit = one note, one model call, a strict JSON contract

Not "one importer source", not "one project". A note is naturally bounded, traceable and idempotent: re-running on the same note updates the pending pages it produced last time instead of duplicating them, and one failure does not affect the rest.

The model may output only a JSON array whose elements are `{op:"create_page"|"add_link"|"attach_note", ...}`; an unparseable reply writes **nothing at all** (the same rule W4 uses: a hallucinated link is noise a human must clear, not a partial success). All output lands as `pending` + `source='wiki-compile'`.

### 3. The compiler may only create pending pages; it can never edit an approved one

The core MVP trade-off, and the item most needing approval. If a concept already has an approved page and a new source revises it, the compiler does **not** touch that page; it emits a lint-style suggestion into `project_todos` and a human decides. Reasons:

- "let the model revise an existing conclusion" is precisely where ADR-0014's "deeper corruption" happens: edits overwrite human judgement, and the page layer has no equivalent of `note_versions`;
- create-only writes are **fully reversible**: rejecting a pending page deletes one row, cascading its links and note attachments with it, touching nobody else's work;
- it makes "approved" a state transition only a human can perform, rather than something the model can complete by itself.

The cost is stated plainly: concepts will fork (two pending pages on one topic) and the human merges during review. MVP accepts that, because what it buys is "the compiler cannot quietly change something you already approved".

### 4. Every compiled page must attach at least one source note, with W4 as the backstop

Each page the compiler creates records its input note in `note_pages`. W4's existing `no-source` check flags violations — **the anti-corruption layer guarding the generator**, a property that exists only because W1/W4 shipped first.

### 5. Hard budgets, and stop-on-overrun

Defaults (all configurable): ≤20 notes per run, ≤6 pages per note, ≤1500 bytes per page body, ≤60 pages total per run. On hitting a limit the run **stops and reports where it stopped** rather than squeezing the rest in — the reason a budget exists is that the model does not know when to stop.

### 6. Its own switch, and never auto-triggered

- New config key `wiki_compile`, off by default; also requires a configured chat endpoint (reusing `ai_chat_*`, no new provider surface).
- **No ticker of any kind**: W2/W4's scheduled loops cover reading and checking only. Compilation must be started by a human per note or per project — this is ADR-0014's "one source at a time, human in the loop" made concrete.
- **Never inside `auto_import`**: importers write notes; note → page is always a separately authorized step.
- **No new MCP tool**: MCP is for agents, and "an agent may write unreviewed knowledge into the store" is exactly the human-in-the-loop this document exists to keep. A desktop binding plus `/api/rpc` suffice.

### 7. The privacy surface widens, and says so

`semantic_search` sends note text out to get a vector back. This sends note text out to get **content written into the store** back. Different consent class, hence a **separate key**, each off by default, neither implying the other. Documentation must state: once enabled, the notes compiled (possibly containing code snippets and session excerpts) pass through your chat endpoint, and the model's text is persisted in the local database.

## Explicitly not in MVP

Automatic synthesis rewriting, automatic concept de-duplication, contradiction-driven revision, page-level vector indexes, a review UI, scheduled compilation, cross-project compilation, two-way file sync (ADR-0014 Decision 2). For review, MVP uses bindings plus a CLI view (`repotory-wiki-export` marks pending rows); the review screen is a separate step.

## Consequences

**Positive**: the "compile at ingest" promise is finally realized, with a write surface small enough to be fully reversible; W4 graduates from "checking human notes" to "checking model output", closing the loop; pending/approved separation means AskAI evidence contains only what a human accepted.

**Negative (no sugar-coating)**: token cost grows linearly with notes and never stops by itself (the budget only blocks); model output can still be wrong, only now the error is quarantined in `pending`, so its cost becomes review load — **review fatigue is the most realistic failure mode of this route**, and MVP only bounds it with budgets and per-note compilation rather than solving it; concepts will dirty the graph; the prompt-injection surface widens (a note reading "ignore the above instructions" enters the prompt), mitigated by JSON-only output, pending-only creates and never touching approved pages — not by trusting the model to behave.

## Open questions (five calls to make, with my recommendation)

| # | Question | Recommendation |
|---|----------|----------------|
| 1 | May the compiler edit approved pages? | **No** (Decision 3); revision intent degrades to a todo |
| 2 | Do pending pages appear in the export bypass / `index.md`? | Export **includes** them tagged `status: pending` (so review happens in Obsidian); `index.md` stays approved-only |
| 3 | Compile unit | One note (Decision 2); "compile this whole project" = N runs in order under one budget |
| 4 | Budget defaults | 20 notes / 6 pages each / 1500 B per page / 60 pages per run |
| 5 | Does closing the loop require a review UI? | CLI + bindings suffice for MVP; UI is a later step and must not block this one |

## Promotion criteria (Proposed → Accepted)

1. The v18 migration ships with a reversibility test, and asserts that pre-existing pages are still retrievable after the upgrade;
2. Pending-leak tests hold on all three retrieval paths (`SearchWikiPages`, `SearchWikiPagesMatch`, `ListWikiPages`) and on the AskAI evidence path;
3. Contract tests: non-JSON reply → zero writes; a nonexistent `page_id`/slug in the reply → that item dropped; 100% of compiled pages carry `note_pages` provenance;
4. Idempotency: compiling the same note twice produces no duplicate pending pages;
5. One real compile run over your own library, reviewed by a human — a fixture cannot settle this (W2's lesson: mechanism correctness ≠ real-world benefit).
