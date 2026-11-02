# ADR-0037: Three-layer convergence of brand legibility — taking "the printed card" from an icon into the interface language

Status: **Accepted (2026-11-02)**
Date: 2026-11-02
Related: [ADR-0034](0034-desktop-three-column-shell.md) (three-column desktop shell, brand mark lives in the left rail), [ADR-0036](0036-ide-chat-console.md) (both IDE panels become a chat console), [ADR-0006](0006-scope-reduction.md) (scope freeze), [ADR-0007](0007-session-memory-protocol.md) (session memory protocol)
Starting schema: v28 (**this ADR changes no schema**)

## Background

The user said: **"legibility is low"**, and added a constraint — **"don't force awareness up; that would make later changes hard"**.

Taken together, those two sentences set a direction rather than a magnitude. The conventional way to raise legibility is to swap in a flashier, more distinctive symbol or a stronger contrast color — something people remember at a glance. But that is exactly what the user ruled out: **once awareness is built on a specific shape, that shape becomes a liability** — and changing it later (a different typeface, different color, different shape) starts with "will we lose them?"

So this ADR is not after "a stronger symbol". It is after **a structure that can keep carrying recognition while still allowing piece-by-piece replacement**. That requires diagnosing first: legibility is low — **low at which layer**?

### Diagnosis: three layers out of sync

**Layer one — the master (`build/icon.svg`, the v4 artifact)**

The current master is "a cream card + hairline frame + a bold R": warm paper `#F5F0E4`, near-black ink `#1B1B1B`, a 9-unit hairline frame (512 grid, inset 26), and the R from IBM Plex Sans Bold (320-unit cap height, nudged 6 right and 2 up because an R is heavier on its stem side).

The master itself is **good** — "a printed card" is a real concept, the relationship between the square edge and the hairline is well argued, and the three-domain rule (brand / OS app / glyph) has already engineered "one letterform, three containers".

The problem is **that the wrong element carries the recognition**: "R" is the most generic letter in the world, and swapping the typeface still leaves "a letter in a box". What actually distinguishes this card from "some other app icon" is **the hairline frame** — but the frame is currently **decoration** (a stroke), not **semantics** (a construction rule anyone can restate). **Change the typeface and the whole brand collapses**, which means the brand does not rest on anything transferable.

**Layer two — colour (UI tokens)**

The master is warm cream plus near-black. The UI tokens are a different language:

```
--bg-primary: #f5f5f3   --accent: #2563eb (Tailwind blue-600)
--bg-secondary: #ffffff --accent-soft: #eff6ff
```

Measure it: `#F5F0E4` (the brand cream) appears in **exactly one component in all of `web/src`** — `BrandMark.tsx`. Not one other pixel of the interface uses it. The interface reads as "a neutral, slightly blue piece of software".

That is the split the user sees: **a cream icon pasted into a blue interface**. The icon and the interface do not look like one product.

**Layer three — scale (the mark inside the UI)**

`BrandMark` renders at 26px in the desktop left rail. The master's hairline is **9/512 units** — at 26px that is **0.46px**, under one pixel, so the browser renders it as a blurred grey line.

So the brand mark in the UI actually reads as: **"an R with a nearly invisible border"**. It shares no distinguishing feature with ordinary text or with another product's single-colour glyph. That is the most direct source of "low legibility" — **at the one size where the mark most needs to be recognised, it has lost its only identifying feature.**

> This is a lesson the repository has already recorded: `CONTRIBUTING.md`'s "brand icon three-domain rule" says "re-weight at small sizes — the master's hairline is 9 units on the 512 grid, which is only 0.42px at 24px (under a pixel, blurring into a grey line)". **The glyph domain was already re-weighted to 34 units (1.6px at 24px) — but the UI's `BrandMark` was not.** It copies the master's geometry verbatim, which leaves a solved problem sitting inside the application interface.

### Why "make the mark bigger" does not work

Bumping 26px to 48px, adding a gradient, adding a coloured field, switching to a rarer letter — every one of these lifts "recognised at a glance" immediately, and every one of them manufactures the liability described above:

