# ADR-0055: One Agent Surface Contract and Real Observability — Hardening from the Four-Dimension Audit

Status: **Accepted (2026-11-11 direction settled; 2026-11-12 P0 (D1/D2), P1 (D3/D4), the doc-contract gate (12.4) and the schema-free half of D5 all shipped. What is left of D5 — idempotency keys, optimistic locking, version history — needs schema v31 and now lives in [ADR-0057](0057-write-idempotency-and-version-history.md), tracked as TODO 12.14. See the Implementation record)**
Date: 2026-11-11
Related: [ADR-0048](0048-core-contract-version.md) (contract version semantics and the drift gate), [ADR-0051](0051-conversation-as-interface.md) (the tool surface as the unified operation layer), [ADR-0054](0054-rolling-checkpoint-handoff.md) (handoff read/write loop), [ADR-0056](0056-desktop-dialogue-persistence.md) (desktop dialogue history, shipped the same day)
Starting schema: v30 → **v30 (no schema change)**; `CoreVersion` 1.5.0 → **2.0.0 (effective 2026-11-12)** — D1 changes how a client decides that a call failed, which is a major under `core.go`, not an additive change

## Background

A read-only four-dimension audit (contract / memory / observability / resilience) ran across all 22 MCP tools, three entry points, the DB and the background jobs on 2026-11-11. These are not four independent problems: **they are four breaks in one chain** — an inconsistent result contract → errors reported as successes → no instrumentation → silence.

### 1. The contract is explicit but inconsistent

- The tool surface has a single source of truth: `cmd/mcp/main_test.go:138-161` pins 22 tools plus a `len()` assertion, and `internal/version/core.go:19` `CoreVersion = "1.5.0"` has two copies and a CI gate.
- But `makeJSONResult(name, data)` in `cmd/mcp/results.go:34-44` **never uses `name`**, returns bare JSON, and has no `ok` / error code / request id. `tools_metrics.go:27` and `tools_chat.go` use `mcp.NewToolResultError` (`IsError=true`); the other 20 tools use `makeTextResult("error: " + ...)` with `IsError=false` (e.g. `tools_ask.go:83`). One client needs two parsers.
- The version gate only compares two literals (`scripts/check-version-drift.mjs:404-416`); the major/minor semantics in `core.go:13-15` have no code check, and the "two-surface alignment assertion" promised by ADR-0051 does not exist in the script.
- Doc drift is outside the gate: `README.md:143` / `README.zh-CN.md:146` still say 20 tools; root `SKILL.md:101-102` lists non-existent `offset?` / `starred_only?`; four MCP↔binding mappings in `skill/repotory/references/mcp-tools.md:301,307,312-314` are wrong; `docs/api/reference.md:248` admits the hand-written openapi.json was removed with no machine-readable contract left.
- Three different `path` boundaries: `tools_scope.go:25-27` silently `return 0`, `tools_context.go:64-66` rejects explicitly, `main.go:157` does not validate at all. `Required` is declared on 7 of 22 tools.

### 2. Memory is never physically lost, but is logically lost

- Everything lands in SQLite with daily `VACUUM INTO` snapshots plus `VerifySnapshot` (`internal/db/backup.go:50-105`), so the floor holds.
- `internal/service/handoff.go:29-33` accepts `summary 4000B + 4 sections × 20 items × 1000B` (≈84KB), while `internal/service/context.go:20` truncates the latest handoff at `contextHandoffFullLen = 4000`. The template order is Changes → Decisions → **Gotchas → Next Steps** (`handoff.go:320-323`), so exactly the gotchas and todos get cut first — despite `context.go:17-19` claiming the exit record "must survive complete".
- `context.go:270-288` renders only the handoff with the largest `updated_at` in full; older ones collapse into 140-char one-liners with **no cap and no chronological order**. `context_test.go:129-177` pins "the old gotcha disappears" as an assertion.
- The SessionEnd hook is `repotory-capture >/dev/null 2>&1 || true` (`internal/agentinit/agentinit.go:387`); `cmd/repotory-capture/main.go:53-59` writes to stderr and exits 0; `claude_session_capture` is off by default. When enabled, a low-quality auto-captured handoff can displace a human one as the "latest full text".
- Only hard-coded per-section caps exist (notes 10 / handoff 4000 / timeline 140): **no global budget, no config, no "N omitted"**. The 4h import sync unconditionally refreshes `updated_at`, letting `kind=log` notes crowd out curated knowledge.
- Rollback is incomplete: the version trigger stores `new.content` and is `AFTER UPDATE` (`internal/db/migrate.go:169-173`), so the created state never enters history; there is no `RestoreBackup`; `internal/integrity` is read-only; writes have no optimistic lock.

