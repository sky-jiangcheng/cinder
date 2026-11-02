---
title: Knowledge: Notes and Wiki Pages
order: 3
---

# Knowledge: Notes and Wiki Pages

"Knowledge" is **one** tab in the main navigation with two views inside it: **Notes** (the raw material — writable, organizable) and **Wiki pages** (the finished product — read-only). The notes hub itself provides Markdown / block editor, tag categories, FTS5 full-text search, version history, and AI memory import.

## One tab, two views (ADR-0038, decision 4)

The knowledge base and the wiki list used to be two peer top-level tabs with nearly identical skeletons (search box + filter row + result count + list), while they retrieve two different kinds of things — one is **raw material** (notes / todos: writable, organizable, importable), the other **finished product** (approved pages, read-only). You had to work out which one to open, and the capabilities were **not symmetric**: the pages view had a project-scope dropdown and the notes view did not, even though note retrieval is cross-project anyway.

They are now one tab:

- **Route** `/knowledge`, with the view switched by `?view=notes|pages` (deep-linkable and shareable; the browser back button walks the view history)
- **Shared skeleton**: one search box, **one project-scope dropdown**, one result-count bar, one set of empty and error states
- **Different bodies**: the notes view is a grid plus filters (all / knowledge / other, pinned, tags, topics) plus writing actions (new / import); the pages view is a list plus read-only, and its "**approved pages only**" semantics are unchanged (ADR-0032, decision 1 still holds)
- **The scope dropdown is now given to the notes view too** — it was missing, even though the retrieval was cross-project all along

> The old `/wiki` address redirects to `/knowledge?view=pages` (carrying `?q=` and `?project=` along), and the `/wiki/:id` detail route is untouched — deep links and shared links keep working.
>
> The search box here is a **filter within the view**, not a global search: global retrieval lives in the home console's "search" mode (see [Console: Ask & Search](search.md)).

### Two tabs inside the notes view (ADR-0039, decision 3)

The notes view carries a second tab pair: **"All notes"** and **"To file (N)"**.

"To file" used to render *after* the note grid — and the note grid is a page roughly 10,000px tall, so it actually sat at the very bottom of the page, reachable only through a "To file 14" jump link. **The very existence of a control like that says "it is elsewhere, and it is hard to find".** It is now a tab inside the notes drawer, and the jump link is gone with it: when there are no notes to file, the tab does not take a slot at all.

"To file" is not a filtered subset of notes — it is **another workbench** (re-filing the notes in the landing buckets into real projects, deleting emptied placeholders), with a different shape and different actions from the note grid. So under ADR-0039's three-way rule it is a **tab**, not a chip.

The top-level "Notes / Wiki pages" switch stays a **segmented control** rather than a tab: it is **two states of one object** (raw material / finished product), not two places. The two sets of controls differ in shape and position (pill segment / underlined tab), and only the notes drawer has an inner tab, so there is no ambiguity.

```mermaid
flowchart TB
    NEW["Create a note<br/>home / project detail"] --> ED["Block editor<br/>Markdown ↔ blocks"]
    ED --> AUTO["Autosaved draft"]
    AUTO --> SNAP["Save → snapshot<br/>latest 50 kept"]
    SNAP --> DIFF["LCS<br/>line-level diff"]
    DIFF --> BACK["One-click<br/>restore"]
    ED --> IDX[("FTS5 index<br/>trigram · trigger-synced")]
    IDX --> SEARCH["Search / ask<br/>snippet highlights"]
        classDef store fill:#fffbeb,stroke:#f59e0b,color:#78350f
        classDef read fill:#f0fdf4,stroke:#22c55e,color:#14532d
    class IDX store
    class SEARCH read
```

How to read it: one **trunk (edit → save → snapshot → diff → restore)** plus one **branch (index → search)**. The trunk solves "knowledge must not be lost"; the branch solves "knowledge must be findable" — both are triggered by the same save action (snapshots via a write trigger, the index via the FTS sync trigger), so there is no save button to remember. The **History** button on a note card enters the version side; the search box enters the branch.

## Managing notes

