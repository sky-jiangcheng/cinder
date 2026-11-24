# ADR-0078: A note gets its own address — the `/note/:id` detail route

- **Status**: Proposed (2026-11-24, raised by the owner: "a knowledge point opens a detail page, a note opens a project" — the owner chose to land the change)
- **Date**: 2026-11-24
- **Related**: ADR-0023 (note ownership and knowledge organize — this ADR revises where its "click a note" link lands), ADR-0038 (desktop information architecture — "knowledge = notes / pages"), ADR-0032 (the read-only page detail route `/wiki/:id`), ADR-0057 (note optimistic-lock version)

## Background

One action — "open this record" — had two different outcomes depending on the record:

| | Knowledge page `wiki_pages` | Note `project_notes` |
|---|---|---|
| Ownership column | `project_id` **nullable** | `project_id` **NOT NULL** + FK CASCADE |
| Address | `slug UNIQUE` | no slug, only the auto-increment id |
| Frontend route | `/wiki/:id` (`App.tsx`) | **no `/note/:id`** |
| Where a click lands | its own detail page | `/project/<pid>?tab=notes&noteId=<n>` |

This was not an oversight. ADR-0023 recorded the pain verbatim ("clicking a note in the knowledge base only jumps to the project, you cannot see the note itself") and fixed it with a **deep link plus a flash**: jump to the owning project's Notes tab, scroll to that card, flash it. The reasoning: a note necessarily belongs to a project, so "detail" means "that one row inside its project's context", and not adding an entity avoided touching `NOT NULL` and everything chained to it — FTS, the embedding queue, cascades.

That reasoning holds for *a record has an owner*. It does not hold for *reading and citing*:

1. **The address travels with the project.** Every entry point concatenated two ids (knowledge grid, context rail, home evidence cards, dashboard handoff ring, review page source notes, wiki page source notes — nine places). After `MoveNote` re-files a note, the old link lands in the old project and cannot find the note: a record's addressability depends on where it happened to live, which is the smell itself.
2. **Reading one note means going through a project page.** Land on project detail → switch to the Notes tab → scroll to the card, with the project header, tab strip and every other note around it. `NoteCard` rendered the note's full markdown, so a project with a handful of long notes was a multi-thousand-pixel scroll.
3. **A page can be pasted and shared; a note cannot.** `/wiki/17` is that page. A note was a pair of parameters, and the recipient still had to know whether the project existed.

## Decision

**Give a note an address made of its id alone, and separate reading from managing.**

1. **New route `/note/:id`** (`web/src/pages/notes/NoteDetail.tsx`). The body reads at the full page measure (820px, serif 16px/1.7 — the same scale as `/wiki/:id`); the header carries kind / project / topic / provenance badges plus created and updated times; below the body sit Edit / History / Open in project / Delete.
2. **The data model does not change.** `project_notes.project_id` stays `NOT NULL`: a note belonging to a project is unchanged; what changes is that **the address need not pass through the project**. `GetNote(noteID)` is already on the binding surface, and the note carries its own `project_id`, which the page uses for the scope badge and the "open in project" link.
3. **Cross-page links point at `/note/:id`**: knowledge grid cards, knowledge search hits, context-rail evidence cards, home answer evidence and search-mode hits, the dashboard handoff ring, and the "source notes" chips on the review page and wiki pages. Todos are untouched — a todo has no per-row address and keeps landing on `/project/:pid?tab=todos`.
4. **`?noteId=` stays.** Project detail still honours it, scrolling to and flashing the card (old links, external deep links and the note page's "open in project" all use it). `/project/:pid?tab=notes&noteId=<n>` is demoted from "the only entry" to "the managing entry".
5. **The return target comes from `state.from`.** A list passes its own URL as router state, so "back to notes" returns to that search and scope; a direct deep link has no such state and falls back to `/knowledge?view=notes`.
6. **The in-project card becomes "title links out + body collapses".** The title opens the note page; a long body is clamped to roughly 168px with an "Expand" control (a soft mask, not a hard cut). The Notes tab goes back to being a bulk-management surface instead of doubling as a long-form reader.

## Consequences

- One more page and route, and the note's edit / history / pin / delete actions now have an entry in two places. What they share is `NoteEditor` / `VersionHistoryPanel` / `useNoteVersionHistory` / `useNoteMutations` — no copied logic — but the two action sets have to be maintained together.
- The detail page is for reading: it deliberately has **no** AI tidy and no "new note". Tidying belongs in the batch context; putting it on a single-record reading page turns that page into a workbench.
- Two address shapes coexist. After a note is moved, `/note/:id` still works and `/project/:pid?tab=notes&noteId=<n>` does not — and the latter cannot be redirected to the former, because the project id it carries is already wrong.
- Deleting the record the page represents leaves nowhere to stand, so the page returns the user to the note's (former) project's Notes tab with `replace`.

## Reversal triggers

- Notes need a **stable identifier across projects** (public share links, a note that must still resolve after migrating databases) — that is when a slug earns its keep; id addressing is not enough.
- The note page grows into a **standalone workbench** (compiling pages from it, batch organizing from it) — that contradicts this ADR's "reading and managing are separate" premise and the boundary has to be redrawn.
- IDE plugins and the desktop need one shared note-link format — that is `repotory://` and MCP territory; this ADR does not touch the binding surface.
