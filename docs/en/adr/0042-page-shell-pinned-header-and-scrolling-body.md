# ADR-0042: One page shell — pinned header plus its own scrolling body, so the scrollbar never rises above the tab strip

- Status: Accepted (2026-11-03, raised and decided directly by the user)
- Date: 2026-11-03
- Related: [ADR-0034](0034-desktop-three-column-shell.md) (three-column shell and the content column's gutters), [ADR-0039](0039-in-page-tabs-over-stacked-sections.md) (in-page tabs; this ADR overturns the **explicitly accepted cost** recorded there — "tab strips are not sticky"), [ADR-0038](0038-desktop-information-architecture.md) (desktop information architecture)

## Background

The trigger is a user report (with a 1360×860 desktop screenshot, the red box drawn over the upper half of the scrollbar):

> **"The scrollbar in the place I marked in red shouldn't scroll that high, right? In the knowledge base, review, settings-AI and the other operation pages, the scrollbar scrolls all the way up to the top of the tab."**

The sentence is about the **height of the scrollbar**, but the root cause is **where the scrolling region starts**.

### One: the line the user drew

The screenshot is the review page (v2.27.0 desktop, dark theme):

- the vertical scrollbar sits at `x=1064..1072` and **starts at `y=28`** — the very top of the content column, above the toolbar;
- the tab strip (pending / merge / scope / jobs / rejected) is at `y=186`.

So the **scrollbar's track is 158px taller than the tab strip**, and the content in that stretch (the toolbar and the lead line) is scrollable. What the user means is: **the scroll must not reach above the tab strip**.

### Two: why — the scroll container holds the tab strip

The shell's centre column `.mainContent` is the scroll container (`overflow-y: auto`), and the **toolbar, the tab strip and the page content all live inside it**. Two consequences follow from that one fact:

1. the tab strip is content, so **it scrolls away with the content** (on the review page it is only visible on the first screen);
2. the scrollbar's track is measured from the top of the column, so **its top edge is always above the tab strip**.

Those are two faces of one thing: **the tab strip is not pinned, so the scrolling region does not start below it**.

### Three: a second finding — three sticky offsets had already drifted away from the shell's padding

ADR-0034 (commit `3204df2`) changed `.mainContent`'s padding from `32px` to `24px 28px`, but the sticky offsets in Review / Knowledge / Settings were still written against **32px** (and against **20px** at the 768px breakpoint, where the real value is **16px**). Therefore:

- the `top: -32px` + `margin-top: -32px` + `padding-top: 32px` trick — negative margin plus the padding that compensates for it — **was computing against a padding that no longer exists**;
- the pinned bars sat **8px off** the top of the scrolling region (4px on narrow screens);
- those numbers were copied into three stylesheets, each keeping its own 32px / 20px — **the same fact written down three times**, which is exactly the spot ADR-0034 forgot to follow up on.

### Four: and the dashboard and project pages already had a different answer

`Dashboard.module.css` and `ProjectDetail.module.css` each carry their own `.page` / `.fixed` / `.scroll`: **a pinned header plus a separate scrolling body**, so the scrolling region naturally starts under the header. In other words:

- **the project page was already right** (its tab strip lives in `.fixed`);
- **the dashboard put its tab strip inside `.scroll`**, so it scrolls away too;
- the same thing (the page skeleton) exists as **three hand-copied variants** (dashboard / project detail / shell), while ADR-0039 records the cost "tab strips are not sticky" — **and states explicitly that it is not an oversight but an accepted trade-off**.

## Decisions

### 1. One page shell: pinned header plus its own scrolling body

A new shared component `web/src/components/PageShell.tsx` + `PageShell.module.css`, three slots:

```
<PageShell header={…pinned chrome…} footer={…status bar…}>{…scrolling body…}</PageShell>
```

- the **header** (toolbar / tab strip / banners) does not scroll;
- the **body** is the page's own scroll container (`flex: 1; min-height: 0; overflow-y: auto`);
- the optional **footer** (status bar) does not scroll either.

Five pages use it — **dashboard / project detail / knowledge / review / settings** — and each page's hand-copied `.page` / `.fixed` / `.scroll` is deleted. **"Where does this page scroll?" now has one answer across the whole product.**

### 2. The tab strip is chrome, so it lives in the pinned header

**The scrolling region starts below the tab strip.** That gives the user the line they asked for:

> **The scrollbar's top edge is the tab strip's bottom edge.**

The strip can no longer scroll away, and it no longer needs `position: sticky` — it is outside the scrolling region, so it simply does not move.

### 3. No magic offsets to pin things

The chrome is pinned **by being outside the scrolling region**, not by the `top: -32px` + negative-margin compensation. The 8px misalignment ADR-0034 introduced is gone with it.

The side gutters become **one variable**: the shell's `.mainContent` declares `--main-gutter` (28px on wide screens, 16px at ≤768px), and every full-bleed offset reads it — **the same fact is no longer written down three times**.

### 4. Switching tabs returns to the top of the new panel

The scrolling body resets itself (`scrollTo({ top: 0 })`). It used to `scrollIntoView` the tab strip (with a `scroll-margin-top` keeping it clear of the pinned bar) — **the strip does not move any more, so that scroll target no longer exists**. The semantics become the thing the user actually wanted: after switching, you read the new panel from its beginning.

### 5. The scrollbar's position and the content width do not change

The scrolling body pulls the scrollbar **back out to the content column's right edge** (`margin-right: -gutter` + `padding-right: gutter`): the scrollbar stays where it is today (the vertical line at `x=1064` in the screenshot), **it never covers the content**, and **the content's usable width is unchanged**.

### 6. What does not change

**No schema, backend, MCP tool, binding or data-scope change, and none of ADR-0039's decisions change** (the tab-ification itself, the badge requirement, the URL convention — all unchanged); **no brand or icon-domain change**; **the home page's chat console is untouched** (ADR-0038's full-page console has its own layout and scrolling region); **ADR-0034's three-column shell is untouched**.

## Consequences

- **This overturns the cost ADR-0039 explicitly accepted.** That ADR ends with "tab strips are not sticky … **but this is a cost this decision explicitly accepts, not an oversight**", on the grounds that "the strips sit at the top of their own scrolling regions, the entry point is on the first screen, reachability has not regressed". **That reasoning was falsified by the user's feedback**: the tab strip is the page's **only section switch**, so once it scrolls away, changing section means scrolling back up first — "reachability has not regressed" holds only **on the first screen**. This was not an oversight but **a trade-off later overturned by facts**, so this ADR rewrites it explicitly (ADR-0039 keeps its original text and gains a pointer to this one).
- **The review page's order changed**: the tab strip used to sit **below** the lead line, and now sits **above** it (chrome with the chrome, content with the content). The settings page and the project page already had "tab strip on top".
- **The pinned header no longer shrinks with the content.** The dashboard's header was already tall (hero + summary bar + heatmap + controls); adding the tab strip makes the scrolling region shorter. That is the price of a pinned header, not a side effect.
- **The scroll container changed owner**: for these five pages the page scrolls itself and the shell's `.mainContent` no longer scrolls them (the home page, search results and the rest still scroll in the shell).
- **The scrollbar's relation to the content changed**: it no longer covers the toolbar and lead-line stretch — exactly the stretch the user's red box marked.

## Implementation checklist

- `web/src/components/PageShell.tsx` + `PageShell.module.css` (new): the page shell.
- `web/src/App.module.css` (changed): `.mainContent` declares `--main-gutter` (28px / 16px); the padding reads it.
- `web/src/pages/Review.tsx` + `Review.module.css` (changed): toolbar + tab strip into the pinned header; the scrolling region starts under the tab strip; a tab switch zeroes the region; the 32px magic offsets are gone.
- `web/src/pages/Knowledge.tsx` + `Knowledge.module.css` (changed): same (the notes drawer's inner tab strip joins the header).
- `web/src/pages/Settings.tsx` + `Settings.module.css` (changed): the tab strip joins the pinned header; the tab labels still sit on the same left rule as the panel text.
- `web/src/pages/Dashboard.tsx` + `Dashboard.module.css` (changed): the tab strip moves out of the scrolling region into the pinned header; the copied skeleton styles are deleted.
- `web/src/pages/ProjectDetail.tsx` + `ProjectDetail.module.css` (changed): joins the shared shell (it already was "header + scrolling region", it just kept its own copy of the styles).
- `web/src/pages/Review.test.tsx` (changed): the "a tab switch returns to the top of the new panel" assertion now asserts that the scrolling region is zeroed.

## Implementation record (2026-11-03)

**Desktop measurements** (headless Chrome, 1360×860 — the same size as the user's screenshot; the scrollbar's top edge read off the pixel row):

| Page | scrollbar top before | scrollbar top after | tab strip bottom |
|---|---|---|---|
| Review (`/review?view=merge`) | **y=28** (top of the content column — the user's red box) | **y=150** | y=131 |
| Knowledge (`/knowledge`) | y=28 | **y=150** | y=140 |
| Settings · AI (`/settings?tab=ai`) | y=28 | **y=80** | y=63 |
| Dashboard (`/dashboard`) | y=28 | **y=505** | y=495 |

After the change every one of them equals "the tab strip's bottom edge + 20px of breathing room" — **the scrollbar no longer rises above the tab strip**.

**In-browser check** (639×795): after scrolling the review page's "merge suggestions" panel by 600px, **the tab strip is still where it was**, the cards scroll past underneath it, and the scrollbar's thumb moves down while its track still starts below the strip.

**Screenshot archive**: [`outputs/screenshots/2026-11-03-adr0042-page-shell/`](../../outputs/screenshots/2026-11-03-adr0042-page-shell/) — `00-before-user-report.png` is the screenshot from the user's report (red box over the upper half of the scrollbar), `01`–`05` are the review / knowledge / settings-AI / dashboard / project-detail pages after the change, at the same size (1360×860).

**Regression**: `npm test` **49 files / 413 cases** pass (the Review scroll assertion now checks the region is zeroed, plus a `scrollTo` mock), `npm run build` / `npm run lint` (0 errors, 0 warnings) / `check-i18n-keys` / `check-docs-mirror` all green.
