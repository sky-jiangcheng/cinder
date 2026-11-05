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
// THREE SHAPE DOMAINS (see CONTRIBUTING "Brand icon domains"):
//   brand domain — the SSOT verbatim: the square-edged printed card.
//     Favicons, marketplace icons, the JetBrains plugin's in-panel brand
//     mark. These sit in square or promotional containers (browser tab,
//     store list, a 96px mark in the tool-window header) where the card edge
//     IS the brand; a squircle here would read as a broken rectangle, not a
//     rounded one.
//   app domain — the same mark inside a macOS squircle container:
//     build/appicon.png (the Wails default icon source), the size ladder,
//     the Windows .ico. macOS has never auto-masked app icons: since Big
//     Sur the source artwork must ship its own squircle or the Dock shows
//     a square card next to every rounded neighbour. (The macOS 26+
//     Liquid Glass .icon format lets the system mask, but a Wails build
//     still ships a plain icns/PNG ladder, so we must pre-shape it.)
//     The inner hairline frame is re-drawn as a concentric squircle
//     (Apple's concentricity rule: nested curves must relate to their
//     container, never a random radius).
//   glyph domain — the same mark cut to ONE colour on a TRANSPARENT
//     background, for slots the host renders as a MASK or as a monochrome
//     stripe icon. Two slots live here:
//     · the VS Code activity bar: VS Code takes the SVG's alpha channel
//       and paints it in the theme's icon foreground, so the opaque cream
//       field of the card turned the whole 24×24 into one flat slab — the
//       activity bar showed a white square with the letter gone (verified by
//       rendering the shipped file through a CSS mask). The alpha shape has
//       to BE the mark: the frame and the letter, nothing else.
//     · the JetBrains tool-window stripe: IntelliJ rasterizes plugin SVG
//       icons as plain images (no mask) and picks the `_dark` sibling when
//       the IDE runs a dark theme — the same convention JetBrains' own
//       stripe icons use (icons/statisticsEventLog.svg + _dark.svg in the
//       bundled dev plugin). So the stripe gets a 16px monochrome LETTER on
//       a transparent background, in two theme colours. The card was wrong
//       there: a cream slab in a 40px dark strip, whose hairline frame is
//       sub-pixel at that size, so it read as a deformed bright blob.
//       The brand card still ships — in the tool-window header (96px) and
//       in the store listing.
//
// Two surfaces stay outside the generated set, both deliberately: the
// JetBrains Marketplace pair (pluginIcon.svg / pluginIcon_dark.svg, 40×40)
// is hand-scaled because the dark variant is a colour inversion, not a
// resize; the docs sidebar mark and the README banners reference the SSOT
// itself (build-docs.mjs inlines it, the READMEs point at the file), so
// there is no second copy to drift.
import fs from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
import sharp from 'sharp'

const SIZES = [512, 256, 128, 64, 48, 32, 16]

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
// the field colour, the ink colour and the letterform group. Extracted
// rather than hardcoded — hardcoded copies of SSOT values are exactly the
// drift this pipeline exists to prevent. A structural change to the SSOT
// that breaks extraction fails loudly here instead of shipping a stale
// shape.
function extractBrandParts(svgText) {
  const field = /<rect width="512" height="512" fill="([^"]+)"/.exec(svgText)
  const frame = /<rect x="26" y="26"[^>]*stroke="([^"]+)"/.exec(svgText)
  const frameBox = /<rect x="(\d+)" y="(\d+)" width="(\d+)" height="(\d+)" fill="none"/.exec(svgText)
  const letterform = /<g transform="translate[\s\S]*?<\/g>/.exec(svgText)
  if (!field || !frame || !frameBox || !letterform) {
    throw new Error('generate-icons: build/icon.svg structure changed (field/frame/letterform not found) — update extractBrandParts')
  }
  return {
    field: field[1],
    ink: frame[1],
    frameBox: {
      x: Number(frameBox[1]),
      y: Number(frameBox[2]),
      width: Number(frameBox[3]),
      height: Number(frameBox[4]),
    },
    letterform: letterform[0],
  }
}

// The glyph domain: the mark cut to one colour on a transparent background.
//
// The colour is the PAPER (#F5F0E4), not the ink. The activity bar is dark
// in every VS Code theme (activityBar.background is #333333 / #2C2C2C),
// so a near-black glyph would disappear if a host ever rendered the file
// unmasked; under the mask the colour is ignored anyway.
//
// The frame's stroke is re-weighted for the small size: the SSOT hairline
// is 9 units on the 512 grid, which is 0.42px at 24 — below one pixel, so
// it renders as a faint smear instead of a frame. 34 units is 1.6px at 24.
function glyphSvg(parts, size, strokeWidth) {
  const letter = parts.letterform.replace(/fill="[^"]+"/, `fill="${parts.field}"`)
  const box = parts.frameBox
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="${size}" height="${size}" role="img" aria-label="Cinder">
  <!-- Glyph-domain cut of build/icon.svg (brand SSOT): one colour, transparent
       background — for slots the host renders as a mask. See CONTRIBUTING
       "Brand icon domains". Generated by scripts/generate-icons.mjs; do not edit. -->
  <rect x="${box.x}" y="${box.y}" width="${box.width}" height="${box.height}" fill="none" stroke="${parts.field}" stroke-width="${strokeWidth}"/>
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
  const letter = parts.letterform.replace(/fill="[^"]+"/, `fill="${fill}"`)
  return `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512" fill="none">${letter}</svg>`
}

