# ADR-0043: Handoff instructions into the project's instruction files — making "read at the start, write at the end" a product feature (a managed block)

- Status: Accepted (2026-11-03, the user asked to "improve along the design that fits best")
- Date: 2026-11-03
- Related: [ADR-0007](0007-session-memory-protocol.md) (the session-memory protocol: `repotory_handoff` / `repotory_context` are its two endpoints), [ADR-0010](0010-session-auto-capture.md) (session-end automatic capture, **off by default**), [ADR-0009](0009-ide-presence.md) (one-command `repotory-init` registration; this ADR supplies the half it left out — the tools were registered, the instructions were not), [ADR-0040](0040-unscoped-ask-orientation-and-handoff-evidence.md) (handoff records in the evidence layer)

## Background

The timebox criterion in TODO 2.1 (≥ 5 handoffs over two consecutive weeks, counting from 2026-10-28)
measured **0 inside the window**. Checked producer by producer on 2026-11-03, the conclusion is that
this is **not "the user did no work"**: `~/.claude/projects` holds 6 session files inside the window,
and not one handoff was added. All three possible producers are disconnected:

| Producer | Measured state | Where it breaks |
|---|---|---|
| ① the agent calls `repotory_handoff` itself | the global `~/.claude.json` registers `repotory-mcp` — **the tool is available** | **nothing ever tells the agent "call it when the session ends"** |
| ② the Claude Code SessionEnd hook | `find` across the repo: **not one project has `.claude/hooks/repotory-handoff.sh`** | never installed; and the hook swallows failures in three layers, so **it can be installed and still silently produce 0** |
| ③ the desktop's manual "write a handoff" | live DB has `claude_session_capture=1` (on) | `handoff_capture.go:28` states *"triggered only explicitly (no startup/hook automation on this path)"* — that switch only **permits**, it never triggers |

The existing four records line up with this too: three real handoffs are `source=mcp` and all dated
2026-10-26 — the audit session where the agent was **explicitly told** to call the tool — and one is a
manual `Test Handoff Note` from 2026-10-18. **"When it was asked, it produced; when it wasn't, zero."**
That sentence pins the root cause: **the tools are present, the instructions are absent**.