- **Create a note**: pick a project in "Quick create note" on the home page, or create one from the notes panel on the project detail page
- **Metadata**: title (falls back to the first line if left empty), tags (comma-separated), category (knowledge / log / idea / other), pinned
- **Move across projects**: use the "Linked project" dropdown while editing to migrate in one click
- **Autosaved drafts**: edits are saved locally in real time, so nothing is lost if the app closes unexpectedly

## Block editor

Type `/` to open the block panel and insert structured blocks:

| Block | Description |
|----|------|
| Callout | TIP / WARNING / NOTE callouts |
| Code block | with language highlighting |
| Mermaid diagram | flowcharts / sequence diagrams, etc. |
| Math formula | rendered with KaTeX |
| Todo list / table / divider | common structures |
| Collapsible block / Tabs | `<details>` and `{% tabs %}` |

- Blocks can be drag-sorted, moved up or down, and deleted individually
- **Switch between Markdown and blocks at any time**: the storage format is always plain Markdown, so any editor can open it

## Rich rendering

highlight.js code highlighting, Mermaid diagrams, KaTeX math formulas, GFM callouts, and task lists.

## Search

- The search box at the top of the "Knowledge" tab is a **filter within the view**: it narrows notes / todos in the notes view, or approved pages in the pages view; the empty states and the count bar are shared by both
- The home console's "search" mode is the **global retrieval** (notes / todos / project names, hits grouped by project), and its "ask" mode returns answer snippets ranked by relevance
- FTS5 trigram + bm25 ranking, with snippets highlighting matched terms; short CJK queries automatically fall back to LIKE
- Covers notes and todos; the `⌘/Ctrl+K` command palette works from anywhere

### Semantic search (optional, off by default)

Lexical search answers "which characters matched", not "the same fact phrased differently". Optional vector recall covers exactly that blind spot:

- **Two-stage fusion**: FTS5 and vector recall are merged with RRF (k=60); vectors only ever **add** — if any step fails (endpoint unset, request error, index missing) it falls back to the pure lexical result, so **turning it on cannot return fewer hits**. That is a design constraint, not a reassurance.
- **Off by default**: active only when `semantic_search=1` **and** an embedding endpoint is configured (`embedding_base_url` + `embedding_model`). The settings page does not expose these yet; enable them via the configuration keys described in [AI Integration](ai-integration.md).
- **The first time needs one full rebuild**: every note's text is embedded, which is an O(all notes) walk of the endpoint. It also persists the vector dimension it discovers.
- **No further rebuilds afterwards**: note creation, title/body edits and deletions are queued by SQLite triggers and drained in the background every 5 seconds — live notes get re-embedded, deleted notes are removed from the vector index (otherwise text you deleted keeps showing up in recall). Three deliberate conservations: pinning, retagging and moving a note do **not** trigger an embed (they do not change the text sent to the endpoint); when the endpoint is unavailable the work stays queued and retries, so an offline laptop loses nothing; and when the dimension is unknown or the index does not exist yet it skips rather than guessing.
- **Imported notes are indexed the same way**: queueing happens in database triggers, and every writer (UI, MCP, CLI, agent-memory importers) converges on that layer — which is precisely why this was not built into application logic.
- **Privacy boundary**: enabling semantic search means note text is sent to the embedding endpoint you configured (local Ollama or a remote API, depending on `embedding_base_url`). That is an explicit choice, off by default; `embedding_api_key` and `vector_store_api_key` are secrets, returned masked when configuration is read and never handed to the UI.
- **The vector index is a derived cache**; the notes themselves are the source of truth: it can be dropped and recomputed at any time, living in the same database file as the notes, in the same transactions and the same backups (pure-Go sqlite-vec, zero CGO). A remote vector store is only worth reaching for at real scale — see [AI Integration](ai-integration.md).
- **Whether it helps is measurable before it ships**: `cmd/abeval` runs "lexical vs lexical+vector" against a live database and reports Recall@k / NDCG@k with a threshold gate — again against a throwaway copy, so it writes nothing to your database. The gate comes before the switch deliberately — until there is a real labelled query set, no UI toggle ships.

## Version history

A snapshot is created automatically on every save (the latest 50 are kept). The **History** button on the note card opens:

- A version list (time, title)
- A line-level LCS diff of any version vs the current one (+/- markers)
- One-click restore to any historical version

