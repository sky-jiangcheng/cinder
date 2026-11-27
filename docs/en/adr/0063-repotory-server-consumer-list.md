# 0063 Re-pinning the repotory-server consumer list — who still needs the local service after ADR-0062, and the provider-configuration gap

- Status: **Proposed (drafted 2026-11-15)** — the consumer list is established fact; the channel decision awaits the owner
- Related: [ADR-0035](0035-jetbrains-mcp-primary-channel.md) (decision 6 positioned `repotory-server` as "a user-fetched optional enhancement, no distribution channel"), [ADR-0062](0062-ide-project-lock-over-mcp.md) (the project lock leaves the server, narrowing the dependency surface), [ADR-0044](0044-ide-panel-windsurf-shape.md) (the IDE panels' provider configuration reads and writes the service's `ai_chat_*`), [ADR-0048](0048-core-contract-version.md) (contract-version semantics)

## Background

### Fact 1: ADR-0035's "optional enhancement" position rests on a consumer list that has gone stale

ADR-0035 decision 6 positioned `repotory-server` as "a user-fetched optional enhancement" and **declined** to build install.sh / Homebrew / Scoop channels. The reason given at the time was concrete: **"the VS Code extension has always shipped without repotory-server — deep links are not a usability precondition for any client."**

But that consumer list was implicit, and it is now stale: **ADR-0062 (landed 2026-11-15) moved the project lock off the server** — workspace resolution now goes over the MCP tool `repotory_workspace_resolve`, and neither IDE panel's project lock needs the local service. In other words, the single largest consumer from ADR-0035's era — "the project lock depends on the server" — no longer exists, and what remains is **not** the single item "deep links" but a set of capabilities of different kinds.

### Fact 2: after ADR-0062, each server consumer checked one by one

Checking, capability by capability, what actually happens **without** `repotory-server` (the code paths of both panels and the desktop app):

| Capability | Channel | Without the server | Nature |
|---|---|---|---|
| Project lock (workspace resolution) | **MCP** (ADR-0062) | ✅ locks as usual | **moved out** — no longer consumes the server |
| Search / read / write / create / organize / project list | MCP | ✅ works as usual | no server consumption |
| **IDE panel AI provider configuration** (`GetConfig` / `UpdateConfig` / `ListAIModels` / `TestAIChat`) | HTTP RPC | ❌ **cannot configure an AI endpoint**; `repotory_ask` permanently falls back to keyword matching | **substantive gap** |
| `MoveNote` (re-filing a note between projects) | HTTP RPC | ⚠️ degrades: content saved, project unchanged, says so | degradable |
| `BindWorkspaceToProject` (filing a workspace folder under a project) | HTTP RPC | ❌ errors (a write, human-only, no MCP twin) | honest error |
| Deep links (into the web UI) | HTTP | ⚠️ best-effort: `servesWebUi` pre-check, says so when absent | enhancement |
| IDE diagnostics / health check | HTTP | informational, does not block the panel | non-blocking |

**The key finding**: ADR-0035 treated "deep links" as the server's representative consumer, but the one that forms a **substantive dependency** is **provider configuration** — an IDE-only user (has `cinder-mcp`, no desktop app) has **no entry point to configure an AI endpoint** without the server: the panel's provider form writes `ai_chat_*` over RPC and cannot write; the desktop app is another configuration surface, but this class of user has no desktop. The result is that `repotory_ask` permanently sits on the "no endpoint configured → keyword fallback, labelled as not-an-answer" path — **the capability is there, the answer is never an answer**. Desktop users are unaffected (the desktop writes the same database directly).

### Fact 3: "optional enhancement" and "substantive gap" cannot share one position

ADR-0035's "no channel" decision targeted an **enhancement**: when deep links are unavailable the panel still works, just without a jump. Missing provider configuration is a **functional gap**: that class of user's core question-answering capability (`repotory_ask` answering real questions) is blocked. Mixing the two under one "optional enhancement" phrase strips the channel decision of its basis — **building a channel for an enhancement is not worth it; building one for a substantive gap means repositioning it**.

## Decision

**Five decisions**:

**Decision 1: this ADR pins the consumer list as the single source of truth.**

