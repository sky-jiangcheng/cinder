# ADR-0050: The family design standard — a neutral-minimal palette, the master art redrawn (inverse seal), Warden de-drifted, layout subtraction

- Status: Accepted (2026-11-05, the user settled each of the four forks in "evolve the UI / theme colours / icons / branding, with Manus Studio and Cue as the reference"; the exact colour values, master-art construction and Warden signature colour are below)
- Date: 2026-11-05
- Related: [ADR-0049](0049-brand-restructure-manus-style.md) (the brand restructure — this ADR **lands the two items it explicitly left undecided**: "the UI subtraction and hierarchy rework" and "whether the master art is drawn again"), [ADR-0037](0037-brand-legibility-and-ui-language.md) (brand legibility and the interface grammar — this ADR **revises its "warm-paper editorial" tone and its "square edge + hairline is the style" master construction**), [ADR-0047](0047-resident-agent-separate-host.md) (two products, one job each — Warden is an independent product, so it gets its own glyph and signature colour), [ADR-0006](0006-scope-freeze.md) (scope freeze and the frozen identifiers), [ADR-0048](0048-core-contract-version.md) (the contract version — this ADR does not bump the contract)
- Starting schema: v28 (**this ADR does not change the schema, the MCP tool surface, or `CoreVersion`**)

## Background

### The trigger

The user asked: **evolve the UI, theme colours, application icons and branding of both the cinder and warden repos, with Manus Studio and Cue as the reference, and ideally align the theme-colour standard**. ADR-0049 already fixed "Manus / Manus Studio / Cue" as this brand's naming-architecture archetype (engine = parent brand / desktop = parent brand + qualifier = Cinder Desktop / Agent = a distinct word = Warden), and explicitly left "the UI subtraction and hierarchy rework" and "whether the master art is drawn again" as **a separate ADR**. This is that ADR.

### Fact one: Cinder is already a mature system, and shares its origin with Manus' *official brand*

Cinder Desktop's `web/src/styles/design-system/tokens.css` is a complete "warm-paper editorial" token system: cream paper `--brand-paper #F5F0E4` + near-black ink `--brand-ink #1B1B1B`, a warm-bronze accent `#7A3E12` (dark `#E09155`), a three-state `data-theme` dark mode, WCAG property assertions (`brandTokens.test.ts`), and `semanticInk.test.ts`'s cascade-order invariant. Checking Manus' official brand: the ink is a **warm near-black `#34322D`**, the ground is **off-white `#F8F8F8` + white**, and the type is an **editorial pairing of Libre Baskerville (serif) + DM Sans (sans)**. **Cinder and Manus' official brand were already the same orientation**, and Cinder has already gone further than Manus — ADR-0037 deliberately de-blued it, replacing the cold Tailwind blue with warm bronze. So for Cinder, "reference Manus" is not a colour swap, it is **convergence and refinement**.

### Fact two: Warden is a snapshot of Cinder *from before the de-blue* — the real gap

Warden (`src/styles.css`, no framework) is still on a cold-blue accent `#2563eb` (dark `#60a5fa`), pure-white cards `#ffffff`, a cold-grey dark mode `#17181a`; its dark mode switches only passively through `prefers-color-scheme`, with no `data-theme` mechanism, no brand-consistency test of any kind, and no `danger`/`info` semantic colours; its icon simply reuses Cinder's "C" master (`generate-icons.mjs --agent-repo` derives it). **Only the brand layer (the cream-paper band + ink black) shares its origin with Cinder; the functional layer has drifted systematically.** This is the substance of "the standard is not aligned".

### Fact three: what transfers from the Manus family is the *system*, not a hex

