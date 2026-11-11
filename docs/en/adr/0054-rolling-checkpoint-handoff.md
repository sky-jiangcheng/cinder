# ADR-0054: Rolling-checkpoint handoff — surviving credit exhaustion and abnormal termination

Status: **Accepted (2026-11-10 — the user asked for the ADR and the code in one breath, implemented the same day; the precondition that the install actions have not been executed on any real working directory yet is in the implementation record below)**
Date: 2026-11-10
Related: [ADR-0010](0010-claude-session-capture.md) (the SessionEnd hook and the privacy gate), [ADR-0043](0043-agent-instructions.md) (the instruction block teaches agents to read and write the memory layer), [ADR-0040](0040-handoff-as-channel.md) (handoff as an evidence channel), [ADR-0048](0048-core-contract-version.md) (changes to the MCP tool surface bump the contract version)
Starting schema: v30 → **v30 (no schema change)**; `CoreVersion` 1.2.0 → **1.3.0** (`repotory_handoff` gains the optional field `milestone`)

## Background

### Problem 1: the handoff feature has been spinning idle

On 2026-11-10 an audit found that Cinder's handoff feature is fully implemented in code (the MCP tool, the SessionEnd hook, evidence injection and UI all exist and carry unit tests), but in practice **it had been idle for 15 days**: the newest `handoff`-tagged note in `repotory.db` stopped at 2026-10-26, and after it came 189 commits (v2.15.0 → v2.33.5) with not one handoff. Three breaks existed at the same time:

1. **The SessionEnd hook was never installed**: `cinder/.claude/settings.json` does not exist; the global `~/.claude/settings.json` SessionEnd hook is iTerm2's `it2 handle-agent-hook`, not Cinder's repotory-capture

2. **The instruction block was never written**: `cinder/AGENTS.md` had no "Before you finish, call repotory_handoff" block (the one `SetupAgentClients` is supposed to write); nor does `CLAUDE.md` exist

3. **The MCP path is severed**: `cinder/.mcp.json` points to `build/bin/repotory-mcp`, but the file does not exist (build/bin only contains Cinder.app)

Even with all three fixed, a deeper design gap remains.

### Problem 2: handoff is necessarily lost when credits run out

The handoff has two write paths today:

| Path | How it works | Credit dependency |
|---|---|---|
| The agent calls `repotory_handoff` at session exit | depends on the agent actually making the call before the session ends | consumes the agent's inference credits |
| SessionEnd hook → `claude -p` | fires after the session ends and calls a model to summarize the transcript | **consumes credits** (same pool as the session) |

When an agent is hard-killed by credit exhaustion:

- **The active path is gone**: the agent never gets the chance to make that final call
- **The hook path is gone too**: `claude -p` needs a model call; with credits exhausted the hook's call fails the same way
- **End state**: the agent died at 80% and left nothing behind. The next agent can only reverse-engineer the intent from git commits — the intermediate decisions and unfinished steps are simply lost

Meanwhile the `repotory-capture` binary (`handoffFromSession` → `CreateHandoffNote`) never calls a model — it takes the last assistant message, the tool list and the git branch from the transcript and writes them straight into a handoff note. **This is a zero-credit path, and the current hook script does not use it.**

### Problem 3: handoff is a session-end snapshot, not a rolling checkpoint

The current design assumes the agent ends its session cleanly and writes one full handoff. But sessions end randomly: credit exhaustion, timeout, crash, the user hitting stop. The right model is that handoff is updated throughout the session — one checkpoint per meaningful work unit — so that whenever the session is cut off, the newest handoff already is the current state.

## Decision

### 1. Two handoff kinds: milestone (rolling checkpoint) and final (session summary)

`repotory_handoff` adds an optional `milestone` parameter (boolean, default false):

| Kind | `milestone` | Title | Tags | Next Steps means | When it is written |
|---|---|---|---|---|---|
| milestone | `true` | `Milestone Handoff` | `handoff, milestone` | what is unfinished right now | after each meaningful work unit |
| summary | `false` (default) | `Session Handoff` | `handoff` (unchanged) | what the next session should do | before session end |