- **Adding a gradient**: a gradient is a strong visual feature and therefore a strong lock-in. The master explicitly states "No gradient, no shadow, no rounded corners — the square edge and the hairline are the style". A gradient voids that style statement, and withdrawing it later becomes a project.
- **Switching to a rare letter**: legibility jumps, but the brand is now welded to one glyph, and "rare" conveys nothing about what the product is.
- **Enlarging**: it eats the already-narrow left rail (72px, set by ADR-0034), and does nothing about the underlying "feature invisible" problem.

## Decisions

### Decision 1: legibility rests on "construction", not "symbol" — promote the hairline frame to brand semantics

The brand mark's recognition point **migrates from "the letter R" to the construction of "a printed card"**:

> **A square warm-paper card, edged with a line of ink heavier than the body text, bearing one near-black glyph.**

Three reasons:

1. **Transferable**: change the typeface, the letter, the paper colour — the recognition survives. The other way round, recognition resting on a letter dies with the letter.
2. **Restatable**: a user can say "that beige thing with the black frame" — describing a construction, not a glyph.
3. **Extendable**: the construction naturally extends to interface elements (decision 2) instead of staying an icon.

**The letter is demoted to a placeholder**: R stays R (no gratuitous upheaval), but it no longer carries recognition. **The construction carries the brand; the letter is the variable.**

> This **amends rather than overturns** v4: v4's "one master, three derived domains" discipline stands intact. What this adds is a **re-specification of where recognition lives**, plus **alignment on the UI side**.

### Decision 2: turn "the printed card" into interface language — brand colour enters the UI tokens

The UI stops being "neutral grey plus Tailwind blue" and becomes **sourced from the master**:

- **Warm paper ground**: the page ground shifts from `#f5f5f3` toward the master's cream `#F5F0E4` family; card surfaces take a paper-white one step brighter than the ground, rather than pure `#ffffff` (screen white next to a warm ground reads dirty).
- **Ink**: body ink moves from `#1a1a1a` toward the master's ink `#1B1B1B` family, so it is of a piece with the mark.
- **Accent**: **the Tailwind blue is dropped**. The new accent is derived from the master's **ink plus a warm shift** — a deep warm brown/ink accent line, not a cold blue.

**Why**: once brand colour is in the interface, recognition no longer depends on "the user remembering the icon" but on **the whole product looking consistent**. That is steadier and more sustainable than any symbol — and when the master, the typeface or the whole palette changes later, the interface changes with it, with no "long-time users only know the old icon" problem.

**Constraint**: the accent must still clear WCAG AA (≥ 4.5:1 against its ground), in dark theme too. **This is a hard gate, not an aesthetic preference** — the existing invariant in `semanticInk.test.ts` continues to apply.

### Decision 3: re-weight the UI mark at small sizes (the same rule the glyph domain already follows)

`BrandMark` in the UI **does not reuse the master's 9-unit hairline**; the stroke width now **scales with the rendered size**:

- at 26px the line lands on **at least 1 physical pixel** (20 units), so the frame — the recognition feature — is actually visible;
- **the frame moves inward**, to preserve **at least 1 physical pixel of paper margin**: once the stroke thickens, keeping the master's 26-unit inset lets the frame eat the card's edge, and the mark reads as a "dark square" rather than as a printed card;
- **at ≤16px it degrades to the bare glyph** (single colour, taking `currentColor`, so it belongs to its surface instead of being a fixed near-black) — matching the repository's existing "glyph domain" rule (16px carries the letter only, no frame; a 13px cream card on a dark sidebar reads as a glowing blob);
- **512px is still the master itself** (9-unit stroke, inset 26) — the re-weighting must not creep upward.

**Why**: decision 1 says recognition is "the frame", so the frame **must be visible at the sizes where it is actually seen**. The master's 9 units are tuned for the 512px print/large-size case and necessarily fail when scaled to 26px — the same error that was already identified once, and then left in the UI side. And "frame + paper margin" is a pair: **a frame with no paper around it is just an outline**.