The table above ("server consumers after ADR-0062") is the authoritative description of `repotory-server`'s dependency surface. Every later discussion of the server (channels, positioning, documentation wording) reads from this list, not reverse-engineered from ADR-0035's prose. Maintenance discipline: **when a capability's channel changes (as ADR-0062 moved the project lock), this table updates in the same batch** — list drift is exactly why this re-pin exists.

**Decision 2: provider configuration is classified separately as a "substantive gap", not listed with deep links.**

`GetConfig` / `UpdateConfig` / `ListAIModels` / `TestAIChat` are exposed over HTTP RPC and are the **only** entry point through which an IDE-only user configures an AI endpoint. When they are absent it is not "one enhancement missing" — it is "the core question-answering capability unavailable". The documentation (IDE READMEs, `packaging/README.md`) and any later channel discussion must speak in these categories — `packaging/README.md`'s current "manually fetched (deep-link enhancement)" wording updates with this decision.

**Decision 3: no full channel now — ADR-0035 decision 6's "no install.sh / Homebrew / Scoop" position stands.**

Reason: the substantive gap (provider configuration) is **not closed by adding a channel** — a user who installs the server still has to know it exists and start it; a channel solves "installable", not "used". Building a channel before the configuration-surface decision is made wraps a positioning problem in a distribution problem. The channel decision is **deferred until decision 4 is settled**.

**Decision 4: two paths for the configuration surface — after the 2026-11-15 re-review, B is rejected; A is recommended, in two steps.**

- **Option B (provider configuration over MCP) — rejected on re-review.** The first draft recommended B and judged "configuration writes are not knowledge content, low risk"; that judgment was **half right (technically) and wrong on policy**. Two registered, load-bearing decisions reject B outright:
  - **The surface registry**: `GetConfig` / `UpdateConfig` are explicitly `SurfaceDesktopOnly` with the reason "changing privacy gates must never be something an agent can do to itself" (`internal/app/surface.go:150-151`), and an integrity test enforces the registry. Exposing them as MCP tools means reversing that decision — the AI endpoint / model / key determine where the user's memory queries go and whose key is used, a control-plane change in the same family as MoveNote / `BindWorkspaceToProject`'s human-only stance.
  - **The single-writer contract (ADR-0058)**: `UpdateConfig` is in `bindingWrites` but has **no catalogue method** — it is classified as a human-gate write; `DecideWrite` returns `WriteRefuse` for it from a client process and `GuardedLocal` rejects it explicitly. Landing B requires **adding a catalogue method for configuration writes** (moving ADR-0058's write classification), far beyond "add two tools".
  - The low technical risk (agents already have local file access; `GetConfig` already masks to `********`) does not change the policy cost: B is a policy reversal, not a feature. **B is only re-evaluated if the owner explicitly decides to reverse the privacy-gate stance.**
- **Option A (distribution channels for `repotory-server` + a one-click start) — recommended, in two steps**:
  - **Step one, A-light**: give VS Code a one-click server start (port the JetBrains `RepotoryServerManager` pattern: binary resolution + child process + `--web-dist`; the server binary is still fetched from the release once). After that, both IDEs' provider-configuration gap becomes "one click", with no channel at all.
  - **Step two, A-full (as needed)**: if users still find "download the server once" too much after step one, add install.sh `--with-server` / Homebrew / Scoop channels. The channel is the second step — first verify whether the one-click start already reduces the friction to acceptable.
  - A touches no tool surface, no surface registry, no write-contract — all existing discipline stands; the cost is the server's positioning moving from "optional enhancement" to "the IDE user's configuration surface".
- **The final choice is the owner's**: this ADR recommends A (A-light first).

**Decision 5: deep links stay a "best-effort enhancement"; this ADR does not touch them.**

Deep-link unavailability has a pre-check, an honest notice, and does not block the panel — that fits the "enhancement" position. ADR-0035 decision 6's conclusion on deep links stands and is not changed by this re-pin.

## Consequences (accepted)

- **This ADR changes no code**: it is a re-pin plus a decision framework. Whichever option is chosen (A or B) needs its own implementation and gates, out of scope here.
- **The list needs ongoing maintenance**: when a capability's channel changes, this table updates in the same batch — that is the price of this list existing.
- **If B is chosen** (rejected; the cost is recorded for the record): the tool surface grows by one (`CoreVersion` minor), both panels' provider forms change transport, the skill tool signatures and documentation sync; **and** the `UpdateConfig` `SurfaceDesktopOnly` registration must be reversed (surface registry + integrity test), **a catalogue method must be added for configuration writes** (moving ADR-0058's write classification), and ADR-0035's "provider configuration over HTTP" position amended — a policy reversal, not a feature.
- **If A is chosen (recommended)**, in two steps: **A-light** (VS Code one-click server start, porting the JetBrains pattern) turns both IDEs' provider-configuration gap into "one click", with no channel and no tool-surface change; **A-full** (install.sh / Homebrew / Scoop channels, as needed) repositions the server from "optional enhancement" to "the IDE user's configuration surface", moving `packaging/README.md` and the IDE README wording, the install script and the CI packaging surface with it, and growing the maintenance surface.
- **The cost of inaction**: IDE-only users' `repotory_ask` keeps falling back to keyword matching — that is the real current state, and this decision does not hide it.