**Why a tag rather than a new `kind`**: `project_notes.kind` has three values (`other/knowledge/log`), and a handoff currently persists as kind=knowledge with a tag of "handoff". Adding `milestone` as a tag routes through the existing tag predicate (`',' || REPLACE(LOWER(tags), ' ', '') || ',' LIKE '%,handoff,%'`) without inventing a new classification axis. The reader (`ListRecentHandoffNotes`) sorts by `updated_at DESC` and leads with the newest — milestones are written more often, so the newest is naturally the current state; nothing in the query needs to parse the kind. The tag is for humans and the UI ring; the ordering already does the work.

**Why an optional parameter rather than a new tool**: per ADR-0048, adding an optional field is a minor bump and breaks no existing caller. Every current call — including the `claude -p` hook path — omits `milestone` and behaves exactly as before.

### 2. The instruction block teaches rolling checkpoints

The `instructionsBlockBody` in `instructions.go` / `index.mjs` changes from "Before you finish, call repotory_handoff" to:

1. call `repotory_context` at the start of work (unchanged)
2. **after each meaningful work unit — a feature, a fix, a passing test run — call `repotory_handoff` with `milestone=true` to record the current state and what is unfinished**
3. before you finish, call `repotory_handoff` without milestone for the session summary (unchanged)
4. **when credits look low, write a milestone handoff immediately before continuing**

Rule 4 (the credit warning) depends on the harness exposing credit state; where it cannot, the rule is inert and rule 2 still covers the case.

### 3. The SessionEnd hook goes zero-credit first

`hookScript` becomes two-tier:

```
primary: repotory-capture (reads transcript → writes handoff; zero model calls, zero credit)
  ↓ fails (binary absent / gate off / no matching project)
fallback: claude -p (model summarizes, costs credit)
```

`repotory-capture` never calls a model (`handoffFromSession` only reads parsed transcript fields), so it keeps working when credits are gone. The `claude -p` fallback is kept because it produces a richer, more structured handoff when credits *are* available — it is strictly an upgrade over the raw-transcript version, never the only option.

### 4. The reader side leads with the newest checkpoint, whichever kind it is

`evidenceHandoffs` (wiki_evidence.go) already takes the newest 3 handoffs by `updated_at DESC` and puts the most recent in full. Once milestones are written, the newest record *is* a milestone and leads by ordering — no predicate change needed. What changes is only how `repotory_context` describes them: the newest record is introduced as current state, the rest as a one-line history, and each line marks `[milestone]` or `[final]` so the next reader knows whether the newest one is a checkpoint or a session summary.

### 5. Contract and versions

- `CoreVersion` 1.2.0 → **1.3.0**: `repotory_handoff` gains an optional field (minor bump per ADR-0048)
- `wantCoreVersion` in `cmd/mcp/main_test.go` bumps in the same change (the eighth drift check pairs them)
- schema stays at v30 — `milestone` is a tag plus an in-note label; no DB column and no migration
- the two implementations of the wire format (Go `agentinit` and the mjs script) change together and the golden fixture `internal/agentinit/testdata/contract.json` is regenerated (the `-update` flag); `scripts/check-agent-init-contract.mjs` keeps the two byte-identical
- the hook script body itself changes; it is *not* behind the MCP tool contract (a Claude Code hook is a file on disk, not a tool surface), so no extra bump

## Consequences / tradeoffs

- **The instruction block grows from three bullets to four.** It lands in every agent's context every session and competes with the project's own instructions. Kept under twenty lines; the negative criterion ("do not record what the commit log already shows") is unchanged — that is the part that matters.
- **A single session may produce several milestone notes.** The budget caps it: `handoffEvidenceLimit = 3` at reading time, and the old milestones collapse to one line each in the timeline. The cost of a milestone — one extra note row per write — is bounded by the number of work units, not by token budget.
- **`repotory-capture` output is terser than the model-driven path.** A raw-transcript capture cannot write `decisions` or `gotchas` at the depth a model could; it records what *was last happening* more than *why it was worth recording*. That is the price of being zero-credit, and the reason the `claude -p` fallback stays when credits are available.
- **Credit warnings are harness-specific.** `Workbuddy` may not expose credit state to the agent; in that environment rule 4 in the instruction block is a hint, not a guarantee. The milestone model still holds because of rule 2, not because of the warning.
- **Two tiers in one hook means two failure modes.** When `repotory-capture` succeeds you get a terse but present checkpoint; when it fails and the fallback runs you get a richer one; when both fail nothing lands, which is exactly today's behaviour for the credit-exhausted case today. The change strictly reduces the window of "nothing lands" without adding a new way for "nothing lands" to look like success.