### Decision 4: no new symbol, no change to the three-domain structure, no change to the brand name

- **No letter swap, no recomposition, no gradient/shadow/radius**: see "why making the mark bigger does not work".
- **The three-domain rule (brand / OS app / glyph) is unchanged**: this ADR only changes **UI-side alignment** and **where recognition lives**; the master's own geometry stays put (9 units on the 512 grid is right there).
- **The brand name `Repotory` is unchanged**: a frozen identifier (ADR-0006). The "legibility" problem lives in the **visual construction**, not the name.
- **No new MCP tool, no schema change, no new binding**: pure visual layer.

### Decision 5: the recognition point must be pinned by assertions, or it will be changed back

Brand discipline is enforced by CI, not by memory. This ADR's implementation adds:

1. **`BrandMark`'s hairline scales with size**: a test asserting that **at the sizes the UI actually uses, the rendered frame width is ≥ 1px** — asserting a **property**, not a magic number.
2. **Brand colour really is in the tokens**: a test asserting `--accent` is no longer that Tailwind blue, and that the brand colour in the tokens is **of the same source** as the values in `build/icon.svg` (read the master and compare, so the two sides cannot drift).
3. **`semanticInk.test.ts`'s invariants keep covering the new accent**: with 102 uses of `var(--accent)`, the risk of a semantic colour being silently flattened is exactly as it was before.

> Lesson source: `CONTRIBUTING.md` records "this brand shipped half-applied four times (the activity bar, the tool window, the favicon, the README each missed a move while its twin moved) — the root cause of every one was a hand-kept copy". **The corresponding risk here is "the UI and the master drifting apart again"** — so the assertions must read **the master** as the source of truth rather than copying colour values into a test.

## Consequences

- **Positive**: legibility shifts from "the user remembers the icon" to "the whole product looks consistent, and the mark keeps its identifying feature at every size" — and it **manufactures no new lock-in**: when the master changes later, the interface changes with it, with no worry that long-time users know only the old symbol.
- **Positive (for the IDE surfaces)**: ADR-0036's two IDE panels already carry brand marks (VS Code activity bar / JetBrains tool window); this ADR's "warm paper + ink" semantics are of a piece with them, so all three distribution surfaces finally speak one visual language.
- **Negative (a wide change surface)**: `--accent` has **102** uses, so changing the colour ripples across the whole desktop interface. Every screen has to be checked, not just the home page.
- **Negative (contrast must be re-balanced)**: moving from cold blue to warm ink means every text and icon carried by `--accent` needs its AA contrast re-checked; the same in dark theme.
- **Neutral**: no schema change, no new tool, no new binding, no brand-name change, no change to the master's geometry.
- **Neutral (relationship to ADR-0036)**: ADR-0036 fixes the panels' **structure and interaction** (conversation feed, project lock, folded history); this ADR fixes the **brand mark and interface language**. They are orthogonal and do not conflict.

## Landing checklist

1. `web/src/styles/design-system/tokens.css`: brand colour into tokens (warm paper family / ink family / new accent + dark-theme counterparts); drop the Tailwind blue accent; re-check AA contrast item by item.
2. `web/src/components/BrandMark.tsx`: hairline re-weighted by rendered size; degrade to the bare glyph at small sizes (matching the glyph-domain rule).
3. `web/src/components/BrandMark.test.tsx`: add the "frame ≥ 1px at UI sizes" assertion.
4. Add a token↔master same-source assertion (read values out of `build/icon.svg` and compare, to prevent drift).
5. `docs/**` and `docs/en/**`: mirror this ADR in both languages + update the indexes; sync brand-related manual pages.
6. `CHANGELOG.md` / `TODO.md`: record this ADR's landing.
7. Check whether the VS Code / JetBrains brand marks need aligning to the same semantics (if so, the change lands in each distribution surface; the master stays put).

## Landing record (2026-11-02)

