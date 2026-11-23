# ADR-0070: Temporal recall channel — date expressions are structural signals, the third recall arm

- **Status**: Accepted (2026-11-23)
- **Date**: 2026-11-23
- **Related**: [ADR-0012](0012-semantic-search.md) (semantic search and the RRF fusion architecture this channel reuses), [ADR-0018](0018-knowledge-graph-foundation.md) (the discipline that any new retrieval form passes the A/B gate first), [ADR-0065](0065-hindsight-absorb-not-replace.md) (absorption route, first P2 item), [ADR-0068](0068-builtin-credential-pii-presets.md) (the isomorphic argument: format/structure itself is the signal)

## Context

The per-case hybrid retest of evaluation set v2 (70 labeled queries, 2026-11-23, remote endpoint) relocated the remaining gaps: hybrid overall recall@10 0.945, the memory class up from 0.333 to 1.000, the 51 legacy cases up from 0.745 to 0.990 — **but the temporal class is the one class vectors cannot rescue**:

- `temporal-natural` ("11月17号那天 MiddlePlatform 项目在解决什么问题?" / "What was the MiddlePlatform project solving on Nov 17?"): lexical 0.000, hybrid still **0.000**.
- `temporal-relative` ("上周五的会话交接记录了什么?" / "What did last Friday's session handoff record?"): both arms stuck at 0.500.
- One `temporal-literal` case ("2026-11-17 那天有哪些会话交接?" / "Which session handoffs happened on 2026-11-17?") **regressed 0.83→0.67** under hybrid: RRF pushed two lexically-matched handoffs out of the top-10.

Verified: the target notes all exist with clean `created_at` values (213 = 2026-11-17, 222 = 2026-11-20, a Friday). The failure mechanism is twofold: the Chinese date "11月17号" shares **zero trigrams** with the ISO date "2026-11-17" in the title, and "what was it solving that day" carries no calendar semantics in embedding space either. A date is a **structural signal** — isomorphic to ADR-0068's argument for structured credentials: it survives no semantic rewording, deterministic rules can identify it precisely, and both existing recall arms are in principle unable to represent it.

## Decision