Manus' own products do not describe their accent consistently (the marketing site's web uses blue `#0081f2`, community teardowns give teal `#00D4AA`/gold). So "align the theme colour" **cannot be read as copying some blue** — that would overturn the de-blue decision Cinder already made (`brandTokens.test.ts`'s `LEGACY_HEXES` blacklist is actively blocking `#2563eb` from coming back everywhere). What transfers is the **system**: a warm-neutral ink + paper ground, one restrained functional accent, editorial typography, whitespace and minimal chrome, a disciplined dark mode.

### Fact four: the whole existing icon mechanism is built around the "hairline frame" — redrawing the construction = rewriting four places

The user picked "redraw the master construction" + "inverse seal (drop the hairline frame)". The current master is "a cream square card + a 9-unit hairline ink frame + a letter", and a whole mechanism **depends on that frame**:

- `scripts/generate-icons.mjs`'s `extractBrandParts()` uses regexes to pull **the field colour + the hairline frame box + the glyph** out of the master, and all three domain derivations (brand-domain square edge / OS-app-domain squircle / glyph-domain monochrome) anchor on the frame;
- `web/src/components/BrandMark.tsx`'s `MIN_FRAME_PX` re-weighting apparatus exists entirely to "keep the 9-unit hairline from smearing into a grey line at 26px";
- `scripts/check-brand.mjs`'s gate ⑤ compares BrandMark against the master **byte for byte** on the **path `d` / `<g transform>` / `MASTER_STROKE` / `MASTER_INSET` / the two hexes**;
- `web/src/styles/brandTokens.test.ts`'s `readMaster()` reads the ink colour from the `<rect x="26" y="26" … stroke="…">` **frame** line.

**Drop the frame and all four have to be rewritten to the new construction** — this is the biggest cost of this ADR, not the palette.

### Fact five: the palette and the master are hard-bound, so they must land atomically in one batch

`brandTokens.test.ts` binds `--brand-paper` = the master's field, `--brand-ink` = the master's ink, `--text-primary`(light) = the master's ink, `--text-primary`(dark) = the master's field, letter for letter, and asserts the whole palette's WCAG AA/3:1 **properties** (not magic numbers). `check-brand.mjs` in turn binds `BrandMark.tsx` to the master. Roughly 30 icon artifacts across the two repos derive from the master and are watched by a CI sync gate. **So the master / tokens / BrandMark / generator / guards / every derived icon must land in one go** — any halfway state turns the guards red.

## Decision

The user settled each item on the visual proposal (a family neutral base + signature accents + three master constructions + before/after + layout subtraction):

### ① Palette direction: neutral-minimal, converging on Manus' official brand

Converge from "warm-paper editorial" to **warm ink `#34322D` + a neutral off-white**: Cinder's cream paper `#F5F0E4` is tuned toward a more neutral warm off-white (keeping a trace of warmth so it does not read as a cold grey). **Weaken the colourful accent** — the primary interactive colour returns to the ink itself, and the signature colour is used only at key anchors (the primary action, the active state, brand moments), no longer painted over large areas.

### ② Warden identity: the shared family standard + its own signature accent (the Cue form)

Warden adopts **the same family neutral base** (warm ink/off-white, the neutral ramp, radii, `data-theme` dark, WCAG discipline, typography) but keeps **one signature accent of its own** as an independent-product identifier — exactly as Cue is to Manus. Warden's signature colour = **Nightwatch Teal `#2F6B63`** (dark `#5FB3A6`): against Cinder's warm ember it forms an instant "same family, different product" separation, with a nightwatch/calm assistant tone. Cinder's signature colour = **Ember `#A2441A`** (dark `#E09155`), continuing the ember imagery and the de-blue.

### ③ Master redrawn: concept 2 "inverse seal"

A solid warm-ink field `#34322D` + the glyph/symbol knocked out in paper `#F7F4EE`, **with no hairline frame** (the solid field is itself the mark). Highest contrast, most legible in store lists and at small sizes, closest to Manus' monochrome minimalism. **The signature colour does not enter the mark** (the mark is monochrome ink + paper); it lives only in the UI accent — echoing decision ①'s "weaken the colourful accent".

### ④ Warden glyph: an abstract lantern/nightwatch symbol (not the letter W)

Warden is an independent product (ADR-0047 decision ③, ADR-0049 decision ②) and **no longer reuses Cinder's "C"**. It shares the "inverse seal" construction, with the glyph replaced by an **abstract lantern/nightwatch symbol** (bold, minimal, legible at 16px), consistent with the Cinder family but with its own identity.

### ⑤ Typography: editorial serif + sans (the counterpart to Manus' Libre Baskerville + DM Sans)

**Self-host IBM Plex Serif as the display face** (brand name / page titles / key figures), with the body keeping the system sans stack. Rationale: the master's glyph was already drawn from IBM Plex Sans Bold, so IBM Plex Serif is of the same family and is self-hostable under SIL OFL; this product is local-first, so **no Google Fonts CDN** (offline first). Define a modular type scale and a **spacing-scale token** (currently absent — every component hardcodes px).

### ⑥ The dark mechanism is promoted to a family standard

The `data-theme` attribute (Cinder's existing one) becomes the family standard; Warden migrates from `prefers-color-scheme`-only to **`data-theme` + a manual toggle + a three-state system**. The dark mode is a **warm near-black (not pure black)**.

### ⑦ Layout subtraction and hierarchy rework (the item ADR-0049 left behind)

- **Content first, metadata behind**: the card's first screen gives the title and status; commit counts / timestamps and other metadata drop a level (smaller, fainter, collapsible).
- **The permanent 288px right rail → contextual**: collapsed by default into a narrow strip, expanded or turned into an overlay only when there is a selection/context, giving the main column's width back to the content.
- **The floating bot does not cover the body text**: the `AgentBot` ball anchors to the centre column's edge with a reserved safe margin, and the expanded panel slides in / overlays rather than covering the body; the body measure stays `max-width:72ch`.
- **Chrome subtraction**: merge duplicate strokes, unify radii to a softer step, lighten shadows.

### ⑧ The family neutral base (the concrete tokens)

> WCAG AA/3:1 is checked live by `brandTokens.test.ts`'s **property assertions**; the landing batches fine-tune the borderline values (`--text-tertiary`/`--border-default` etc.) against those assertions, but must not touch the brand anchors below (`--brand-ink`/`--brand-paper` = the master's two colours).

| Token | Light | Dark |
|---|---|---|
| `--brand-ink` (= the master's solid field) | `#34322D` | *(not redefined in dark)* |
| `--brand-paper` (= the master's knocked-out glyph) | `#F7F4EE` | *(not redefined in dark)* |
| `--bg-primary` (page ground) | `#F4F2EE` | `#1A1712` |
| `--bg-secondary` (card) | `#FBFAF8` | `#221E18` |
| `--bg-tertiary` | `#ECEAE4` | `#2A251E` |
| `--bg-hover` / `--bg-active` | `#E7E4DD` / `#E1DED6` | `#302A22` / `#373027` |
| `--border-subtle` / `--border-default` | `#E0DCD3` / `#857E6C` | `#3A352E` / `#7A7260` |
| `--text-primary` | `#34322D` | `#F7F4EE` |
| `--text-secondary` / `--text-tertiary` / `--text-muted` | `#57524A` / `#6E6759` / `#8C8577` | `#B8B1A2` / `#9B9484` / `#7E7768` |
| Cinder `--accent` / `-light` / `-dark` / `-soft` | `#A2441A` / `#B8541F` / `#8A3A14` / `#F0E2D6` | `#E09155` / `#F2B278` / `#C07B3D` / `#3A2E1D` |
| Semantic `danger` / `warning` / `info` / `success` | `#8B4A4A` / `#6B5A18` / `#456380` / `#2D6A2D` | `#DE7676` / `#E0C24A` / `#7A9AB8` / `#4EB352` |
| Radii `sm/md/lg/xl` | `8 / 12 / 18 / 24px` (chip `999px`) | same |
| Spacing scale (new) | `4 / 8 / 12 / 16 / 24 / 32 / 48px` | same |
| Motion `fast/base/slow` | `0.15s / 0.25s / 0.35s ease` | same |

**Warden side**: adopt the entire family base above (drop the cold blue, drop the pure-white card, drop the cold-grey dark, add `danger`/`info`), swap the signature accent to **teal `#2F6B63` (light) / `#5FB3A6` (dark)** + `--accent-soft`; align `--brand-field`/`--brand-ink` to the family anchors.

### ⑨ What does not move

The identifier layer (`repotory_*` / `repotory` / `repotory-mcp` / `repotory-server` / the data directory / `com.jiangcheng.repotoryagent` / plugin ids / npm package names / the Go module / the DB sentinel) **does not move one character**; `build/brand.json`'s four brand words (Cinder / Cinder Desktop / Warden / Cinder Memory) **do not change** (this ADR changes only the visuals, not the brand words); the schema / MCP tool surface / `CoreVersion` do not move; historical ADRs / CHANGELOG are not rewritten.

## Consequences (accepted)

- **This ADR's biggest cost = rewriting the icon system, not the palette**: `generate-icons.mjs`'s `extractBrandParts()` and the three domain derivations lose the "hairline frame" anchor, `BrandMark.tsx`'s `MIN_FRAME_PX` re-weighting apparatus is deleted along with the frame, and both `check-brand.mjs`'s gate ⑤ and `brandTokens.test.ts`'s `readMaster()` are rewritten to the "inverse seal" reading logic and semantic mapping (field = ink, glyph = paper).
- **Warden needs its own master SSOT**: the glyph is no longer the C, so `--agent-repo` can no longer derive from Cinder's master — a Warden master has to be added (e.g. `build/icon-warden.svg`), and the generator and both repos' guards have to recognise "two masters, one construction".
- **Atomic coupling**: batch 1 cannot be split internally, the guards are red halfway through; and roughly 30 binary icons across the two repos have to be regenerated (including `.icns` via `iconutil` and `.ico` as a hand-built container).
- **The brand assets are wiped once more**: ADR-0037 decision 1's "recitable construct" was "square edge + hairline"; this ADR replaces it with "inverse seal", so recitability has to be re-verified (does the solid field still read as a printed object rather than a dark patch at small sizes / in store lists).
- **Self-hosting the font has a build-surface cost**: the woff2 enters the repo, `@font-face` and FOUT/FOIT handling, and the docs site and both repos each load it.
- **Layout subtraction touches many components and their CSS Modules**, and `semanticInk.test.ts`'s cascade-order invariant and `--context-rail-width`'s FAB-anchoring comment both have to be re-checked along with it.

## Not decided (explicitly left to the landing batches or later)

- **The exact geometry of Warden's lantern/nightwatch symbol**: batch 1 produces an SVG draft to settle it (requirements: bold, legible at 16px, the same optical area as the C).
- **The final setting of the borderline colour values**: `--text-tertiary` / `--border-default` etc. are fine-tuned live against `brandTokens.test.ts`'s property assertions.
- **The exact interaction of the right rail's "contextualisation"** (a permanent narrow strip vs an overlay vs selection-triggered): batch 4 produces the options.
- **Whether to give the two repos one shared token source file** (today each repo maintains its own CSS, with `brand.json` preventing drift): this ADR does not force a merge; it aligns the values on the Warden side first, and leaves a shared source for later evaluation.

## Landing checklist (batched, guards run at the end of each batch)

1. **Batch 1 · master + palette (the atomic core)**: redraw `build/icon.svg` (inverse seal) + add `build/icon-warden.svg` (lantern symbol) → rewrite `generate-icons.mjs`'s `extractBrandParts()` and the three domains (drop the frame anchor) → sync `BrandMark.tsx` (delete the hairline frame and the re-weighting) → replace `tokens.css` with the §⑧ palette → update `brandTokens.test.ts`'s `readMaster()` and semantic mapping and `check-brand.mjs`'s gate ⑤ → regenerate every icon in both repos → run `vitest run` (brandTokens/semanticInk) + `node scripts/check-brand.mjs` + `generate-icons.mjs --check`.
2. **Batch 2 · Warden de-drift**: migrate `src/styles.css` to the family base, drop the cold blue, `data-theme` dark + a toggle switch, wire in the teal signature accent, swap in its own lantern icon (`src-tauri/icons/` regenerated by batch 1's generator).
3. **Batch 3 · typography**: self-host IBM Plex Serif, a modular type + spacing scale, applied to both repos' titles and key figures.
4. **Batch 4 · layout subtraction and hierarchy rework**: contextualise the right rail, make the floating bot yield, demote the metadata, subtract chrome.
5. **Batch 5 · verification**: both repos' guards all green (`check-brand` / `brandTokens` / `semanticInk` / `docs-mirror` / `i18n` / `npm run build`+`lint`+`test` / `cargo fmt`+`clippy`+`test`); start the dev servers and check the UI in the browser / the real app (**a UI change must be actually run, never reported green by word of mouth**).
6. **Written in the same batch**: CHANGELOG (one entry each in zh and en), `TODO.md`'s status snapshot, this ADR's "Landing record" section, the ADR index (one row each in zh and en; the next number increments from `0051`).

## Landing record

**Batch 1 (2026-11-05) · master art + palette (the atomic core)**

- The master redrawn as the inverse seal: `build/icon.svg` = a solid warm-ink field `#34322D` with the glyph knocked out in paper `#F7F4EE`, **the hairline frame removed**; added `build/icon-warden.svg` (the lantern/nightwatch symbol: a ring bail + flared hood + bulged glass + flared foot, `fill-rule="evenodd"` cutting the bail's inner ring).
- `generate-icons.mjs`'s `extractBrandParts()` now reads "the field rect's fill = ink, the glyph path's fill = paper"; the three domains drop the frame anchor: brand = the square seal verbatim, app = squircle, glyph = one colour on transparent (the activity bar / stripe use field/glyph; the old stroke constants are deleted).
- `BrandMark.tsx` drops the hairline frame and the whole `MIN_FRAME_PX` re-weighting apparatus (the seal is a flat field — there is no stroke to re-weight); at ≤16px it still collapses to a `currentColor` glyph.
- `tokens.css` moves to the §⑧ family palette; adds the spacing scale `--space-1..7` and `--transition-slow`.
- `brandTokens.test.ts`'s `readMaster()` reads the field/glyph pair, and the semantic mapping flips (`--brand-ink`=field, `--brand-paper`=glyph, light `--text-primary`=ink, dark `--text-primary`=paper).
- **Borderline values set**: the light `--accent` moves from the §⑧ draft `#A8471A` to `#A2441A` — the property assertion demands 4.5:1 against `--bg-active` (the draft managed only 4.36:1); the §⑧ table and the signature-colour prose were changed to `#A2441A` in the same batch, the dark `#E09155` untouched.
- All icons regenerated across both repos (including `.icns`/`.ico`/tray); `generate-icons --check --tracked-only` (this repo) green; `vitest run` 418 pass, `tsc` passes.
- **Corrected on review, 2026-11-06**: the claim that `check-brand` gate ⑤ had been rewritten for the inverse seal does not hold — that file has zero diff and stayed green only because "the master has no frame, so the old hairline branch is dead code". The Warden icon set was also **outside CI's `--check` scope** (CI never passes `--agent-repo`); it could only be verified by hand. Both were closed in the same batch: the Marketplace 40×40 pair joined the generator and `--check`, and `check-brand` gained exact checks for the window title, `productName` and the cross-repo `brand.json`.
- Negative case: the activity-bar glyph domain being near-invisible on a light ground is **by design** (the VS Code activity bar is dark in every theme); verified on the contact sheet.

**Batch 2 (2026-11-05) · Warden de-drift**

- `src/styles.css` migrates to the family base (same values as Cinder §⑧), the cold blue `#2563eb`/`#60a5fa` removed; `--danger`/`--info` added.
- Signature accent = Nightwatch Teal `#2F6B63` / dark `#5FB3A6`, measured ≥4.5:1 on every surface it lands on.
- `prefers-color-scheme`-only → `data-theme` + a three-state (system/light/dark) toggle: a pre-paint script in `index.html` prevents the flash, `main.ts`'s `initThemeToggle()` resolves and persists (key `warden-theme`), and an OS change is followed only in the system state.
- The brand band becomes the inverse-seal field (ink field + paper copy), measured 11.7:1.
- The lantern icons regenerated by the batch-1 generator via `--agent-repo` (app/tray/icns/ico).
- Run for real: `vite` dev server up, the dark/light/system cycle and its persistence checked in a browser; `tsc`, `check-brand`, `check-docs-mirror` green.

**Batch 3 (2026-11-05) · typography**

- Self-hosted IBM Plex Serif (SIL OFL 1.1): the latin 400/600 woff2 vendored into `web/src/assets/fonts/` (Cinder) and `src/assets/fonts/` (Warden), licence travelling with them; **no Google Fonts CDN**, the woff2 land in both repos' build output.
- Added `--font-display` and a modular type scale (`--text-display`…`--text-micro`); Cinder's h1/h2 and markdown headings and Warden's brand name / card titles set the serif; h3 and below stay sans (a serif at sub-label sizes reads as decoration, not hierarchy).
- Run for real: the browser confirms `document.fonts` has the 600 loaded and the h1's computed family is IBM Plex Serif; CJK falls through the generic `serif` (a Song-style face), the intended editorial read.

**Batch 4 (2026-11-05) · layout subtraction**

- The right rail's contextualisation decided: **collapsed by default** to a 30px handle (a first run no longer spends 288px permanently), pinned wide only by an explicit open (stores `0`); the folded handle carries a pending-count badge, so waiting context reaches the user without the rail occupying the page.
- The floating bot's yield and the 72ch body cap **stay as they are** (already satisfying §⑦); metadata demotion assessed as already met by the existing quiet styling, and no further chrome was touched this batch to avoid half-landed regressions.
- Run for real: the browser confirms a first run has no `aside`, the handle is present, and clicking it opens the rail and persists `0`.

**Batch 5 (2026-11-05) · verification**

- Cinder: `check-brand` / `check-docs-mirror` / `check-i18n-keys` / `check-version-drift` / `generate-icons --check` (this repo; the Warden set needs an explicit `--agent-repo`, which CI does not pass) / `vitest run` 418 / `tsc` / `vite build` all green.
- Warden: `check-brand` / `check-docs-mirror` / `tsc` / `vite build` all green.
- UI checked for real: Cinder dev (dark/light, the inverse-seal mark, the folded rail) and Warden dev (three-state theme, ink band, teal) both eyeballed in a browser.