### 3. Observability is close to zero

- 223 `log.Printf` calls, **zero slog**; the only `log.SetOutput` is `main.go:233` (Wails desktop only, `O_APPEND`, no rotation). `cmd/mcp` and `cmd/server` write to stderr only and lose everything when the IDE does not capture it. stdout protocol isolation relies on upstream library defaults, with no comment or test pinning it.
- No request / trace / correlation id. `internal/httpapi/server.go:45-59` wraps only `loopbackGuard` — no access log, no timing, no recover. Outside `main.go`, `cmd/mcp/` has no `log.` or `time.Since`; all 22 tools are uninstrumented.
- `/health` only does `db.Ping()` (`internal/service/service.go:317-331`); no `/metrics`. `internal/stats` is git business metrics, `repotory_integrity` is an on-demand read-only check, and `ai_call_log` is off by default with no latency or success fields.
- Backup, LLM compile and import failures only `log.Printf`, with no alert and no failure counter. `docs/troubleshooting.md:71-79` documents only the desktop log path, and `:83` contradicts the headless server and S3 push.

### 4. The data layer is resilient; the Agent face is not graceful

- Already solid: WAL + `busy_timeout=5000` + 15s backoff (`internal/db/db.go:20-38,173-192`), six open retries in `cmd/server/main.go:186-206`, AI timeouts and localhost fallback, vector-store failure falling back to FTS5 with "never reducing results" (`internal/service/search_semantic.go:63-120`), single-transaction reentrant migrations, pending-only LLM wiki with tombstones, HTTP status classification.
- The break is at the boundary: `internal/service/search.go:49-53` logs a DB error then `return nil` → `cmd/mcp/main.go:160` serialises `"null"` with `IsError=false` → HTTP `200 {"hits":null}`; `tools_ask.go:94-100` renders `nil` as `No results found`. **A failure is reported as zero hits.**
- Partial success is reported as full success (`internal/service/wiki_compile_job.go:223-245` unconditionally sets `CompileJobDone`; `internal/core/plugin/runtime/runtime.go:303-376` accumulates `firstErr` yet `return run, nil`).
- The review gate has a bypass: `internal/service/wiki_evidence.go:588` `FileAnswerAsPage` → `internal/db/wiki.go:723-726` writes `WikiStatusApproved, "manual"`, putting model text into retrieval without review.
- No unified error codes; classification uses `strings.Contains` (`cmd/server/main.go:210`, `web/src/utils/apiError.ts:15-23`). Handoff has no idempotency key; MCP has no open-lock retry (unlike server); the embedding drainer retries every 5s forever with no backoff.

## Decision

### D1 One MCP result contract, with machine-readable failure

> **Implementation correction (2026-11-12)**: the **success face is frozen** — all 22 tools still return bare JSON / the original text, because Warden and both IDE plugins already parse it and rewriting three parsers is not part of this fix. The machine-readable `{code, message, retryable, request_id}` lives in MCP's standard `StructuredContent`, while `Content[0]` keeps the original `"error: …"` text (no text assertions or client displays change); every failure sets `IsError=true`; the dead `name` parameter of `makeJSONResult` was removed. The envelope therefore applies to failures only; the tradeoff is recorded under Consequences.

Collapse `cmd/mcp/results.go` into a **single failure exit**: `makeErrorResult` / `makeInvalidResult` produce an `IsError=true` result whose `StructuredContent` carries `{code, message, retryable, request_id}` while `Content[0]` keeps the original `"error: …"` text. Remove the dead `name` parameter from `makeJSONResult` and every in-band error branch. Codes distinguish at least `invalid_argument` / `not_found` / `retryable` / `internal`, so an Agent can decide whether to fix arguments or retry. `request_id` goes into both the result and structured logs (D4). **The success face is frozen** — see the implementation correction at the top of this section.

