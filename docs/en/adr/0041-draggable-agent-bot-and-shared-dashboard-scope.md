# ADR-0041: The floating button becomes a draggable agent entry, and the dashboard scope becomes the shared single source of truth

- Status: Accepted (2026-11-03, raised directly by the user, who settled the shape)
- Date: 2026-11-03
- Related: [ADR-0024](0024-home-workbench-merge-search-page.md) (home = full-page console), [ADR-0038](0038-desktop-information-architecture.md) (one authority per number), [ADR-0039](0039-in-page-tabs-over-stacked-sections.md) ("All / Starred" is a scope switch, not a tab), [ADR-0040](0040-unscoped-ask-orientation-and-handoff-evidence.md) (orientation layer for unscoped questions), [ADR-0029](0029-in-app-update-download.md) (visible installation info)

## Background

It started with a user report: **"the floating button overlaps the chat send button"**, followed by a redesign requirement: **"this floating conversation button needs to become an agent entry like the DSH bot or Trae's little robot, movable, and it needs to connect the desktop app's entry points — you can ask it what the dashboard says, how many commits today, and it should expand the dialog and give a card"**.

That sentence hides three things, each harder than the last.

### One: two controls fighting for the same pixels

The old floating button (`QuickCaptureFab`) was `position: fixed` in the **bottom-right**. The knowledge console's send button (`HomeKnowledgeAssistant`) also lives in the bottom-right, inside the composer. The two overlapped in nearly the same spot — **and the one the user actually pressed was the less useful of the two**: the send button is the console's main path, while the floating button was only a small "capture a note" form.

### Two: the floating button was never an agent entry to begin with

All it could do was capture a note. It **did not know which page the user was on**, so every question about the current page had to be typed from scratch ("how many commits today?" "which project was I working on?"). Yet the answer was already on the page — the dashboard holds today's counts, a project page holds that project's stack and activity. **The page already holds the answer, and the floating button offers a blank input box.**

### Three: the hardest one — once you add answers, two numbers fight

This is the problem this ADR actually exists to solve, and the one most easily gotten wrong.

What the dashboard calls "today's code" depends on **two choices the user makes**: **which day** (the date picker) and **starred projects only or the whole library** (the "All / Starred" scope switch, which ADR-0039 decision 4 defines as a scope switch, not a tab).

Measured against the real library (v2.24.0, date 2026-11-02):

| Scope | repo_count | total_added | my_added | my_deleted | my_files |
|---|---|---|---|---|---|
| **Starred** (the dashboard's default) | 6 | 768 | **371** | 318 | 13 |
| **Whole library** | 7 | 20,689 | **10,327** | 3,236 | 149 |

The dashboard's summary strip follows the scope switch and defaults to **371** — the same number as the hero bar's "today's goal 371 / 500 lines".

So:

- If the agent aggregates the **whole library**, it says 10,327 — **two numbers on one screen, 28 times apart**, with no way for the user to tell which one is the app's answer;
- If the agent uses the **wall-clock today**, it says "0 commits" in the morning while the dashboard next to it says 371 lines — **one question, two numbers**;
- If the agent uses yesterday but the wrong scope, it says 768 (the team figure) while the dashboard's "personal added" is 371 — **and not even under the same name**.

ADR-0038 decision 5 already said it: **"numbers have exactly one authority"**. What this ADR has to do is make sure this agent **stops being the second one**.

## Decisions

### 1. The floating button becomes a draggable robot

- `AgentBot.tsx` replaces `QuickCaptureFab.tsx`: a 52×52 robot button, pointer-event dragging, a **4px threshold** separating a click from a drag (below the threshold it is a click; above it, the button moves and the panel does not open).
- **It lands in the bottom-left by default** (the 72px side rail + 20), **not the bottom-right** — the bottom-right belongs to the console's send button, while the left rail edge is empty on every page.
- The position belongs to the user: after a drag it is written to `localStorage` (`repotory.agentBot.pos`) and stays there next launch; a window resize re-clamps it into the viewport. **A floating control that moves itself is a floating control that gets in the way.**
- Keyboard reachable: Enter / Space opens and closes, Escape closes the panel.

### 2. It opens on a card, not an empty input box

**Opening the panel produces the current page's card right away.** An agent that opens to a blank input has said nothing; an agent that opens with "39 commits today" has already answered the question the user was going to ask.

### 3. The context follows the route

`agentContext.ts` derives, from the current path, the questions that page can answer and the cards behind them:

| Page | Quick questions |
|---|---|
| Dashboard | Today's commits / Last 7 days / Todos and notes / Latest commit |
| Project | Project overview / Today's commits here / Recent commits / Tech stack |
| Review | Pages to review / Today's commits |
| Knowledge | Todos and notes / Today's commits |
| Wiki pages | Recent handoffs / Today's commits |
| Settings | Installation / Today's commits |
| Home (console) | Recent handoffs / Today's commits |

Free-form questions inherit the page's scope too: on a project page the question is about **that project**; everywhere else it is about **the whole library**.

### 4. Cards come from local data; only free-form questions go to the AI

Cards read exactly the data **the page itself reads**: precise, instant, and **working with no AI endpoint configured**. "39 commits today" is a fact, not a guess. Free-form questions go to the AI's streaming answer (reusing ADR-0040's evidence layer and reference numbering).

### 5. The dashboard scope becomes a shared single source of truth

New `web/src/state/dashboardScope.ts`: **the day and the starred switch are written by the dashboard and read by the agent**.

```
dashboard (writes) ──► dashboardScope ◄── agent (reads)
```

