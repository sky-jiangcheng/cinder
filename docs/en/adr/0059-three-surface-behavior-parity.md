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

## Implementation record (part one) (2026-11-13, gate B)

**Gate B (the conformance fixture) is shipped**; gate C landed the same day, see "Implementation record (part two)". Against the acceptance bullets:

- **"a conformance fixture under scripts/ (request → expected scope / turns / fallback marking)" — done**: [`scripts/shared-chat-conformance.fixture.json`](../../scripts/shared-chat-conformance.fixture.json), with structural guards in [`internal/conformance`](../../internal/conformance/fixture.go) (settled rules must carry an expect, skipped rules must carry `assert_forbidden` plus the TODO they wait on, rule count = `contract_version.rules`). **5 settled rules** (only already-decided ones): `pairing.attach` / `pairing.order` / `fallback.not_persisted` / `fallback.not_persisted.zero_hit` / `fallback.local_render`; **3 skipped**: `scope.path` (TODO 11.1) and `pairing.orphan_reply` / `pairing.multiple_replies` (TODO 11.3 product decisions). **The fixture ↔ CoreVersion relationship is written down and gated**: the tool face unchanged means CoreVersion stays 2.0.0; adding rules bumps `contract_version.rules` (checked by `check-version-drift.mjs` gate 11), deleting a rule is a behaviour break and needs its own ADR.
- **"each surface has a runner reading it" — done, three surfaces = JetBrains / VS Code / Go**: Kotlin `SharedChatConformanceTest` (the real `sharedMessageGroups` + `SharedChatSession`), TS `ide/vscode/src/__tests__/conformance.test.ts` (the real `sharedTurns` + `SharedChat.send`), Go `cmd/mcp/conformance_test.go` (the fixture's fallback quotes = the server's actual `keywordFallback` output — the fixture and the server pin each other). All three hook into the existing CI jobs (`gradle test` / `npm test` / `go test`); no new job; all three read the one fixture file, no copies.
- **"injected negative — change one end's parameter or pairing rule, the gate must go red and name the end and the rule" — proven**: TS, flipping `pairing.order`'s expected order → red (naming the VS Code panel as diverged); deleting the fallback refusal from `sharedChat.ts` → both `fallback.not_persisted` and `.zero_hit` red; KT, reverting `sharedMessageGroups` to the old createdAt sort → `pairing.order` red; a duplicate rule id in the fixture → gate 11 red. All green again after restoring.
- **"the docs name the authority and the rule version, derived from the fixture" — done**: authority = the Cinder server contract (the decision section above), rule version = `contract_version.rules` (8 today), and gate 11 keeps the fixture's rule ids and the TODO narrative consistent.

**Real findings the gate went red on (caught and fixed the day the fixture landed — exactly the gap this ADR predicted)**:

1. **KT `sharedMessageGroups` re-sorted by createdAt** — three clients, three clocks; lexical order across machines is not chronological. A live probe confirmed the turn order INVERTS under clock skew (a question asked first rendered after a later one). Fixed to preserve the server's read order (seq ASC); pairing still resolves by `replyTo`. The old test "tolerates interleaving reversed order" tested a simulated server bug and now pins the contract instead.
2. **Both IDE panels stored the keyword fallback as an answer** (the TODO 11.2 other halves) — VS Code `SharedChat.send` and KT `SharedChatSession.transportAsk` appended the `No AI endpoint is configured…` match list as an assistant message, which the other panel then rendered as knowledge. Both now refuse to store it and surface the failure locally (the zero-hit branch `No results found for: …` carries no such prefix and is matched separately — which is exactly why the fixture lists it as its own rule).

**Honest boundary**: gate B proves the rules agree; "the panel still opens" is covered by gate C (below). `fallback.local_render` is asserted on the TS side as structural safety (DOM `textContent`) plus the not-stored check, not byte-for-byte escaping; `scope.path`, orphan replies and double replies still wait for their product decisions.

## Implementation record (part two) (2026-11-13, gate C)

**Gate C (the headless E2E smoke) is shipped.** Acceptance bullet: *"start a headless service and drive the VS Code / JetBrains panels through one search + deep link + ask each; either one failing is red; it must run with no display"* — **done**.

- **It drives each panel's own model, not a copy of the logic**: both sides use the conversation model in their own product code (TS `ChatSession` / Kotlin `ChatSession`) through their own `Transport` / `ChatTransport` port. Putting the model behind a port in ADR-0036 was for exactly this day — so the gate has no IDE, no webview and no display, and runs on `ubuntu-latest`.
- **`ChatSession` drives REAL binaries**: `cinder-mcp` (stdio JSON-RPC) and `cinder-server` (loopback HTTP, in both its `--web-dist` and API-only shapes). The gate builds the binaries itself ([`scripts/e2e-gate-c.sh`](../../scripts/e2e-gate-c.sh)) rather than taking an artifact — a stale artifact would test yesterday's code.
- **One assertion per journey**: (1) search — the real MCP server returns renderable hits (title AND snippet); (2) deep link — the hit produces the SPA route **pointing at the row the search actually returned**, a UI-serving server is recognised as openable, and an **API-only server must be recognised as not openable** (ADR-0035 decision 6's silent-404 guard); (3) ask — the answer comes from the model stub (a local fake OpenAI endpoint, running the whole tool-call loop) **and carries its source list**.
- **The fake endpoint for ask**: `/chat/completions` returns one fixed answer, so this journey needs no model, no network and no money — while still exercising the real evidence retrieval, prompt assembly and tool loop.
- **Injection negatives (each verified, everything restored green)**: on the KT side, making `servesWebUi` always true turns the deep-link refusal branch red; on the TS side, returning `[]` from `parseSearchHits` turns journeys 1 and 2 red (journey 3 stays green — the assertions are independent); on the TS side, stripping the source list turns journey 3 red.
- **CI**: a new standalone `e2e-gate-c` job (the only one needing Go + Node + JDK 21 at once; folding it into either IDE job would drag a toolchain into a job that deliberately stays small). Locally, one command: `bash scripts/e2e-gate-c.sh [vscode|jetbrains|both]`.

**Three real defects the gate caught on its first day (each went red on the gate first)**:

1. **The MCP child inherited the real HOME** — neither MCP client could set the child's environment, so the gate read the **user's real Cinder database and config**, and the ask journey answered from that data instead of the seed. Both clients gained `env` (production leaves it unset, preserving inheritance) and the gate passes an isolated HOME. **This is the one worth remembering**: a gate that pollutes the user's real data directory is worse than no gate.
2. **Gradle cached the gate into a false green** — the second run reported `Task :test UP-TO-DATE` and "passed" in 929ms without starting a single process. With the gate configured, the test task now forces `outputs.upToDateWhen { false }`. **A cached green is indistinguishable from "did not run"**; this gate must actually run every time.
3. **The gate depended on a pristine database** — the seed note's id was hard-coded to 1, so a second run against the same HOME went red for the wrong reason. It now reads the id back from the write. Related: the Gradle **daemon** keeps the environment it was started with, so `export` is invisible to the build (the first "pass" was a silent skip) — the gate's variables travel as **Gradle properties**, not environment variables.

**Honest boundary**: gate C proves the three journeys complete headlessly. It does **not** prove webview rendering is pixel-correct (that stays with `preview-console.mjs` and the three-surface DOM assertions), and it does not cover the stock JetBrains first-open crash (it never starts an IDE). The deep-link check verifies the URL and whether the server serves the UI, not the page behind it.
