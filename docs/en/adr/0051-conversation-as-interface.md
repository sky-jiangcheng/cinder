# ADR-0051: Conversation as the interface — Function Calling tool surface as the unified operation layer, doing UI subtraction

Status: **Accepted (2026-11-06, the user settled on "strongest user experience, cleanest UI")**
Date: 2026-11-06
Related: [ADR-0036](0036-ide-chat-console.md) (IDE chat console: thin client for shared conversations), [ADR-0041](0041-draggable-agent-bot-and-shared-dashboard-scope.md) (draggable robot and shared dashboard scope), [ADR-0045](0045-resident-desktop-bot.md) (robot panel decisions), [ADR-0047](0047-resident-agent-separate-host.md) (two products, one job each: desktop workbench vs assistant), [ADR-0048](0048-core-contract-version.md) (contract version: this ADR does not bump the contract, does not change MCP tool names), [ADR-0009](0009-ide-presence.md) (thin client discipline)
Starting schema: unchanged (this ADR does not change the schema, the MCP tool surface, or `CoreVersion`)

## Background

### The trigger

The user proposed: **"Cinder assistant and Warden both use Cinder's Function Calling capability to simplify interface design, and the conversation interfaces of the various IDE plugins can also be simplified."**
That is: make conversation (backed by the Function Calling tool surface) the unified entry point for operations and data, and let the UI keep only what conversation cannot carry.

### Fact one: Cinder desktop already has a complete Function Calling loop, but each of the two surfaces is missing half

The desktop core already has an FC tool registry (`internal/service/aitools.go`, registered once at `service.go` construction, streaming `tool_call` / `tool_result` events back to the frontend over Wails events). The 8 tools are all dashboard-statistics-oriented (`query_daily_stats` / `list_projects` / `get_project_stats` / `query_project_stats` / `get_recent_commits` / `get_heatmap` / `get_project_overview` / `search_notes`); consumers are the floating robot `AgentBot` and the home-page knowledge assistant `HomeKnowledgeAssistant`; but the desktop registry has **no** memory/retrieval tools, while the MCP tool surface (20 tools) lacks the desktop's disk-computed statistics (heatmap, recent commits, etc.) — each surface is missing half, so "Cinder's Function Calling capability" is not yet a cross-end capability.

### Fact two: Warden's model request has no tools at all

Warden's LLM request body is only `{model, messages, stream}` (`llm.rs`): no `tools`, no system prompt, and a model-returned `tool_calls` is silently dropped; `conversations.rs` `context()` explicitly rejects system roles. Rust already has a **frontend-never-called** generic command `mcp_call_tool` (`lib.rs`) that can serve as the FC execution backend. Warden therefore grew a batch of read-only display panels (quant panel, etc.) and operation entry points — essentially compensation for "conversation cannot act".

### Fact three: both IDE plugins are thin shells with zero LLM code; but their panels carry plenty of chrome

The VS Code and JetBrains plugins are thin shells over repotory-mcp (`repotory_chat` for shared conversations, `repotory_ask` for Q&A), with zero LLM code; yet their panels still carry the provider sheet / ProviderDialog, the inline note editor, a search button duplicating Notes mode, a Copy button, a VS Code status-bar handoff timer, and a JetBrains Rename button — chrome that conversation itself cannot carry, occupying the most visible real estate.

## Decisions

### D1 Surface alignment: MCP tool surface is the cross-end source of truth; desktop registry adds memory/retrieval

The MCP tool surface (`repotory_*`, `tools/list` dynamically pulled, schema from the server) is the cross-end source of truth — **not copied, not modified, not duplicated**. The desktop-internal FC registry adds memory/retrieval tools (`notes_search` / `wiki_search` / `ask` mapped onto the same service layer) so the Cinder assistant can answer knowledge questions instead of only statistics. The alignment of the two surfaces is asserted by the `check-version-drift` gate to prevent drift. Schema, MCP tool names, and contract version unchanged (ADR-0048).

### D2 Warden: Rust-side FC loop with a read-only whitelist (the biggest addition in this ADR)

Orchestration point in `conversations::send`; at connect time, filter `tools/list` down to a read-only whitelist (`repotory_metrics` / `repotory_context` / `repotory_projects_list` / `repotory_projects_stats` / `repotory_notes_search` / `repotory_notes_read` / `repotory_wiki_search` / `repotory_wiki_read`) and pass it to the model request in OpenAI-compatible format; if the model names a tool outside the whitelist (including write tools the server advertises), the **client refuses** and feeds back a refusal as the tool result — never executes. `repotory_chat` is entirely outside the model's tool surface — persisting replies is Warden's own job, and a model calling `append` would double-write. Round cap 5 (same as the desktop `maxToolRounds`); beyond the cap, force one final text-only round, never an empty answer. `tool_call` / `tool_result` are pushed to the frontend as Tauri `Channel` events, and the frontend renders cards by tool name; tool events live only in the UI layer — **not written to `repotory_chat`** (they are a UI-layer process, not a conversation artifact). A conversation without Cinder carries no tools and no system prompt; the single-turn contract is unchanged.

The system prompt is assembled locally (tool-use rules + current repository path — this lands roadmap M2's "wire repository context into the model request"); no system role injected by the server is accepted (the `context()` refusal of system roles is retained, changed to accept only this layer's own assembled system).

