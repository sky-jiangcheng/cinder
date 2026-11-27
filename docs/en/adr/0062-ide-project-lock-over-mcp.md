# 0062 The IDE panel's project lock goes over MCP — workspace resolution joins the MCP tool surface, so the lock no longer needs the local service

- Status: **Accepted (drafted and settled 2026-11-15, implemented the same day)**
- Related: [ADR-0035](0035-jetbrains-mcp-primary-channel.md) (MCP primary channel + HTTP backfill — this ADR closes a gap in its decision 2), [ADR-0036](0036-ide-chat-console.md) (the project lock's UI and degradation wording), [ADR-0044](0044-ide-panel-windsurf-shape.md) (project recognition and `BindWorkspaceToProject`), [ADR-0048](0048-core-contract-version.md) (contract-version semantics), [ADR-0059](0059-three-surface-behavior-parity.md) (the three-surface behaviour-parity gate)

## Background

### The symptom: a banner that misstates the panel's actual state

When either IDE panel opens, `refreshScope()` tries to resolve the current workspace folder into a project and lock the panel to it. The resolution goes through the read-only HTTP-bridge binding `ResolveWorkspacePath` (VS Code [chatSession.ts:150-188](../../../ide/vscode/src/chatSession.ts#L150-L188), JetBrains [ChatSession.kt:109-147](../../../ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/ChatSession.kt#L109-L147)). **Whenever that HTTP call throws (connection refused / timeout), the panel posts**:

> The local Cinder service is not reachable at http://127.0.0.1:18765 — the project lock needs it, so the panel searches across all projects.

Facing two facts, this sentence misstates the situation:

1. **The panel is not broken.** Since ADR-0035, search, read, write, create, organize and the project list all go over MCP, not over 18765 — with no service up, the panel's eight data capabilities work as usual and only the project lock is lost. The wording describes "the panel is degraded" as if it were "the panel is unusable".
2. **That service is not there for most users, by design of the distribution.** What listens on 18765 is `repotory-server` (`cmd/server`); the desktop app is a Wails process that listens on no port (ADR-0001 decision M1), `scripts/install.sh` installs `repotory` + `repotory-mcp` and **not `repotory-server`**, and `repotory-mcp` has three distribution channels (install.sh / Homebrew / Scoop) while `repotory-server` has **none** (ADR-0035 leftover ①). In other words, after a user installs from the official channels, the project lock is the **only** capability that necessarily depends on a binary nobody installed.

And the banner **does not clear itself**: the webview only empties it on ✕ ([chatConsoleHtml.ts:1197-1199](../../../ide/vscode/src/chatConsoleHtml.ts#L1197-L1199)), and `refreshScope` runs only on activation, panel readiness and workspace-folder changes — a service started later leaves the banner up until the user dismisses it or switches workspaces.

### Fact 1: keeping the project lock on HTTP was an ADR-0035 choice, but the problem it was answering was not the transport layer

ADR-0035 decision 2 fixed "eight to MCP, two to HTTP": `MoveNote` stays HTTP because "re-filing knowledge is a human decision an agent must not make"; the other HTTP keeper is "service-existence / web UI probing, deep links". `ResolveWorkspacePath`, as the project lock's transport, stayed on the HTTP side along with the latter — **what it lacked was not an argument, it was an MCP twin**. ADR-0035 decision 8's boundary was "add no new MCP tools", so the dependency point was inherited as-is.

### Fact 2: the `SurfaceDesktopOnly` annotation objects to a second matcher, not to a second transport

`internal/app/surface.go:258` annotates the binding:

```
"ResolveWorkspacePath": {SurfaceDesktopOnly, "", "agents pass `path` to repotory_context instead; a second client-side matcher is the disagreement this avoids"},
```

The comment in [bindings.go:973-980](../../../internal/app/bindings.go#L973-L980) gives the same-source reason: the matching rules must live server-side, and a client-side copy of "which project owns this directory" is precisely how an agent and the sidebar end up disagreeing about what knowledge a session gets.

**That reason objects to "the client (inside the plugin) copying the matching logic", not to "calling the same server-side resolution over MCP".** MCP tools and the HTTP binding hit the same service layer (ADR-0035 fact 5); `repotory-mcp` calls `svc.ResolveProjectByPath` in-process — the matching rules stay server-side, and the panel still receives the same `ProjectResolution`. **Calling it over MCP does not create a second matcher**: it is the same function, the same struct, the same rules as the HTTP call today.

### Fact 3: a human-side panel calling the binding surface over MCP is already authorized by ADR-0035 — it is not a new interface

ADR-0035 decision 1 pinned the terminology: "MCP is the only execution interface" is always read as "MCP is the only **AI** execution interface"; **a human-side interface calls the binding surface, and both channels are legal**. The two IDE panels are human-side clients — their eight data capabilities are already called over MCP today. Adding a read-only MCP resolution tool for the panel is an extension of an already-authorized shape, not a violation of any existing decision.

### Fact 4: agents do not need this tool — which is exactly why it can be exposed safely

`repotory_context`'s `path` argument already performs the same resolution server-side (ADR-0033 decision ④: `path` is resolved by the same matcher on the server). The new tool changes nothing about `repotory_context`; it exposes the same **read-only** resolution to the human-side client that needs to **display** the result. It is a pure read with no write and no side effect — even if a model calls it out of curiosity, it touches no write surface and no gate (ADR-0015's write-side gate is unrelated to it). The real concern behind the surface.go objection (an agent treating it as a second entry point and diverging from `repotory_context`'s resolution) is held at bay with **tool description + contract tests**, not by refusing to expose it.

## Decision

**Seven decisions**:

**Decision 1: add the read-only MCP tool `repotory_workspace_resolve(path)`.**

- Input `path` (absolute directory), length-capped by `bounds.MaxPathLen` — the **same constant from the same package** as the binding, `repotory_context`'s `path`, and `BindWorkspaceToProject`. The three surfaces must not disagree about how long a path they will look at (the binding's comment already states this constraint, [bindings.go:982-986](../../../internal/app/bindings.go#L982-L986)).
- Returns a `ProjectResolution` **field-for-field identical** to the binding's (`Project` / `Repository` / `MatchedOn` / `Candidates`), calling the same `svc.ResolveProjectByPath`.
- **Pure read, no write, no side effect.** The tool description must state that agents do not normally need it — `repotory_context`'s `path` argument already includes the same resolution; it exists for the human-side client (the IDE panel's project lock) that needs to **display** "which project this folder belongs to".

**Decision 2: both IDE panels resolve the project lock over this MCP tool; the HTTP binding stays.**

- VS Code `ChatSession.refreshScope` and JetBrains `ChatSession.refreshScope` switch from `transport.rpc("ResolveWorkspacePath", …)` to `repotory_workspace_resolve`. Both sides' `Transport` / MCP facades (VS Code's `callTool`, JetBrains' `RepotoryMcp`) already carry the eight data capabilities; resolution is just the **ninth** capability over the same channel.
- The HTTP binding `ResolveWorkspacePath` is **not deleted**: the desktop app and the web UI still use it. This ADR only makes the panels stop depending on it; removing the binding is a breaking change and is out of scope. In `internal/app/surface.go`, the entry's annotation **changes to `SurfaceMCPBacked, "repotory_workspace_resolve"`** — the surface table's meaning is "this binding's agent reachability", and once a MCP twin exists the annotation must follow, with the reason column updated.

**Decision 3: the project lock's degradation semantics stay; the banner wording must change.**

- The three states — resolved / no project / candidates — and their wording are **unchanged** (ADR-0036, ADR-0044 decision 6 keep the status quo).
- The trigger changes: **the banner appears only when the MCP channel itself is unreachable**, and the wording truthfully points at the MCP channel (e.g. "the MCP channel is not reachable — the project lock needs it…"), **never "the local Cinder service"** — the project lock no longer needs the local service. Manual dismissal and the `refreshScope` re-run triggers (activation / panel readiness / workspace-folder change) stay as they are.

**Decision 4: `MoveNote`, `BindWorkspaceToProject` and deep links stay on HTTP — this ADR touches none of them.**

- `MoveNote` is human-only and does not enter MCP (ADR-0035 decision 2 and leftover ②; the product policy has not changed).
- `BindWorkspaceToProject` is a write and a human decision (ADR-0044 decision 6) — **the write side of the project lock (filing an unrecognized folder under a project) still needs the service**. This must be stated in the documentation: this ADR removes the project lock's **read-side** dependency, not the write side.
- Deep links can physically only be served by `repotory-server --web-dist` (ADR-0035 decision 6) and stay positioned as a best-effort enhancement.

**Decision 5: contract version minor bump — `CoreVersion` 2.0.0 → 2.1.0.**

- A new tool is additive (ADR-0048 decision ②: minor = a new tool / a new optional field / a new interface). `internal/version/core.go`'s `CoreVersion` and `CoreVersionChange = "minor"` move together, and `cmd/mcp/main_test.go`'s `wantCoreVersion` updates in the same batch (decisions ⑤⑥: the contract version is pinned next to the tool list, and check ⑧ guards the two copies). ADR-0055's envelope semantics are unaffected.
- Documentation sync: the MCP tool catalogue in `docs/features/ai-integration.md` (both languages), plus the shipped skill tool-signature docs (`skill/repotory/SKILL.md`, `skill/repotory/references/mcp-tools.md` — the lesson from ADR-0035's implementation correction 2: the signatures are taken from the actual `tools/list` response, and adding a tool without syncing them is drift).

**Decision 6: same-source contract tests and the three-surface parity gate follow.**

- `TestToolsRegistered` pins the new tool (count +1) and the new `wantCoreVersion`.
- **The new tool's result for the same input must be field-for-field identical to the HTTP binding's** — guarding against the two surfaces drifting. This is ADR-0059's "same source" idea made concrete for this binding: when the resolution rules change, both surfaces must produce the same answer in the same batch of tests.
- ADR-0059's conformance fixture gains a rule: the panel's project lock resolves over MCP (pinning the transport layer of the existing `scope` rule, so no one can silently switch the panel back to HTTP).
- Both sides' session unit tests (VS Code `chatSession.test.ts`, JetBrains `ChatSessionTest.kt`) change their fake transports to return resolution via `callTool`, covering the three states (resolved / no project with candidates / no candidates) and the MCP-unreachable wording.

**Decision 7: no new bindings, no schema change, no change to `repotory_context`, no change to the desktop app or the web UI.**

This ADR does three things only: add one MCP tool, route both panels' resolution through it, and sync the surface annotation and the documentation.

## Consequences (accepted)

- **The tool surface grows by one**: the agent-visible surface gains a tool. Even though the description says agents do not need it, a model may still call it — it is a pure read with no side effect, so the worst case is one redundant call in the context; no write surface or gate is touched.
- **The project lock's dependency moves from `repotory-server` to `repotory-mcp`.** For users who installed from the official channels (repotory-mcp has all three channels) this is a **net improvement** — the project lock goes from "necessarily unavailable" to "available wherever MCP is". In theory a user could have only the server and no MCP, but the panel's eight data capabilities already depend on `repotory-mcp`, so such a panel user effectively does not exist.
- **The project lock's write side still depends on the service**: `BindWorkspaceToProject` (filing an unrecognized folder) remains unavailable without the service, and the panel reports it truthfully. This is kept deliberately (the write is human-only), but it means the "fully functional without a service" boundary still has one corner — the documentation must state this, otherwise users will assume the project lock is entirely service-free.
- **Banner wording and both panels' copy change**, and per repo convention both languages change in the same batch (the `i18n-keys` gate does not cover the IDE panels directly, but the two panels' wording must stay identical, ADR-0036 decision ⑧).
- **A minor contract-version bump**: no production code today strictly validates `CoreVersion` (ADR-0048 fact 2), so there is no migration cost; the cost is one step on the version number.

## Implementation checklist (implementation notes, not a plan)

| Location | What changes |
|---|---|
| `cmd/mcp/` (new file or merged into `tools_context.go`) | Register `repotory_workspace_resolve(path)`, call `svc.ResolveProjectByPath`, validate with `bounds.MaxPathLen`, return via `makeJSONResult` |
| `cmd/mcp/main_test.go` | `TestToolsRegistered` tool list +1; `wantCoreVersion` → `"2.1.0"`; add the "field-for-field identical to the HTTP binding" contract case |
| `internal/version/core.go` | `CoreVersion` → `"2.1.0"`, `CoreVersionChange` → `"minor"` |
| `internal/app/surface.go` | `ResolveWorkspacePath` entry → `SurfaceMCPBacked, "repotory_workspace_resolve"`, reason column updated |
| `ide/vscode/src/chatSession.ts` | `refreshScope` switches to `callTool("repotory_workspace_resolve")`; unreachable wording points at the MCP channel |
| `ide/jetbrains/.../ChatSession.kt` | Same, via the `RepotoryMcp` facade |
| `ide/vscode/src/__tests__/chatSession.test.ts`, `ide/jetbrains/src/test/.../ChatSessionTest.kt` | Fake transports return resolution via `callTool`; add MCP-unreachable wording cases |
| `docs/features/ai-integration.md` / `docs/en/features/ai-integration.md` | MCP tool catalogue gains `repotory_workspace_resolve` |
| `skill/repotory/SKILL.md`, `skill/repotory/references/mcp-tools.md` | Tool signatures synced (taken from the actual `tools/list` response) |
| The "project lock needs the local service" phrasing in `docs/features/` and the IDE READMEs | Reworded to "the project lock runs over MCP; filing under a project still needs the service" (both languages) |

## Regression

`go build ./...` / `go vet ./...`; `go test ./...` green (including `TestToolsRegistered` and the new contract case); `node scripts/check-version-drift.mjs` green (check ⑧'s negative case still holds); `ide/vscode` `npm test` and `ide/jetbrains` `gradle test` green; the `docs-mirror` gate green (this ADR lands in both languages in one batch). Manual check: stop `repotory-server`, keep `repotory-mcp`, open both panels — the project lock resolves and the banner stays away.

## Implementation record (2026-11-15, shipped the same day)

No decision changed; everything landed as written above:

1. **`cmd/mcp/tools_context.go`**: `repotory_workspace_resolve(path*)` registered in `registerContextTools`, calling `svc.ResolveProjectByPath`, validated with `bounds.MaxPathLen` plus a blank-path rejection, returned via `makeJSONResult`.
2. **`internal/version/core.go`**: `CoreVersion` 2.0.0 → **2.1.0**, `CoreVersionChange = "minor"`; `cmd/mcp/main_test.go`'s `wantCoreVersion` updated in the same batch.
3. **`cmd/mcp/main_test.go`**: tool list +1; new `TestWorkspaceResolveMatchesBinding` — for five paths (repository-exact / repository-ancestor / project-exact / project-ancestor / unresolved) it asserts the MCP tool's answer is **byte-for-byte equal** to `MarshalIndent` of `svc.ResolveProjectByPath`, and pins the over-limit / missing / blank argument rejections.
4. **`internal/app/surface.go`**: the `ResolveWorkspacePath` entry moves to `SurfaceMCPBacked, "repotory_workspace_resolve"`, reason column updated.
5. **Both panels**: VS Code `chatSession.refreshScope` switches to `callTool("repotory_workspace_resolve")` plus a new `parseWorkspaceResolution` (headless.ts; non-JSON throws); JetBrains goes through the `RepotoryMcp` facade and `McpPayloads.parseWorkspaceResolution` (new `WorkspaceResolution` data class). The unreachable wording is unified to "The Cinder MCP channel is not reachable — the project lock needs it…".
6. **Tests**: both sides' session unit tests' fake transports synthesise the resolution via `callTool`; each side gains an "MCP channel unreachable" wording case and a transport-level assertion that the lock resolves through the MCP tool. The JetBrains fake's synthesized JSON once carried a trailing comma (`null,}`) that broke parsing — fixed; that is the discipline "unparseable must error" verified in the field.
7. **Documentation**: the MCP tool catalogue and the "Workspace path resolution" paragraph in `docs/features/ai-integration.md` (both languages), `skill/repotory/SKILL.md` and `references/mcp-tools.md` (22 → 23 tools), the "project lock needs the local service" phrasing in both IDE READMEs, and `scripts/mcp-tools.fixture.json` (+1).
8. **Regression, actually run**: `go test ./...` green; `gradle test` 97 cases green and `gradle buildPlugin` passed; `npm test` 54 cases green and e2e gate C (real binaries) 3/3; `check-version-drift` (23 tools / contract 2.1.0 / doc counts) and `check-docs-mirror` green.

**One environment fact, for the record**: this machine's `~/.gradle` native cache is corrupted (`libnative-platform.dylib` fails to load); the build works around it with `GRADLE_USER_HOME=/tmp/gradle-home` and Gradle 8.10.2 (the CI version), and the Go module-cache writes under `~/go` are blocked by the sandbox, so builds use `GOCACHE`/`GOMODCACHE` under `/tmp`. Both are environment issues, unrelated to this ADR.