## Implementation record (deviations)

**Landed the same day (2026-11-10)**:

- **D1**: `internal/service/handoff.go` gains `milestoneTag`, `HandoffInput.Milestone`, `handoffTitle()` (the two titles `Session Handoff` / `Milestone Handoff`) and `HandoffResult.Milestone`; the rendered body carries the header `[Rolling checkpoint — not a session summary]`; `cmd/mcp/tools_context.go` threads `milestone` through schema, description and handler. Legacy callers that omit the field are untouched.
- **D2**: the instruction block in `internal/agentinit/instructions.go` and its SSOT copy `scripts/repotory-init/index.mjs` now spells the four rules (start / rolling checkpoints before every work unit / session summary at exit / write one first when credits look low) plus the unchanged negative criterion; `testdata/contract.json` was regenerated with `go test -run TestContractFixture -update`, and both `check-agent-init-contract.mjs` and `sync-init.mjs` are green (the VSIX copy is byte-identical).
- **D3**: `hookScript` prefers the zero-credit path (`repotory-capture`, model-free) and falls back to `claude -p` only when that path cannot run.
- **D4**: **one step less than the plan** — the extra `[milestone]` / `[final]` markers in the one-line history were not implemented: the note title already encodes the kind (`Milestone Handoff` vs `Session Handoff`), and `ListRecentHandoffNotes` leads with the newest record by `updated_at DESC`, so a second layer of marking would duplicate signal already present. The read path was left unchanged and its ordering behaviour was verified by tests.
- **D5**: `CoreVersion` 1.2.0 → **1.3.0** with both pinned copies bumped in the same change; schema stays v30; CHANGELOG `[Unreleased]`, both ADR index pages and this EN twin land in the same batch.
- **Tests**: service-level checkpoint-kind test + a newest-milestone-leads-context test, one MCP tool-level end-to-end `milestone` test; `go test ./...`, docs-mirror, i18n-keys, version-drift and contract all green.
- **Desktop UI untouched**: `HandoffComposer` still writes only the final kind — milestone semantics currently reach agents through the MCP tool, the instruction block and the hook; a manual composer-side choice is left for a follow-up and does not weaken the protocol loop.

**Preconditions (residual breaks — deployment, not code)**:

1. the hook script and the instruction block are **install actions**: they only exist after `repotory-init` (the CLI twin of the desktop `SetupAgentClients`) is run in a project; the user's `Script`/`cinder` workspace still has neither entry (`.claude/settings.json` absent, AGENTS.md without the block) — exactly what the audit found.
2. the zero-credit primary path sits behind ADR-0010's `claude_session_capture` gate (default off); with it off the hook takes the `claude -p` fallback, which is intended.
3. `build/bin/repotory-mcp` being absent breaks `.mcp.json`'s path — a build-artifact issue outside this ADR's scope; this change only touches the protocol surface.

**Follow-up (2026-11-10, the collection checklist)**: precondition 1's "hope the user runs the setup wizard" has been narrowed — the collected-project checklist shipped (`agentinit.Options.InstructionsOnly` plus `AgentInstructionsStatusFor` / `MissingAgentInstructionsCount` / `InstallAgentInstructions`, and `repotory_scan` now returns an `agent_instructions_missing` count): the project page shows a banner for projects whose AGENTS.md/CLAUDE.md lacks the block and offers a one-click top-up (block only, no MCP binary dependency); the write still happens only on a user click. See CHANGELOG [2.35.0].

## Links

- [ADR-0010: Claude session capture](0010-claude-session-capture.md) — the hook and the privacy gate
- [ADR-0043: Agent instructions](0043-agent-instructions.md) — the instruction block that teaches reading and writing
- [ADR-0040: Handoff as channel](0040-handoff-as-channel.md) — handoff rides its own evidence channel
- [ADR-0048: Core contract version](0048-core-contract-version.md) — MCP surface changes bump the version