## Import knowledge from agent memory

Six built-in sources idempotently import the memory and session transcripts you already left in other agent tools as knowledge notes (re-importing updates existing notes instead of duplicating them):

| Source | Trigger | Reads |
|--------|---------|-------|
| `claude` | automatic at startup | `~/.claude/projects/*/memory/*.md` |
| `codex` | manual | session transcripts `rollout-*.jsonl` (first instruction + last reply) |
| `opencode` | manual | session titles and summaries |
| `openclaw` | manual | `~/.openclaw-autoclaw/workspace/*.md` |
| `hermes` | manual | `~/.hermes/memories/{MEMORY,USER}.md` |
| `cursor` | manual | Cursor sessions inside `globalStorage/state.vscdb` (best effort, see below) |

**Settings → Plugins** lists every source, lets you trigger each one manually, and shows the per-source `{created, updated, skipped}` counters.

Three rules that are easy to trip over:

1. **Imports land on a project, but a non-match is no longer dropped.** Documents are matched to a specific project by project name / repository path; whatever does not match (ADR-0023) lands on a **"Unfiled repo" placeholder** named after the repo (promoted to a real project once a scan finds that same repo), or on the always-present **Uncategorized** default project when there is no repo path — and can be re-filed later from the **Organize** area in this page's **"To file" tab**. Scanning projects first still raises the hit rate.
2. **`openclaw`, `hermes` and `codebuddy-memory` target projects are now optional** (`openclaw_project` / `hermes_project` / `codebuddy_project`): set one to attach all their notes to it, or leave it unset and they land on Uncategorized — no more silent skipping.
3. **A repeat import is not a failure.** `created=0` plus `updated=N` means idempotency worked and the content was refreshed.

**`cursor` is the only best-effort source**: Cursor's on-disk format is undocumented and versioned, so it takes only the **first prompt and last reply** of each session (never a full transcript), resolves ownership through `workspaceStorage/<id>/workspace.json` and, as of ADR-0023, files a folder that matches no project onto a placeholder rather than skipping it — and when the database shape is not one it recognizes, **the whole source fails loudly instead of quietly importing zero notes** (because "nothing imported" must stay distinguishable from "you have no sessions"). Every read opens that file `mode=ro`; another application's data is never written.

Imported notes carry `kind` = `knowledge` (memory files) or `log` (session records) and are findable by full-text search as soon as they are written; with semantic search enabled they are also picked up by vector recall without another rebuild (see above). Full paths, matching rules and limitations per source are in [Knowledge source plugins](../plugins/overview.md).

### Landing & organize ([ADR-0023](../adr/0023-note-ownership-knowledge-organize.md))

Knowledge imported with no matching project is no longer lost — it lands on a **placeholder** (an "Unfiled repo" project named after the repo) or the **Uncategorized** default project, marked in the data by `project_kind`. The **Organize** area in this page's **"To file" tab** lets you re-file a batch of notes from a placeholder / Uncategorized bucket into a real project (or back into Uncategorized), or **confirm-delete** an emptied or orphaned placeholder (Uncategorized is never deletable; deletion is always a manual confirmation, never an automatic cascade). Project cards and the detail page badge these two buckets and show the associated repo path.

For browsing, clicking a note in the knowledge base now **jumps straight to that note in the project's Notes tab and highlights it** (`/project/:id?tab=notes&noteId=N`) instead of only opening the project.

## Knowledge pages and the review queue

Notes are the **raw record**; pages are the **compiled artifact**. The same fact can live in a note (who did what, when, how) and still grow concept and entity pages linked to each other in a graph — the shape `project_notes` cannot express on its own, since a note hangs off a project and references nothing else.

| Page kind | Holds |
|-----------|-------|
| `entity` | A concrete thing (module, service, repository, person) |
| `concept` | An idea (idempotency key, session timeout, scan-depth semantics) |
| `source` | A summary page for one source document |
| `synthesis` | Cross-source synthesis and conclusions |
| `query` | A filed answer from Q&A (see "File as page" in [AI Integration](ai-integration.md)) |

The page layer is a **derived cache**: notes remain the source of truth, the whole layer can be dropped and rebuilt (`DropWikiSchema`), and if it is dropped it grows back on the next open.

