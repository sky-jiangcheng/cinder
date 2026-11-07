// Regenerate every icon derivative from the single brand SSOT:
//   node scripts/generate-icons.mjs          # (re)generate all derivatives
//   node scripts/generate-icons.mjs --check  # verify derivatives are in sync
//   node scripts/generate-icons.mjs --check --tracked-only
//                                            # verify only the COMMITTED
//                                            # derivatives (CI runs this FIRST:
//                                            # checking after generating
//                                            # compares the generator with
//                                            //  itself and catches nothing)
//
// build/icon.svg is the single source of truth for the Cinder brand mark
// (the design rationale lives in the comment inside that file). Everything
// else is derived. Change the SSOT, re-run this script, and every surface
// shows the new mark. `--check` is wired into CI, so a rebrand can never
// ship half-applied — that gate exists because hand-kept copies are how
// this brand drifted for three releases running (the activity bar, the
// tool window, the favicons and the README banner each missed a move).
//
// The agent product (Warden) has its OWN SSOT, build/icon-warden.svg: a
// separate product cannot wear the desktop app's mark (ADR-0050
// decision ②). It is drawn by hand but takes its two colours from the same
// palette as the Cinder SSOT, and it flows through the same three shape
// domains below, so the sync gate covers it too.
//
// FOUR SHAPE DOMAINS (ADR-0052 "The Knot" / 结; see CONTRIBUTING):
//   brand domain — the SSOT verbatim: a flat, square-edged ink field with
//     the woven band knocked out in paper. Token-bindable, no gradients.
//     Favicons, marketplace icons, the JetBrains in-panel mark.
//   app domain — the same mark in a macOS squircle, with the v6 material
//     layer (`lift` paper gradient + `press` soft impression shadow).
//     appicon.png, the size ladder, ico/icns, the whole Tauri set.
//   glyph domain — one colour on a transparent background, for slots the
//     host renders as a mask or a monochrome stripe (VS Code activity bar,
//     JetBrains tool window, macOS menu bar). The alpha shape must BE the
//     mark; inkCoverage() fails an opaque slab.
//   small domain — at <= SMALL_DOMAIN_MAX the woven cuts are sub-pixel, so
//     a solid stroked silhouette on the same centre line takes over
//     (1.6x stroke, no cuts). renderPng() switches by rendered size, so
//     ICO/ICNS frames and the Tauri ladder all follow automatically.
//
// The master can hold SEVERAL paths (Cinder 3, Warden 1): at each crossing
// the under-strand is genuinely absent. Every fill replacement over the
// letterform group is therefore global; a single replace would leave the
// inverted Marketplace pair with mixed colours.
//
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import sharp from 'sharp'

const SIZES = [512, 256, 128, 64, 48, 32, 16]

// v6 / ADR-0052: at this rendered size and below the woven knot's cuts
// are sub-pixel, so the solid-stroke small-domain master takes over.
const SMALL_DOMAIN_MAX = 20

const root = process.cwd()
const svgPath = path.join(root, 'build/icon.svg')
const outDir = path.join(root, 'build/icons')

// Marketplace-facing bitmap: the VS Code extension icon (media/icon.png)
// is the brand mark at 128×128, the size the marketplace requires.
const MARKETPLACE_ICON_SIZE = 128
const marketplaceIconPng = path.join(outDir, `marketplace-icon-${MARKETPLACE_ICON_SIZE}.png`)

// macOS app-icon container: Apple's squircle ≈ 22.37% of the icon size
// (the Big Sur-and-later official proportion), approximated here as a
// superellipse with n≈5.2 — visually indistinguishable from the system
// squircle at every shipped size, and unlike a circular border-radius it
// stays smooth all the way into the corner.
const SQUIRCLE_N = 5.2
const SQUIRCLE_STEPS = 360

function sha(buf) {
  return crypto.createHash('sha256').update(buf).digest('hex')
}

// Superellipse outline filling a `size` box, centred. `half` shrinks the
// axes for concentric insets (the inner frame); the centre never moves.
function squirclePath(size, half = size / 2, n = SQUIRCLE_N, steps = SQUIRCLE_STEPS) {
  const c = size / 2
  const e = 2 / n
  const pts = []
  for (let i = 0; i < steps; i++) {
    const t = (i / steps) * Math.PI * 2
    const cos = Math.cos(t)
    const sin = Math.sin(t)
    const x = c + half * Math.sign(cos) * Math.abs(cos) ** e
    const y = c + half * Math.sign(sin) * Math.abs(sin) ** e
    pts.push(`${x.toFixed(1)} ${y.toFixed(1)}`)
  }
  return `M${pts.join('L')}Z`
}

