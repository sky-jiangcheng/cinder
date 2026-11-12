# ADR-0059: Three-surface behaviour contract — one authority and a parity gate

Status: **Accepted (raised by the 2026-11-12 engineering assessment; the owner chose B + C, both gates, the same day; not implemented — tracked as TODO 13.2)**
Date: 2026-11-12
Related: [ADR-0048](0048-core-contract-version.md) (contract version semantics), [ADR-0055](0055-agent-surface-contract-and-observability.md) (exit contract and doc gate), [ADR-0033](0033-mcp-ask-evidence-answer.md) (agent and desktop get the same answer), [ADR-0036](0036-ide-chat-console.md) (console rendering and escaping)
Starting schema: v31 → **v31 (no schema change)**; `CoreVersion` unchanged

## Background

Desktop, the VS Code extension, the JetBrains plugin (and Warden) consume the same memory, but **only names and versions are gated**: `scripts/mcp-tools.fixture.json` pins the 22 tool names and `CoreVersion`, and `check-version-drift.mjs` checks documented tool counts. **Behavioural parity has no gate at all.**

Three verified drifts (this ADR does not judge which client is right):

- **Scope**: JetBrains sends the workspace path when asking (`ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/SharedChatSession.kt:217-218`); VS Code sends only `query` (`ide/vscode/src/chatSession.ts:462`). The same question gets a different evidence scope, so "the thread syncs but the answers do not" (TODO 11.1).
- **Fallback text persisted**: JetBrains writes `answer` verbatim as an `assistant` message into the shared table (`SharedChatSession.kt:196`) and explicitly preserves the keyword-fallback wording (`:222`); the other client reads it back as a real answer (TODO 11.2).
- **Turn pairing**: grouping/pairing of shared turns is implemented twice (`sharedMessageGroups` at `ChatModel.kt:233` vs `sharedTurns` at `ide/vscode/src/sharedChat.ts:36`), with no gate proving the two are equivalent (TODO 11.3).

The consequences are not theoretical: the JetBrains panel "aborts on first open" is recorded as a **stock crash carried by every release** (`CHANGELOG.md:75`), and the 11.4 stored XSS was found in the plugin only after shipping. `go test ./...` cannot see the Kotlin or TS panels, and the `internal/app` binding layer measured 15.8% coverage in the 2026-11-11 assessment — **none of the 19 CI gates exercises the layer users actually click**.

## Decision (owner, 2026-11-12)

**Both B and C**: one conformance fixture for "are the rules equal", one headless E2E smoke for "does the panel still open". They do not answer the same question, and either one alone leaves half the surface blind:

| Gate | Question it answers | What a failure shows |
|---|---|---|
| B conformance | Same input — do the three clients produce the same scope / turns / fallback marker under one rule? | Which client, which rule, expected vs actual |
| C E2E smoke | Is the layer users actually click still alive (search → deep link → ask)? | The whole panel is down (including the "aborts on first open" class) |

B's fixture is the **target contract**, not a snapshot of current behaviour — pinning the status quo would freeze the drift into the standard and the gate would never go red again. That implies an ordering: **the fixture may only contain rules that have already been decided.**

- Decided, pinnable now: 11.2 (the keyword fallback must be marked and **must not be persisted**; ADR-0033 requires the marking, ADR-0036 decision 8 requires escaped rendering) and 11.3 (turn pairing has exactly one authoritative rule, and that contract lives on the Cinder side).
- Undecided, explicitly skipped in the fixture with a reason: 11.1 (whether the ask carries `path` — whether a shared topic belongs to the workspace or to the person is a product decision, not this ADR's). C's smoke path **does not depend** on 11.1: each client asks with its own parameters and the assertion is "the search returns, the deep link opens, the ask answers", not "the answers are byte-identical".

**Authority still lives server-side** (the spirit of A), but this round makes no architectural change: every expected value in B is written to server semantics, and any client that cannot produce it is the drift to fix — which is B's advantage over A: A needs all three clients to move at once, B measures the gap first.

## Open questions (answered above; original text kept)

1. Where does authority live: the Cinder server (clients only render), or each client implementing it under contract?
2. What does "parity" mean: same answer, same scope and same turn split for the same input, or merely "does not crash"?
3. Where does the gate live: an end-to-end CI smoke, a shared conformance fixture, or both?
4. 11.1–11.3 are product decisions (does a shared thread belong to the workspace or to the person); this ADR does not decide them.

## Options and trade-offs

| Option | Approach | Upside | Cost / risk |
|---|---|---|---|
| A Server authority | Scope, pairing and fallback marking are decided server-side; clients render only | One implementation, smallest drift surface; consistent with "thin client, zero logic copying" | The contract must carry scope / pairing fields; one change bumps three clients at once; offline degradation needs its own decision |
| B Shared conformance suite | One golden fixture (request → expected answer / turns / markers) run by each client | Pinpoints which client drifted; no architecture change required | Three harnesses to maintain; fixtures rot as the product evolves; no real UI coverage |
| C E2E smoke only | CI boots headless and drives both panels through "search → deep link → ask" | Directly proves the clicked layer is alive | Slow, flaky, needs GUI/IDE runtimes; few paths per run |
| D Status quo + docs | Manual sync plus per-client unit tests | Zero cost | Already proven insufficient by two release-level defects |

## Explicitly undecided

- ~~No option is chosen~~ — **B + C chosen on 2026-11-12**; no client code changes until the gates exist (red first, then fix the behaviour), no new MCP tools, no `CoreVersion` change.
- 11.1–11.3 are not recorded as decided; they stay open as TODO 11.1–11.3, with 11.1 explicitly skipped inside the fixture.
- The three clients are not required to share UI code.

## Acceptance criteria

- **B**: a conformance fixture under `scripts/` (request → expected scope / turns / fallback marker) with one runner per client; an injected negative case — change one client's parameters or pairing rule — must turn the gate red **and name the client and the rule**. The fixture's relationship to `CoreVersion` is documented: a changed rule bumps minor, a removed rule bumps major.
- **C**: a new CI job boots headless, drives the VS Code and JetBrains panels through "search + deep link + ask", and fails if either side fails; it **must run without a display** (headless IDE or a webview DOM harness), otherwise it becomes a gate that stays red and gets ignored.
- Documentation states where authority lives and what the rule version is, sourced from the same fixture (the `check-version-drift.mjs` pattern: derive the number, never hardcode it).
