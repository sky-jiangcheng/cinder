# ADR-0056: Desktop Dialogue History Is a Local Snapshot, Not the Cross-Client Shared Thread

Status: **Accepted (2026-11-11, reported as "conversations are not saved, switching tabs loses them"; shipped the same day)**
Date: 2026-11-11
Related: [ADR-0055](0055-agent-surface-contract-and-observability.md) (four-dimension hardening in the same batch), [ADR-0051](0051-conversation-as-interface.md) (conversation as interface), [ADR-0047](0047-resident-agent-separate-host.md) (the floating bot is a view inside the workbench, not a resident assistant)
Starting schema: v30 → **v30 (no schema change)**; `CoreVersion` 1.5.0 → **1.5.0** (no MCP tool or binding surface change)

## Background

### Root cause: the lazy route unmounts — not a key change, not a missing backend write

`web/src/App.tsx:21` declares `Home` as `lazy(() => import('./pages/Home'))` and `App.tsx:277`'s `<Routes>` swaps the element on pathname change; `SideRail.tsx:105-123` links to the six top-level entries from `navEntries.ts:15-22`. **What the user calls a "tab" is one of those six entries**: clicking unmounts `Home` → unmounts `Home.tsx:51`'s `HomeKnowledgeAssistant` → `useState<DialogueMessage[]>([])` in `HomeKnowledgeAssistant.tsx:240` runs again and history is gone.

Two other explanations are ruled out: the real in-page tabs use `hidden` rather than unmount (`Knowledge.tsx:330,342`), and the Home "ask / search" switch only calls `setMode` without clearing `messages`. The floating `AgentBot` (`App.tsx:255`) lives **outside** `<Routes>` so it never unmounts on navigation — but `AgentBot.tsx:173-175` resets `answer` / `evidence` / `toolCalls` on every question, so it has no multi-turn history to begin with.

All dialogue state is component-local `useState` (`HomeKnowledgeAssistant.tsx:232-241`, `AgentBot.tsx:112-128`): no Context, no store, no localStorage chat snapshot. The UI calls `askAIWithEvidenceStream` (`transport.ts:296-303`) with a body of just `{ project_id, question }`, so the server is stateless and persists nothing. "Save as note / page" files a single answer into the knowledge layer; it is not conversation history.

### A conversation table already exists, but its semantics do not fit

Migration v29 created `chat_threads` / `chat_messages` (`internal/db/chat.go:7-35`); `internal/service/chat.go` has all five methods (List / Create / Read / Append / Rename) and MCP `repotory_chat` exposes them to Warden and both IDE plugins (`cmd/mcp/tools_chat.go:13-108`). But `internal/app/bindings.go` has **no chat methods**, `internal/httpapi/server.go:52-57` has no `/api/chat`, and `web/src/api/endpoints.ts` has never wrapped any of those calls. The desktop UI was never wired to the cross-client shared thread.

Pushing the desktop console into v29 hits four hard conflicts:

1. `source` only allows `warden` / `vscode` / `jetbrains` (`db/chat.go:24`, `service/chat.go:240`); desktop would have to masquerade as `warden` and pollute the shared thread list.
2. Messages are immutable (`no_update` trigger, `db/chat.go:33-34`), while a streaming answer mutates the same message on every delta — only `onDone` could write once.
3. `ChatMessage` has 6 fields (`service/chat.go:40-47`) and cannot carry `evidence` / `projectCards` / `knowledgeCards` / `searchGroups` / `isStreaming` / scope; restoring would drop every citation and search card.
4. The non-empty `content` check (`service/chat.go:243`) rejects search-mode messages, whose results live in `searchGroups`.

**The request is "don't lose it on tab switch / reload", not "sync the desktop and IDE conversations"**. The latter is a separate product capability requiring a contract extension, extra fields and a source decision; it does not belong in a bug fix.

## Decision

### D1 Snapshot the Home console in localStorage, settled messages only

- One key, `repotory.home.dialogue.v1`, holding `DialogueMessage[]` as JSON — **rich fields preserved** (evidence, project cards, search groups, scope, fallback flag).
- Lazy `useState` initialiser reads the snapshot; a `useEffect` writes it with a 200ms debounce (a stream triggers a state update per delta; serialising the whole array per token is not acceptable).
- Only `!isStreaming` messages, keeping the latest 50; corrupt JSON / non-array / malformed entries fall back to an empty array.
- "Clear conversation" removes the key. Read and write are two module-level pure functions, `serializeDialogue` / `loadDialogue`, each covered by tests (one red-able gate per defect).
- Zero new dependencies, zero backend changes, zero new types.

### D2 Explicitly do not write to `chat_threads`

