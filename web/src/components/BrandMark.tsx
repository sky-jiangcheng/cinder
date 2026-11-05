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
// The design is the inverse seal (ADR-0050): a solid warm-ink field with the
// letter knocked out in off-white paper — no hairline frame, because the
// mark's recognition comes from the reversed figure, not from an edge that
// has to be re-weighted to survive small sizes. That deletes the whole
// re-weighting apparatus the framed card needed: the field is a flat fill, so
// there is no stroke to fatten and no margin to preserve.
//
// Two size regimes:
//
//   • Above GLYPH_ONLY_MAX the seal is drawn in full — the ink field with the
//     paper letter knocked out of it, exactly the master's geometry.
//
//   • At or below GLYPH_ONLY_MAX the field is dropped and only the letter is
//     drawn — the glyph-domain rule from the icon generator (16px slots hold
//     a letter, not a solid slab). The glyph takes currentColor so it belongs
//     to whatever surface it lands on; a solid ink field at 16px would render
//     as one dark square.

interface Props {
  /** Rendered width/height in px. The mark is square. */
  size?: number
  className?: string
  /** Accessible name. Omit when an adjacent text label already names the product. */
  title?: string
}

/**
 * At or below this rendered size the seal's field is dropped and only the
 * letter is drawn — the glyph-domain rule from the icon generator (16px slots
 * hold a letter, not a solid slab).
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
      {/* Solid warm-ink field — the seal's ground. Same fill as the master's
          field rect in build/icon.svg; the letter is knocked out of it in
          paper below. Dropped in the glyph-only regime, where a solid field
          at 16px reads as a dark square rather than a letter. */}
      {!glyphOnly && <rect width="512" height="512" fill="#34322D" />}
      {/* Same geometry as build/icon.svg — the C knocked out of the field in
          off-white paper, at a 320-unit cap height, shifted 9 right / 2 up
          because the C's aperture opens to the right and the ink reads
          left-heavy. The letterform is the C from IBM Plex Sans Bold
          (© IBM Corp., SIL OFL 1.1), outlined. Process: outputs/icon-redesign-v5.

          In the glyph-only regime it takes currentColor instead of the brand
          paper: a monochrome letter belongs to its surface. */}
      <g transform="translate(21.06 13.06) scale(0.941176)">
        <path
          fill={glyphOnly ? 'currentColor' : '#F7F4EE'}
          fillRule="nonzero"
          d="M270.36 431.85Q235.78 431.85 208.01 420.89Q180.25 409.93 160.76 388.25Q141.28 366.57 130.56 334.18Q119.85 301.79 119.85 258.44Q119.85 215.57 130.56 182.2Q141.28 148.84 160.76 126.19Q180.25 103.54 208.01 91.85Q235.78 80.15 270.36 80.15Q317.61 80.15 348.3 99.4Q378.99 118.64 397.5 160.04L333.69 193.16Q326.87 171.73 312.5 158.82Q298.13 145.91 270.36 145.91Q237.73 145.91 218 167.1Q198.27 188.29 198.27 228.72V283.28Q198.27 323.71 218 344.9Q237.73 366.09 270.36 366.09Q297.64 366.09 313.96 351.23Q330.28 336.37 338.07 315.43L398.47 350.5Q379.47 389.47 348.54 410.66Q317.61 431.85 270.36 431.85Z"
        />
      </g>
    </svg>
  )
}
