# ADR-0036: The IDE panel becomes a chat console — conversation feed, project lock, folded history

Status: **Accepted (2026-11-02)** — settled by the owner; both IDE distribution surfaces are rebuilt against Cline's interface standards
Date: 2026-11-02
Related: [ADR-0009](0009-ide-presence.md) (VS Code extension), [ADR-0027](0027-jetbrains-plugin.md) (JetBrains plugin — this ADR **amends** the interface shape of its decision 2), [ADR-0035](0035-jetbrains-mcp-primary-channel.md) (channel layering: the eight data capabilities go over MCP), [ADR-0032](0032-approved-knowledge-page-visibility.md) (approved knowledge pages), [ADR-0033](0033-evidence-ask.md) (evidence Q&A, `repotory_ask`), [ADR-0015](0015-human-gate.md) (the human gate)
Starting schema: v28 (**this ADR changes no schema**)

## Background

The two IDE panels are the same thing today: a **searcher** — one input box, a column of thumbnails, one detail pane. On the VS Code side it is the webview in `searchView.ts`; on the JetBrains side it is the Swing panel in `RepotoryToolWindow.kt` (the "knowledge-search tool window" of ADR-0027 decision 2①).

That shape has three structural problems, none of them aesthetic:

**One — the subject is not a conversation, so memory cannot be read as a sequence.** A query's results overwrite the previous ones. Repotory is positioned as "the local memory layer for AI coding agents" — and memory's value is in the **sequence**: which gotcha followed which incident, which decision pre-empted which experiment. What the user faces is a window that always shows "the most recent search result" and therefore **cannot be read as a history**. And the one thing in this repo that can actually answer a question — `repotory_ask` (ADR-0033, an evidence answer with a `Sources:` list) — **is not wired into the IDE panel at all**: the panel only greps.