### The review page: five queues = five tabs (ADR-0039, decision 2)

The review page (`/review`) used to stack six blocks into one long scroll: the pending queue / merge candidates / page scope / compile jobs / rejected pages / community summary. Their **shapes and actions are completely different** (approve / merge / promote / assign / cancel / delete) — **not filters over one list, but six places** — and merge candidates ran up to 20 rows in front of the review queue, so working through the queue meant scrolling past a pile of unrelated things.

They are now five tabs, each carrying a **count badge**:

| Tab | Holds | When it appears |
|---|---|---|
| **Pending review** | the `pending` page queue (approve / mark unqualified) | always (with an empty state when empty) |
| **Merge suggestions** | candidate pairs found by structural signals | always — "nothing found" is itself an answer the reviewer wants |
| **Page scope** | promoting / demoting approved pages | when there is content |
| **Background jobs** | compile / lint jobs + the community summary report | when there is content |
| **Error pages** | the records replaced by error pages | when there is content |

The badge is not decoration: tabbing means "content outside the current tab is invisible", so **the count is the only thing that tells a user whether a tab is worth clicking**. Empty tabs take no slot (a block with nothing in it should not occupy a position), but a **zero count is still shown** — "Pending 0" says "the queue is empty", whereas a missing badge says nothing at all.

`?view=pending|merge|scope|jobs|rejected` is deep-linkable and shareable, and the browser back button walks back through the queues.

### Edges between pages: a controlled relation vocabulary (schema v21)

Pages are wired into a graph by **directed links**, and every link carries a **relation type**. Early on that type was free text (the compiler wrote `compiled-from`, a filed answer wrote `cites`, and the model wrote whatever it liked), so "what are all the dependency links in this graph" was unanswerable. v21 ([ADR-0018](../adr/0018-knowledge-graph-foundation.md), first half) freezes it into eight enumerated values, enforced by a database `CHECK`:

| Relation | Meaning |
|----------|---------|
| `ref` | generic reference (the default; reach for it when unsure) |
| `part-of` | composition: A is part of B |
| `depends` | dependency: A depends on B |
| `implements` | A implements a concept/interface B |
| `documents` | A describes B |
| `supersedes` | A replaces an older B |
| `contradicts` | A and B conflict |
| `mentions` | A's body links to B |

Edges grow from three places: the **compiler** creates `pending` edges using the vocabulary above; **answer filing** points a query page at the evidence pages it used (`ref`); and when you **approve a page**, the system resolves the `[[wikilinks]]` in its body into `mentions` edges — linking only to pages that actually exist, so a link to a missing page stays a "dangling link" for lint rather than fabricating a node. None of the three needs a model, so they can run automatically while keeping the human in the loop: the approve action itself is the signal that "these links are trustworthy".

> When migrating to v21, the assorted historical relation values in an old store are **normalized** into these eight (for example, legacy `compiled-from` and `cites` fold into `ref`, `depends-on` into `depends`). This relabels only; it never drops an edge, and parallel edges between the same pair that collapse to one value are merged.

lint also runs three **structural checks** along relation type (all pure SQL/in-memory, no model, no cost): whether a `supersedes` edge is inverted (the superseder is older than what it supersedes), whether `part-of`/`depends` form a cycle, and whether a `contradicts` is one-sided (a contradiction should be symmetric). Like every other check, they **only write project todos for a human to read — lint never auto-edits a page or deletes an edge**.

### Pending and approved (the one most often mistaken for a bug)

Pages have a status, and status decides whether a page may answer anything:

- `approved`: searchable, listed in the export index, usable as Q&A evidence.
- `pending`: **visible but not searchable**. Lists, the export bypass, the review queue and lint all see it; retrieval and Q&A deliberately do not.

So "I generated a page but search cannot find it" is not a defect: anything a model wrote is pending until a human approves it. Pages created manually in the app, and answers you file via File as page, are `approved` at birth — those are human actions. Only compiler output lands as `pending`. Rejecting a pending page deletes that row and cascades its links and source attachments; rejecting is explicitly refused for approved pages.

### Who writes pages

