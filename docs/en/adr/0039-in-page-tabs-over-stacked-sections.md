# ADR-0039: Stacked in-page sections become tabs — one thing at a time

- Status: Accepted (2026-11-02 — the user pointed out that "the dashboard, the knowledge base and the review page could all use tabs in several places and don't")
- Date: 2026-11-02
- Related: [ADR-0038](0038-desktop-information-architecture.md) (**this ADR reverses none of its decisions**; it only refines its landing granularity from "section" to "tab"), [ADR-0032](0032-approved-knowledge-page-visibility.md) (the approved-pages-only semantics are unchanged), [ADR-0019](0019-entity-disambiguation-merge.md), [ADR-0020](0020-community-summary.md), [ADR-0023](0023-knowledge-import-landing.md), [ADR-0017](0017-one-kernel-two-entry-points.md)

## Background

ADR-0038 cut the top-level entries from 7 to 5 — that was the "one action, one entry" layer. **Inside** the pages, the same disease went untreated: three pages each stack several **mutually unrelated sibling sections** into one long scroll, so the user has to scroll past unrelated content to reach the section they came for.

What the code actually shows:

| Page | Sibling sections stacked flat | The problem |
|---|---|---|
| Review | pending queue / merge candidates / page scope / compile jobs / rejected / community summary | **Six flat sections, one of them needed at a time**; merge candidates run up to 20 rows, and the review queue sits before them |
| Knowledge (notes view) | note grid / 待整理 (organize) | "待整理" sits at the **bottom of a ~10,000px page**, reachable only through a "待整理 14" jump link |
| Dashboard | memory ring (sessions) / starred projects / other projects | "my last session" and "today's output" — two unrelated objects stacked as equals |

**And the tab already exists next door**: the project page (`/project/:id?tab=notes`), the settings page and the quick-capture FAB are all real tabs with `role="tablist"` and arrow-key roving focus. The review, knowledge and dashboard pages **never use one** — they keep hand-rolling "small heading + stack of sections", so the same product speaks two languages about switching.

And this is not an aesthetic complaint:

- The review page's six blocks have completely different shapes (an approval queue / a table of candidate pairs / a scope table / a job list / a rejection record / a statistics report) and completely different actions (approve, merge, promote, assign, cancel, delete). They are **not filters over one list** — they are six places.
- Knowledge's "待整理" is **another workbench** (re-filing the notes in the landing buckets into real projects), not a filtered subset of notes. The "待整理 14" jump button is the proof: the existence of a control like that says "it is elsewhere, and it is hard to find".
- The dashboard's memory ring is about **the last session**; the project list is about **today's output**. ADR-0038 decision 5 moved the memory ring into the dashboard because "it belongs to *my state*" — but it did not give it a container of its own, so two unrelated objects ended up stacked as equals again: **the exact shape ADR-0038 set out to destroy, reappearing one level down**.

At the root this is the same thing as ADR-0038: **putting things that only ever need to be seen one at a time side by side, and making the user scroll**. ADR-0038 treated the space between top-level entries; this ADR treats the space between sections inside a page.

## Decisions

### 1. A three-way rule: what is a tab, what is a chip, what is a segmented control

| Shape | Meaning | Test | Example |
|---|---|---|---|
| **tab** | **sibling sections where only one is ever needed at a time** | different content, different shape, different actions | the review page's five queues; the dashboard's "Projects / Sessions" |
| **chip / filter** | **a way of slicing one list** | one body of data, different subsets | knowledge's tags / topics / all-knowledge-other; the dashboard's "All / Starred" |
| **segmented control** | **two faces of one object** | the same content, two guises | knowledge's "Notes / Knowledge pages" (ADR-0038 decision 4, **unchanged**) |

ADR-0038 already drew one line ("two states of one object → segmented control"); this ADR completes it into a three-way rule. **Follow it — there is no fourth way.** The three pages each inventing their own is exactly what happened because this line was missing.

### 2. The review page: five queues = five tabs

`Pending` (default) / `Merge candidates` / `Page scope` / `Jobs` / `Rejected`.