// The app-domain mark: SSOT letterform and colours inside the squircle
// container, with the hairline frame re-drawn as a concentric squircle
// (radius shrunk by the frame's 26-unit inset, matching the SSOT).
function appMarkSvg(parts) {
  const inset = 26
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512" role="img" aria-label="Cinder">
  <!-- App-domain variant of build/icon.svg (brand SSOT): same letterform,
       same colours, inside the macOS squircle container with a concentric
       inner frame. Generated by scripts/generate-icons.mjs; do not edit. -->
  <path d="${squirclePath(512)}" fill="${parts.field}"/>
  ${parts.letterform}
  <path d="${squirclePath(512, 256 - inset)}" fill="none" stroke="${parts.ink}" stroke-width="9"/>
</svg>
`
}

async function renderPng(svg, size) {
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
  const box = await alphaBBox(letterOnlySvg(parts, parts.ink))
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
  const parts = extractBrandParts(svgText)
  const appSvg = appMarkSvg(parts)

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
  const faviconIco = await buildIco(svg)
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

  // 3. VS Code marketplace icon (media/icon.png, 128×128) — brand domain:
  //    store listings render in square or system-shaped thumbnails, the
  //    card edge is the recognition.
  const marketplacePng = await renderPng(svg, MARKETPLACE_ICON_SIZE)
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
  //    The alpha shape has to BE the mark, so this cut keeps the frame and
  //    the letterform and drops the field.
  const ACTIVITY_BAR_SIZE = 24
  const ACTIVITY_BAR_STROKE = 34 // 1.6px at 24; the SSOT hairline is 0.42px
  const activityBarSvg = glyphSvg(parts, ACTIVITY_BAR_SIZE, ACTIVITY_BAR_STROKE)
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
  const STRIPE_INK_LIGHT = parts.ink // #1B1B1B — brand ink, light theme
  const STRIPE_INK_DARK = parts.field // #F5F0E4 — brand paper, dark theme
  const frame = await letterFrame(parts)
  const stripeSvg = (ink) => {
    const letter = parts.letterform.replace(/fill="[^"]+"/, `fill="${ink}"`)
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
  const markPng = await renderPng(svg, MARK_PNG_SIZE)
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
  //    The agent is a SEPARATE product with its OWN name (Warden), so it
  //    cannot wear the desktop app's icon; it wears the same BRAND MARK in
  //    the same APP DOMAIN (the squircle), because Tauri's set is consumed
  //    as an OS app icon and the system will not mask it for us.
  //
  //    It lives in another checkout, so this section only runs when the
  //    caller names the repo (`--agent-repo <path>`). The default run must
  //    never reach into a sibling working tree.
  if (agentRepo) {
    const iconsDir = path.join(path.resolve(agentRepo), 'src-tauri', 'icons')

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
      const png = await renderPng(appSvg, size)
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

    // The .ico Tauri's bundler embeds on Windows: Tauri's own size set.
    const agentIco = await buildIco(appSvg, [256, 64, 48, 32, 24, 16])
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
      icnChunks.push({ type, buf: await renderPng(appSvg, size) })
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

    // The macOS menu-bar icon is a TEMPLATE image: the letterform alone,
    // cut to one colour on transparent, framed the same way as the
    // JetBrains stripe glyph so the letter fills the slot. macOS recolours
    // it for light and dark menu bars, so the colour here only matters on
    // the platforms that draw the 32x32 app icon instead.
    const TRAY_SIZE = 22
    const traySvg = `<svg xmlns="http://www.w3.org/2000/svg" width="${TRAY_SIZE}" height="${TRAY_SIZE}" viewBox="${frame.x.toFixed(2)} ${frame.y.toFixed(2)} ${frame.side.toFixed(2)} ${frame.side.toFixed(2)}" fill="none">
  <!-- Menu-bar glyph: the letterform of build/icon.svg (brand SSOT), cut
       to one colour on a transparent field — the glyph domain.
       Generated by scripts/generate-icons.mjs; do not edit by hand. -->
  ${parts.letterform.replace(/fill="[^"]+"/, `fill="${parts.ink}"`)}
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
