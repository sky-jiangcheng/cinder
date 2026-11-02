# ADR-0009: IDE presence — thin-client distribution strategy (one-command registration → VS Code extension → JetBrains deferred)

- Status: Accepted (M4 `repotory-init` has landed: `scripts/repotory-init/`, 2026-10-02)
- Date: 2026-10-02
- Related: [ADR-0006](0006-scope-freeze.md) (reconciliation clause with the scope freeze), [ADR-0007](0007-session-memory-protocol.md) (MCP as the single AI execution interface), TODO session-memory roadmap (M4-M5)

## Background

Repotory's existing distribution assets — the MCP server, the dsh Harness plugin, llms.txt — all serve agents, none of them "meet a human": MCP tools only appear in an agent's chat panel; in the IDE-native surfaces (command palette, status bar, sidebar), Repotory does not exist. That blind spot is a hard distribution constraint:

1. **Agents do not discover our plugin on their own.** Nothing prompts a new user's AI agent to say "you should install repotory-mcp" — distribution must be done by a human, and humans only discover tools in the interfaces they open every day.
2. **Word of mouth needs a visible shelf.** "What do you use to manage project memory?" — if the answer is a panel or a command in the IDE, the conversation has a landing point; if the answer is "an MCP server that goes invisible after install", the referral chain breaks at step one.

Capability is not the gap: both VS Code (Copilot Chat) and JetBrains (AI Assistant / built-in MCP client) natively support MCP registration, and `docs/features/ai-integration.md` already documents the manual path. What is missing is **onboarding friction** (hand-editing JSON) and **the human's in-IDE experience** (zero native UI). Roadmap item M4 (`npx repotory-init`, one-command registration) is approved but unbuilt — it is the first-priority fix for exactly this gap.

This must also reconcile with ADR-0006 head-on: the scope freeze froze "platform infrastructure" (plugin SPI, HTTP server, multi-backend storage), not "human-visible distribution surfaces". This ADR uses a thin-client discipline to ensure the new distribution assets do not restart platformization.

## Decision

1. **Thin-client discipline**: MCP stays the single **AI** execution interface (ADR-0007; the qualifier "AI" is pinned down by [ADR-0035](0035-jetbrains-mcp-primary-channel.md) decision 1 — a human-facing UI calls the **binding surface**, not the execution interface), `internal/service` stays the single source of truth. Every new IDE asset (init script, extension, plugin) is a thin shell — it calls the shared service layer via headless HTTP or MCP stdio, with zero logic duplication (the same pattern as `dsh-plugin-repotory`). Proposals that violate this discipline do not enter the roadmap by default.
2. **Step one: M4 `repotory-init`**: one command completes MCP registration — writes `.mcp.json` / client config, detects the repotory-mcp binary, and offers the optional SessionEnd hook setup — covering every MCP client in both the VS Code and JetBrains IDE families. Pure script + docs, no new infrastructure, no conflict with ADR-0006.
3. **Step two: a VS Code extension (the only IDE extension)**: three command-palette commands (context / handoff / search), a status-bar item showing the last handoff time, a sidebar for note search with jump-to-file, and first-run guidance that registers the MCP server in one click. One VSIX covers the whole fork family (VS Code / Cursor / Windsurf) — the only form where one investment meets the entire family.
4. **JetBrains plugin deferred**: an independent Kotlin/Gradle codebase with a doubled maintenance surface; it stays un-chartered and codeless until a real demand signal (issues/stars) arrives and a follow-up ADR argues the case.
5. **Distribution principle (new evaluation gate)**: from now on, every distribution asset must answer "in which interface does a human see it"; assets only agents can consume need to state whether they serve retention of existing users rather than acquisition.

## Rationale

- The distribution limit is a product-level constraint, not a marketing problem: agents do not install plugins for us, so visibility must be bought by the product itself; extension marketplaces (VS Code Marketplace / JetBrains Marketplace) are the only ready-made shelves developers actually browse.
- VS Code first is arithmetic, not preference: one VSIX covers the fork family (VS Code / Cursor / Windsurf); a JetBrains plugin is one build for one vendor and needs a separate stack. Do the one with the larger coverage first.
- M4 comes first because it removes the same friction for everyone (including terminal users who never install the extension) at script-level cost; the extension's UI investment builds on top of it.
- The thin-client discipline keeps ADR-0006's maintenance-cost concern bounded: IDE extensions evolve only with the external API surface (headless HTTP endpoints / MCP toolset), never with internal implementation.

## Consequences

- Positive: the product gains visible presence inside the IDE (command palette / status bar / sidebar); new-user onboarding drops from hand-edited config to a single command; one VSIX investment covers the whole VS Code family; the marketplace listing becomes a landable destination for word of mouth.
- Negative: +1 TypeScript extension codebase (a thin shell whose maintenance surface is limited to the API layer); JetBrains users remain on manual config during the extension phase.
- Open: the JetBrains demand-signal threshold (order of magnitude of issues/stars) is set after the VS Code extension ships, from real conversion data; the extension's lifecycle handling of the headless server (auto-start / reuse) follows the dsh plugin's practice and is documented at implementation time.

## Amendment record (2026-11-02, [ADR-0036](0036-ide-chat-console.md))

The decisions in this ADR are kept as written. ADR-0036 **amends the FORM of the sidebar in decision 3**, not its existence:

- **Decision 3's "sidebar note search" becomes "sidebar chat console"**. The panel is no longer a searcher ("a box + a thumbnail list + a detail pane") but a conversation feed: one question and its answer is a turn, the old turns fold away, and details open in place inside the turn that produced them (ADR-0036 decisions 1 / 3 / 7). **The sidebar stays where it is; what it holds changes.**
- **The panel can answer for the first time**: `repotory_ask` (ADR-0033) is wired, and the answer carries its source list. The retrieval surface existed long ago; the answering surface arrives with this round — and "being able to answer questions" is exactly what makes a memory readable as a sequence.
- **The scope goes from global to "locked to the current project" by default**: the chip shows `🔒 <project>` and clicking it unlocks back to cross-project (ADR-0036 decision 2). The lock is a default, not a restriction.
- **The command palette commands stay**, but `Search Knowledge` / `Search Approved Knowledge Pages` now **append a turn to the conversation** instead of replacing the result, and a new `Ask the Knowledge Base` command is added.