And those two instructions **were already written down as documentation**: lines 83-84 of the
[local agent recipe](../features/local-agent-recipe.md) ("call `repotory_context` before starting
work… call `repotory_handoff` before ending it…"), and the same page's own "Boundaries" section admits the gap:

> **No guarantee the model will call tools on its own.** Small models need the instruction written down
> ("call `repotory_context` first"); putting it in the client's prompt or a hook is the reliable approach.

**The product wrote that judgement into a manual page and never turned it into anything clickable.**
So every user has to find that page, copy the two sentences, and decide which file to paste them into —
that is the mechanism behind the zero, not a matter of attitude.

### Why the fix is not the hook

Producer ② (the SessionEnd hook) is already implemented, but it carries three properties this ADR
does not want to inherit:

1. **Client-specific.** Only Claude Code has hooks. Cursor / opencode / Windsurf / any other MCP client
   **cannot use it** — while "a cross-agent persistent memory layer" is exactly the positioning
   (ADR-0007, and the first-screen statement from Sprint 4.4).
2. **Failures are silent.** The hook script is designed never to interrupt a session:
   `command -v claude || exit 0`, `[ -f .mcp.json ] || exit 0`, and `>/dev/null 2>&1 || true` at the end.
   Three layers swallow every failure. **"Installed but not working" and "not installed" are
   indistinguishable in the observation.**
3. **It spawns an extra Claude Code process**, running another model round just to record one note —
   a cost that should not be paid by the end of a session.

The instruction-file path **fixes all three at once**: protocol-neutral (every agent that reads
`AGENTS.md` gets it), visible once written (it is a diff, not a process), and written by **the agent
that is already doing the work**, so no extra inference is spent.

## Decisions

**① The product writes those two instructions into the project's instruction files, as a managed block.**
`agentinit` gains a step: write a block delimited by `<!-- repotory:begin -->` / `<!-- repotory:end -->`
into `AGENTS.md` and `CLAUDE.md`. **Existing block → replaced in place; none → appended at the end;
file missing → created and reported as `created`.** Nothing outside the block is ever touched —
this is the only acceptable shape for "writing into the user's own files": **we own that paragraph,
not the file**.

**② Write both files rather than picking per client.**
`AGENTS.md` is the de facto cross-agent standard (Codex / opencode / Cursor / Amp… all read it);
`CLAUDE.md` is Claude Code's own. An instruction file nobody reads is **inert** — it produces no
behaviour and no error — so the cost of "write both" is one extra file, while the cost of "pick per
client" is **silently breaking the moment the user switches agent**. The report lists files
individually (`created` / `updated` / `unchanged`) so the user sees what was written.

**③ The block text is for the model, written in English, and states what NOT to record.**
The reason matches the hook's prompt (precedent already set): **this is machine-instruction text, not
UI copy**. It does not go into `web/src/locales`, so the bilingual-mirror and "no Chinese left in the
English locale" gates do not apply — UI copy is still changed in both languages together, while the
instruction text exists once and is shared by both implementations.
The block carries three sentences: at the start call `repotory_context` (and that it **leads with the
newest handoff**); at the end call `repotory_handoff` (naming the `gotchas` / `decisions` / `next_steps`
fields); and **one negative criterion**: "do not record what a commit log already shows; record what the
next session would otherwise re-discover the hard way". That third sentence is the substance of this ADR:
it encodes the "when should a handoff be produced" discussion into the instruction itself — otherwise the
instruction just makes agents log activity, which is exactly the shape M6/M7 starved on
(`idea` 0 / `manual` 1).

**④ Uninstall = delete what sits between those two marker lines; no `--remove` flag.**
Same posture as the hook script (whose comment literally says "delete this file and the matching
settings entry to uninstall"). Reason: the managed block is **readable, editable text** for the user,
not hidden state the product has to babysit; adding a removal path means another byte-level contract to
keep aligned across two implementations, for no proportional gain.

**⑤ This step and the hook are two independent switches, both on by default.**
The settings page gains one checkbox next to the existing "also install the SessionEnd hook".
**The two paths neither exclude nor double-charge each other**: the hook catches "the user forgot to
wrap up", the instruction file catches "the user does not use Claude Code". Both write through the same
`CreateHandoffNote` path, so **there will never be two shapes of handoff record**.

**⑥ No schema change / no new MCP tool / no new binding / no change to the `repotory_handoff` and
`repotory_context` protocol surface / no change to `claude_session_capture`'s default-off semantics /
no reversal of any ADR-0007·0010·0026 decision.**
`SetupAgentClients` **gains a parameter** instead of becoming a new binding: it is still the same single
"connect" action, and splitting it into two buttons would make the user think they owe two decisions.
The frontend transport's arity changes with the backend (`internal/app/frontend_contract_test.go` pins
method-name existence and the name is unchanged; `internal/httpapi/rpc.go` maps arguments positionally,
so one more position suffices).

**⑦ Both implementations remain pinned by the same golden fixture.**
`internal/agentinit` and `scripts/repotory-init/index.mjs` must produce **byte-identical** files.
`internal/agentinit/testdata/contract.json` gains the expected content of these two files,
`scripts/check-agent-init-contract.mjs` extends its `PRODUCED` list, and the copy under
`ide/vscode/repotory-init/index.mjs` is regenerated by `sync-init.mjs`.
**The block's correctness is pinned by tests (idempotency / preserving user content / creating missing
files), not by prose.**

## Consequences

- **Positive**: after one click, the two instructions sit in `AGENTS.md`, and **every agent that reads it
  gets them** — this is the first time the product's "protocol-neutral" positioning is a button rather
  than a paragraph in a manual. The timebox criterion (2.1) changes from "wait for users to read docs"
  to "wait for users to click", which is the only way that supply side can actually move.
- **Costs (accepted)**:
  - **It writes into the user's repository** (two files created, or two existing ones modified). It is a
    setup action, with a checkbox, a per-file report, and a `.repotory-init.bak` backup — but it does
    show up in the user's `git status`, and **the manual and the block's own comment must both say so**;
    the first person to click will see one more diff.
  - **The model may still not obey the instruction.** Compared with the hook path this only moves the
    "no guarantee" from "there is no trigger" to "there is a trigger with no execution guarantee".
    The honest framing: **the instruction file turns "recording" into an event with a roughly 80% chance;
    0% is what it is today**. So producer ② is **not deprecated** — both paths coexist.
  - **`CLAUDE.md` will bloat in the same family as this repository's own `AGENTS.md`.** The block is
    deliberately capped at three sentences; before lengthening it, measure its effect on the
    `repotory_context` budget once.
  - **Attributing the timebox reading gets harder.** If handoffs start appearing inside the window it may
    be the instruction file, or the hook, or the user clicking "write a handoff". **The `source` field
    separates some of it** (`mcp` / `desktop`), but "this prompt made the agent call it on its own" and
    "the hook made it call" are the same value inside `source=mcp`. Separating them further would need a
    handoff source subtype, which **this ADR does not do** (no new protocol field).
- **What this does not pretend to fix**: the upstream starvation of M6/M7 (`knowledge` 38 / `idea` 0 /
  `manual` 1) is not cured by this block. It fixes **"nobody catches the cognitive state at the end of a
  session"**, not "where knowledge-type notes come from".

## Implementation checklist

