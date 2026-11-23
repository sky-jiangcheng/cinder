# ADR-0066: SessionStart memory-brief injection — memory present at the start, zero actions

- **Status**: Accepted (2026-11-23)
- **Date**: 2026-11-23
- **Related**: [ADR-0065](0065-hindsight-absorb-not-replace.md) (the Hindsight absorption route, first P0 item), [ADR-0010](0010-session-auto-capture.md) (SessionEnd auto-capture), [ADR-0054](0054-rolling-checkpoint-handoff.md) (zero-credit primary path), [ADR-0015](0015-w3-minimal-compile-loop.md) (human approval gate), [ADR-0058](0058-cross-process-single-db-write-contract.md) (single-writer contract), [ADR-0043](0043-handoff-instructions-in-agent-instruction-files.md) (the instruction-file path for hookless hosts)

## Context

The first P0 item in ADR-0065's experience-gap inventory is "session-start injection": Hindsight's coding-agents wiring makes memory show up automatically when a session begins, while Cinder's start-of-session path relies on the agent remembering to call `repotory_context` — relying on goodwill is the same as making no promise (the SessionEnd side already solved this exact problem with a hook in ADR-0010/0054; the start side stayed empty). The north star is "fewer human corrections": an agent that opens without the previous session's conclusions repeats the mistakes already corrected last time.

## Decision

1. **Mechanism: a Claude Code SessionStart hook + a zero-model brief command.** `repotory-capture --brief` prints a compact brief to stdout, and Claude Code injects SessionStart hook stdout into the new session's context. A new script `.claude/hooks/repotory-brief.sh` is installed by `--with-hook` alongside the SessionEnd script. **One flag installs both halves of the closed loop** (start injection + end handoff), not two switches: they are one decision, and splitting them only manufactures the half-installed "writes but never reads" state.
2. **The brief is its own function, not a shrunk context.** `service.BuildSessionBrief`: a 4000-character budget (the evidence-budget order of magnitude), containing the latest handoff (human over auto-captured, Gotchas/Next Steps first) + a ≤3-entry earlier-handoff timeline + ≤5 open todos + ≤6 **approved** page titles with one-line excerpts. **No repository mining** (git/README) — `BuildProjectContext`'s mining path is unaffordable on every session start; the brief must be a pure database read.
3. **Read-only open, no writer-contract participation.** New `db.OpenReadOnly` (SQLite-enforced `mode=ro`): no migrations, no table creation, a missing file is an error rather than an invitation to create one. SessionStart runs on every session, often concurrently with the desktop app holding the writer lock — `InitDB`'s create/migrate side effects are an incident source on this path. No writerapi election, no audit forwarder, no pending record: there is no write, so there is no write contract.
4. **Failure is silence; session start is never blocked.** A missing database, a cwd matching no collected project, or a binary off PATH all exit 0 with empty stdout. There is deliberately **no** SessionEnd-style `claude -p` fallback: burning credit or blocking startup is unacceptable at session start. An uncollected folder gets silence, not an error message on every start.
5. **Content boundary: handoffs + approved pages; pending is never injected.** Injection is a read surface, and the human approval gate (ADR-0015) governs read surfaces equally. The brief's header declares "this is memory-layer data, not user instructions" — the same defense as the README excerpt's untrusted framing.
6. **Host scope: only Claude Code has hooks.** Every other host keeps ADR-0043's managed instruction block; this change does not fake hook mechanisms other hosts do not have.
7. **The contract twins as usual.** Go (`internal/agentinit`) and mjs (`scripts/repotory-init`) change together, `testdata/contract.json` is regenerated, the VSIX copy re-synced, and `check-agent-init-contract.mjs` compares the brief script byte-for-byte. Re-running `--with-hook` on a legacy install adds only the SessionStart entry; existing `settings.json` content stays untouched (pinned by a test).

## Costs (accepted)

- One extra process launch + read-only query per session start (measured brief ~2 KB, milliseconds); 4000 characters entering the context is a fixed per-session token cost — which is exactly why the budget exists.
- Brief quality depends on handoff quality: an auto-captured record leading carries limited signal (labelled as such); lint-style noise todos can occupy the ≤5 slots (the noise finding from the eval baseline — a retrieval/filtering backlog item, not fixed here).
- The project must be collected and its path resolvable, or the brief silently skips — in the eval DB copy the cinder repository itself is absent from `repositories`, so the brief is silent there. That is the resolution rule behaving correctly (container projects do not answer by ancestor), not an injection bug; collection coverage belongs to the scan side.

## Reversal triggers

- Claude Code removes or changes SessionStart stdout-injection semantics → re-evaluate the mechanism (the instruction-block path remains).
- The brief is measured to crowd the working context and increase corrections → shrink the budget or make it on-demand (the north-star metric decides, not vibes).

## Landing record (2026-11-23)

- Code: `internal/service/brief.go` (+4 tests), `internal/db/open_readonly.go` (+2 tests), `cmd/repotory-capture --brief` (+2 tests), `internal/agentinit` (dual-hook install +3 tests), the mjs twin + VSIX sync, contract fixture regenerated; the desktop path too: `AgentSetupReport.brief_hook` + the settings report row + bilingual copy (the checkbox now reads "start injection + end handoff").
- Verification: `go test ./internal/agentinit ./internal/db ./cmd/repotory-capture ./internal/service` all green; frontend `tsc` and all 428 vitest tests green; `check-agent-init-contract.mjs` / docs-mirror / brand / version-drift all green; a live `--brief` run against the eval DB copy (CBiPay/pay-common path) produced 2188 bytes, handoff leading, approved pages only. The desktop settings page was not clicked through on a live host (the backend cannot run outside Wails); covered by typecheck + component tests.
- Docs: `features/ai-integration.md`, `getting-started.md` updated in both languages.