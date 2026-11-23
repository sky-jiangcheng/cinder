# ADR-0072: Search transparency — recall provenance badges and source-type filtering

- **Status**: Accepted (2026-11-23, landed after UX optimization route c was approved)
- **Date**: 2026-11-23
- **Related**: ADR-0012 (semantic search), ADR-0022 (source types, pending item 2 closed here), ADR-0038 (knowledge tab, one object two faces), ADR-0070 (temporal arm), ADR-0071 (fusion tuning)

## Context

The retrieval-quality batch (ADR-0066–0071) lifted recall@10 to 0.986 and ndcg@10 to 0.873, but **users cannot see how those gains happen**:

1. Home assistant cards carry recall-source badges (fts/graph/semantic/fact); the knowledge-page search and the Cmd+K palette show nothing — the same note has different transparency on two surfaces, and "why did this match?" has no visible answer.
2. The semantic-search switch lives deep in Settings → AI; at the search field there is no way to know whether recall is lexical-only or hybrid.
3. ADR-0022 gave notes a source-type badge (agent-import/user-conversation/project-extract/manual), but pending item 2 ("filter search/lists by source") kept waiting for a demand signal — this UX analysis confirms search is the high-frequency surface with five daily entry points, so the signal exists.

## Decision

1. **Two optional fields on SearchHit**: `recall []string` (lexical | semantic | temporal, in fusion-stage order, multi-valued — a note recalled by several arms carries several) and `source_type` (the derived source bucket of note hits; empty for todos). Purely additive → **CoreVersion 2.3.0 → 2.4.0 (minor)**, `wantCoreVersion` in `cmd/mcp/main_test.go` synced, check-version-drift gate.
2. **Provenance is stamped in the service layer and never changes ordering**: the db exit gets a lexical base stamp (`markLexicalRecall`); `fuseRankList` gains a channel parameter and appends the arm's channel to every id it surfaced (deduped, stage order preserved); the annotation is pure metadata, fusion order stays byte-identical. With semantic off or an embed failure, hits honestly carry lexical only — the badges reflect **the recall that actually happened**, not what the config claims.
3. **Source types reuse the single derivation mapping** (`pluginruntime.SourceTypeFor`, an ADR-0022 decision): one batch lookup at the service exit (new `db.NoteSourcesByIDs`) fills source_type on note hits; a lookup failure leaves the field empty and logs — it **never fails the search**. No materialized column (ADR-0022 pending item 3 stays deferred; no schema change).
4. **Three frontend surfaces aligned**: knowledge-page search and the Cmd+K palette show recall badges (reusing home.sourceFts/sourceSemantic wording + new sourceTemporal); a semantic-state pill sits by the search field (reads config; hidden when unknown rather than guessing); filter chips appear when results hold ≥2 source buckets (client-side filtering), and a stale filter degrades to "all" at render time instead of an effect reset.
5. **ADR-0022 pending item 2 closed**: filtering lands client-side; item 3's trigger is unchanged — materialize the column when type filtering becomes a high-frequency server-side path.

## Costs

- One extra local batch SQL per search (note hit ids → source); negligible at human-scale result sets.
- Badges add visual density: small tertiary-color badges (same surface as hitType) keep the noise down; no new color semantics.
- recall says "which arm could surface this", not "why this rank" — RRF scores stay internal (SearchHit.Rank carries bm25 semantics; mixing them would mislead).

## Reversal triggers

- Badges prove to be noise in real use → hide them behind a setting, default off.
- Source filtering becomes high-frequency and client-side filtering is insufficient (result-window truncation leaves too few hits after filtering) → materialize the source_type column + server-side filtering (ADR-0022 pending item 3).

## Landing record (2026-11-23)

- Backend: `internal/domain/types.go` (two fields), `internal/db/vecindex.go` (NoteSourcesByIDs), `internal/service/search.go` (markLexicalRecall/appendRecallChannel/annotateSourceTypes + wiring in the three search functions), `search_semantic.go`/`search_temporal.go` (channel parameter), `internal/version/core.go` + `cmd/mcp/main_test.go` (2.4.0).
- Frontend: `web/src/api/types.ts`, `pages/Knowledge.tsx` (state pill + badges + filter), `components/CommandPalette.tsx` (badges), both module.css files, locales zh/en in the same batch (6 new keys).
- Tests: backend `search_recall_test.go` adds 4 cases (lexical-only / semantic channel / temporal channel / todos stay lexical), 52 packages green; frontend adds 1 Knowledge case (badges + state + filter interaction), vitest/build/lint gates.
- Real-machine validation (2026-11-23, headless dev server + browser): live-API hits carry recall=['lexical','semantic'] and derived source_type; the knowledge page renders the Vector-on state pill, source chips and Full text/Vector badges; clicking the Manual filter narrows 35→1 and that hit carries only the Vector badge (zero lexical match, pure vector recall) — the badges honestly reflect the recall that happened.