- **Every tab carries a count badge** — the number is what tells you whether to click; a tab without one makes the user click through to find out.
- **Empty tabs do not take a slot**: "Page scope / Jobs / Rejected" appear only when they have content. The current code already renders nothing when empty; this decision keeps that semantics and moves it from "section" to "tab".
- **"Pending" and "Merge candidates" are always present**: the former is the main queue (with an empty state when it is empty), and for the latter "the structural pass found nothing" is itself an answer a reviewer wants (ADR-0019).
- **The "Jobs" tab also holds the community summary report**: both are "what the machine produced", not queues.
- Switching tabs returns the content area to the top — otherwise it stays parked mid-way through the old section and the switch looks like it did nothing.

### 3. The knowledge page: "待整理" comes up from the bottom of the page into a tab in the notes view

- The notes view gets a tab pair: **"All notes" / "待整理 (N)"**.
- **The "待整理 N" jump button is deleted** — it was a patch written for "the thing is hidden at the bottom of the page"; once the tab exists, the patch has no reason to exist.
- The "待整理" tab **appears only when there are notes to file** (it takes no slot at zero), matching today's "renders nothing when there is nothing to organize".
- The top-level "Notes / Knowledge pages" segmented control is **unchanged** (ADR-0038 decision 4). The two layers each have their own job: the outer segmented control is "the knowledge base's two drawers", the inner tab is "two views inside the notes drawer". They differ in shape and position, so they cannot be confused.

### 4. The dashboard: two tabs, "Projects / Sessions"

- **"Projects" (default)**: the existing starred-projects and other-projects sections. **Those two blocks are two slices of one body of data — chip semantics, not tab semantics** — so the "All / Starred" range switch and the summary's aggregation scope stay **exactly as they are**.
- **"Sessions"**: the memory ring (recent handoffs + "write a handoff").
- The "Sessions" tab **carries the number of handoff records as a badge** — ADR-0038 decision 5 asked for "reachable from the first screen", and the tab strip itself is on the first screen; the badge makes "3 records I haven't read" visible at a glance. Burying it in a tab without a badge would be quietly weakening ADR-0038's landing.

### 5. One authoritative tab implementation

- Extract `web/src/components/TabStrip.tsx`: `role="tablist"`, `role="tab"`, `aria-selected`, `aria-controls`, arrow-key / Home / End roving focus, count badges.
- Styling reuses the design system's existing global `.tab-btn` / `.tab-active` — **no second visual language**.
- All three pages share this one component. The settings page and project page are **not migrated for now** (each already has a tab implementation; migrating them is a separate job, out of scope here) — but new code does not write a third style.

### 6. Addressability: all three pages' tabs live in the URL

- `/review?view=pending|merge|scope|jobs|rejected`
- `/knowledge?view=notes|pages&sub=organize`
- `/dashboard?view=projects|sessions`

The criterion is **exactly** the one fixed during ADR-0038's landing: only `useNavigationType() === 'POP'` (back / forward) counts as an outside change, and one's own writes are accounted for by the `written` ref — react-router's `setSearchParams` goes through a transition, and for one frame the URL still holds the old value; reading that frame as an outside change eats what the user just typed.

### 7. What does not change

No backend changes, no schema changes, no change to the data scope (the dashboard's "All / Starred" range switch and the summary's aggregation scope are **untouched, word for word**); no change to the review page's batch semantics (pending / rejected / approved are still fetched separately); no changes to the project page's or settings page's tabs; no ADR-0038 decision is reversed; the knowledge page's read-only semantics are unchanged (ADR-0032 decision 1 still holds).

## Consequences

- **Positive**: three pages change from "scroll past unrelated content to reach the target" to "one thing at a time"; the review page goes from six flat sections to five counted queues — **click the one you want**; "待整理" comes up from 10,000px down to a first-class tab, and the jump button can be deleted; the dashboard's "sessions" and "projects" stop sharing a page.
- **Positive (one authority)**: one tab implementation, one accessibility semantics, one keyboard model; the tabs already on the project and settings pages and the three new ones speak the same language.
- **Negative (content is hidden by default)**: the direct cost of tabbing is that content outside the current tab is invisible. The mitigation is the **count badge** — every review queue, the project counts and the handoff count carry a number, so nobody has to click in to learn whether there is anything there. That is why the badge is a hard requirement of this decision rather than decoration.
- **Negative (one more layer of controls)**: the knowledge page now has an outer segmented control plus an inner tab pair. The two differ in shape and position (pill segment / underlined tab), and only one of the two outer drawers has an inner tab, so there is no ambiguity.
- **Negative (one more deep-link surface)**: each of the three pages gains a URL parameter. That is the cost of addressability, and it is how ADR-0038's "addressability does not degrade" is honoured.
- **Neutral**: the review page's three fetches (pending / rejected / approved) still start in parallel on mount — switching tabs does not re-fetch (the volume and latency are unchanged; a tab is a display-layer thing).
- **Neutral**: the dashboard's "All / Starred" range switch stays a chip and not a tab, **deliberately** — it switches the scope of the fetch (and with it the summary), not "which block you are looking at".

