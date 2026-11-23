# ADR-0067: The install-all CLI — init / uninstall hosted on cinder-capture

- **Status**: Accepted (2026-11-23)
- **Date**: 2026-11-23
- **Related**: [ADR-0065](0065-hindsight-absorb-not-replace.md) (the Hindsight absorption route, second P0 item), [ADR-0066](0066-session-start-brief-injection.md) (the SessionStart brief, the other half of the loop), [ADR-0009](0009-ide-presence.md) (MCP thin-client wiring), [ADR-0010](0010-session-auto-capture.md) (capture default-off), [ADR-0043](0043-handoff-instructions-in-agent-instruction-files.md) (the managed instruction block), [ADR-0054](0054-rolling-checkpoint-handoff.md) (the SessionEnd two-tier fallback)

## Context

The second P0 item on the ADR-0065 gap list: Hindsight's `install all` wires every harness with one command, and `uninstall all` removes exactly that. Cinder's wiring capability already existed (`internal/agentinit`: four clients + both hooks + the managed instruction block, pinned by the Go/mjs contract twins), but it had only two entry points: the desktop settings button, and `scripts/repotory-init/index.mjs`, which needs node. A terminal user had no "install the binary, run one command" path; uninstall did not exist at all — removing what was installed meant deleting files by hand against the docs, with luck deciding whether too much or too little went.

## Decision

1. **Host: `cinder-capture init` / `cinder-capture uninstall` subcommands.** capture is the companion binary every agent-side install already carries (install.sh / brew / scoop all ship it, with the `repotory-capture` compat symlink), so hosting here adds **zero new release assets**. Three rejected alternatives: a new `cinder-init` binary (one more release pipeline for a solo maintainer, against the repo-slimming stance); `cinder-mcp init` (the stdio server's main would need restructuring, and a server identity should not double as an installer); mjs-only (the node dependency breaks the single-file zero-dependency promise).
2. **`init --all` = every client + both hooks + the instruction block; bare `init` = client registration only** (matching the mjs default). The subcommands dispatch before the capture flag set and **never open the database**: installing the hooks does not enable transcript reading — `claude_session_capture` stays default-off (ADR-0010 decisions 1/2 untouched). Go's flag package accepts both `-all` and `--all`, so both spellings work across the twins.
3. **Precise uninstall semantics (`agentinit.Unregister`, mirrored clause by clause by the mjs `--uninstall`)**: remove only what we wrote — the `repotory` entry from each client config (every other server stays), the hook entries pointing at our two scripts from `settings.json` (every other hook stays; an emptied event key and an empty hooks map are pruned so the file returns to its pre-install shape), the hook scripts (**only while their bytes still equal the installed constant**; a user-modified script is left in place with a warning — it is the user's edit, not ours to delete), and the managed instruction block (every byte outside the markers survives; a file that held only the block is deleted, returning to the pre-install "no file" state). Every overwrite backs up first (`.repotory-init.bak`); a missing file/entry is unchanged, so a second uninstall is a silent no-op.
4. **The contract twins extend to the post-uninstall state.** A new fixture, `testdata/contract-uninstall.json`: JSON compared by meaning, scripts/instruction files as exact text, `null` meaning "must not exist". Both Go's `TestContractFixture` and `check-agent-init-contract.mjs` compare the install output first, then run the uninstall and compare the residue — "installs identically" and "uninstalls identically" are two halves of one promise.
5. **The mjs keeps its own `--dry-run`** (the Go CLI has no such flag: agentinit has no dry-run mode, and none is invented for symmetry); under dry-run the mjs uninstall path prints would-remove and deletes nothing.
6. **No MCP tool surface change, no schema change, no desktop button change.** CoreVersion unchanged; the desktop settings page goes through the same `agentinit` package, so its behavior agrees by construction.

## Costs (accepted)

- Two equivalent terminal entry points to document (`cinder-capture init` and `node index.mjs`) — the contract fixture guarantees identical output, but the docs must teach both.
- The Windsurf config lives in the global home directory, so `uninstall` touches it by default (symmetric with install); `--client` narrows the scope.
- Backups are not auto-cleaned: `.repotory-init.bak` files stay after uninstall — the price of "a user file is never lost".
- The Go CLI has no interactive confirmation (there is no prompt to skip), so it does not offer the mjs's `--yes`; a mistaken install is precisely reversible via `uninstall` — reversibility is guaranteed by the uninstall, not by a confirmation dialog.

## Reversal triggers

- Brand consolidation produces a unified multi-purpose `cinder` CLI → the subcommands move there, capture keeps compat aliases.
- The client set outgrows the four-client contract (a Hindsight-style 22-harness world) → revisit the "one config file per client" wiring model instead of piling more into the fixture.

## Landing record (2026-11-23)

- Code: `internal/agentinit/unregister.go` (+6 tests: removes only ours, idempotent, modified script left in place, foreign hook preserved, never-installed dir no-op, invalid JSON reported not overwritten); `cmd/repotory-capture/init.go` subcommands (+4 tests: init --all never touches HOME/the database, uninstall round-trip idempotent, bad client exit 2, missing binary exit 1 with guidance); mjs `--uninstall` twin + VSIX sync; the `contract-uninstall.json` fixture + both contract checks extended.
- Verification: `go test ./internal/agentinit ./cmd/repotory-capture` green; `check-agent-init-contract.mjs` passes both the install-state and uninstall-state comparisons; a real-binary smoke in a temp dir: `init -all` writes all six files, `uninstall` removes exactly them with backups, a second uninstall reports unchanged everywhere.
- Docs: `getting-started.md`, `features/ai-integration.md` synced bilingually.