The cross-client shared thread (v29) stays exclusive to MCP `repotory_chat`: plain-text Q&A shared by three clients, immutable messages, isolated from knowledge. The desktop snapshot restores local rich UI state. These are **not two storage formats for one dataset**; neither is a replica of the other and there is no sync.

If cross-client desktop sync is wanted later, open a separate ADR covering: a `desktop` value in the `source` enum, a projection or extension for rich fields, and the interaction rule that a stream is persisted only on completion. This ADR deliberately paves no road in advance.

### D3 Leave the floating `AgentBot` semantics alone this round

It is a single-turn overwrite panel (every ask resets `answer` / `evidence` / `toolCalls`), so there is no history to save, and it lives outside `<Routes>` so navigation does not drop it. Reload/restart does lose it, but its product role is still undecided (TODO 12.11: keep / turn into a config + health assistant / hand over to Warden). Adding persistence now is investing in a panel that may be deleted.

### D4 Fix the floating panel size (reported in the same batch as "too small, not tall enough")

`AgentBot.tsx`: `PANEL_WIDTH` 420 → **480**, `PANEL_HEIGHT` 560 → **640**. `panelStyle` computes one effective height `Math.min(PANEL_HEIGHT, innerHeight - 24)` and uses it for both the top clamp and `maxHeight` (previously the two used different numbers, so on the 600px minimum window the panel pinned near y=12 while still overflowing the bottom). CSS `.panel` gets a `max-height: calc(100vh - 24px)` + `overflow: hidden` backstop, and `.thread`'s `min-height: 120px` becomes `0` (in a flex column that floor pushed the composer out of short windows). **No bindings, no tools, no change to the ADR-0047 role boundary.**

### What this ADR explicitly does not do

1. No cross-client / cross-device sync, no thread list UI, no rename.
2. No restoration of an unfinished stream (there is no stream to resume on remount; writing a draft would leave half an answer).
3. No writing dialogue into the knowledge layer (the ADR-0015 gate: session output is not knowledge).
4. No new Wails binding, MCP tool, HTTP route or schema migration; no `CoreVersion` change, no identifier change.

## Consequences

- **Only this browser context on this machine**: another device, another install, or clearing browser data loses it. That is a real capability gap versus v29 and is documented, not glossed over.
- **Bounded quota**: 50 messages plus `try/catch`; very large rich messages push out the oldest. The `catch` degrades silently (the in-memory conversation still works) rather than interrupting the user.
- **Two notions of "conversation" coexist**: `chat_threads` and the local snapshot have different semantics. Anyone wiring cross-client sync later must read D2 first or create two competing sources of truth.
- **A bigger panel does not settle the product question**: 480×640 only fixes the feel. ADR-0050 §⑦'s "floating layer must not cover content" and the panel's actual value remain open — tracked in TODO 12.11.

## Acceptance (all testable)

1. Serialise then read back: `DialogueMessage[]` matches field for field, including `evidence` / `searchGroups` / `projectCards`.
2. Streaming drafts are neither written nor restored; an `isStreaming` entry in an old snapshot is discarded on read.
3. 80 messages truncate to the latest 50; corrupt JSON / non-array / malformed entries never throw.
4. Switching between top-level entries keeps the dialogue; "Clear" followed by a revisit shows an empty console.
5. In a 600px-tall window the panel does not overflow, the composer stays visible and the message area scrolls on its own.
6. `go test ./...`, `npm test`, `npm run build`, `npm run lint`, `check-docs-mirror`, `check-i18n-keys`, `check-version-drift` all pass.

## Implementation record (2026-11-11)
> **Addendum (2026-11-12, decision)**: of the four options for the floating bot, the user chose **B = keep as-is plus the size fix** (D4 already shipped). The three mid-term options — collapsing it into a launcher for the Home console, a settings-page health card, and deleting it in favour of Warden — are **not started**. "Let the bot change config" is rejected: the privacy gate in `internal/app/surface.go:151` and the ADR-0047 role boundary stay untouched. TODO 12.11 moved from "pending decision" to "decided: B". D1 in ADR-0055 raised `CoreVersion` to 2.0.0 in the same batch; the metadata above records the contract version as of 11-11 and is not rewritten.


- `web/src/pages/home/HomeKnowledgeAssistant.tsx`: `serializeDialogue` / `loadDialogue`, lazy initialiser, debounced persistence effect, key removal on clear.
- `web/src/pages/home/HomeKnowledgeAssistant.test.ts`: 4 new cases (round-trip fidelity / streaming exclusion / 50-item cap / corrupt data), with the key literal pinned in the test — renaming it without a migration abandons saved conversations.
- `web/src/components/AgentBot.tsx` + `AgentBot.module.css`: the six size and clamp edits from D4.
- ZH/EN ADRs, both index pages, TODO 12.1 / 12.11 and CHANGELOG `[Unreleased]` land in the same batch.