// Pull the parts the app-domain variant re-assembles out of the SSOT:
// the field colour, the glyph colour and the glyph group. Extracted
// rather than hardcoded — hardcoded copies of SSOT values are exactly the
// drift this pipeline exists to prevent. A structural change to the SSOT
// that breaks extraction fails loudly here instead of shipping a stale
// shape.
//
// The SSOT is the "inverse seal" (ADR-0050 decision ③): a SOLID warm-ink
// field with the glyph knocked out in paper, and NO hairline frame. So the
// two brand colours are the field rect's fill (the ink) and the glyph
// path's fill (the paper); there is no frame rect to read.
function extractBrandParts(svgText) {
  const field = /<rect width="512" height="512" fill="([^"]+)"/.exec(svgText)
  const glyph = /<path[^>]*\sfill="([^"]+)"/.exec(svgText)
  const letterform = /<g transform="translate[\s\S]*?<\/g>/.exec(svgText)
  if (!field || !glyph || !letterform) {
    throw new Error('generate-icons: build/icon.svg structure changed (field/glyph/letterform not found) — update extractBrandParts')
  }
  return {
    field: field[1],
    glyph: glyph[1],
    letterform: letterform[0],
  }
}

// The glyph domain: the mark cut to one colour on a transparent background.
//
// The colour is the PAPER, not the ink. The activity bar is dark
// in every VS Code theme (activityBar.background is #333333 / #2C2C2C),
// so a near-black glyph would disappear if a host ever rendered the file
// unmasked; under the mask the colour is ignored anyway.
//
// The inverse seal has no frame, so the glyph domain is the GLYPH alone —
// the alpha shape has to BE the mark, and a solid field here would fill the
// whole slot and render as one flat slab under a mask.
function glyphSvg(parts, size) {
  const letter = parts.letterform.replace(/fill="[^"]+"/g, `fill="${parts.glyph}"`)
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="${size}" height="${size}" role="img" aria-label="Cinder">
  <!-- Glyph-domain cut of build/icon.svg (brand SSOT): one colour, transparent
       background — for slots the host renders as a mask. See CONTRIBUTING
       "Brand icon domains". Generated by scripts/generate-icons.mjs; do not edit. -->
  ${letter}
</svg>
`
}

/**
 * Share of the box the glyph's ink covers.
 *
 * A glyph that fills its box is the bug this gate exists for: masked
 * rendering paints an opaque background as one solid slab (the activity bar
 * showed a white square). The card form covers ~100%; the cut glyph ~35%.
 */
async function inkCoverage(svgText) {
  const png = await renderPng(svgText, 64)
  const { data, info } = await sharp(png).raw().toBuffer({ resolveWithObject: true })
  let opaque = 0
  for (let p = 0; p < data.length; p += info.channels) {
    if (data[p + 3] > 8) opaque++
  }
  return opaque / (info.width * info.height)
}

/**
 * The ink bounding box of an SVG, in its own user space.
 *
 * Used to frame the JetBrains stripe glyph: the letterform's position inside
 * the 512 grid is a design decision (the C sits 9 units right and 2 up of
 * dead centre), so the glyph's viewBox has to be derived from where the
 * letter actually IS — not hardcoded, or a rebrand of the letterform would
 * silently break the framing.
 */
async function alphaBBox(svgText, size = 512) {
  const png = await renderPng(svgText, size)
  const { data, info } = await sharp(png).raw().toBuffer({ resolveWithObject: true })
  let minX = Infinity
  let minY = Infinity
  let maxX = -1
  let maxY = -1
  for (let y = 0; y < info.height; y++) {
    for (let x = 0; x < info.width; x++) {
      const p = (y * info.width + x) * info.channels
      if (data[p + 3] > 8) {
        if (x < minX) minX = x
        if (x > maxX) maxX = x
        if (y < minY) minY = y
        if (y > maxY) maxY = y
      }
    }
  }
  if (maxX < 0) throw new Error('generate-icons: the letterform rendered empty — cannot frame the glyph')
  const scale = 512 / size
  return {
    x: minX * scale,
    y: minY * scale,
    width: (maxX - minX + 1) * scale,
    height: (maxY - minY + 1) * scale,
  }
}

/** The letterform alone on a transparent field — the shape a stripe icon shows. */
function letterOnlySvg(parts, fill) {
  const letter = parts.letterform.replace(/fill="[^"]+"/g, `fill="${fill}"`)
  return `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512" fill="none">${letter}</svg>`
}

/**
 * The JetBrains Marketplace pair: the seal at 40×40, drawn in the 512 grid
 * and scaled by the viewBox.
 *
 * The dark variant is a colour inversion, not a different drawing: an ink
 * field at #34322D sits at roughly 1.2:1 against a Darcula list background,
 * so the mark would read as a dark hole. Paper field + ink letter keeps the
 * same two-colour object legible under the other light.
 *
 * This pair used to be hand-maintained outside the generator ("the dark
 * variant is an inversion, not a resize") — which is exactly how it kept a
 * previous letterform for three redesigns. It is generated now.
 */