1. **Humans**: filing an answer as a `query` page, and future manual page creation.
2. **The compiler** (once `wiki_compile` is on): it reads one note and proposes which pages to create, which links to add, which sources to declare — all landing as `pending`. It **can only create; it can never edit an approved page** — a revision request becomes a todo for a human instead. Every page it produces is automatically attached to the note it read.
3. **Community synthesis** (once `wiki_community_summary` is on *and* a person clicks "Run synthesis" on the review page): it finds topics in the graph of approved pages and drafts one `pending` `synthesis` page per topic. The page, its `documents` edges and its source-note attachments are all built by code; the model only writes the body paragraph (see "Community synthesis" below).
4. **Lint** (read-only): it inspects graph health and never edits a page.

(Merge is a fifth thing, but it is a human-only graph rewrite rather than one more automated writer — see "Concept merge" below.)

### Memory layers (L3 → L0)

"What this project is" and "a raw session transcript" should not carry equal weight in one context. `LayeredProjectContext` assembles memory by stability, and Q&A uses the same two top layers:

| Layer | Contents | When fetched |
|-------|----------|--------------|
| L3 global profile | **Approved** pages owned by no project (cross-project conventions, preferences) | always, first |
| L2 project scenario | repository list + mined tech stack: what the project is | **when a project is selected** |
| L2 library catalog | which projects the library holds: name + path + repo count + note count | **when no project is selected** (mutually exclusive with the row above) |
| L1 notes | curated knowledge (hand-written, Claude/OpenClaw/Hermes memory files, filed answers) | **only when a specific question is asked** |
| L0 transcripts | session records (codex / opencode / cursor imports, and kind=log) | only on explicit request |

Three points: the classification is a **provenance heuristic** — it says where a note came from, not how true it is; budgets are **per layer**, so a verbose L0 cannot crowd out the L3/L2 that give the model its bearings; and L3 reads approved pages only, so unreviewed compiler output never steers an answer.

**"No project selected" is a scope too** ([ADR-0040](../adr/0040-unscoped-ask-orientation-and-handoff-evidence.md)). The ask panel's scope dropdown defaults to "all knowledge and all projects", and that option previously gave the model **nothing at all** — the whole context assembly was skipped, so it only got the retrieved fragments and did not even know which projects the library holds. That option now yields the **L2 library catalog**: each project's name, path, repository count and note count, **deliberately coarse** (tech stack and README excerpt stay project-scoped). The two L2s are **mutually exclusive**: pick a project and you get that project's scenario, pick none and you get the library catalog.

**The library catalog is not L3.** L3 is "knowledge pages a human approved that belong to no project"; the catalog is the *fact* of which projects exist — different sources, different gates, and conflating them would hollow out L3's human gate.

**L3 used to be structurally empty.** L3 only reads approved pages owned by no project, while every page-creating path forced a project — a real library held 45 pages and **not one global page**, so the layer could only ever answer "nothing". The review page now carries a **Page scope** area: an **approved** page can be **promoted to a global page**, which puts it into L3 and makes it visible to every project's answers, and it can be moved back into a named project (which is what makes the promotion reversible). It is a **human act** by design: ADR-0015 states that producing L3 automatically is exactly "the model writes into the store with no gate", so no automatic path can fill this layer. Promotion is a **widening** — one misjudged page then steers every project — so the button takes two clicks.

**This layer is still empty most of the time.** The action exists, but nobody has promoted a page yet — a real library holds 45 pages and 0 global pages. Using L3 requires promoting an approved page in the review page first, which is a human act; no code can stand in for it. The **library catalog** in the paragraph above answers "which projects does this library hold", but not "what rules did I set for this library" — that is L3's job.

### lint: five checks and what they emit

