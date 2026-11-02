# ADR-0038: Closing the desktop information architecture — one action, one entry; two states of one object, a switch

- Status: Accepted (2026-11-02; raised directly by the user, who decided the trade-offs)
- Date: 2026-11-02
- Related: [ADR-0024](0024-home-workbench-merge-search-page.md) (**this ADR supersedes its decision 1** and relocates its decisions 2 and 3), [ADR-0034](0034-desktop-three-column-shell.md) (the three-column shell is unchanged; the rail's search entry is removed here), [ADR-0036](0036-ide-chat-console.md) (**the desktop side aligns with its chat-console model for the first time**), [ADR-0032](0032-approved-knowledge-page-visibility.md) (the read-only semantics of knowledge pages are unchanged), [ADR-0017](0017-one-kernel-two-entry-points.md) ("the dashboard does not leave the first screen" is honoured in a new way)

## Background

Using the desktop app, the user pointed out that the Home / Dashboard and Knowledge / Wiki page pairs have "elements that are redundant or confused", and asked whether the dialogue box on the home page and the search should be merged into one place.

This is not an aesthetic remark — it is **the same data being drawn once per place**. What the code actually says:

| Data | Where it appears | The conflict |
|---|---|---|
| The project list | Home's "quick look" — 3 summary cards / the Dashboard's ProjectCard list (starred + others) | two card shapes, two truncation rules |
| Lines added | Home's "today's dev activity" / the Dashboard's GoalRing + SummaryBar | Home reads `getSummary('')` (everything), the Dashboard reads **the selected date** |
| Repository count | Home's "repos" = `projects.length` (rows in the projects table) / the Dashboard's "repos involved" = `summary.repo_count` (projects that actually carry git repos) | **two numbers on one screen that need not match** — Home's own comment admits the different basis, but the user only sees a contradiction |
| Search inputs | `/search`'s big box, the `/knowledge` top bar, the `/wiki` top bar, the Dashboard's project dropdown + the rail's input + ⌘K | **six entries for one action**, each implemented separately |
| "Recent" lists | Home's memory ring (recent handoffs) / the rail's "recently updated" (recent knowledge pages) | two columns both called "recent", carrying different things |
| Knowledge pages | the `/wiki` list / the rail's "recently updated" / `/search` hits | three places |

And **the Knowledge and Wiki pages have almost the same skeleton** (search box + filter row + result count + list) while retrieving two different kinds of thing: the first is the **raw-material face** (notes / todos — writable, fileable, importable), the second the **finished-product face** (approved pages, read-only). Two peer top-level tabs put "raw material" and "finished product" on the same level, so the user has to work out which one to go to; and the two are **not equal in capability** — Wiki has a project-scope dropdown and Knowledge does not, even though Knowledge's retrieval was cross-project all along.

**The dialogue box and the search are two faces of the same thing**: one gives an answer with sources (`AskAIWithEvidence`, ADR-0033), the other gives a list of hits. They live on two pages, and the home page even links to the search page from itself.

**The "expand on streaming" behaviour has never been designed on the desktop side.** The dialogue card's DOM order is indeed "header / stream / input pinned to the bottom" (the same shape ADR-0036 gave the two IDE panels), but it is packaged as a `min-height: 520px` card whose internal `.chatStream` scrolls on its own (`max-height: 580px`), embedded in a page that scrolls too — **two scroll containers**. That is not "expanding"; it is **caging the conversation in a box**: the more you ask, the fuller the card, and the page still has to scroll separately to see it all. The search page, meanwhile, is a whole page with **no collapsed state at all**.

At the root this is one thing: **these seven top-level tabs mix three kinds of thing into one flat navigation** — action faces (ask / search), object faces (notes / pages / projects), transactional faces (review / settings). Splitting one kind of thing across peer entries guarantees redundancy and confusion.

## Decisions

### 1. Five top-level entries

`Home · Dashboard · Knowledge · Review · Settings`.

- **Home = ask + search** (decisions 2 and 3): the single entry for "getting something out of the knowledge base".
- **Dashboard = today** (decision 5): the one place for "what I produced".
- **Knowledge = notes / wiki pages** (decision 4): two states of the same knowledge object.
- **Review / Settings** unchanged.

`/search` is no longer a top-level tab; the route redirects to `/` and keeps the `?q=` deep link. `/wiki` redirects to `/knowledge?view=pages`, and the `/wiki/:id` detail route stays (shared links are unaffected).

### 2. Home = a whole-page chat console: one scroll container, input pinned to the bottom

- **Idle**: one large input centred on the page, plus a recommendation area. The recommendations **merge three sources**: hot projects, hot tags (carrying over ADR-0024 decision 3), and quick prompts (today split between the `/search` idle state and the dialogue's empty state) — **one authority**.
- **After a question**: the input area is **pinned to the bottom of the page**, the dialogue / result stream **becomes the page's only scroll container**, and the header collapses into a status bar.
- **No "expand" button**: the layout is decided by whether there is a conversation — two shapes, not an animation.
- This is **the desktop's first alignment with ADR-0036's chat-console model** (the stream is the only scroll area / the input is pinned to the bottom), which until now existed only in the two IDE panels.

### 3. Ask / search are two modes: one input box, one explicit mode switch

- **Ask** (the default): a streamed answer plus the `Sources:` list (reusing `askAIWithEvidenceStream`, interruptible). With no AI endpoint configured it falls back to local retrieval and **says plainly that this is keyword matching, not an answer** (ADR-0033 decision 2).
- **Search**: a list of hits (notes / todos, grouped by project).
- **No guessing**: the mode is chosen explicitly by the user, not inferred from "enter or click".
- Both modes **share one project-scope selector** — that is their single point of coupling: a hit can be "asked about" directly, feeding a search result into the dialogue stream.

### 4. Knowledge = one tab, two views, one skeleton

- Route `/knowledge`, two views inside: **notes** (default) / **wiki pages**, switched by a segmented control, with `?view=` in the URL (deep-linkable).
- **Shared skeleton**: one search box, **one project-scope dropdown**, one result-count bar, one set of empty states and error handling.
- **Different bodies**: the notes view is a grid plus filters (all / knowledge / other, pinned, tags, topics) plus the writing actions (create / import / file); the pages view is a list and read-only (**only approved pages**, per ADR-0032 decision 1 — unchanged).
- **The scope dropdown is added to the notes view** — it is missing today, even though its retrieval was cross-project all along.

### 5. One authority for every number: the Dashboard

- The Dashboard is the **only** "my output" face: goal ring, summary bar, heatmap, control row, project list, status bar — all kept as they are.
- The Home page **loses** "quick look" (3 cards) and the "today's dev activity" card — the first duplicates the Dashboard's project list, the second duplicates its statistics with a different basis. The home page no longer computes project counts or lines added on its own.
- The home page's "open the full dashboard" link is removed (the tab is one click away).
- **The memory ring (recent handoffs + "write a handoff") moves to the top of the Dashboard**, as "where the last session left off" — it belongs to "my state", not to "I want an answer"; and "write a handoff" is the only zero-configuration entry into the session loop, which the Dashboard still reaches from the first screen.

### 6. Search entries converge

- **The rail's global search input is removed** (the console has an input box; ⌘K stays). The rail returns to ADR-0034's two blocks: pending review and recently updated.
- **The two search boxes inside the Knowledge tab stay** — they are **filters within their own view**, not global retrieval, and are therefore not the same thing as the console's ask / search.
- The Dashboard's project dropdown **stays** (it filters projects; it is not global retrieval).

### 7. What does not change

No schema change, no new MCP tool, no new `internal/app` binding, no change to the brand name or the three icon domains; the two IDE panels are untouched (they are already ADR-0036 chat consoles); the backend semantics of asking and retrieving are untouched (`askAIWithEvidence` / `searchAll` / `browseWikiPages` are all reused); `/project/:id` and the review page keep their existing responsibilities.

## Consequences

- **Positive**: top-level entries go from seven to five; each dataset now has exactly one authoritative presentation, so contradictions like the repository count disappear at the root; the home page changes from "four things of different kinds each doing half a job" into a dialogue console with one clear intent; the Knowledge tab makes the raw-material / finished-product relation explicit; and the desktop side finally speaks the same interaction language as the two IDE panels (ADR-0036's model).
- **Positive (addressability does not regress)**: all three kinds of deep link — `/search?q=`, `/wiki?project=`, `/wiki/:id` — survive (redirected to their new landing points), and the IDE panels and `repotory://` deep links travel over MCP / HTTP, never over web routes, so they are unaffected.
- **Negative (the cost of fewer entries)**: old bookmarks and documentation links pointing at `/search` or `/wiki` go through one redirect (a one-off migration cost, which is how ADR-0024 decision 1 was costed too); the home page no longer shows "how much did I produce today" at a glance — that is the Dashboard's job now; and getting from notes to pages in the Knowledge tab costs one extra switch.
- **Negative (a heavier console)**: the home page changes from a 520px card into a whole-page console, so the first screen now carries a lot more; the idle recommendations have to be genuinely useful or the first screen will feel empty — that is **the one point of this decision that must be tested in practice**.
- **Neutral**: the left/right shell (ADR-0034) is unchanged; the rail loses one block, but its two remaining contents (pending review, recently updated) were context to be seen without leaving the page in the first place.
- **Neutral (relation to ADR-0024)**: this ADR supersedes its decision 1 (home page as a workbench) and **relocates** its decisions 2 (global search as its own page) and 3 (hot recommendations) — search no longer gets a page of its own, and the recommendations move to the console's idle state. ADR-0024's original text is kept as history.

## Checklist

1. `web/src/App.tsx`: the route table — `/search` → `/` (with `?q=`), `/wiki` → `/knowledge?view=pages`, `/wiki/:id` kept; add the `/knowledge?view=` view state.
2. `web/src/components/SideRail.tsx`: drop the "Search" entry; reorder the rest to "Home · Dashboard · Knowledge · Review · Settings".
3. `web/src/pages/Home.tsx`: rewrite as a whole-page console; remove the memory ring, the quick-look cards and the dev-activity card.
4. `web/src/pages/home/HomeKnowledgeAssistant.tsx`: promote from "component inside a card" to "whole-page console"; add the ask / search mode switch; merge the three recommendation sources; pin the input area to the bottom of the page.
5. `web/src/pages/Knowledge.tsx`: merge with the pages view into one skeleton + two bodies; add the project-scope dropdown.
6. `web/src/pages/Wiki.tsx`: become the "pages view" inside the Knowledge tab (data sources and read-only semantics unchanged).
7. `web/src/pages/Dashboard.tsx`: receive the memory ring (recent handoffs + "write a handoff").
8. `web/src/components/ContextRail.tsx`: remove the global search input.
9. `web/src/pages/Search.tsx`: its retrieval moves into the console's "search" mode; the idle recommendations move into the console's idle state.
10. i18n (`zh-CN` + `en` in the same batch): nav entries, mode switch, view switch, idle recommendations, migration notices.
11. Tests: `Home.test.tsx` / `Dashboard.test.tsx` / `Knowledge.test.tsx` / `Wiki.test.tsx` / `App.smoke.test.tsx` rewritten for the new structure; new structural assertions for "the two knowledge views share one skeleton" and "one authority for the numbers".
12. Docs: `docs/features/*.md` (dashboard / knowledge / search) in both languages; one new row each in the Chinese and English ADR indexes; `TODO.md` / `CHANGELOG.md` record the landing.

## Revision record

(No revisions to this ADR yet. See the landing record at the end.)

## Landing record (2026-11-02)

All 12 checklist items are landed. **Three landing spots differ from what the text says**, all because the original checklist underestimated the size of the change — not because the decision moved:

- **Checklist items 3 and 4 turned out to be a division of labour, not two pieces of code**: `Home.tsx` shrank to two jobs — "load the project list" and "render the console" — while the body of the console (mode switch, the two layouts, the three recommendation sources, URL sync, hit grouping) all lives in the rewritten `home/HomeKnowledgeAssistant.tsx`. `Home.tsx` is 60 lines.
- **The file in checklist item 9 was deleted, not "merged in"**: `web/src/pages/Search.tsx` went away together with `Search.module.css` / `Search.test.tsx`. The retrieval capability did not move house — it was **re-implemented inside the console's "search" mode** (`searchAll` + client-side grouping by project + a separate group for project-name matches).
- **Checklist items 5 and 6 produced one extra new file**: `web/src/hooks/useKnowledgePage.ts` holds the state the two views share (debounced query, scope, notes and hits). `Knowledge.tsx` is the shared skeleton (segmented control + one search box + one scope dropdown + one count bar + one ErrorBanner/loading path), `Wiki.tsx` is demoted to the pages-view body (`WikiPagesView`), and both consume the same state hook.

Four real problems were fixed while landing, none of them "a small blemish" — each one made the interface say something false:

1. **Blank group headings**: the backend's `SearchAll` returns `project_name` as NULL (the join is left empty), so grouping by project produced empty headings. The fallback chain is `hit.project_name` → the project list → `#id` — and the last rung is the **honest floor**: when the name really cannot be found, show `#124` rather than invent one.
2. **The input box was not cleared after submitting**: the root cause is that react-router's `setSearchParams` goes through a transition and **for one frame the URL still holds the old value**. The old heuristic — "the URL changed, so something outside changed it" — read that frame as an outside change and put the just-submitted query straight back into the input box. The heuristic is now narrowed: **only `useNavigationType() === 'POP'` (back / forward) counts as an outside change**, and one's own writes are accounted for by the `written` ref.
3. **The `/?q=` deep link did not run a search on first load**: after the criterion in point 2 was narrowed, the symptom no longer reproduces. Verified against the real library: both `/?q=交接` and `/search?q=交接` go straight into search mode and return 7 hits grouped by project (`Script 7`). **No root cause is written down here** — both symptoms came from the same URL-criterion defect, the fix removed it, and inventing an unverified explanation would only plant a landmine for whoever reads this next.
4. **After the search entries were consolidated, `SearchAll` went from 1 caller to 0**: with the old search page gone, global retrieval exists only inside the console. The backend semantics of `SearchAll` are untouched, but **its front-end call surface got narrower** — recorded here so nobody later tunes the backend against the old search page's usage.

Three honesty tightenings, each tied directly to decisions 2 / 3 / 5:

- **The status pill no longer hard-codes "Live"**: it now states 「AI 已连接」 or 「本地检索」 truthfully. A hard-coded breathing light is **a lie** the moment the AI endpoint drops.
- **The local fallback reply carries a badge**: 「这是关键词匹配，不是答案」. With no AI endpoint the reply *is* the hit list, and it has to say so **on the line where the answer is**, not only somewhere else on the page.
- **Hot project recommendations show no numbers**: the ordering still uses `my_added_7d`, but the recommendation slot shows **not a single number** — the numbers have one authority, the Dashboard (decision 5). Showing numbers would mean the home page computes output a second time.

Regression and gates:

- New `web/src/pages/Knowledge.test.tsx` (8 cases) and `web/src/components/ContextRail.test.tsx`; rewritten `Home.test.tsx` / `Wiki.test.tsx` / `Dashboard.test.tsx` / `App.smoke.test.tsx`; `useKnowledgePage.test.tsx` +4 cases (including the "the input box is cleared after submitting" regression). The whole `web/` suite passes — **44 files / 323 cases** — with `npm run build` / `npm run lint` (0 errors, 0 warnings), `go test ./...`, `check-docs-mirror` / `check-version-drift` / `check-i18n-keys` / `check-agent-init-contract` / `generate-icons --check --tracked-only` all green.

**One environment warning for whoever comes next**: the `repotory-server` installed on the machine may be an **old binary** (this machine has v2.18.0; the workspace source is already v2.23.0). The old binary has no `BrowseWikiPages` binding, and the "knowledge pages" view then fails outright with `unknown method: BrowseWikiPages` — **it looks like a front-end bug, but it is a stale backend**. `scripts/dev.sh` already says so ("the port may be taken by an old build — `pkill` and restart"). Before chasing "some tab says unknown method", check the version with `curl /health`.