## Landing checklist

1. `web/src/components/TabStrip.tsx`: the shared tab component (accessibility + arrow keys + count badge).
2. `web/src/pages/Review.tsx`: six flat sections → five tabs; the community summary report moves into the "Jobs" tab; URL `?view=`; switching returns to the top.
3. `web/src/pages/Knowledge.tsx`: add the "All notes / 待整理" tab inside the notes view; delete the "待整理 N" jump button; URL `?sub=organize`.
4. `web/src/pages/Dashboard.tsx`: add the "Projects / Sessions" tab to the scroll area, the memory ring moves into "Sessions"; URL `?view=`; the range switch and the summary scope stay untouched.
5. i18n (`zh-CN` + `en` in the same batch): the three sets of tab labels, the tablist aria-labels, the badge wording.
6. Tests: `Review.test.tsx` / `Knowledge.test.tsx` / `Dashboard.test.tsx` rewritten for the new structure; new assertions for "switching tabs", "tab deep links" and "badge counts"; a new `TabStrip` unit test.
7. Docs: `docs/features/dashboard.md` and `docs/features/knowledge.md` in both languages (**the review queue is documented inside `knowledge.md`'s "Knowledge pages and the review queue" section — there is no separate `review.md`**); `docs/features-overview.md` in both languages; one new row each in the Chinese and English ADR indexes; `TODO.md`'s status snapshot; `CHANGELOG.md` records the landing.

## Revision record

(No revisions to this ADR yet. See the landing record at the end.)

## Landing record (2026-11-02)

All 7 checklist items are landed. **Four landing spots differ from what the text says**, all because the original checklist underestimated the size of the change or missed an edge case — not because the decision moved:

- **Checklist item 1 produced two extra files**: `web/src/components/TabStrip.tsx` + `TabStrip.module.css` + `TabStrip.test.tsx` (8 cases). The styling **reuses** the design system's existing global `.tab-btn` / `.tab-active` — there is no second set of tab looks, so the three new strips and the tabs already on the project and settings pages read as the same thing.
- **Checklist item 2 gained a rule the text did not state**: with the community summary report moved into the "Background jobs" tab, **the report switches to that tab when it finishes**. A result you just paid for should not have to be hunted for — at the moment it lands, the user is usually still on some other queue.
- **The URL wording of checklist items 2 and 4 was tightened**: decision 6 writes `?view=pending|merge|scope|jobs|rejected`, but the landing made the **default view stay out of the URL** (`/review`, not `/review?view=pending`; `/dashboard`, not `/dashboard?view=projects`). The reason is the same one that came out of the ADR-0038 landing: a URL is something people share, and writing the default into it only makes the link longer without adding information. Knowledge's `?view=notes` is the exception — a `?sub=organize` link also has to say which drawer it belongs to, or the link does not say whether it means "to file, in the notes drawer" or something else entirely.
- **Checklist item 5 gained a set of entries**: the review page's old `pendingHeading` / `mergeHeading` / `scopeHeading` / `jobHeading` / `rejectedHeading` keys were **deleted** (the tab label *is* the heading; the panel does not repeat it) and replaced by `tabPending` / `tabMerge` / `tabScope` / `tabJobs` / `tabRejected`; plus `tablistLabel` (the tablist's aria-label), `rejectedHint` (the "these were replaced" line inside the error-pages panel) and `emptyTab` (the empty-panel hint). Knowledge's `organizeJump` (the "待整理 N" jump button) went away with the button.

Five real problems were fixed while landing, none of them "a small blemish":