Write capability (`notes_create/update`, `repotory_chat` write operations, `handoff`) stays out of the whitelist: reply persistence is done by Warden itself, and a model that could `append` would double-write the very conversation it is answering. Write capability comes later, behind Phase 3's human-in-the-loop mechanism (confirmation card, commands fully visible, execution logged).

### D3 Cinder assistant (desktop): generic tool cards replace per-page hand-written cards

The desktop already has the FC loop; add a generic per-tool-name card renderer and progressively retire the per-page hand-written shortcut cards built in `agentContext.ts`. **The local instant cards shown when no AI is configured are kept** — offline availability is an explicit AgentBot feature, not redundancy: no AI → local cards; AI configured → FC loop; same card shape either way.

### D4 IDE plugins: pure subtraction (thin shells do not pull in an LLM)

Delete: provider sheet / ProviderDialog, the in-panel diagnostics button, the search button duplicating Notes mode, the Copy button on cards, the VS Code status-bar handoff timer, the JetBrains panel Rename button (aligned with VS Code; the rename capability itself is not removed, just not surfaced in the panel).
Keep: input box + Send (the only write path), the message feed (history is the product), the minimal scope indicator (Ask/Pages are server-filtered, Notes is client-filtered — without it the answer's semantics are unclear), one announcement/error channel, the Sources collapsible list (evidence visibility is an explicit decision), and the thread dropdown.
Also fix four cross-end inconsistencies: the `open -a` application name on both sides, the shared-Q&A scope semantics (both pass the thread path), and the 2.5s vs 3s polling wording.

The inline editor is **not retired this round**: retiring it requires the server-side `repotory_ask` to be upgraded with an optional tool loop (shell keeps zero LLM code), left as a follow-up project. When it happens, "instruction in chat + server-side proposal + confirm card" replaces it, with the ADR-0015 save gate narrowed to a single confirmation, not removed.

### D5 Not substitutable (written into gate comments)

Conversation input and the message feed, secret entry (API key goes only into the system keychain, never back to the UI), approval actions (always done in Cinder Desktop), system-level interaction (tray/window/drag/notifications), the ADR-0015 save gate, and the ADR-0036 Sources evidence visibility.

### What this ADR explicitly does not do

- Do not turn Warden into a client calling Cinder's server-side AI: Warden's model requests go through the user's own provider (the one Rust client); Function Calling serves the server's tool surface to that model as tools.
- Do not turn IDE plugins into fat clients with an LLM: plugins keep zero LLM code; plugin-side "instructions in conversation" are served by the optional tool loop on server-side `repotory_ask` (a later project).
- Do not touch the desktop workbench's browse/edit/approve main path — that is the desktop's product identity (workbench vs assistant); this ADR only touches the assistant panel itself.

## Cost

- Warden's Rust side needs SSE `tool_calls` fragment reassembly, the loop, the whitelist, the system prompt assembly, plus new e2e tests (fake LLM / fake MCP scaffolding already exists).
- UI subtraction removes entry points users may be used to (quant panel, provider sheet, etc.); configuration entry converges to desktop/CLI, so plugins must keep "not configured yet" guidance.
- Keeping the two surfaces aligned requires ongoing maintenance, enforced by the `check-version-drift` assertion.

## Undecided (this ADR decides none of the following; record at landing)

1. Warden main-window quant panel: **delete** or keep as a standing display after conversation can answer it? (This ADR's stance: delete, keep the companion window's "N lines · N commits" metrics line — that is part of the lantern form factor; once conversation replaces panels, a standing panel's only remaining value is constant visibility.)
2. Whether to start the project of server-side `repotory_ask` with a tool loop (decides whether the plugin inline editor can be retired).
3. Desktop local instant cards when no AI is configured: **keep** (confirmed: offline availability is an explicit feature).

## Landing plan (in batches, run the gates after each batch)

1. **P1 · Warden minimal FC loop**: `llm.rs` request carrying `tools`, SSE `tool_calls` fragment reassembly, feedback; `conversations.rs` read-only whitelist loop + system prompt assembly; frontend tool-event rendering; delete the main-window quant panel, keep the companion metrics line, collapse the memory-layer tool list card into a single count line; add e2e tests (fake LLM returns `tool_calls` → fake MCP executes → feedback → generates again; whitelist gate: write tool names never appear in the model request).
2. **P2 · Surface expansion and plugin first-round subtraction**: Warden whitelist expanded to the full read-only set; VS Code deletes provider sheet/search button/Copy/diagnostics button/status-bar handoff timer; JetBrains deletes ProviderDialog/DiagnosticsDialog/search action/diagnostics action/Rename button/card Copy; fix the four cross-end inconsistencies; both plugins `tsc`/tests fully green.
3. **P3 · Server-side `repotory_ask` with tool loop + plugin confirm card; plugin inline editor retires; desktop registry adds memory-side tools** (coordinated with the desktop parallel session; this ADR does not touch the desktop workbench main path).

## To be written in the same batch

- CHANGELOG (cinder, zh+en), `TODO.md` current-state check, ADR index (one row each in zh and en; the next number after this is `0052`).
