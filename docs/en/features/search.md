---
title: Console: Ask & Search
order: 3
---

# Console: Ask & Search

The desktop **home page is a console**: one input box that can either ask for an answer with sources or search for hits directly. Search is no longer a top-level page — it moved into the console's "search" mode on the home page (ADR-0038, decisions 2 and 3).

> The old `/search?q=…` address still works: it redirects to `/?q=…`, where the console picks the query up and runs the search. Old bookmarks and external deep links keep working; they just take one extra hop.

## The two modes

| Mode | What it does | Backend |
|------|---------------|---------|
| **Ask** (default) | Streams an answer plus a `Sources:` list, interruptible | `AskAIWithEvidence` — the same retrieval and prompt assembly the MCP `repotory_ask` tool uses |
| **Search** | Lists hits (notes / todos / project names) | `SearchAll` (FTS5 full-text search) |

The mode is switched **explicitly** (the "Ask / Search" segmented control in the header) — it is never guessed from "Enter vs the button". Both modes share **one input box** and **one project-scope selector**; that is their only coupling point.

With no AI endpoint configured, the "Ask" mode falls back to the local index and **says so: "keyword matches, not an answer"**. The status chip in the header tells the truth too ("Local only" rather than pretending to be live).

## Layout

- **Idle**: one large input box, centered on the page, plus the recommendation area.
- **In flight**: the input area **stays docked at the bottom of the page**, and the conversation / result stream becomes the page's **only** scrolling region.

The layout is decided by "is there a conversation", not by an expand animation. This is the desktop's first alignment with the conversation-console model the two IDE panels already use (the stream is the only scroller, the input stays docked at the bottom).

## Idle recommendations: one authority

With no conversation yet, the recommendation area merges three sources that used to be scattered:

- **Quick questions**: four ready-to-send prompts (architecture / handoffs / best practices / todos)
- **Hot projects** (top 6): ranked by recent personal additions — **names only, no numbers**, because project counts, repo counts and added lines have exactly one authority: the dashboard
- **Hot tags** (top 12): every tag in the notes; clicking one searches for it

## Search-mode results

- Hits are **grouped by project**, each group with its own count; project-name matches get their own group
- Every hit carries an **"Ask about this"** action: it prefills the hit's title into the input and switches to the "Ask" mode for you to edit and send. It is the only coupling point between search and ask (the backend has no "answer about this hit" parameter, so the backend semantics are untouched)
- The query is mirrored into `?q=`, so it can be bookmarked, shared and deep-linked

## How it relates to the other search entries

| Entry | Scope | Character |
|-------|-------|-----------|
| The console's "search" mode | notes · todos · project names | Shares the screen with ask, offers "ask about this", deep-linkable via `?q=` |
| ⌘K / Ctrl+K command palette | notes · todos · project names | Keyboard-first quick switcher, first action is "new handoff" (see [Command Palette](command-palette.md)) |
| The search box inside the "Knowledge" tab | filters within each view | A **view filter**, not a global search — a different thing from the console's "search" mode |
| The dashboard's project dropdown | project filter | Filters today's output by project; not a search |
