// BrandMark — the Cinder mark, drawn from the same construction as the
// brand SSOT (build/icon.svg) that generates the app icon, the favicons
// and the IDE extension icons.
//
// Why this exists: the navbar used a "▦" text glyph while every shipped
// artifact carried a different mark. Same product, two identities — and the
// glyph also inherited font metrics, so it shifted with the system font and
// broke at large sizes. One component, used everywhere in the app, keeps the
// UI on the same mark as the icon in the dock.
//
// v6 / ADR-0052 ("The Knot"): the mark is ONE closed band threaded through
// itself. At each crossing the under-strand is genuinely absent, so the
// master is three separate filled paths, not one letter. The paths and the
// group transform are generated into brandMarkPaths.ts; check-brand.mjs
// gate 5 byte-compares every one of them against the master.
//
// The inverse-seal palette is unchanged: a solid warm-ink field with the
// knot knocked out in off-white paper, no hairline frame.
//
// Two size regimes:
//
//   • Above GLYPH_ONLY_MAX the seal is drawn in full — the ink field with the
//     paper knot knocked out of it, exactly the master's geometry.
//
//   • At or below GLYPH_ONLY_MAX the field is dropped and only the knot is
//     drawn — the glyph-domain rule from the icon generator (16px slots hold
//     a shape, not a solid slab). The glyph takes currentColor so it belongs
//     to whatever surface it lands on.

import { BRAND_MARK_PATHS, BRAND_MARK_TRANSFORM } from './brandMarkPaths'

interface Props {
  /** Rendered width/height in px. The mark is square. */
  size?: number
  className?: string
  /** Accessible name. Omit when an adjacent text label already names the product. */
  title?: string
}

/**
 * At or below this rendered size the seal's field is dropped and only the
 * knot is drawn — the glyph-domain rule from the icon generator.
 */
const GLYPH_ONLY_MAX = 16

export default function BrandMark({ size = 26, className, title }: Props) {
  const glyphOnly = size <= GLYPH_ONLY_MAX

  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 512 512"
      className={className}
      role={title ? 'img' : 'presentation'}
      aria-label={title ?? undefined}
      aria-hidden={title ? undefined : true}
      focusable="false"
    >
      {title && <title>{title}</title>}
      {/* Solid warm-ink field — same fill as the master's field rect.
          Dropped in the glyph-only regime, where a solid field at 16px
          reads as a dark square rather than a shape. */}
      {!glyphOnly && <rect width="512" height="512" fill="#34322D" />}
      {/* Generated data, byte-compared with build/icon.svg by gate 5. */}
      <g transform={BRAND_MARK_TRANSFORM}>
        {BRAND_MARK_PATHS.map((d, i) => (
          <path
            key={i}
            d={d}
            fill={glyphOnly ? 'currentColor' : '#F7F4EE'}
            fillRule="nonzero"
          />
        ))}
      </g>
    </svg>
  )
}