**The pairwise AA audit is fully green.** Every token pair (three-step body ink / accent / semantic colours / control border / the two brand colours) against every surface (`--bg-primary` / `--bg-secondary` / `--bg-tertiary` / `--bg-hover` / `--bg-active` / `--accent-soft` / the `-soft` fields) was compared in **both the light and the dark theme**, and every pair that failed in the old palette now passes:

| Pair | Old (light) | New (light) | Old (dark) | New (dark) |
|---|---|---|---|---|
| `--accent` on `--bg-tertiary` | 4.38 ✗ | 6.50 | 5.65 | 6.05 |
| paper-white on `--accent-light` (primary-button hover) | 3.68 ✗ | 6.14 | 8.82 | 9.03 |
| `--warning` on `--bg-primary` | 4.34 ✗ | 5.69 | 8.82 | 10.21 |
| `--warning` on `--bg-tertiary` | 4.02 ✗ | 5.29 | 7.28 | 8.69 |
| `--text-tertiary` on `--bg-active` | 4.22 ✗ | 4.58 | 4.04 ✗ | 4.59 |
| `--danger` on `--bg-active` | 5.16 | 4.83 | 3.88 ✗ | 4.59 |
| `--info` on `--bg-active` | 4.35 ✗ | 4.58 | 4.17 ✗ | 4.71 |
| `--border-default` on `--bg-primary` | 1.52 ✗ | **3.25** | 1.53 ✗ | **3.65** |

`--border-default` draws control boundaries (buttons, inputs, chips), so WCAG 1.4.11's 3:1 non-text contrast applies; the old **1.52:1** was a **pre-existing debt**, cleared here as a side effect rather than as a new requirement.

**Measured on the mark** (pixel-by-pixel at 2x device density): at 26px it changes from "a smeared grey line" to "a solid ink frame with a visible paper margin"; 22 / 20 / 17px keep a ≥1px frame and a ≥1px paper margin; at 16px it degrades to a monochrome letter.

**The same-source assertions** are in place: `web/src/styles/brandTokens.test.ts` **reads the values out of `build/icon.svg`** and compares them against `--brand-paper` / `--brand-ink`, and asserts that the two themes' primary inks are exactly the master's two colours swapped; `BrandMark.test.tsx` asserts a **property** of the frame width and the paper margin (≥1px) rather than a magic number. The accent's and the body ink's AA are checked **by property** in the same file — the palette can be re-tuned, and a value that drops below the bar turns the test red.

**IDE-surface check**: the two IDE surfaces' brand marks **already matched** this ADR's semantics — the VS Code activity bar's 24px is "glyph domain + a 34-unit ink frame" (1.6px), and the JetBrains tool window's 16px is "the bare monochrome letter", which is exactly the two regimes decision 3 defines. The master did not move, so `scripts/generate-icons.mjs --check` **passes with no change**. The UI side had been **the one surface** that never got the re-weighting.

**A fourth surface: the documentation site.** The check turned up that **the docs site was speaking a third colour language** — the app wore "neutral grey plus Tailwind blue", the icons wore "warm paper plus near-black", and the docs site's theme accent was **Material Green 500 (`#4caf50`)**, a leftover related to nothing else in the product. **That is the ecosystem-level root cause of "low legibility"**: not that one icon is unattractive, but that three front doors each speak a different language. The docs site's accent now comes from the brand family: warm bronze `#7A3E12` in the reading area (the same value as the app's light theme), warm amber `#E09155` on the dark sidebar (the same value as the app's dark theme — a dark bronze is unreadable there), and the callout's blue field becomes the brand's warm-paper `--accent-soft`. **A pre-existing AA debt is cleared on the way**: the old green managed only **2.64:1** on the docs site's light ground (link text, non-compliant); the new warm bronze reaches **7.89:1**, and the amber on the dark sidebar **6.78:1**. **Three things are deliberately left alone**: ① the docs site's **neutrals** (pale ground / slate text / navy sidebar) stay as they are — those are the docs site's own structural palette, not brand colour, and re-theming them is a design project rather than an identity fix; ② the four node colours in the homepage architecture diagram (human / agent / core / init) are **legend semantics** — the four layers have to stay distinguishable, and flattening them into one warm family would erase the legend; ③ the docs site's brand mark is **not re-weighted** — at 28px it sits on the **dark** sidebar, where the cream card has high contrast against the ground and the card itself carries recognition, so the master's geometry is inlined as is (this is also where the JetBrains glyph-domain lesson stops applying: **on a dark ground the card reads better, the problem is a card dissolving into a light ground**).