## Implementation checklist (implementation notes, not a plan)

| Location | What changes |
|---|---|
| `docs/adr/0063-repotory-server-consumer-list.md` / `docs/en/adr/...` | This ADR in both languages + one row in each index |
| `packaging/README.md` | The `repotory-server` consumer description moves from "manually fetched (deep-link enhancement)" to the classification of this ADR's decision 2 (deep links = enhancement; provider configuration = substantive gap) |
| IDE READMEs (vscode / jetbrains) | "Which capabilities need the local service" phrased per this ADR's list (updated in the same batch as the chosen option) |
| TODO.md 5.6 | Annotated that this ADR has landed and the channel decision awaits the owner |

**After the option is chosen** (not this round): Option A → install script / tap / bucket and the CI packaging surface; Option B → MCP tools + both panels' provider forms + `CoreVersion` minor + skill signatures + documentation.

## Regression

This ADR has no code change: the `docs-mirror` gate (both languages in one batch) and `check-version-drift`'s ADR-index alignment check suffice. The chosen option's implementation regresses under its own gates.

## Implementation record (2026-11-16, A-light shipped)

**A-light has landed per step one of decision 4** (the owner picked A, one-click start first):

1. **`ide/vscode/src/serverLauncher.ts` (new file)**: a port of the JetBrains `RepotoryServerManager` pattern — ① binary resolution order `repotory.serverBinary` → `REPOTORY_SERVER_BIN` → PATH → common install directories (`cinder-server` first, the `repotory-server` compat name after); a configured-but-missing path is **skipped**, not reported as an error; ② `start()` probes `/health` first (**never fights a running instance**), accepts loopback addresses only, builds `--port` from the URL (default 18765), and adds `--web-dist` automatically when a `web/` sits next to the binary; ③ the health wait is 30 s / 250 ms polls, distinguishing "exited" (log tail included; `address already in use` named separately) from "alive but silent" (`healthy=false`, never reported as a working service); ④ `stopOwned()` kills only the process it started, cleaned up at `deactivate`. spawn / ping / sleep / timeout / log path are all injectable, so the unit tests need no real process.
2. **Wiring**: new command **`Cinder: Start Local Service`** (command palette); the diagnostics panel's local-service row starts it with one click when the service is unreachable; when the panel's provider sheet cannot read its settings, the announcement bar carries a "Start local service" button (the webview dispatches a `startServer` message by `announce.action.target`, the host runs the command); new setting `repotory.serverBinary`; `serviceOfflineHint` wording points at the one-click start.
3. **Tests**: `ide/vscode/src/__tests__/serverLauncher.test.ts`, 15 cases — resolution order (setting > env > PATH, a missing configured path falls through, null + a search list when nothing exists), command construction (`--port` always, `--web-dist` only with a `web/` next to the binary), loopback and port parsing, the four `start()` branches (already running / non-loopback / binary missing / success), the failure wording for exit and port-in-use, alive-but-silent not reported healthy, and `stopOwned` killing only what it started. `npm test` **69/69 green** (including the existing 54).
4. **Documentation**: `ide/vscode/README.md` requirements and settings table updated (the three entry points for the one-click start, the resolution order, where to fetch the binary).