**This is a major bump**: flipping `IsError` changes how clients detect failure — a field's meaning changed under `core.go:15`. When landing, move `CoreVersion` 1.5.0 → 2.0.0, update `wantCoreVersion` and `TestToolsRegistered` in `cmd/mcp/main_test.go`, and keep every tool name intact.

### D2 Let read errors surface; never disguise a fault as an empty result

`Service.SearchAll` / `SearchNotes` return an error (or expose an error-carrying variant); MCP and HTTP exits use `IsError=true` / 5xx. Fix the same swallow in `internal/service/project_overview.go:73-74` and `handoff.go:225-226`. **Zero hits must be an explicit empty array, never `nil`**.

### D3 Context budget and handoff fidelity

- Add a total budget constant + config to `BuildProjectContext`, assembled by priority: latest handoff > Gotchas / Next Steps > current notes > history timeline > mined meta. Print `N notes / M handoffs omitted` when over budget.
- Fix truncation by preserving Gotchas / Next Steps sections first, or raise `contextHandoffFullLen` to the same order as the maximum legal handoff; distinguish `auto-captured` / `milestone` from a human session summary so auto-capture cannot unconditionally displace it.
- Cap the timeline and sort by `updated_at DESC`; replace `content[:1200]` with `truncateBytes`.
- Remove `>/dev/null` from the SessionEnd hook; persist a durable pending record on failure and surface it on the next `repotory_context`; resolve projects via `ResolveProjectByPath` instead of `LastPathSegment`, and prefer writing nothing on ambiguous matches.
- Weight `handoff` hits in search, return whole bullets in snippets, and add a content hash to import upserts so unchanged documents do not refresh `updated_at`.

### D4 Structured logs in all three processes, a correlation id, and instrumentation

- Extract a shared `setupLogging`: Wails / `cmd/mcp` / `cmd/server` each write their own file, `slog` + JSON handler + size rotation. MCP pins stdout to protocol only, with a test.
- HTTP middleware generates `X-Request-ID` (response header + ctx + access log + recover); MCP reuses the JSON-RPC id as the tool call id; wrap `AddTool` to log tool name / duration / outcome / error summary; carry the id into LLM and vector-store calls.
- Extend `/health` with `db_size_bytes`, `last_backup_ok,at`, `compile_jobs_failed_24h`, `llm_calls_failed_24h`, `search_p95_ms`, `import_failed_by_source`, aggregated from existing tables — no new pipeline. Backup / compile / import failures also write a visible event.

### D5 Close the state and rollback gaps

`makeJSONResult` marshal failures use the D1 error exit; batch jobs report `partial` / failure counts; `repotory_notes_create` / `repotory_handoff` gain an optional `client_request_id` with a DB unique key; writes get `WHERE version=?` optimistic locking; `note_versions_snap` stores `old.content` and captures the created state; add `RestoreBackup`; `FileAnswerAsPage` becomes pending + `source="ai"`; drainers get backoff and a dead-letter path; MCP reuses server's `openDatabase` retry.

### What this ADR explicitly does not do