1. **A standalone `internal/search/temporal` package, pure string patterns, zero dependencies.** Supported shapes: ISO / slash dates (`2026-11-17`, `2026/11/17`), full Chinese dates (`2026年11月17日`), month-day (`11月17号/日` — **日/号 required**; a bare "11月" is not parsed), relative days (今天/今日/昨天/昨日/前天 = today/yesterday/day-before), week expressions (上/本/这 + 周/星期 + optional weekday; a bare "周X" = the most recent occurrence on or before the anchor). **Deliberately NOT parsed in the MVP**: time-of-day (晚上/上午), bare months, date ranges, "最近N天" (last N days), English dates — they fall through to the existing arms unchanged. Parsing is byte-deterministic for a fixed anchor (pinned by tests), the precondition for reproducible evaluation.
2. **Window semantics: local calendar day → UTC instant range.** `created_at` is SQLite `CURRENT_TIMESTAMP` (UTC), so a local day boundary is not a UTC day boundary: "Nov 17" in UTC+8 = `[2026-11-16 16:00Z, 2026-11-17 16:00Z)`. Monday-start weeks; half-open intervals; overflow dates (Feb 30) rejected rather than normalised into March. **False-positive guard**: "版本周期" (version cycle) and "资本周转" (capital turnover) contain "本周" but are not dates — a candidate whose 本/这 prefix is preceded by a Han rune is skipped (上 is exempt: "查上周五" is legitimate).
3. **A third RRF arm, always on, no switch.** This does not conflict with ADR-0012's default-off stance: that stance governs **data leaving the machine** (embedding endpoints) and **unreviewed content**; this channel is pure local computation — no endpoint, no new data exposure, nothing requiring user authorization. A query without a date expression takes the byte-identical old path. Fusion reuses `fuseRankList`, extracted from `fuseSemantic` (RRF k=60, window recall K=20), so both arms extend results in the same shape.
4. **Two-stage recall inside the window (`db.NoteIDsInWindow`): FTS bm25 first (strict AND → relaxed OR), then every note in the window by update time.** The second stage is the arm's reason to exist: a date question can share zero trigrams with its target, so content ranking alone returns empty — the window itself must supply the recall.
5. **Surfaces: `SearchNotes` + `SearchAll` (the search surface); `SearchNotesStrict` (the citation path) deliberately NOT wired.** A window hit is a creation-time coincidence, not a content match — exactly the reason the strict path rejects OR relaxation (the "昨日代码提交量" false-citation case from ADR-0012's landing): the search surface shows a human a ranked list, the citation surface presents every hit as evidence, and their tolerance differs.
6. **The anchor travels via context (`temporal.WithAnchor`), not config.** Production uses `time.Now()`; abeval / probe gain `-anchor YYYY-MM-DD` to pin the annotation date — relative expressions like "上周五" keep their labeled meaning only under that date, which is what makes the gate reproducible. No config key: that would be a test backdoor in production settings.
7. **Robustness fixes landed in the same batch (all from measured defects this session)**:
   - `RebuildEmbeddings` / `RebuildPageEmbeddings` now **return an error** when a batch bails (the old contract returned `(partial count, nil)` — the UI reported "rebuilt N" as success while the endpoint was down; hit for real during the live endpoint switch). The durable queue still retains the unfinished rows: failure visibility and no data loss both hold.
   - `RemoteEmbedder` no longer swallows body-read errors (the old `raw, _ := io.ReadAll` let a network truncation masquerade as a JSON decode error); the 8 MiB response cap is now **detected explicitly** (read cap+1 bytes, error on overflow instead of silent truncation).
   - Default timeout 30s→**120s**, `embedBatch` 64→**16**: measured ~15s fixed per-request overhead on the remote endpoint; a 64-long-note batch could not finish reading its body inside 60s. Batch 16 + 120s completed the full 156-note rebuild with one retry under a provider 429 rate limit.

## Evidence (A/B gate, ADR-0018 discipline)

Lexical arm + temporal (semantic off, 149-note snapshot, anchor=2026-11-23):

| Class | n | Lexical | Lexical+temporal |
|---|---|---|---|
| temporal-literal | 3 | 0.611 | **1.000** |
| temporal-natural | 1 | 0.000 | **1.000** |
| temporal-relative | 2 | 0.500 | **1.000** |
| all other classes | 64 | — | **zero per-case regressions** |

Overall lexical recall@10 0.719 → 0.764. The full hybrid+temporal gate numbers are in the landing record.

## Costs (accepted)

- **Narrow pattern surface**: unsupported shapes fall through silently (listed above); a month-day without a year takes the anchor's year — asking about "Jan 2" in December resolves to this year, not next. A memory tool's queries are mostly about the past; accepted.
- **Two-stage RRF is not one three-list RRF**: semantic fusion first, temporal fusion second; the scores differ from a single three-list fusion. Chosen so `fuseSemantic` stays untouched and each arm degrades independently; measured zero regressions. If a case of the two arms squeezing each other appears, revisit a single three-list fusion.
- **The window arm recalls by creation time, not content**: a busy day feeds up to 20 notes into the fusion; the final order of content-matching notes is still decided by the content arms.
- **Bare-weekday heuristic** (most recent on or before the anchor) can misread future-facing phrasings ("Friday meeting" said on Monday resolves to last Friday) — a memory tool's queries are about the past; accepted.

## Reversal triggers

- A false positive the guard misses shows up in real use (a "版本周期"-shaped leak) → narrow the pattern, do not add a whitelist mechanism (the same discipline as ADR-0068).
- A reliable local date NLP arrives → re-evaluate whether the regex surface demotes to its fallback.
- Real evaluation cases appear for "last N days" / ranges / time-of-day → extend the pattern surface through the same A/B gate; no cases, no extension (this ADR's narrow surface is deliberate).

## Landing record (2026-11-23)

- Code: `internal/search/temporal/temporal.go` (Parse / Window / WithAnchor / Anchor), `internal/db/search_temporal.go` (NoteIDsInWindow two-stage recall), `internal/service/search_temporal.go` (fuseTemporal), `search_semantic.go` (fuseRankList extraction + bail error + embedBatch 16), `search.go` (SearchNotes / SearchAll wiring; the Strict comment pins the exclusion), `page_embed_drain.go` (bail error), `embedder_remote.go` (read error / explicit cap / 120s), `cmd/abeval/main.go` (-anchor).
- Tests: 27 parse cases in the temporal package + 3 for local-day boundary / anchor / determinism; 4 in db (FTS stage / fallback stage / local-day boundary / empty window); 4 in service (date-only recall with semantic off, date-free queries byte-unchanged, Strict exclusion, SearchNotes parity); 1 embedder truncated-response test; the bail contract test updated to "must error and retain the queue". `go build` / `go vet` / `go test ./...` green across 52 packages.
- Gate: lexical+temporal 6/6 temporal cases green, zero regressions across 70 cases (table above). **Full hybrid+temporal gate (same evening, -anchor 2026-11-23, after rebuilding the snapshot's 149 vectors): recall@10=0.986 / ndcg@10=0.808, delta +0.221 / +0.189, GATE PASS**; against the temporal-free hybrid baseline of 0.945 / 0.766, the per-case comparison shows zero regressions across 70 cases and 3 improvements, all in the temporal class (0.667→1.000, 0.000→1.000 ×2); all 6 temporal cases reach 1.000 with 5 at rank1=1 (the natural case's rank1 rises from 10 under lexical+temporal to 2 — the vector arm lifts the window recall into the head). The SearchNotes continuity arm matches the production path exactly.
- **Live endpoint switch (user decision, ADR-0012 privacy authorization)**: `embedding_base_url` switched to `https://openrouter.ai/api/v1`, `embedding_model=qwen/qwen3-embedding-8b`, `embedding_dim=4096`, key copied at runtime from the live `ai_chat_api_key`; full rebuild of 156 notes completed (first attempt hit a provider 429, the retry passed — the new error contract proved itself on first contact). The running desktop binary still carries the old batch/timeout parameters; the index itself is correct and the drainer self-maintains at the new dimension; the parameter fixes ship with the next build.