**A-full (channels) is not done**: pending A-light proving in real use whether "download the server once" friction is acceptable, before deciding on install.sh / Homebrew / Scoop channels. **B stays rejected.**

## Implementation record two (2026-11-16, option B shipped — config writes reclassified as catalogue-covered)

**The owner picked B: configuration writes are reclassified from human-gate to catalogue-covered, and provider configuration goes over MCP.** The recommendation in decision 4 flips to B; A-light stays (the one-click start remains the backfill entry for deep links and diagnostics). What landed:

1. **The writerapi catalogue**: a new `config.update` catalogue method (`ConfigUpdateArgs{Key, Value}`), with the `UpdateConfig` binding mapped to it; the executor calls the **same** `svc.UpdateConfig` (the allow-list and value validation are identical on both faces). `Methods()` gains one entry; `UpdateScanRoots` stays human-gate (granting filesystem scope is a different decision).
2. **The surface registry**: `GetConfig` / `UpdateConfig` move to `SurfaceMCPBacked` (tool names `repotory_config_get` / `repotory_config_update`); `TestBindingSurfaceHumanGateHolds` and `write_contract_test.go`'s audit lists are updated **deliberately** for the reclassification (`UpdateConfig` moves from the refusal list to the forward list).
3. **cmd/mcp**: new `repotory_config_get` (full configuration, secrets masked to `************`) and `repotory_config_update` (through `w.ExecuteInto(MethodConfigUpdate, …)`, i.e. the single-writer contract; the allow-list is named in the tool description). `CoreVersion` 2.1.0 → **2.2.0 (minor)**; the fixture goes 23 → 25 tools.
4. **Both panels**: the provider settings' read (GetConfig) and write (UpdateConfig ×3) move over the MCP tools. Neither MCP client checks `IsError`, so a failed write returns `error: …` text — both sides check that discriminator explicitly and throw (new cases pin it). The probes (`ListAIModels` / `TestAIChatConfigured`) stay on HTTP — they are not config writes and degrade honestly without a service.
5. **Tests**: writerapi gains `TestConfigWriteIsCatalogueCovered` (a client role's `DecideWrite(UpdateConfig)` = WriteForward, `UpdateScanRoots` still WriteRefuse, the forwarded write lands); cmd/mcp gains `TestConfigTools` (read masks, write validates, unknown key rejected); VS Code gains three provider cases (read over MCP, three keys written, rejected text surfaces); JetBrains gains a rejected-write case.
6. **Contract-surface impact**: `UpdateConfig` changes from "refused from a client process" to "forwarded to the writer" — a standalone server running in client role now forwards config writes to whichever process holds the write role (the desktop or a self-elected MCP), instead of needing to hold the lock itself. **The cost, recorded**: agents can now read and write provider configuration (endpoint / model / key) — that is the owner's explicit policy reversal, stated plainly in the tool descriptions; `repotory_config_update` writes are still bounded by the allow-list and value validation, and route through the single-writer contract.

**Revision (2026-11-26): the "deep links" row is void, and decision 5 with it.**
ADR-0044's third revision replaced the card's **Open in Cinder** with asking in place, so deep
links have no caller in either panel and the whole path is deleted (see the ADR-0035 revision).
Following decision 1's maintenance rule, this list is updated in the same batch: the server's
consumers are now `MoveNote` / `BindWorkspaceToProject` / one-click start / diagnostics — **there is
no "enhancement"-class consumer left**. Decision 5 ("deep links stay a best-effort enhancement") no
longer holds: not downgraded, gone. `packaging/README.md`'s channel notes were corrected to match.

The same batch removed "add `--web-dist` when `web/` sits next to the binary" from item 2① of the
landing record: both launchers now pass `--port` and nothing else. That flag exists to let a reader
open the knowledge UI in a browser, and since the deep link went away the panels never link into it
(the JetBrains comment itself said "The plugin itself no longer links into that UI" while still
passing the flag); the only consumer of a service the plugins start is the human-only `MoveNote`, so
there is no reason to hang a second SPA off the loopback port. `--web-dist` stays a capability of the
server itself — anyone who wants the UI passes the flag by hand (`ide/jetbrains/README.md` says how).