**Two — the scope is global, while almost everything a user asks is about their own project.** The panel searches the whole knowledge base (33 projects / 143 repositories' worth) and mixes in other projects' uses of the same words. Asking "why do we retry 503" in a workspace gets snippets about 503 from every project in the library, and the panel **cannot say which project each snippet came from**. The desktop app's project page is cut by project; the IDE panel is not — two faces over one store, two different scopes.

**Three — history does not fold.** When a screen runs out, you either scroll or close and start over; there is no "fold the old ones away" action. Cline's answer to the same problem: **reasoning blocks collapse by default, task history lives apart from the body**.

The reference is **Cline** (the agent plugin for VS Code): its `ChatView` puts a **conversation feed** in the middle, a **persistent input area** at the bottom (auto-resizing, @-mentions, slash commands), an **announcement bar** at the top (status and notices, not a status line), and it ships compact sidebar and expanded tab layouts, with the history browser separated from the body.

## Decisions

### Decision 1: the subject is a conversation feed

The panel's main area is an **appendable conversation**: one question and its answer make a turn, and turns do not overwrite each other. The old turn stays in the feed (folded per decision 3), so the reader can see "what I asked, what it answered, and on what evidence".

The feed is the **only scroll area**. There is no separate detail pane any more — opening a note or a knowledge page expands its body **in place as a card** (Cline's expanded-card shape), with the edit / copy / open-in-Repotory actions on that card.

### Decision 2: the project is locked to the current one

The panel's scope **defaults to the project the current workspace belongs to**, shown as a chip in the input area (project name · 🔒). That is the default, not a restriction: the chip opens and the lock can be released, returning to cross-project.

In two of the three modes the lock is a **real server-side filter**:

- **Ask**: `repotory_ask` takes `project_id` (`AskAIWithEvidence` gathers evidence per project);
- **Pages**: `repotory_wiki_search` takes `project_id` (the project's pages plus the global ones, ADR-0031);
- **Notes**: `repotory_notes_search` has **no project filter** on its tool surface (`path` only affects ranking, it filters nothing), so the lock filters the hits by `project_id` **in the plugin** — a **presentation-layer filter**: retrieval and ranking are still computed by the service, and the plugin copies no retrieval logic (the thin-client discipline is unchanged).

### Decision 3: history folds

- **Turn folding**: turns older than the most recent **2** collapse into one line, "N earlier turns ▾", expandable on click. This is Cline's folded reasoning block applied to whole turns.
- **Evidence folding**: each answer's source list folds by default into one line, "Sources (N)", listing its entries only when opened — the answer is the subject; the evidence is its footnote.
- **The folded state is explicit**: folded turns stay folded for the life of the session (no reset on re-render). Cline's panel has a known pain point precisely here: "the expand toggle resets every time you close the panel".

### Decision 4: three modes

The modes are a row of toggles in the input area (where Cline puts its mode switch), one per knowledge surface:

| Mode | Tool | Produces |
|---|---|---|
| **Ask** (default) | `repotory_ask` | an answer with `Sources:`, plus an expandable source list |
| **Notes** | `repotory_notes_search` | cards for the hits (notes / todos) |
| **Pages** | `repotory_wiki_search` | cards for the approved knowledge pages |

Ask takes the default slot because it is the **only mode that produces an answer rather than a hit**, and because ADR-0033 already built that capability.

### Decision 5: an announcement bar replaces the status line

Status and errors move from the panel's bottom status line to an **announcement bar** at the top of the feed (Cline's Announcement Bar): one line, coloured, visible when wrong, dismissible. The bottom of the panel belongs to the input area.

### Decision 6: the input area is persistent at the bottom

A multi-line input box (grows with its content), the mode toggles, the scope chip and the send button, on one row. It is **always there** — instead of today's three-layer vertical split of "input on top, results below, detail below that".

### Decision 7: no separate detail pane

Today's "thumbnail list + detail pane" is dropped. It follows from decision 1: the main area may have only one scroll area, otherwise a narrow sidebar's two columns squeeze the detail into a few unreadable columns (ADR-0027 recorded that trap: the sidebar is narrow and tall, and a side-by-side split deforms the detail exactly where it is read).

### Decision 8: the two IDEs are isomorphic

The VS Code and JetBrains panels have the **same structure, the same wording and the same folding rules** (ADR-0035 decision 2 already pulled the two distribution surfaces into isomorphism; this extends it). Each side implements it separately, but **what the user sees must match** — the difference is allowed only in "how Swing draws HTML".

## Consequences

- **Positive**: the panel can answer questions for the first time (`repotory_ask` is wired), instead of only retrieving; the scope matches the desktop app's project page; folded old turns make "what did I just ask" available at a glance in a long session; both IDEs look like one product.
- **Negative (the panel gets heavier)**: the conversation feed and the input area are a new front-end structure, and both distribution surfaces have to rebuild their panel (on the VS Code side, the webview and the message protocol in `searchView.ts`; on the JetBrains side, the panel in `RepotoryToolWindow.kt`). Retrieval, parsing and transport **do not change** — ADR-0035's channel layering is untouched, and the eight MCP tools stay as they are.
- **Negative (the tool surface does not grow)**: `repotory_ask`'s answer is **text**, and the source list has to be parsed out of it (`[P1] note "Title" (id 42, project X)`). That is a fragile text contract: a parse failure must **degrade to showing the whole text verbatim**, never swallow the sources.
- **Negative (the Notes-mode lock is a presentation-layer filter)**: if most of the global top-N belong to other projects, this project's results come back fewer than N. That is the honest cost of the lock, and the interface must state the scope rather than pretend the hit count is everything.
- **Neutral**: the existing command-palette commands stay (`Repotory: Search Knowledge` and friends) and now **append a turn to the conversation** instead of replacing the results; the settings page, the scope resolution and the MCP binary lookup are **untouched** by this ADR.
- **Unchanged**: the thin-client discipline (the plugin copies no retrieval logic), the human gate (machine-written wording still needs a human), the frozen identifiers, the schema.

## Landing checklist

1. `ide/vscode/src/searchView.ts`: rewrite the webview as a conversation feed + input area + announcement bar + folding; extend the message protocol (`ask` / `turn` / `announce` / folded state).
2. `ide/vscode/src/extension.ts`: wire `repotory_ask`, resolve the scope (workspace → project), parse the sources, turn the commands into appended turns.
3. `ide/jetbrains/`: rebuild the panel in the same shape (Swing: the feed rendered as HTML, the input area at the bottom, the announcement bar at the top).
4. The wording, the folding rules and the mode semantics line up one by one across the two surfaces.
5. The manual and both READMEs follow (the panel's shape changed).

## Landing record (2026-11-02)

The checklist is fully landed, but **the file landing spot of item 1 differs from what the text says** — the text says "rewrite `searchView.ts`", while in practice that file was **deleted and one webview split into four modules**. The reason: the old `searchView.ts` mixed the webview's HTML, the message protocol and the business logic in one file; rewriting it into a conversation feed changes both the UI and the protocol, and splitting them is what lets the protocol layer and the session layer be unit-tested (`chatConsoleHtml.ts` is the only code in the extension that no test can touch, so it stands alone as a file and does not import `vscode`).

- **VS Code side**: `ide/vscode/src/` goes from 4 files to 6 — `chatTypes.ts` (protocol types: every webview↔host message, **both sides import the same file**), `chatConsoleHtml.ts` (panel markup/CSS/JS, **does not import `vscode`**), `chatConsole.ts` (webview provider), `chatSession.ts` (conversation model + `Transport` port); `searchView.ts` deleted. `extension.ts` wires `repotory_ask`, the project lock, the Notes presentation-layer filter and the commands turned into appended turns; new command `repotory.ask`.
- **JetBrains side**: four new **plain-JVM, IntelliJ-free** files (so they can be unit-tested outside the IDE) — `ChatModel.kt`, `ChatSession.kt`, `ChatConsoleHtml.kt`, `McpChatTransport.kt`; the panel body of `RepotoryToolWindow.kt` was rewritten (1372 → 1245 lines); `RepotoryMcp.kt` gained `callToolText` (the console needs the **raw text** of `repotory_ask` to parse the sources).
- **How the two sides achieve isomorphism**: the same wording, the same folding rules (the latest 2 turns visible + 「N earlier turns ▾」 + 「Sources (N)」), the same filter note (`Locked to <project>: K of N matches shown.`). Differences are allowed only in the "how to draw Swing as HTML" layer.
- **Three real bugs fixed in the same batch** (found by the two sides checking each other, each pinned by a regression test): ① the VS Code side's empty-answer wording overwrote itself (wrong assignment order); ② cards were addressed by id alone (notes and todos are separate id spaces, so it opened the wrong item) → changed to type+id; ③ a todo card used its id in `notes_read` and read the note with the same id → changed to show the excerpt directly without a call.
- **A build defect fixed along the way**: `buildSearchableOptions { enabled = false }` in `build.gradle.kts` is not enough — the input directory of `prepareJarSearchableOptions` does not exist, Gradle refuses to execute, and **a clean-tree `gradle buildPlugin` goes red** (the CI gate goes red too). The whole chain has to be switched off.
