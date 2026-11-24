# ADR-0077: Progressive search — local hits immediately, semantic recall as a polled job

- **Status**: Accepted (2026-11-24, user's call: "make the backend do an async job plus polling")
- **Date**: 2026-11-24
- **Related**: ADR-0012 (semantic search opt-in), ADR-0016 (async jobs precedent), ADR-0070 (temporal arm), ADR-0071 (fusion tuning), ADR-0072 (recall provenance)

## Background

Search felt like "nothing happens for two to eight seconds". Measurement put the two stages three orders of magnitude apart:

| Stage | Code | Live library (162 notes / 0.27 MB of bodies) |
|-------|------|----------------------------------------------|
| Local lexical (FTS5 + LIKE fallback + todos) | `db.SearchAll` | **3 ms** |
| Query embedding (remote `qwen3-embedding-8b`) | `fuseSemantic` → `RemoteEmbedder` | **4–12 s** |
| Temporal arm | `fuseTemporal` (pure local parsing) | < 1 ms |

The problem is not slowness; it is **joining both stages into one synchronous response**. `SearchAll` cannot return a single row until the remote embedding comes back. After a 300 ms debounce the user sees seconds of nothing, reads the field as broken, and retypes — which only queues the same wait again. The remote endpoint is a network dependency, so no indexing work removes it: that path's floor is the endpoint's RTT.

## Decision

Split one search into **two stages plus a handle**, for the UI only. Agents and the CLI keep the old shape.

1. **Stage one (synchronous, milliseconds)**: `searchLexical` runs FTS5/LIKE, stamps the lexical recall channel and source_type, and returns that as the job's first batch.
2. **Stage two (asynchronous, seconds)**: `fuseHits` runs the temporal and semantic arms in the background and writes the result back to the same job.
3. **Handle**: `StartSearchJob(query)` returns `{id, state, hits}` at once — `hits` is always populated; while `state` is `refining` the frontend polls `GetSearchJob(id)` every 400 ms; `state=done` is final.
4. **Nothing to pay when semantic is off**: `semanticArmed()` false (the default, or no embedding endpoint configured) means stage two has no remote work, so the job comes back `done` and the frontend **never polls**. The async machinery does not fire once on a default install.
5. **Jobs live in memory, not in the database**: they last under a minute, die with the process (the poller is gone anyway), and writing every query a user types into the knowledge database would leak search history into a long-lived file. TTL 2 minutes, cap 32 entries, and an in-flight job is reused for the same query — a debounce storm must not become a bill for remote embeddings.
6. **`SearchAll` stays**: agents, the CLI, the MCP tool and the home page's answer path all need one complete evidence set — half an answer is not an answer. The synchronous semantics are expressed as `SearchAll = searchLexical + fuseHits`, so both paths share one definition of fusion.

Both new bindings are `SurfaceDesktopOnly`: an agent calls `repotory_notes_search` and gets the fused answer in one call, with no poll loop to babysit. **The MCP tool surface and CoreVersion are unchanged.**

## Consequences

- Two more bindings plus an in-memory registry; `surface.go` gains two entries and the write contract needs an exemption (`StartSearchJob` starts with "Start" but only reads).
- The frontend grows a polling state machine: besides the seq guard it must **cancel** (stop the old poll when the query changes; the backend job finishes on its own and its result goes unread).
- The result list **changes once**, as semantic hits are merged in. The UI says "adding semantic matches…" rather than letting the list silently reshuffle.
- A failed semantic arm still ends the job as `done`, just without semantic hits — the same graceful degradation ADR-0012 promised for an optional arm. No extra error is raised.

## Reversal triggers

- The endpoint becomes **local** embeddings (same-host RTT < 50 ms) → stage two no longer needs to be async; a single synchronous call is simpler.
- The UI needs **streaming** rather than two batches (semantic hits appended one by one) → switch to Wails events and drop polling.
- Jobs must survive a process boundary or restart (several clients searching at once, or desktop and server sharing) → the in-memory registry is not enough; that is when to consider persistence and single-writer routing.

## Rollout log (2026-11-24)

- Backend: `internal/service/search.go` split into `searchLexical` / `fuseHits` (`SearchAll` keeps its signature and semantics); new `internal/service/search_job.go` (`SearchJob`, `searchJobStore`, `StartSearchJob`, `GetSearchJob`, `semanticArmed`) — the background goroutine goes through `bgGo` (panic recovery + Shutdown join) with a 60 s timeout so a hung embedding endpoint cannot pin it; two bindings in `internal/app/bindings.go`, two `SurfaceDesktopOnly` entries in `internal/app/surface.go`, and the "name says write, actually reads" exemption in `write_contract_test.go`.
- Tests: six cases in `search_job_test.go` — semantic off is immediately done and matches `SearchAll` row for row, empty query, `refining → done` with a gated embedding endpoint (the semantic-only note is merged in), same query reuses one job, capacity eviction, finish-after-eviction dropped; green under `-race`.
- Frontend: `api/endpoints.ts` gains `startSearchJob` / `getSearchJob` / `runProgressiveSearch` (400 ms tick, 150-tick ceiling, cancel); `useKnowledgePage` and the home page's search mode consume the job (local hits first, a `refining` hint, final list on done); the hint in `Knowledge.tsx` sits in the results header, and the old "dim the whole list to 0.45 opacity" stale state is gone — the list is the answer now, and dimming it reads as broken.
- Two same-root fixes on the way: scan completion was written only into a `visually-hidden` live region (screen readers saw it, people did not) → the dashboard gets a toast, failures go to the error banner; scan progress rendered the backend's English `ScanStatus.message` ("Scanning 12/12…" inside a Chinese UI) → the frontend formats `progress/total` itself as `扫描中 12/12…`, on the dashboard and the settings page alike.
- Verified on a copy of the live library against a headless server: `StartSearchJob` round trip **4.5 ms** (was 6–8 s); in the browser the first cards land at **476 ms** together with the "adding semantic matches…" line, and semantic hits merge at **10.7 s** (20 → 44 hits, 30 carrying the semantic channel); with the endpoint unreachable the job still ends `done` with lexical results only and no visible error. Scan confirm → button reads "扫描中 12/12…" → "扫描完成" toast in the top-right corner on completion.
