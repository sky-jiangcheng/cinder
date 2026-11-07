# ADR-0052: Redraw the marks — The Knot (one band threading itself) and four derived domains

Status: **Accepted (2026-11-06, the user settled it with "land v6")**; amended 2026-11-07 (card margin, decision 9)
Date: 2026-11-06 (amended 2026-11-07)
Related: [ADR-0037](0037-brand-legibility-and-ui-language.md) (this supersedes "no recognition through a concrete shape"), [ADR-0050](0050-family-design-standard.md) (this supersedes decisions 3 & 4 — the master art and the Warden symbol; its other decisions stand), [ADR-0049](0049-brand-restructure-manus-style.md) (identifier layer frozen), [ADR-0048](0048-core-contract-version.md) (no contract bump here)
Starting schema: unchanged (no schema change, no MCP tool renames, no `CoreVersion` bump)

## Background

ADR-0050's master was an inverse seal of an IBM Plex Sans Bold "C"; Warden carried an abstract lantern. The user rejected three follow-ups in a row: v2 (literal objects / ornate badges / cartoon characters — "unmemorable, no craft"), v3 (six geometric candidates — "low"), v4 (The Ember Cut). The brief converged on: **very few colours, a shape that commands attention, simple but refined**, referencing Manus and ChatGPT — where "material" means a craft process (heavy ink impression + soft shadow), not drawing the product as an object.

ADR-0037 forbids building recognition on a concrete shape, while shareability demands a symbol one can draw by hand and read at 16px. Both hold only if **the shape is a reproducible invention expressed in parameters, not the outline of a recognisable object**.

## Decision

**1. The master is The Knot / 结.** A mark is one **closed equal-width band** following a closed curve that crosses itself. At each crossing the over-strand continues and the **under-strand is genuinely absent**. The cuts are parallel to the over-strand's edges, so the gap is exactly one band-width wide — geometry, not tweaking.

| | Curve | Crossings / lobes | Symmetry |
|---|---|---|---|
| **Cinder** | `z(t) = e^it + 2e^-2it` (trefoil) | 3 / 3 | 3-fold |
| **Warden** | `z(t) = cos t + i·sin 2t` (lemniscate) | 1 / 2 | 2-fold |

Four family rules: **equal-width band / cut weave / gap = band width / ink field, paper glyph**. What changes is the loop count N, not the weave. Cinder ties three things together (browse / edit / approve); Warden reads on one side and runs on the other, staying out of the middle.

**2. No mask.** With one colour and no mask, a true weave reads only if the under-strand is really missing; a mask would break the glyph domain, whose `glyphSvg()` only swaps fills. The master is therefore several paths (Cinder 3 / Warden 1); path order is irrelevant (no overlap).

**3. Equal width is measured, not asserted.** Both normalise to **27.25 u / 512** (Warden's `CURVES.band = 0.073104` solved against the normalised result); ink coverage 17.4% / 18.1%. Keeping Warden at 39.48u crushed the lemniscate's negative space into slits and smeared at 20px.

(Amended 2026-11-07: the card margin moved from 24u to 56u, so the band width and the ink coverage were recomputed against the new normalisation — what shrank is the mark's share of the card, not the weight of the stroke.)

**4. Four domains, one construction.** Brand master (flat, pure colour, token-bindable) · app domain (squircle + `lift` paper gradient + `press` soft shadow) · glyph domain (single colour, transparent) · **small domain (≤20px: solid silhouette on the same centre line, 1.86× stroke = 50.68u, no cuts)**. Master and small master live in `build/`; `scripts/generate-icons.mjs` derives every artefact and `renderPng()` switches domain by rendered size, so the threshold exists in one place.

**5. Palette unchanged.** ADR-0050's `#34322D` (ink field) / `#F7F4EE` (paper glyph) stand; signature accents (ember / teal) stay out of the mark and live in UI tokens and typography.

**6. The four guards were rewritten for multi-path.** (Added 2026-11-07: the VS Code webview panel has no `localResourceRoots`, so the mark can only be inlined into its HTML — and that inline copy was **hand-written and unguarded**. It kept the previous letterform through the v6 rebrand while every other surface had moved. It is generated now and gate 5 byte-compares it too; the negative case is verified: put the old paths back and the gate goes red.) `extractBrandParts()` now replaces fills globally; `check-brand.mjs` gate 5 went from "compare the first path" to **byte-comparing every path plus the `<g transform>`**; `BrandMark.tsx` takes its geometry from generated `web/src/components/brandMarkPaths.ts` (data only); `brandTokens.test.ts`'s two-colour binding is untouched. The guards are stronger, not relaxed.

**7. Copy shares the source.** `build/brand-copy.json` holds 25 zh/en baselines; `scripts/check-brand-copy.mjs` checks structure, banned words, positioning lines and the master's structural parameters.

**8. One atomic commit.** Changing a master turns the sync gates red immediately; 35 derivatives plus the new resources move together.

**9. A 56u card margin.** The master is itself "a dark card with the knot knocked out of it": the glyph lands on **78%** of the 512 grid, 56u of air on every side (Manus ≈74%, ChatGPT ≈70%, Apple's HIG content circle 80%). At the old 24u margin the glyph covered 91% of the card and every derived surface — squircle, marketplace card, JetBrains seal, favicon, navbar — read as cramped: the mark looked cropped by the card instead of sitting in it. The ratio is decided in the master, so one number reaches every face; the small domain's stroke goes 1.6× → **1.86×** to compensate the smaller master (27.25u × 1.86 = 50.68u, 1.58px at 16px).

## Consequences

- **Supersedes** ADR-0050 decisions 3 (the "C" inverse seal) and 4 (Warden's lantern); ADR-0037's "no recognition through a concrete shape" becomes "no literal objects — recognition through a reproducible construction". Both originals stay, with banners.
- **New resources**: `build/icon-small.svg`, `build/icon-warden-small.svg`, `build/brand-copy.json`, `web/src/components/brandMarkPaths.ts`, `scripts/check-brand-copy.mjs`.
- **Regenerated**: 9 in `build/icons`, 2 in `ide/vscode/media`, 4 JetBrains, 4 favicons, 18 in `warden/src-tauri/icons`, `warden/src/assets/warden.png`. The Marketplace inverted pair and the stripe icons must replace fills globally or a multi-path master shows mixed colours.
- **In-app**: Warden's companion lantern (eyes, smile) conflicts with v6's "no face, no letter, no ornament"; it becomes the 8-shaped mark. Element ids and drag behaviour are unchanged.
- **No** `CoreVersion` bump, no MCP renames, no schema change, no change to the four names in `brand.json`.
- **Amended 2026-11-07**: the master's margin moved 24u → 56u; the 35 derivatives + `warden/src/assets/warden.png` were regenerated in the same batch; the small domain's stroke went 1.6× → 1.86×; the band width / ink coverage figures in `build/brand-copy.json` and this ADR were updated to match; `check-brand.mjs` gate 5 now also byte-compares the webview's inlined mark.
- The handbook `brand/brand-system-v6.html` stays in the workspace (outside both repos) as the design record; inside the repos the sources of truth are `build/icon*.svg` and this ADR.
