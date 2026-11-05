# ADR-0048: The memory layer's contract gets its own version number — core version and release version move separately

- Status: Accepted (2026-11-05, the user picked option (a) — "do the two zero-cost things first" — among the four options offered for "split core into a third product")
- Date: 2026-11-05
- Related: [ADR-0047](0047-resident-agent-separate-host.md) (two products, one job each; its addendum settled "self-evident at runtime, warn in plain words" — this ADR defines what that "version" is), [ADR-0035](0035-jetbrains-mcp-primary-channel.md) (MCP as the primary channel), [ADR-0032](0032-approved-wiki-page-visibility.md) / [ADR-0033](0033-mcp-ask-evidence-answer.md) (the two changes that put the knowledge-page tools on the MCP surface), [ADR-0044](0044-ide-panel-windsurf-shape.md) (the IDE panels consume the same outward interface)

## Background

### One version number was answering two different questions

`internal/version/version.go` holds a single `const Version = "2.30.0"` (the SSOT lives in `wails.json`'s `info.productVersion`; `scripts/bump-version.sh` propagates it). It sits in two places with completely different meanings:

- **"Which build is this"** — the release version. The desktop app ships a new one every few days; the updater (`internal/service/update.go`) compares it against the latest release on GitHub, and the install-info path (`internal/service/install_info.go:47`) writes it into the local record.
- **"What may a client rely on"** — the contract version. `cmd/mcp/main.go:37`'s `server.NewMCPServer("repotory-mcp", version.Version)` reports this number in the MCP `initialize` response as `serverInfo.version`, and **it is supposed to answer "is the surface I connected to still one I can talk to"**.

Packing both numbers into one variable does not make things "less pretty" — it makes **both of them meaningless**: a client that reads 2.30.0 cannot tell whether the tool surface changed.

### Fact one: the contract surface rarely moves, the build surface moves constantly

The recent string of desktop changes (ADR-0038's home workbench, ADR-0039's tabs, ADR-0041's draggable bot, ADR-0042's page shell) is **all front-end layout** — not one of the 18 MCP tools changed. The other way round: when `repotory_wiki_search` / `repotory_wiki_read` joined the MCP surface (ADR-0032 / ADR-0033), the desktop app's interface barely changed.

**So reading "did the contract change" out of a version number is structural, not a coincidence.**

### Fact two: the client used to throw the whole `serverInfo` away

The separate repository `repotory-agent` binds nothing in `src-tauri/src/mcp_client.rs` — it only `?`s the `initialize` response (ADR-0047's addendum, verbatim: "the server was reporting it all along; the client threw the whole response away"). Neither IDE plugin reads it either (a repo-wide grep for `serverInfo` hits only the ADRs and one fake response in a JetBrains test).

**So no production code depends on the meaning of this number today** — changing its semantics now costs zero migration. **That window is temporary**: once a client really starts reading it, the semantics can no longer be changed at will.

### Fact three: the release version stopped being a build number a long time ago

`Version = "2.30.0"` has the shape of semver but the job of a "release sequence number" — the desktop app shipped 30 versions in a month, many of them internal chores (`fix(test)` / `chore(packaging)` / `docs(changelog)`). That is not a problem in itself; the problem is only that **nobody can read "did the contract change" out of a 2.30.0**.

### Fact four: `Version` is a `const`, so the release pipeline's ldflags line is a no-op

`.github/workflows/release.yml` passes `-X repotory/internal/version.Version=$APP_VERSION`, but `-X` can only rewrite a `var` — it cannot touch a `const`, so **that argument has always been a no-op** and the version number has always come from the literal in `version.go`.

This is not fixed here (it is unrelated to this ADR), but it shows that **the release number already has exactly one source of truth** — so the new contract number must also have exactly one (the two copies are held in check by CI, see decision ⑥).

## Decisions

**Seven decisions**:

① **The contract version is a separate number** — a new `const CoreVersion = "1.0.0"` in `internal/version/core.go`. It and `Version` **move separately**: the release version moves at the release cadence (`bump-version.sh` + `wails.json`), the contract version moves **with the contract surface**.

② **The semantics follow contract changes, not releases** — `major` = breaking (a tool removed or renamed, a field's meaning changed); `minor` = additive (a new tool, a new optional field, a new endpoint); `patch` = the contract did not change. **An interface change does not bump it; a tool-surface change must.**

③ **MCP's `serverInfo.version` reports the contract version** — `cmd/mcp/main.go` becomes `server.NewMCPServer("repotory-mcp", version.CoreVersion)`. The reason: whoever asks "what may I rely on" wants the contract number; the release number is available in the startup log and on `/health`. MCP's `serverInfo` has exactly two fields, `name` and `version` — there is no second place to put it.

④ **`/health` gains `core_version`, and `version` does not change at all** — `version` stays the release version because **the updater and the install-info path depend on it** (`update.go` compares it against the latest release), and changing it would change the meaning of an upgrade; `core_version` is a **new key**, and old clients ignore unknown keys.

⑤ **The contract version is pinned next to the tool list** — `cmd/mcp/main_test.go`'s `TestToolsRegistered` pins a literal (`wantCoreVersion`). **When a tool is added or removed, that line has to change with it** — this is the physical form of the rule "the version moves with the tool surface": it turns "I forgot to bump the version" into a red build rather than a latent hazard.

⑥ **The two copies may not drift** — `scripts/check-version-drift.mjs` gains an **eighth check**: the `CoreVersion` in `internal/version/core.go` must equal the one pinned in the test. **The negative case was verified**: temporarily changing the test's copy to `9.9.9` turns the script red and exits 1 (restored to `1.0.0` afterwards; a re-run is green).

⑦ **No schema change / no new MCP tool / no change to any tool's arguments or results / no change to the release process** — the contract version **describes** the contract, it is not the contract.

## Costs (accepted)

- **One more version number means one more place to update** — whoever adds or removes a tool and forgets `wantCoreVersion` turns the eighth check red. That is **deliberate**: it turns "remember to update the contract version" from a memory burden into a gate nobody can walk around.
- **The two numbers can now disagree** — a user sees "memory layer v1.0.0" while the desktop app is v2.30.0, and asks which one is newer. The answer has to be on the surface: **the contract number answers "did the surface change", the release number answers "is the build new"** — comparing the two means nothing.
- **`serverInfo.version` changes meaning** — no production code depends on it today (fact two), but **a client that depends on it from now on reads the contract number**. If somebody ever needs the release number over the stdio channel, MCP's `serverInfo` has no second field to hold it and another route would be needed (a tool argument, or `instructions`) — **that trade-off belongs to that day**.
- **`/health` gains a key** — old clients ignore unknown keys, which is harmless; but **any code that deserializes the whole `/health` response into a fixed struct** needs to tolerate it (no such code exists in this repo or in the two IDE plugins — grepped).
- **`Version` is still a `const` and the release pipeline's `-X` is still a no-op** — not fixed here (unrelated to the contract version), but fact four still holds: the release number has one source of truth too.

## What landed

| Where | What changed |
|---|---|
| `internal/version/core.go` | New `CoreVersion = "1.0.0"` (with the semantics spelled out) |
| `cmd/mcp/main.go` | `serverInfo.version` now reports `version.CoreVersion`; the startup log prints both the release version and the contract version |
| `internal/service/service.go` | `Health()` gains `core_version`; `version` unchanged |
| `cmd/mcp/main_test.go` | `TestToolsRegistered` pins `wantCoreVersion = "1.0.0"` |
| `main.go` / `cmd/server/main.go` | The startup log prints both the release version and the contract version |
| `scripts/check-version-drift.mjs` | A new eighth check (the two copies of the contract version must be equal) |
| `docs/api/reference.md` / `docs/en/api/reference.md` | The two version keys on `/health` say what each one answers |

## Regression

`go build ./...` / `go vet ./...` pass; `go test ./cmd/mcp/... ./internal/service/... ./internal/httpapi/...` all green (the existing `/health` assertion only looks for `"status":"ok"`, the new key does not affect it); `node scripts/check-version-drift.mjs` green; **the eighth check's negative case was verified** (changing the copy to `9.9.9` turns it red).
