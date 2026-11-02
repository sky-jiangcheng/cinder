// BrandMark — the Repotory mark, drawn from the same construction as the
// brand SSOT (build/icon.svg) that generates the app icon, the favicons
// and the IDE extension icons.
//
// Why this exists: the navbar used a "▦" text glyph while every shipped
// artifact carried a different mark. Same product, two identities — and the
// glyph also inherited font metrics, so it shifted with the system font and
// broke at large sizes. One component, used everywhere in the app, keeps the
// UI on the same mark as the icon in the dock.
//
// The design is the brand card: warm-paper field, one hairline ink frame,
// near-black R. The frame is what makes it read as a printed object rather
// than a drawn square — keep it if you touch this file.
//
// Two size regimes (ADR-0037):
//
//   • Above GLYPH_ONLY_MAX the card is drawn in full, but the frame is
//     RE-WEIGHTED for the rendered size. The master's 9-unit hairline is
//     right on the 512 grid and useless in the app: at the rail's 26px it
//     renders 0.46px, which the browser smears into a grey line — and that
//     line is the mark's only distinguishing feature. So the stroke scales
//     up until it is at least MIN_FRAME_PX wide on screen. This is the same
//     rule the icon generator already applies to the glyph domain
//     (scripts/generate-icons.mjs re-weights for small sizes); the UI was
//     the one surface that never got it.
//
//   • At or below GLYPH_ONLY_MAX the card collapses to the bare letter —
//     same as the glyph domain, where a framed card reads as a bright
//     square in a dark sidebar rather than as a letter. The glyph takes
//     currentColor so it belongs to whatever surface it lands on.
//
// Re-weighting moves the frame inward as the stroke grows, because a
// heavier frame cannot keep the master's inset and still leave a paper
// margin around itself: at 17px a 1px frame on the master's inset would
// swallow the card edge and the mark would read as a dark square. So the
// outer margin is held at MIN_MARGIN_PX too. At the master's own 512 the
// formula returns the master's geometry unchanged.

interface Props {
  /** Rendered width/height in px. The mark is square. */
  size?: number
  className?: string
  /** Accessible name. Omit when an adjacent text label already names the product. */
  title?: string
}

/** The brand master's grid. Every geometry below is expressed in it. */
const VIEWBOX = 512

/**
 * The master's frame weight, in viewBox units. Correct for the 512 grid
 * (the master itself, the app icon, the favicons); too thin for the app.
 */
const MASTER_STROKE = 9

/**
 * The master's frame inset, in viewBox units — where the frame's outer edge
 * sits, measured from the card's edge.
 */
const MASTER_INSET = 26

/**
 * How much paper the master leaves OUTSIDE its frame: inset minus half the
 * stroke. Holding this floor keeps the printed edge from swallowing the
 * card when the frame is re-weighted heavier.
 */
const MASTER_MARGIN = MASTER_INSET - MASTER_STROKE / 2

/**
 * The frame is the brand's recognition feature, so it must survive the
 * rendering: at least this many CSS pixels wide on screen. One CSS pixel
 * is also one physical pixel on a 1x display, so this is the floor that
 * holds everywhere.
 */
const MIN_FRAME_PX = 1

/**
 * The paper margin outside the frame, in CSS pixels. A frame with no paper
 * around it is not a printed edge, it is a dark border.
 */
const MIN_MARGIN_PX = 1

/**
 * At or below this rendered size the card's field and frame are dropped and
 * only the letter is drawn — the glyph-domain rule from the icon generator
 * (16px slots hold a letter, not a framed card).
 */
const GLYPH_ONLY_MAX = 16

export default function BrandMark({ size = 26, className, title }: Props) {
  const glyphOnly = size <= GLYPH_ONLY_MAX

  // Scale the master's stroke up until the rendered frame is at least
  // MIN_FRAME_PX wide. At the rail's 26px this yields 20 viewBox units
  // (1.02px on screen); at 512px it stays at the master's 9 units, so
  // the large-size rendering is still the master. The ceiling keeps the
  // rendered width from landing a hair under the floor on a size that
  // divides badly (17px would otherwise come out at 0.9999999px).
  const strokeWidth = Math.max(MASTER_STROKE, Math.ceil((MIN_FRAME_PX * VIEWBOX) / size))

  // ...and pull the frame inward by as much as that thickening costs, so at
  // least MIN_MARGIN_PX of paper survives around it.
  const margin = Math.max(MASTER_MARGIN, Math.ceil((MIN_MARGIN_PX * VIEWBOX) / size))
  const inset = Math.max(MASTER_INSET, margin + strokeWidth / 2)
  const frameSize = VIEWBOX - inset * 2

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
      {!glyphOnly && (
        <>
          <rect width="512" height="512" fill="#F5F0E4" />
          {/* Hairline frame (9 units on the 512 grid, inset 26) — the printed
              edge. Same construction as build/icon.svg, re-weighted for the
              rendered size so the edge actually reads. */}
          <rect
            x={inset}
            y={inset}
            width={frameSize}
            height={frameSize}
            fill="none"
            stroke="#1B1B1B"
            strokeWidth={strokeWidth}
          />
        </>
      )}
      {/* Same geometry as build/icon.svg — a filled R at a 320-unit cap
          height, shifted 6 right / 2 up because an R is heavier on the stem
          side and bounding-box centring reads left-heavy. The letterform is
          the R from IBM Plex Sans Bold (© IBM Corp., SIL OFL 1.1), outlined.
          Process: outputs/icon-redesign-v4.

          In the glyph-only regime it takes currentColor instead of the brand
          ink: a monochrome letter belongs to its surface, and the fixed
          near-black would vanish on a dark ground. */}
      <g transform="translate(21.06 13.06) scale(0.941176)">
        <path
          fill={glyphOnly ? 'currentColor' : '#1B1B1B'}
          fillRule="nonzero"
          d="M195.6 426H121.56V86H282.79Q306.17 86 325.17 93.79Q344.17 101.59 357.56 115.96Q370.96 130.33 378.26 150.3Q385.57 170.27 385.57 194.62Q385.57 229.21 370.23 255.03Q354.88 280.84 323.71 292.53L390.44 426H308.12L248.69 301.3H195.6ZM273.54 238.95Q290.1 238.95 299.6 230.43Q309.09 221.9 309.09 205.34V183.91Q309.09 167.35 299.6 158.82Q290.1 150.3 273.54 150.3H195.6V238.95Z"
        />
      </g>
    </svg>
  )
}