1. **The selected tab could make itself disappear**: when a queue emptied underfoot (the last item approved, the last candidate merged), the strip withdrew that tab — the panel vanished in front of the user while the URL still pointed at a tab that no longer existed; worse, the next record of that kind arrived, the tab came back, and the panel **jumped back on its own**. The rule is now that **the selected tab never disappears** (the `tabs` memo became `approved.length > 0 || view === 'scope'` and the like); only the unselected ones are withdrawn.
2. **The community summary did not switch tabs when it finished**: after the report landed, the user was still on "Pending review" and had to count the tabs to find out what the money bought. Both outcomes now switch to "Background jobs" (including the "feature not enabled" notice when the gate is off).
3. **The default view leaked into the URL**: a link like `/review?view=pending` carries no information when shared. Tightened to "the default view is not written into the URL", matching the existing `?view=` wording of the knowledge page.
4. **An empty panel looked like "still loading"**: after the tabbing, "nothing here" is a common state, and leaving it silently blank reads as "still loading". `review.emptyTab` was added (「这里还没有内容。」/「Nothing here yet.」).
5. **`aria-controls` pointed at a panel that was not in the DOM**: inactive panels are not rendered (rendering only the current panel is this repository's existing pattern — the project page and the settings page do exactly the same), so an inactive tab's `aria-controls` dangles. **These three pages were not fixed on their own** — fixing half of them would give one product two different answers to "should a tab's panel stay mounted?". It is recorded as a **known limitation**; the repository-wide fix (every panel mounted and hidden with `hidden`, including the project page and the settings page this ADR explicitly does not migrate) is a separate matter; and it is not free: the review page's "Page scope" panel alone carries roughly 800 `<option>` elements, so mounting everything permanently has a price. **The current tab's reference is always valid**; only the inactive tabs' references dangle.

Two honesty tightenings, each tied directly to decisions 2 / 4:

- **The badge is a hard requirement, not decoration**: tabbing means "content outside the current tab is invisible", and the count is the user's only way to tell whether a tab is worth entering. **Zero counts show too** — 「待审核 0」 is information, not noise.
- **The dashboard's "All / Starred" stays a chip**: it switches the **scope of the fetch** (and with it the summary), not "which block you are looking at". The badge follows the scope (real library: starred 5 → all 18), and the summary's wording is **untouched, word for word**.

Verified against the real library (v2.23.0, 31 projects):

- `/review`: all five tabs and their badges correct — pending 0 / merge suggestions 20 / page scope 27 / background jobs 10 / error pages 18; the default view stays out of the URL (`/review`); clicking "Merge suggestions" → `/review?view=merge`, and the panel switches to the candidate-pair list.
- `/knowledge`: two tabs in the notes view (All notes / To file 17); the deep link `/knowledge?view=notes&sub=organize` lands directly on the workbench, and the badge 17 matches the actual number of rows in the panel; switching back to "All notes" cleans the URL back to `/knowledge`.
- `/dashboard`: two tabs, Projects / Sessions (projects 5 / sessions 4); clicking "Sessions" → `/dashboard?view=sessions` and the memory ring appears; **the back button returns to `/dashboard` with "Projects" selected** (the POP criterion works); switching "All / Starred" moves the projects badge from 5 to 18.

Regression and gates:

- New `web/src/components/TabStrip.test.tsx` (8 cases); rewritten `Review.test.tsx` (33 cases) / `Knowledge.test.tsx` (18 cases) / `Dashboard.test.tsx` (18 cases). The whole `web/` suite passes — **45 files / 367 cases** — with `npm run build` / `npm run lint` (0 errors, 0 warnings), `go test ./...`, `check-docs-mirror` / `check-version-drift` / `check-i18n-keys` / `check-agent-init-contract` / `generate-icons --check --tracked-only` all green.

One cost, put on record: **the tab strips are not sticky**. Each of the three pages puts its strip at the top of its own scroll area (the entry point is on the first screen, in the same place the old "待整理 N" jump button was, so reachability has not regressed). Review and Knowledge already have a sticky toolbar above; stacking a second sticky layer on top of that would need a magic-number offset or a refactor of the toolbar, for too little gain — **but this is a cost the decision accepts explicitly, not an oversight**.

> **Revision (2026-11-03)**: this cost is **overturned by [ADR-0042](0042-page-shell-pinned-header-and-scrolling-body.md)** — "reachability has not regressed" holds only **on the first screen**: the strip is the page's only section switch, so once the content scrolls it away, changing section means scrolling back up first (the user's report: "In the knowledge base, review, settings-AI and the other operation pages, the scrollbar scrolls all the way up to the top of the tab"). The fix is not another sticky layer but **moving the strip out of the scrolling region**: the strip becomes part of the pinned header, the scrolling region starts **below** it, and the scrollbar's top edge is the strip's bottom edge. The original text stays here, undeleted.