function sealSquareSvg(parts, size, inverted) {
  const field = inverted ? parts.glyph : parts.field
  const glyph = inverted ? parts.field : parts.glyph
  const letter = parts.letterform.replace(/fill="[^"]+"/g, `fill="${glyph}"`)
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 512 512">
  <!-- Marketplace icon (${size}x${size}) generated from build/icon.svg (the brand
       SSOT): the inverse seal — solid field, letterform knocked out, no frame.
       ${inverted ? 'Colour-inverted for dark UI lists.' : 'For light UI lists.'} Generated by
       scripts/generate-icons.mjs; do not edit. -->
  <rect width="512" height="512" fill="${field}"/>
  ${letter}
</svg>
`
}

// The web UI's own copy of the letterform, as DATA. A hand-copied <path d>
// in BrandMark.tsx used to be byte-compared against only the FIRST master
// path; the v6 master has three. Generating the whole list keeps gate 5
// honest: it compares every path, not one of them.
function brandMarkPathsModule(masterText) {
  const paths = [...masterText.matchAll(/<path[^>]*\sd="([^"]+)"/g)].map((m) => m[1])
  const transform = /<g transform="([^"]+)"/.exec(masterText)?.[1]
  if (!paths.length || !transform) throw new Error('generate-icons: cannot derive web UI mark from master')
  return `// Generated from build/icon.svg by scripts/generate-icons.mjs — do not edit.
// check-brand.mjs gate 5 byte-compares every path and the group transform
// against the master, so a rebrand cannot leave the navbar behind.
export const BRAND_MARK_TRANSFORM = '${transform}'
export const BRAND_MARK_PATHS: string[] = [
${paths.map((d) => `  '${d}',`).join('\n')}
]
`
}

// The app-domain mark: SSOT letterform and colours inside the squircle
// container. The inverse seal carries no hairline frame, so there is no
// inner curve to re-draw concentrically — the container holds the solid
// field and the knocked-out glyph, nothing else.
function appMarkSvg(parts, label = 'Cinder') {
  // v6 / ADR-0052: the app domain carries the material layer — a paper
  // gradient along the glyph and a soft drop shadow (letterpress). The
  // brand master stays flat so the token-bound monochrome domains survive.
  const letter = parts.letterform.replace(/fill="[^"]+"/g, 'fill="url(#lift)"')
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512" role="img" aria-label="${label}">
  <!-- App-domain variant of the brand SSOT: same letterform, same colours,
       inside the macOS squircle container, with the v6 material layer.
       Generated by scripts/generate-icons.mjs; do not edit. -->
  <defs>
    <linearGradient id="lift" gradientUnits="userSpaceOnUse" x1="58" y1="38" x2="446" y2="474">
      <stop offset="0" stop-color="#FFFDF6"/>
      <stop offset="0.44" stop-color="${parts.glyph}"/>
      <stop offset="1" stop-color="#D6CEBD"/>
    </linearGradient>
    <filter id="press" x="-22%" y="-22%" width="144%" height="150%">
      <feDropShadow dx="0" dy="7" stdDeviation="8" flood-color="#100E0C" flood-opacity="0.45"/>
    </filter>
  </defs>
  <path d="${squirclePath(512)}" fill="${parts.field}"/>
  <g filter="url(#press)">${letter}</g>
</svg>
`
}

// The small-domain master is a square brand card whose glyph is a solid
// stroked centre line. Re-container it into the same squircle; the
// silhouette needs no lift gradient (it would be invisible at 16px anyway).
function smallAppSvg(smallText) {
  const m = /<rect width="512" height="512" fill="([^"]+)"\/>/.exec(smallText)
  if (!m) throw new Error('generate-icons: small-domain master has no field rect')
  const squircle = `<path d="${squirclePath(512)}" fill="${m[1]}"/>`
  return smallText.replace(/<rect width="512" height="512" fill="[^"]+"\/>/, squircle)
}

// A mark is either one SVG (string/Buffer) or a { normal, small } pair.
// Every raster target — size ladder, ICO frames, ICNS chunks, Tauri set —
// goes through here, so the small-domain switch is one place, not N.
function svgFor(mark, size) {
  if (typeof mark === 'string' || Buffer.isBuffer(mark)) return mark
  return size <= SMALL_DOMAIN_MAX && mark.small ? mark.small : mark.normal
}

async function renderPng(mark, size) {
  const svg = svgFor(mark, size)
  // sharp treats a string argument as a file path; both domains hand us
  // strings and buffers, so normalise here.
  return sharp(Buffer.isBuffer(svg) ? svg : Buffer.from(svg)).resize(size, size).png().toBuffer()
}

function buildIco(svg, icoSizes = [256, 128, 64, 48, 32, 16]) {
  // ICO container: 6-byte header, one 16-byte directory entry per size,
  // then the embedded PNGs. Sizes >=256 are recorded as 0 per spec.
  return Promise.all(icoSizes.map((s) => renderPng(svg, s))).then((bufs) => {
    const entries = bufs.map((buf, i) => ({ size: icoSizes[i], buf }))
    const headerSize = 6 + entries.length * 16
    let offset = headerSize
    let total = headerSize
    for (const e of entries) total += e.buf.length
    const ico = Buffer.alloc(total)
  ico.writeUInt16LE(0, 0)
  ico.writeUInt16LE(1, 2) // type: icon
  ico.writeUInt16LE(entries.length, 4)
    let at = 6
    for (const e of entries) {
      ico.writeUInt8(e.size >= 256 ? 0 : e.size, at)
      ico.writeUInt8(e.size >= 256 ? 0 : e.size, at + 1)
      ico.writeUInt16LE(1, at + 4) // planes
      ico.writeUInt32LE(32, at + 6) // bpp
      ico.writeUInt32LE(e.buf.length, at + 8)
      ico.writeUInt32LE(offset, at + 12)
      e.buf.copy(ico, offset)
      offset += e.buf.length
      at += 16
    }
    return ico
  })
}