This is not abstraction for its own sake; it is the direct consequence of the table in section three: **two consumers reading the same choice cannot disagree.** It is a tiny external store rather than React context — the two consumers are not even near each other in the tree (the dashboard is a page, the bot is mounted at the app root), and a store spares them from having to be siblings.

The scope therefore **survives navigation**: switch the dashboard to "All", walk to the knowledge tab, come back — still "All". The user's choice should not evaporate because they walked away. (It does not survive a restart: the store lives in memory, so a restart returns to the defaults.)

### 6. The card and the dashboard use the same scope

The card reads **`my_added` / `my_deleted` / `my_files` (the personal figures)**, which are **literally equal** to the dashboard strip's "personal added / personal files / repos".

The only figure **deliberately on a different scope** is the **commit count**: it comes from the heatmap, which has no starred filter and counts the user's commits across **every repository** — which is exactly what the dashboard's own heatmap shows. **So the card's footer has to say so:**

> Line counts use the same scope as the dashboard's "My lines added" (starred projects); the commit count comes from the heatmap and covers every repository.

Without that line, the footer would be covering up two numbers fighting each other.

### 7. The date is honest

When the scope's date is not yesterday (the user is looking at another day), neither the title nor the chip says "today" — they say "code volume" instead, and the card's subtitle **always shows the concrete date**. A card titled "Today's commits" that answers for 2026-01-01 is lying.

### 8. Nothing is lost, only relocated

- The old `AIAskPanel` capabilities (evidence answers, save as note, file as wiki page) **move into the agent panel's AI card**;
- The old FAB's capture form is **extracted as `QuickCapturePanel`**, becoming the agent panel's "capture a note" mode;
- The two old files (`AIAskPanel.tsx` / `QuickCaptureFab.tsx`) and their styles are deleted.

### 9. What does not change

No backend change, no schema change, no `internal/app` binding change, no change to any decision in ADR-0038 / 0039 / 0040, no change to the brand's three icon domains, no change to the knowledge console itself.

## Consequences

- **The robot's default bottom-left position covers some page content** (the home page's "popular projects" area sits there). That is the inherent cost of a floating control; **being draggable is the answer**, and it is exactly the shape the user asked for.
- **The commit count and the line counts sit on different scopes** (the heatmap has no starred filter), so the footer is a hard requirement, not decoration.
- **Moving the scope into a shared store makes the dashboard's day and scope switch survive navigation** — a behavior change (they used to reset when you left the dashboard and came back), but it is the precondition for "the agent and the dashboard always say the same number".
- **A failed read still reports the failure instead of falling back to zeros**: "0 commits" is a fact about the user's day, and saying 0 when the read failed is a lie the user cannot detect.
- **Cards give facts, not advice**. They answer "how much", not "what are you going to do about it" — the latter is the AI path's job.

## Implementation checklist

- `web/src/state/dashboardScope.ts` (new): the shared scope store + `useSyncExternalStore` wiring.
- `web/src/hooks/useDashboardData.ts` (changed): the day and the starred switch now read the shared store; `getSummary` reads the scope at call time (no more ref).
- `web/src/components/agent/agentContext.ts` (new): route → context + local card builders (pure data, no React).
- `web/src/components/AgentBot.tsx` + `.module.css` (new): the draggable robot + panel + card rendering + AI streaming answers.
- `web/src/components/QuickCapturePanel.tsx` (new): the capture form extracted from the old FAB.
- `web/src/components/AIAskPanel.tsx` / `QuickCaptureFab.tsx` (deleted): their capabilities moved.
- `web/src/App.tsx`, `Icon.tsx`, `styles/design-system/features/fab.css` (changed): mount the robot, add the `bot` icon, drop the old FAB styles.
- Chinese and English locales (changed): the `agent.*` namespace completed.

## Implementation record (2026-11-03)

**Measured against the real library** (v2.24.0, date 2026-11-02, measured in the browser):

- The robot button was measured at `(92, 685)`, 52×52 — **bottom-left** — and **does not overlap** the console's send button (measured at `x=568, y=304`, the composer's bottom-right);
- Dragging measured: from `(269, 695)` to `(132, 272)`, and **the panel did not open** (the click/drag distinction holds); after a reload the position stayed at `(134, 274)` (`localStorage` works);
- Card scope: under **"Starred"** the card shows `commits 39 / my lines added +371 / my lines deleted -318 / my files 13 / repos 6` — **literally equal** to the dashboard strip's "personal added 371 / personal files 13 / across 6 repositories";
- After switching to **"All"**, the card becomes `commits 39 / my lines added +10,327 / my lines deleted -3,236 / my files 149 / repos 7`, and the dashboard switches in step to "today's goal 10327 / 500 lines / personal added 10327 / personal files 149 / across 7 repositories" — **same source, same numbers on both sides**; the card's footer switches to "Line counts use the same scope as the dashboard (all repositories)".

**Regression**: 35 new frontend tests (`agentContext.test.ts` 19: route → context dispatch, project page scope, the card taking the personal figures, **reading in the dashboard's scope**, the date-aware title, the footer following the scope, a failed read reported honestly; `AgentBot.test.tsx` 13: the button staying clear of the send button, opening the panel on a click, a drag not opening the panel, the position persisting, staying inside the window on resize, **the card showing the personal figures**, **reading in the dashboard's scope**, **re-reading when the scope changes**; `dashboardScope.test.ts` 3: the defaults, no new object when nothing changed, notifying only on a real change). The full `npm test` run passes **48 files / 410 tests**; `npm run build`, `npm run lint`, `check-i18n-keys` and `check-i18n-interpolation` are all green.