**Checklist, item by item**: 1–4 done (tokens / component / both assertions); 5 this ADR mirrored in both languages plus the indexes; 6 see `CHANGELOG.md` and `TODO.md`; 7 checked as above — **the IDE sides need no change**; **plus the docs site as a fourth surface (accent aligned, AA fixed)**.

## Landing record (2026-11-02, continued): the last hand-kept copies of the old palette inside the UI

The previous section closed the **front doors** (the app / the docs site / the two IDE surfaces). This round looked **inside the UI itself**: everywhere a colour value was written by hand instead of taken from a token, a private copy of the old palette had been sitting there — unrelated to the master, unrelated to the new tokens, just "written that way at the time".

| Where | Was | Now |
|---|---|---|
| The theme swatches in the appearance tab (`AppearanceTab.module.css`) | neutral-grey paper + a Tailwind blue accent stroke | a miniature of that theme's **real** palette (warm paper + warm ink stroke / warm black + amber stroke) |
| The activity heatmap on the dashboard (`Heatmap.module.css`) | a cold neutral five-step ramp plus a separate dark-theme override | five steps derived from `--text-primary` over `--bg-secondary`; the dark theme inverts on its own (the override block is gone) |
| The note callout in the dark theme (`features/markdown.css`) | a blue rgba field | a warm amber field derived from `--accent` |
| The code block (`design-system/typography.css`) | cold near-black + cold grey text | light: brand ink ground carrying brand paper text; dark: warm ground carrying the warm text ramp |
| `BlockEditor`'s `--accent` fallback | blue | warm bronze |
| `index.html`'s `theme-color` | cold near-black | warm paper / warm black under the two `prefers-color-scheme` schemes |

**The swatches are the one that mattered most here.** Those three 64×44 chips **claim** to be "a miniature of the app in that theme" (the source comment says so). Before the re-colour they drew neutral grey plus blue — **they were lying to the user**, in the language of a product that no longer exists. They now draw the real palette, and they **cannot simply read the live tokens**: a swatch has to be able to show the theme that is *not* active, so both palettes are listed explicitly in `tokens.css` (`--preview-light-*` / `--preview-dark-*`) and an assertion pins each catalogue entry to the live token it depicts — **if the catalogue drifts, the test turns red**.

**Two new assertions** (`web/src/styles/brandTokens.test.ts`):

1. **The preview catalogue must equal the live palette**: ten pairwise comparisons (`--preview-light-accent` == the light `--accent`, `--preview-dark-page` == the dark `--bg-primary`, …); and the catalogue is declared **exactly once, in `:root`** — the dark theme must not re-declare it, or there is a second copy free to drift.
2. **The old palette must not come back**: every old colour ADR-0037 replaced (the Tailwind blue family, the cold grey family, that one blue rgba) is listed, `web/src/**` and `web/index.html` are scanned, and **a hit turns the test red**. The guard scans comments too — and it duly caught two old hex values that this round had written into a comment.

> Lesson: **re-colouring was never "change one token"**. Every place that hard-codes a colour keeps its own copy of the palette, so changing the tokens only reaches the half that already used them; the other half has to be swept up by "find every hand-written colour value and put it back on the tokens". Had this round only swapped the tokens, four surfaces would still have been speaking the old language (the theme swatches, the heatmap, the callout, the code block) — and each one on its own looks like nothing is wrong.

## Revision record

- **2026-11-02 (second closing pass)**: records the clearing of the last hand-kept copies of the old palette inside the UI, and the two new assertions (the preview catalogue equals the live palette; the old palette must not come back). **The decisions themselves are unchanged** — decision 2 ("the brand colour enters the UI tokens") was simply carried through to the end this time.