/**
 * The ICNS container (Apple).
 *
 * An 8-byte header ('icns' + total file length, big-endian) followed by
 * typed chunks: a 4-byte type, a 4-byte big-endian length, then the data.
 * The length counts the 8 bytes of the chunk header as well, so a reader
 * advances by the number it finds. Since macOS 10.7 a chunk may hold a
 * PNG, which is what every modern icon set ships — the chunk list below
 * is the one `iconutil` produces from an .iconset, so the result is a
 * file macOS reads exactly like a hand-made one.
 */
function buildIcn(chunks) {
  const parts = []
  for (const { type, buf } of chunks) {
    const header = Buffer.alloc(8)
    header.write(type, 0, 4, 'ascii')
    header.writeUInt32BE(buf.length + 8, 4)
    parts.push(header, buf)
  }
  const body = Buffer.concat(parts)
  const out = Buffer.alloc(8 + body.length)
  out.write('icns', 0, 4, 'ascii')
  out.writeUInt32BE(out.length, 4)
  body.copy(out, 8)
  return out
}

/**
 * The square frame around the letterform's own bounding box.
 *
 * The letter's position inside the 512 grid is a design decision, so the
 * frame is derived from where the letter actually IS — not hardcoded, or a
 * rebrand of the letterform would silently break the framing.
 */
async function letterFrame(parts) {
  const box = await alphaBBox(letterOnlySvg(parts, parts.field))
  const side = Math.max(box.width, box.height) * 1.1
  return {
    x: box.x + box.width / 2 - side / 2,
    y: box.y + box.height / 2 - side / 2,
    side,
  }
}

async function pixelDiff(bufA, bufB) {
  // Both buffers are PNG encodings of the same dimensions from the same
  // encoder family. Compare raw pixels; return the share of pixels whose
  // channels differ beyond the allowance.
  const [a, b] = await Promise.all([
    sharp(bufA).raw().toBuffer({ resolveWithObject: true }),
    sharp(bufB).raw().toBuffer({ resolveWithObject: true }),
  ])
  if (a.info.width !== b.info.width || a.info.height !== b.info.height) {
    return 1 // dimension drift = totally stale
  }
  const ch = Math.min(a.info.channels, b.info.channels)
  let diff = 0
  const total = a.info.width * a.info.height
  for (let p = 0; p < total; p++) {
    for (let c = 0; c < ch; c++) {
      if (Math.abs(a.data[p * a.info.channels + c] - b.data[p * b.info.channels + c]) > 3) {
        diff++
        break
      }
    }
  }
  return diff / total
}