| Surface | File |
|---|---|
| Block text + upsert pure function | `internal/agentinit/instructions.go` (new) |
| Go wiring | `internal/agentinit/agentinit.go` (`Options.WithInstructions` / `Report.Instructions`) |
| Go regressions | `internal/agentinit/instructions_test.go` (new) + `agentinit_test.go` (contract fixture grows two files) |
| Golden fixture | `internal/agentinit/testdata/contract.json` (regenerate with `-update`) |
| Terminal twin | `scripts/repotory-init/index.mjs` (`--with-instructions`) + `ide/vscode/repotory-init/index.mjs` (generated by `sync-init.mjs`) |
| Contract gate | `scripts/check-agent-init-contract.mjs` (`PRODUCED` gains two entries) |
| service / binding | `internal/service/agent_clients.go`, `internal/app/bindings.go`, the rationale text in `internal/app/surface.go` |
| Frontend | `web/src/api/endpoints.ts`, `web/src/api/types.ts`, `web/src/pages/settings/ActionsTab.tsx` and its test |
| Bilingual entries | `web/src/locales/{zh-CN,en}/common.json` (`settings.agentClientsInstructions` etc., **both at once**) |
| Manuals | `docs/features/local-agent-recipe.md`, `docs/features/ai-integration.md` (both languages) |
| TODO | 2.1 gets one line: "the supply side is now connected; re-read the window on 11-11" |

## Implementation record (2026-11-03)

**Implemented (2026-11-03)**: the managed block lives in the new `internal/agentinit/instructions.go` — `upsertInstructions` is a pure function with three shapes (existing block replaced in place / existing file appended / missing file becomes the block alone; a half-open marker only appends, never swallows what follows), and `installInstructions` writes each file separately and reports each separately, so one failure cannot block the other. `Options.WithInstructions` feeds `Report.Instructions`, reaching the binding through a fourth `service.SetupAgentClients` parameter and the frontend transport (`args: [projectId, clients, withHook, withInstructions]`); the settings page gains one checkbox (on by default) and the report lists each file as created / updated / unchanged with its backup path.

**Both implementations write byte-identical files** (this repository's hardest cross-language gate): `scripts/repotory-init/index.mjs` gains `--with-instructions`, `ide/vscode/repotory-init/index.mjs` is regenerated by `sync-init.mjs`, `testdata/contract.json` is regenerated with `go test ./internal/agentinit -run TestContractFixture -update` (now carrying the `AGENTS.md` / `CLAUDE.md` keys), and `scripts/check-agent-init-contract.mjs` extends `PRODUCED` by two entries and compares those two Markdown files as **exact text** — like the hook script, not semantically like JSON. The gate was red before the fixture was regenerated and green after: `agent-init contract OK: the CLI script and the Go implementation write the same files`.

**Regressions**: 12 cases in `internal/agentinit/instructions_test.go` (idempotency, content outside the markers preserved, half-open marker, missing file created, backup taken, both files written, the two switches never crossing into each other, the block carrying the negative criterion); 2 cases in `internal/service/agent_clients_test.go` (through the real `SetupAgentClients` path, second click must report `unchanged`; hook and instructions stay independent); 2 cases in `ActionsTab.test.tsx` (the switches map to separate arguments, and every file is reported with `updated` distinguishable from `created`). **Full run**: `go test ./cmd/... ./internal/...` green, `go vet` clean, `npm test` **415 tests across 49 files**, `npm run lint` (0 errors, 0 warnings), `npm run build`, `check-i18n-keys` (920 static keys, both locales changed together), `check-docs-mirror`, and `check-version-drift` (43 ADRs, both indexes aligned).

**CLI end to end** (an isolated `/tmp` directory holding an `AGENTS.md` with `# My project\n\nBuild with make.\n` and a fake `repotory-mcp`): the first run reports `AGENTS.md: updated (backup: AGENTS.md.repotory-init.bak)` / `CLAUDE.md: created`, the user's original text stays at the top; the second run reports `already contains the repotory block, no change`, and `cmp` proves the bytes are **identical** — idempotency verified as a hash, not as "looks unchanged"; the backup equals the original byte for byte.

**One observation unrelated to this ADR**: the first full `npm test` showed a single red case in `NoteSection.test.tsx > keeps the stored note when the tidy call fails`; running that file twice standalone and re-running the full suite were all green. The shape matches the family of "waiting for the mock instead of waiting for the data" races fixed on 2026-11-03 (the one ADR-0026 records). This change does not touch that file, so it is **not handled here** — recorded so it is not re-investigated as something new.

**Accepted costs (confirmed at implementation time)**: the block lands in the user's `git status` — the block itself now says how to delete it and states this; the duplication between `CLAUDE.md` and `AGENTS.md` is the deliberate consequence of decision 2 (redundant rather than inert); model compliance is still not guaranteed, so the hook path is **not deprecated**.