`RunWikiLint` performs five checks. Three are decidable in SQL — orphan pages, missing cross-references, and data gaps (thin pages, pages without a source note, body links pointing at nonexistent pages, leaf pages with no synthesis). The other two, contradictions and stale claims, ask a model and require `wiki_lint_llm` to be explicitly enabled. Every finding becomes a project todo prefixed `[lint]`; re-running does not pile up duplicates (dedupe matches open todos with the same title; a finding that reappears after you ticked it off is new information). One rule is structural rather than advisory: lint produces suggestions, and no code path lets it rewrite a page. (Merge judging also hangs off this lint pass's model half, as a second thing the same run does — see "Concept merge" below.)

### Page version history (schema v22, [ADR-0021](../adr/0021-temporal-graph-page-versions.md))

Notes have had `note_versions` all along; pages did not — `wiki_pages.content` is overwritten in place, so "what did the human actually approve, and what did it become afterwards" had no answer. v22 adds `page_versions`: a snapshot of each page's **approved** content.

- **The writer is a database trigger, not application code.** It freezes a snapshot when a page becomes `approved`, and when an **already-approved** page's content, title or kind changes. `pending` drafts and `rejected` tombstones are never snapshotted — they are not conclusions. Turning an approved page into a tombstone (a merge or a reject) moves the trigger's `new.status` away from `approved`, so its WHEN clause is false and no junk version is written.
- **Append-only is a trigger, not a policy**: `BEFORE UPDATE` / `BEFORE DELETE` both `RAISE(ABORT)`. "Cleaning up the history table" means deliberately dropping those two triggers first, editing, then re-adding them — that is the point, and what it buys is non-repudiation.
- **There is deliberately no foreign key on `page_versions.page_id`.** This is worth writing down for users and for whoever maintains this next: SQLite fires row triggers for foreign-key CASCADE actions, so a cascading FK plus the append-only guard together makes `DELETE FROM wiki_pages` — and therefore deleting a whole project, which cascades through `wiki_pages` — fail as "append-only". You can have one, not both: an audit row that vanishes the moment the thing it records is deleted is not an audit row. So snapshots outlive the page they describe, and the history of a deleted page stays readable.
- **The backfill**: migration v22 files one baseline snapshot for every page that was already approved, guarded per page by `NOT EXISTS`, so it is safe on every database open — it can neither overwrite nor duplicate a real snapshot.
- The review page can list a page's versions and show a **line diff** of any snapshot against the current content (the same +/- format as note version history, in the same version → current direction).

**What is NOT implemented, stated plainly**: `revise_page` — a compiler op that proposes a revision of an approved page. ADR-0021 Decision 4 defers it explicitly until a real stale-approved-page case exists; today a revision still surfaces as a `[lint] compile-revision` todo for a human rather than the system editing the page.

### Concept merge ([ADR-0019](../adr/0019-entity-resolution-merge.md))

The compiler writes one note at a time, so concepts split. ADR-0015 accepted "concepts will split, a human merges them" as the price of that design but shipped no merge tool. The review page now has a **merge suggestions** tab.

- **Candidate discovery is structural, free, with no model and no vector.** Four signals: shared source notes, slug shape (one is a prefix of the other, or they are within 2-rune edit distance), title word overlap, and an existing `ref` / `mentions` edge between the two pages. A pair needs **at least two independent signals** to be reported (the two cheapest sum to exactly the threshold). `contradicts` and `supersedes` edges are deliberately **not** similarity signals — they assert that the two pages are different things.
- **Cost stays bounded**: pairs only ever form between pages that share a token, a slug prefix, a source note or an edge — never a full cross product; a bucket larger than 40 pages is skipped outright (that is the stopword guard — a word like `project` or `api` that appears everywhere carries no similarity information).
- **The model is a second opinion only**, gated behind **two** switches (`wiki_lint_llm` AND `wiki_merge_suggest`, both default off). Its verdict becomes a single `[lint] merge [[loser]] → [[winner]]` todo — it cannot merge anything.
- **Merging is human-only, from the review page, behind a two-step confirm.** It is an **absorption, not an edit**: the winner's row is byte-for-byte untouched (content, slug, `updated_at` all unchanged), the loser's edges and source notes are redirected to the winner, and the loser becomes a "merged into" tombstone — the row is kept, exactly like reject ≠ delete, because deleting it would cascade away its inbound edges and turn every page that cited it into a dead link. Cross-project merges are refused in the MVP (a global page, project_id 0, may sit on either side).
- **No `page_merges` audit table was created**, per ADR-0019 Decision 6's own instruction not to build one until a real undo request exists. The loser's tombstone plus the version snapshot frozen while it was approved is the audit record.

### Community synthesis ([ADR-0020](../adr/0020-community-synthesis.md))

`synthesis` is the fifth page kind, and until now it had **no generation path at all** — which meant lint's `no-synthesis` check (a pile of leaf pages, zero syntheses) could never be turned green. This step adds the generation path, so the check can now actually pass.

- **Topics are detected by Label Propagation over the page graph**, propagating only along `ref / part-of / depends / implements / documents`. `contradicts`, `mentions` and `supersedes` are excluded on purpose — conflict, a passing mention and a supersession in time each mean something other than "the same topic".
- The implementation is **synchronous (Jacobi) with a lexicographic tie-break and one self-vote per node**, so the same graph always yields a byte-identical partition. That determinism is what makes re-running safe rather than churning the graph.
- **Input is approved pages only, and synthesis pages are excluded from their own input** — otherwise run N's output enlarges run N+1's input, the member set grows by one each run, the stable slug stops being stable, the revision todo never fires, and a machine-written summary starts summarizing itself.
- Each community produces one `synthesis` page with `status=pending` and `source=wiki-community`, and a stable slug `synthesis-<first 8 hex of sha256 over the sorted member slug list>`. The member → synthesis `documents` edges and the members' source-note attachments are created by code. The model writes **only the paragraph of body text** — it has no vocabulary in which to name a page or a link, so it cannot invent a page through this path.
- If the slug is already held by an **approved** page, it is never rewritten: a `[lint] synthesis-revision` todo is filed instead.
- **Budgets**: ≤10 communities per run, ≤24 member pages each, ≤1500 bytes per page body, wall clock bounded by the batch timeout; when a budget is hit the report says where it stopped (which communities were left for next time, which body was clipped).
- Gated by `wiki_community_summary` (default off), **never on a timer**, and there is **no MCP tool** — an agent able to pour summaries into the library would be the human gate, bypassed. It requires an explicit project (project id 0 means the *global* tier — pages belonging to no project — not "all projects").
- Thresholds are configurable: `community_min_size` (default 3) and `community_max_size` (default 30).
- **An honest limitation**: Label Propagation's boundaries are fuzzy. The algorithm's own test keeps a concrete failure on record — two triangles joined by a single bridge do not split cleanly: the bridge node defects to one side, and the stranded remainder can fall *below* `community_min_size` and be discarded, so a topic silently gets no summary. The report surfaces `dropped_small` / `dropped_large` counts so "nothing found" can be told apart from "fragmented graph" and "one giant blob".

### Export to Obsidian (a read-only bypass)

```
go build -o repotory-wiki-export ./cmd/wiki-export
./repotory-wiki-export -out ./vault            # refused if the target holds foreign files
```

It renders `vault/wiki/<entities|concepts|sources|synthesis|queries>/<slug>.md` with YAML frontmatter (including `status` and `source`), `[[wikilink]]` outgoing and back links, a `wiki/index.md` catalogue and `EXPORT-MANIFEST.json`. Its purpose is **to look at the graph**: Obsidian's graph view answers whether this taxonomy is actually the shape of your knowledge. Two deliberate properties:

- **One-way**: edits made in Obsidian never flow back. That is a decision, not a missing feature — two writers would mean a second implementation of versioning, de-duplication and link consistency (see [ADR-0014](../adr/0014-llm-wiki-knowledge-compiler.md) Decision 2).
- **Nothing is deleted**: files left behind by deleted pages are reported as stale and are yours to clean. The export never claims ownership of that tree.

`index.md` lists approved pages only, so an unreviewed page cannot be routed into; pending pages are still exported, tagged `status: pending`, which is exactly where review happens.

## Export

- **Export .md** on the note card copies the Markdown — with YAML frontmatter (title / tags / project / type / updated time) — to the clipboard. For bulk AI consumption, see llms.txt in [AI integration](ai-integration.md).
- **OMP-style memory export** (`ExportMemoryJSON`): dumps the knowledge base as OMP (Open Memory Protocol) style Memory Objects for other tools speaking that protocol. **Export only — there is no import side**, and the field mapping is still marked provisional: the protocol itself is not stable, so two-way sync was deliberately left undone. It is reachable through the app binding layer; there is no dedicated button yet.