async function main() {
  const check = process.argv.includes('--check')
  // --agent-repo <path> extends the run to the sibling checkout of the
  // standalone copilot app: its Tauri's icon set is the same brand mark in
  // the same app domain, so it is derived here rather than hand-edited
  // there. Opt-in, because the default run must not reach into another
  // working tree.
  const agentFlag = process.argv.indexOf('--agent-repo')
  const agentRepo = agentFlag >= 0 ? process.argv[agentFlag + 1] : null
  // --tracked-only checks just the artefacts that are COMMITTED to the repo.
  // The rest (build/icons/*, build/appicon.png) are gitignored intermediates:
  // a fresh checkout has none of them, so they cannot be verified until they
  // are generated. CI runs `--check --tracked-only` FIRST — that is the gate
  // that catches somebody editing build/icon.svg or a committed derivative
  // without re-running the generator. (Running the generator first and
  // checking afterwards compares the generator with itself and catches
  // nothing.)
  const trackedOnly = process.argv.includes('--tracked-only')
  const svg = fs.readFileSync(svgPath)
  const svgText = svg.toString()
  const smallSvgText = fs.readFileSync(path.join(root, 'build/icon-small.svg')).toString()
  const wardenSmallSvgText = fs.readFileSync(path.join(root, 'build/icon-warden-small.svg')).toString()
  const parts = extractBrandParts(svgText)
  const brandSvg = { normal: svg, small: smallSvgText }
  const appSvg = { normal: appMarkSvg(parts), small: smallAppSvg(smallSvgText) }

  // The agent product's own SSOT (build/icon-warden.svg): same palette and
  // same three domains, its own glyph. Loaded unconditionally so a broken
  // Warden master fails the run even when the caller did not name the agent
  // repo — the sync gate covers it either way.
  const wardenSvgText = fs.readFileSync(path.join(root, 'build/icon-warden.svg')).toString()
  const wardenParts = extractBrandParts(wardenSvgText)
  const wardenAppSvg = { normal: appMarkSvg(wardenParts, 'Warden'), small: smallAppSvg(wardenSmallSvgText) }

  if (!check) fs.mkdirSync(outDir, { recursive: true })

  const stale = []

  // 1. App domain — squircle container (macOS / Windows OS icons).
  //    Size ladder + Wails appicon (build/appicon.png is the Wails default
  //    icon source) + Windows .ico. The system will not mask these for us.
  if (!trackedOnly) {
    for (const size of SIZES) {
      const png = await renderPng(appSvg, size)
      const file = path.join(outDir, `icon-${size}.png`)
      if (check) {
        if (!fs.existsSync(file)) stale.push(`${file} (missing)`)
        else if ((await pixelDiff(png, fs.readFileSync(file))) > 0.005) stale.push(`${file} (pixel drift)`)
      } else {
        fs.writeFileSync(file, png)
        console.log(`  ✓ icon-${size}.png (app domain, squircle)`)
      }
    }
  }

  const appIcon = path.join(root, 'build/appicon.png')
  const appPng = await renderPng(appSvg, 512)
  if (trackedOnly) {
    // gitignored intermediate — nothing committed depends on it.
  } else if (check) {
    if (!fs.existsSync(appIcon)) stale.push(`${appIcon} (missing)`)
    else if ((await pixelDiff(appPng, fs.readFileSync(appIcon))) > 0.005) stale.push(`${appIcon} (pixel drift)`)
  } else {
    fs.writeFileSync(appIcon, appPng)
    console.log('  ✓ appicon.png (512x512 for Wails, app domain, squircle)')
  }

  const ico = await buildIco(appSvg)
  const icoPath = path.join(outDir, 'icon.ico')
  if (trackedOnly) {
    // gitignored intermediate — nothing committed depends on it.
  } else if (check) {
    if (!fs.existsSync(icoPath) || sha(ico) !== sha(fs.readFileSync(icoPath))) stale.push(`${icoPath} (stale)`)
  } else {
    fs.writeFileSync(icoPath, ico)
    console.log(`  ✓ icon.ico (6 sizes, app domain, squircle)`)
  }

  // 2. Brand domain — the SSOT verbatim: square-edged printed card.
  //    Browser favicons: the .ico is the fallback every browser requests
  //    without a <link>; the .svg is the same mark for pages that declare
  //    one (web/index.html and the rebuilt docs pages do).
  const faviconSvg = svg
  if (check) {
    const favPath = path.join(root, 'docs/favicon.svg')
    if (!fs.existsSync(favPath) || sha(faviconSvg) !== sha(fs.readFileSync(favPath))) {
      stale.push(`${favPath} (stale)`)
    }
  } else {
    fs.writeFileSync(path.join(root, 'docs/favicon.svg'), faviconSvg)
    console.log('  ✓ docs/favicon.svg (brand domain, square card)')
  }

  // The favicon .ico is its own build from the brand mark — NOT a copy of
  // the app-domain icon.ico: the OS icon wears the squircle, the browser
  // tab wears the square card, same letterform, different containers.
  const faviconIco = await buildIco(brandSvg)
  const DERIVED = [
    { buf: faviconIco, rel: 'docs/favicon.ico' },
    { buf: faviconIco, rel: 'web/public/favicon.ico' },
    { buf: faviconSvg, rel: 'web/public/favicon.svg' },
  ]
  for (const d of DERIVED) {
    const gen = d.buf
    const target = path.join(root, d.rel)
    if (check) {
      if (!fs.existsSync(target)) stale.push(`${target} (missing)`)
      else if (sha(gen) !== sha(fs.readFileSync(target))) stale.push(`${target} (stale)`)
    } else {
      fs.mkdirSync(path.dirname(target), { recursive: true })
      fs.writeFileSync(target, gen)
      console.log(`  ✓ ${d.rel} (brand domain)`)
    }
  }

  // 2b. JetBrains Marketplace pair (40×40) — brand domain, generated so the
  //     store listing can never keep a previous letterform. The pair used to
  //     be hand-scaled outside this script, and it silently held the R from
  //     two redesigns ago.
  const MARKETPLACE_PAIR = [
    { rel: 'ide/jetbrains/src/main/resources/META-INF/pluginIcon.svg', svg: sealSquareSvg(parts, 40, false) },
    { rel: 'ide/jetbrains/src/main/resources/META-INF/pluginIcon_dark.svg', svg: sealSquareSvg(parts, 40, true) },
  ]
  for (const d of MARKETPLACE_PAIR) {
    const target = path.join(root, d.rel)
    if (check) {
      if (!fs.existsSync(target)) stale.push(`${target} (missing)`)
      else if (sha(d.svg) !== sha(fs.readFileSync(target))) stale.push(`${target} (stale)`)
    } else {
      fs.writeFileSync(target, d.svg)
      console.log(`  ✓ ${d.rel} (brand domain, 40x40)`)
    }
  }

  // 2c. Web UI mark data — every master path, generated not hand-copied.
  const pathsModule = brandMarkPathsModule(svgText)
  const pathsTarget = path.join(root, 'web/src/components/brandMarkPaths.ts')
  if (check) {
    if (!fs.existsSync(pathsTarget)) stale.push(`${pathsTarget} (missing)`)
    else if (sha(Buffer.from(pathsModule)) !== sha(fs.readFileSync(pathsTarget))) stale.push(`${pathsTarget} (stale)`)
  } else {
    fs.writeFileSync(pathsTarget, pathsModule)
    console.log('  ✓ web/src/components/brandMarkPaths.ts (web UI mark data)')
  }

  // 3. VS Code marketplace icon (media/icon.png, 128×128) — brand domain:
  //    store listings render in square or system-shaped thumbnails, the
  //    card edge is the recognition.
  const marketplacePng = await renderPng(brandSvg, MARKETPLACE_ICON_SIZE)
  const vscodeIcon = path.join(root, 'ide/vscode/media/icon.png')
  if (trackedOnly) {
    // Only the committed marketplace icon is verified here; the PNG in
    // build/icons/ is a gitignored intermediate.
    if (!fs.existsSync(vscodeIcon)) stale.push(`${vscodeIcon} (missing)`)
    else if (sha(marketplacePng) !== sha(fs.readFileSync(vscodeIcon))) stale.push(`${vscodeIcon} (stale)`)
  } else if (check) {
    if (!fs.existsSync(marketplaceIconPng)) stale.push(`${marketplaceIconPng} (missing)`)
    else if ((await pixelDiff(marketplacePng, fs.readFileSync(marketplaceIconPng))) > 0.005) {
      stale.push(`${marketplaceIconPng} (pixel drift)`)
    }
    if (!fs.existsSync(vscodeIcon)) stale.push(`${vscodeIcon} (missing)`)
    else if (sha(marketplacePng) !== sha(fs.readFileSync(vscodeIcon))) stale.push(`${vscodeIcon} (stale)`)
  } else {
    fs.writeFileSync(marketplaceIconPng, marketplacePng)
    fs.writeFileSync(vscodeIcon, marketplacePng)
    console.log(`  ✓ build/icons/marketplace-icon-${MARKETPLACE_ICON_SIZE}.png (brand domain)`)
    console.log('  ✓ ide/vscode/media/icon.png (128x128 from build/icon.svg, brand domain)')
  }

  // 4. VS Code activity-bar icon (media/repotory.svg)
  //    GLYPH domain, not the card. VS Code renders an extension's
  //    activity-bar icon as a MASK: it takes the SVG's alpha channel and
  //    paints it in the theme's icon foreground. The card's opaque cream
  //    field covered the whole 24×24, so the mask was a solid rectangle —
  //    the activity bar showed a white square and the letter vanished into it
  //    (reproduced by rendering the shipped file through a CSS mask).
  //    The alpha shape has to BE the mark, so this cut keeps the glyph and
  //    drops the field.
  const ACTIVITY_BAR_SIZE = 24
  const activityBarSvg = glyphSvg(parts, ACTIVITY_BAR_SIZE)
  const activityBarIcon = path.join(root, 'ide/vscode/media/repotory.svg')
  if (check) {
    if (!fs.existsSync(activityBarIcon)) stale.push(`${activityBarIcon} (missing)`)
    else if (sha(Buffer.from(activityBarSvg)) !== sha(fs.readFileSync(activityBarIcon))) {
      stale.push(`${activityBarIcon} (stale)`)
    }
    // The guard for the bug this domain exists for: a masked slot must
    // never hold a filled shape, or it renders as one flat slab.
    const coverage = await inkCoverage(activityBarSvg)
    if (coverage > 0.75) {
      stale.push(`${activityBarIcon} (opaque slab: ink covers ${(coverage * 100).toFixed(0)}% of the box — masked rendering would paint a solid block)`)
    }
  } else {
    fs.writeFileSync(activityBarIcon, activityBarSvg)
    const coverage = await inkCoverage(activityBarSvg)
    console.log(`  ✓ ide/vscode/media/repotory.svg (activity bar, 24px glyph domain, ink ${(coverage * 100).toFixed(0)}% of the box)`)
  }

  // 5. JetBrains tool-window stripe icon (icons/repotory.svg + _dark.svg)
  //    GLYPH domain — a 16px monochrome LETTER on transparent, in the two
  //    theme colours IntelliJ's own stripe icons use.
  //
  //    The card was the wrong shape for this slot and was replaced on
  //    2026-10-29: IntelliJ rasterizes plugin SVG icons as plain images (no
  //    mask) and picks the `_dark` sibling under a dark theme, so the mark
  //    has to carry its own colour — and a cream card at stripe size is a
  //    bright slab whose hairline frame is sub-pixel, which reads as a
  //    deformed blob rather than a letter.
  //
  //    16px, not 13: the bundled dev plugin ships its Statistics stripe
  //    icon at 16×16, and a 13px icon is scaled up by the stripe button.
  //    The viewBox is framed from where the letter actually sits in the
  //    512 grid, so the letter fills the slot without being cropped.
  const TOOL_WINDOW_SIZE = 16
  const STRIPE_INK_LIGHT = parts.field // #34322D — brand ink, light theme
  const STRIPE_INK_DARK = parts.glyph // #F7F4EE — brand paper, dark theme
  const frame = await letterFrame(parts)
  const smallLetterform = /<g transform="translate[\s\S]*?<\/g>/.exec(smallSvgText)
  if (!smallLetterform) throw new Error('generate-icons: small-domain master has no glyph group')
  const stripeSvg = (ink) => {
    // 16px: solid stroked centre line of the small domain, no woven cuts.
    const letter = smallLetterform[0].replace(/stroke="[^"]+"/g, `stroke="${ink}"`)
    return `<svg xmlns="http://www.w3.org/2000/svg" width="${TOOL_WINDOW_SIZE}" height="${TOOL_WINDOW_SIZE}" viewBox="${frame.x.toFixed(2)} ${frame.y.toFixed(2)} ${frame.side.toFixed(2)} ${frame.side.toFixed(2)}" fill="none">
  <!-- Tool-window stripe icon: the letterform of build/icon.svg (brand
       SSOT), cut to one colour on a transparent field — the glyph domain.
       IntelliJ picks the _dark sibling under a dark theme, the same
       convention as the platform's own stripe icons.
       Generated by scripts/generate-icons.mjs; do not edit by hand. -->
  ${letter}
</svg>
`
  }
  const stripeTargets = [
    { rel: 'ide/jetbrains/src/main/resources/icons/repotory.svg', svg: stripeSvg(STRIPE_INK_LIGHT) },
    { rel: 'ide/jetbrains/src/main/resources/icons/repotory_dark.svg', svg: stripeSvg(STRIPE_INK_DARK) },
  ]
  for (const target of stripeTargets) {
    const file = path.join(root, target.rel)
    if (check) {
      if (!fs.existsSync(file)) stale.push(`${file} (missing)`)
      else if (sha(Buffer.from(target.svg)) !== sha(fs.readFileSync(file))) stale.push(`${file} (stale)`)
    } else {
      fs.mkdirSync(path.dirname(file), { recursive: true })
      fs.writeFileSync(file, target.svg)
      console.log(`  ✓ ${target.rel} (tool window stripe, ${TOOL_WINDOW_SIZE}px letter-only glyph)`)
    }
  }

  // 6. JetBrains in-panel brand mark (icons/repotory-mark.png)
  //    BRAND domain — the card itself, at 96px (rendered at 44px in the
  //    panel, so the 2× source stays crisp on a Retina display).
  //
  //    This file used to be a hand-made copy, which is exactly what this
  //    pipeline exists to prevent: it drifted from the SSOT silently. It is
  //    generated now, so the CI sync gate covers it too.
  const MARK_PNG_SIZE = 96
  const markPng = await renderPng(brandSvg, MARK_PNG_SIZE)
  const markFile = path.join(root, 'ide/jetbrains/src/main/resources/icons/repotory-mark.png')
  if (check) {
    if (!fs.existsSync(markFile)) stale.push(`${markFile} (missing)`)
    else if ((await pixelDiff(markPng, fs.readFileSync(markFile))) > 0.005) stale.push(`${markFile} (pixel drift)`)
  } else {
    fs.mkdirSync(path.dirname(markFile), { recursive: true })
    fs.writeFileSync(markFile, markPng)
    console.log(`  ✓ ide/jetbrains/src/main/resources/icons/repotory-mark.png (${MARK_PNG_SIZE}px brand card)`)
  }

  // 7. The agent repo (the standalone copilot app) — Tauri's icon set.
  //
  //    The agent is a SEPARATE product with its OWN name (Warden) and its
  //    OWN master (build/icon-warden.svg), so it cannot wear the desktop
  //    app's icon; it wears its own mark in the same APP DOMAIN (the
  //    squircle), because Tauri's set is consumed as an OS app icon and the
  //    system will not mask it for us.
  //
  //    It lives in another checkout, so this section only runs when the
  //    caller names the repo (`--agent-repo <path>`). The default run must
  //    never reach into a sibling working tree.
  if (agentRepo) {
    const iconsDir = path.join(path.resolve(agentRepo), 'src-tauri', 'icons')
    const wardenFrame = await letterFrame(wardenParts)

    // The file names are Tauri's contract: `tauri icon` emits exactly this
    // set and the bundler looks the files up by name. The Square* ladder is
    // the Windows Store set, StoreLogo the store listing thumbnail.
    const TAURI_PNGS = [
      ['32x32.png', 32],
      ['64x64.png', 64],
      ['128x128.png', 128],
      ['128x128@2x.png', 256],
      ['icon.png', 512],
      ['Square30x30Logo.png', 30],
      ['Square44x44Logo.png', 44],
      ['Square71x71Logo.png', 71],
      ['Square89x89Logo.png', 89],
      ['Square107x107Logo.png', 107],
      ['Square142x142Logo.png', 142],
      ['Square150x150Logo.png', 150],
      ['Square284x284Logo.png', 284],
      ['Square310x310Logo.png', 310],
      ['StoreLogo.png', 50],
    ]
    for (const [name, size] of TAURI_PNGS) {
      const png = await renderPng(wardenAppSvg, size)
      const file = path.join(iconsDir, name)
      if (check) {
        if (!fs.existsSync(file)) stale.push(`${file} (missing)`)
        else if ((await pixelDiff(png, fs.readFileSync(file))) > 0.005) stale.push(`${file} (pixel drift)`)
      } else {
        fs.mkdirSync(iconsDir, { recursive: true })
        fs.writeFileSync(file, png)
      }
    }
    if (!check) console.log(`  ✓ agent icons: ${TAURI_PNGS.length} PNGs (app domain, squircle)`)

    // The in-app brand mark (index.html .brand__mark) and the companion
    // surface used to be a hand-made lantern PNG with no sync gate. It is
    // derived now, so ADR-0052's mark reaches the UI too.
    const uiMarkPng = await renderPng(wardenAppSvg, 64)
    const uiMarkPath = path.join(path.resolve(agentRepo), 'src/assets/warden.png')
    if (check) {
      if (!fs.existsSync(uiMarkPath)) stale.push(`${uiMarkPath} (missing)`)
      else if ((await pixelDiff(uiMarkPng, fs.readFileSync(uiMarkPath))) > 0.005) stale.push(`${uiMarkPath} (pixel drift)`)
    } else {
      fs.mkdirSync(path.dirname(uiMarkPath), { recursive: true })
      fs.writeFileSync(uiMarkPath, uiMarkPng)
      console.log('  ✓ agent UI: src/assets/warden.png (64px app domain)')
    }

    // The .ico Tauri's bundler embeds on Windows: Tauri's own size set.
    const agentIco = await buildIco(wardenAppSvg, [256, 64, 48, 32, 24, 16])
    const agentIcoPath = path.join(iconsDir, 'icon.ico')
    if (check) {
      if (!fs.existsSync(agentIcoPath)) stale.push(`${agentIcoPath} (missing)`)
      else if (sha(agentIco) !== sha(fs.readFileSync(agentIcoPath))) stale.push(`${agentIcoPath} (stale)`)
    } else {
      fs.writeFileSync(agentIcoPath, agentIco)
      console.log('  ✓ agent icons: icon.ico (6 sizes, app domain, squircle)')
    }

    // The .icns Tauri's bundler embeds in the macOS .app. The chunk list
    // is the one `iconutil` produces from a modern .iconset: every
    // standard slot, PNG-encoded.
    const ICNS_SLOTS = [
      ['icp4', 16],
      ['icp5', 32],
      ['ic07', 128],
      ['ic08', 256],
      ['ic09', 512],
      ['ic10', 1024],
      ['ic11', 32],
      ['ic12', 64],
      ['ic13', 256],
      ['ic14', 512],
    ]
    const icnChunks = []
    for (const [type, size] of ICNS_SLOTS) {
      icnChunks.push({ type, buf: await renderPng(wardenAppSvg, size) })
    }
    const agentIcn = buildIcn(icnChunks)
    const agentIcnPath = path.join(iconsDir, 'icon.icns')
    if (check) {
      if (!fs.existsSync(agentIcnPath)) stale.push(`${agentIcnPath} (missing)`)
      else if (sha(agentIcn) !== sha(fs.readFileSync(agentIcnPath))) stale.push(`${agentIcnPath} (stale)`)
    } else {
      fs.writeFileSync(agentIcnPath, agentIcn)
      console.log(`  ✓ agent icons: icon.icns (${ICNS_SLOTS.length} PNG chunks, app domain)`)
    }

    // The macOS menu-bar icon is a TEMPLATE image: the Warden glyph alone,
    // cut to one colour on transparent, framed the same way as the
    // JetBrains stripe glyph so the glyph fills the slot. macOS recolours
    // it for light and dark menu bars, so the colour here only matters on
    // the platforms that draw the 32x32 app icon instead.
    const TRAY_SIZE = 22
    const traySvg = `<svg xmlns="http://www.w3.org/2000/svg" width="${TRAY_SIZE}" height="${TRAY_SIZE}" viewBox="${wardenFrame.x.toFixed(2)} ${wardenFrame.y.toFixed(2)} ${wardenFrame.side.toFixed(2)} ${wardenFrame.side.toFixed(2)}" fill="none">
  <!-- Menu-bar glyph: the glyph of build/icon-warden.svg (Warden SSOT), cut
       to one colour on a transparent field — the glyph domain.
       Generated by scripts/generate-icons.mjs; do not edit by hand. -->
  ${wardenParts.letterform.replace(/fill="[^"]+"/g, `fill="${wardenParts.field}"`)}
</svg>
`
    const trayPng = await renderPng(traySvg, TRAY_SIZE)
    const trayPath = path.join(iconsDir, 'tray-22.png')
    if (check) {
      if (!fs.existsSync(trayPath)) stale.push(`${trayPath} (missing)`)
      else if ((await pixelDiff(trayPng, fs.readFileSync(trayPath))) > 0.005) stale.push(`${trayPath} (pixel drift)`)
    } else {
      fs.writeFileSync(trayPath, trayPng)
      console.log(`  ✓ agent icons: tray-22.png (${TRAY_SIZE}px glyph domain, menu-bar template)`)
    }
  }

  if (check) {
    if (stale.length) {
      console.error(`✗ ${stale.length} icon derivative(s) out of sync with build/icon.svg:`)
      for (const s of stale) console.error(`  - ${s}`)
      console.error('\nRun: node scripts/generate-icons.mjs')
      process.exit(1)
    }
    const scope = trackedOnly ? 'committed artefacts' : 'all three domains'
    console.log(`✓ icon derivatives are in sync with build/icon.svg (${scope})`)
  } else {
    console.log(`\nAll icons generated (SSOT: build/icon.svg; app domain = squircle, brand domain = square card, glyph domain = single colour on transparent)`)
  }
}

main().catch((err) => {
  console.error('Failed to generate icons:', err)
  process.exit(1)
})