1. **No new MCP tools, no renames, no change to the frozen `repotory_*` identifiers** (ADR-0049).
2. **No schema change** (if D5's unique key / version column needs one, that is a separate ADR and migration).
3. **No distributed tracing, external APM or new framework**: local-first and account-free is a product premise; structured logs plus one correlation id cover three processes on one machine.
4. **Do not use `chat_threads` as desktop UI state** (see ADR-0056).
5. **No service-layer rewrite**: this ADR closes the exits, the budget and the observability gap, not a re-layering (the P0 "no new architecture" rule still holds).

## Consequences

- **The major bump is a real break**: clients that detect failures via `IsError=false` plus a text prefix must change. The success face and the error text itself are unchanged, so Warden and both IDE plugins need no parser changes — but it is not transparent.
- **Failures carry structured metadata**: only failed results gain a `structuredContent` object; successful results are unchanged. Tool descriptions must state that `request_id` is metadata, not part of an answer.
- **Logging to disk adds a disk and privacy surface**: rotation and size caps are mandatory, and note bodies / API keys must never be logged (the `getConfig` masking rule applies here too).
- **Faithful handoffs grow the first screen**: the total budget and `omitted` counters are hard requirements, or we trade "lost" for "crowded out".
- **Optimistic locking changes write semantics**: concurrent edits move from silent last-write-wins to an explicit conflict, which the UI must explain.

## Landing plan (batched; run the gates after each batch)

**P0 (contract + resilience)**: D1 `results.go` / all tool error exits / `main_test.go` assertions / `CoreVersion` 2.0.0 / semantic check in `check-version-drift`; D2 read-error propagation.
**P1 (observability + memory)**: D4 logging base and wrapper, D3 budget and handoff fidelity, hook de-silencing, `/health` fields, three-process paths in `docs/troubleshooting.md`.
**P2 (state and rollback)**: D5 idempotency / optimistic lock / `RestoreBackup` / review bypass / drainer; bring the doc contract under a CI gate (generated comparison for README, SKILL, mcp-tools, api reference).

## Acceptance (all testable)

1. When any tool's service returns an error, `IsError=true` and `error.code` is in the agreed set; argument errors and storage faults get different codes.
2. With `db.SearchAll` failing, neither MCP nor HTTP may emit `null` / `200 {"hits":null}`.
3. A long handoff containing Gotchas / Next Steps survives `BuildProjectContext` with both sections readable; total output stays under budget and reports `omitted` counts.
4. A failed SessionEnd hook leaves a pending record that the next `repotory_context` reports.
5. One MCP tool call and its LLM call share the same `request_id` in logs; `cmd/mcp` / `cmd/server` log files exist and stdout carries nothing outside the protocol.
6. `go test ./...`, `npm test`, `npm run build`, `check-docs-mirror`, `check-i18n-keys`, `check-version-drift`, `release-notes --check` all pass.

## Implementation record (2026-11-12)

**P0 (2026-11-12)**: `cmd/mcp/results.go` is now the single failure exit (`makeErrorResult` / `makeInvalidResult`, with `StructuredContent` carrying `code / message / retryable / request_id`); 20 `makeJSONResult(name, …)` calls lost the dead parameter and every failure branch across the 22 tools sets `IsError=true`. `CoreVersion` moved 1.5.0 → **2.0.0**, `cmd/mcp/main_test.go` followed and gained `TestToolErrorIsMachineReadable`; `scripts/check-version-drift.mjs` gained gate ⑨, validating the declared change kind against the actual version distance via `CoreVersionPrevious` / `CoreVersionChange` (automatic tool-list diffing remains 12.4). `Service.SearchAll/SearchNotes` now return `([]SearchHit, error)`; MCP and HTTP exits use `IsError=true` / 500, and zero hits is an explicit `[]`. Wails bindings keep their single return value but log and return an empty slice (**residual**: the desktop UI still has no error channel, tracked as 12.13).

**P1 (2026-11-12)**: `internal/platform/logging.go` provides a shared slog JSON base with size rotation, one file per process, and a test pinning MCP's stdout-only-for-protocol rule. `internal/httpapi/middleware.go` adds `X-Request-ID`, access logging and recover; `cmd/mcp` instruments all 22 tools non-invasively via mcp-go's `WithToolHandlerMiddleware`; `Health()` gained three on-demand metrics (`db_size_bytes`, `compile_jobs_failed_24h`, `last_backup_at`). `internal/service/context.go` gained a 32000-char total budget with `omitted` counters, Gotchas / Next Steps priority, a capped and sorted timeline, CJK-safe truncation, and auto-capture no longer displacing human handoffs. `internal/platform/capture_pending.go` plus `cmd/repotory-capture` write a durable pending record surfaced on the next context; the SessionEnd hook in `internal/agentinit/agentinit.go` stopped silencing output, both `repotory-init/index.mjs` copies and `contract.json` were synced in the same batch, and the fixture comparison is strict again. Project resolution now uses `ResolveProjectByPath`.

**Same batch**: TODO 12.2 / 12.3 / 12.6 / 12.7 / 12.8 checked off; 12.11 changed from "pending decision" to "decided: B"; CHANGELOG `[Unreleased]` entries added.

**Explicitly not shipped**: D5 (idempotency / optimistic locking / `RestoreBackup` / review bypass / drainer backoff) and 12.4 (doc contract gate), 12.5, 12.9, 12.10, 12.12 remain in TODO.
