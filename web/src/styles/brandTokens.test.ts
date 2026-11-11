// brandTokens.test.ts — the app's palette is not a copy of the brand's
// palette; it IS the brand's palette.
//
// The regression this prevents: this brand shipped four half-landed
// rebrands, and every one of them was a hand-kept copy (CONTRIBUTING.md,
// "Brand icon domains" — "the four half-landed rebrands were all caused by
// hand-maintained copies"). The UI's palette was the newest copy: the app
// wore a neutral grey with a Tailwind blue accent, and the brand's own
// warm-paper and near-black appeared in exactly ONE component — the mark in
// the rail. Nothing failed. The mismatch just read as "this icon doesn't
// look like this product" (ADR-0037).
//
// So this file never hardcodes a brand colour. It reads build/icon.svg —
// the brand SSOT — and compares. Re-colour the master and this test follows
// it: it fails only where the UI has actually drifted, never because a
// number was written down twice.
//
// The contrast half of the file asserts a PROPERTY (WCAG AA 4.5:1 for text
// and the accent, 3:1 for control borders) rather than a magic value, so
// re-tuning the palette does not require editing the test — and re-tuning
// it into a failure does not go unnoticed.

import { describe, it, expect } from 'vitest'

const fsName = 'node:' + 'fs'
const pathName = 'node:' + 'path'
const procName = 'node:' + 'process'

interface NodeFs {
  readFileSync(p: string, enc: string): string
  readdirSync(p: string, opts: { withFileTypes: true }): NodeDirent[]
}
interface NodeDirent {
  name: string
  isDirectory(): boolean
}
interface NodePath {
  resolve(...p: string[]): string
  join(...p: string[]): string
}
interface NodeProcess {
  cwd(): string
}

async function readRepoFile(...segments: string[]): Promise<string> {
  const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
  const path = (await import(/* @vite-ignore */ pathName)) as unknown as NodePath
  const proc = (await import(/* @vite-ignore */ procName)) as unknown as NodeProcess
  return fs.readFileSync(path.resolve(proc.cwd(), ...segments), 'utf8')
}

// --- WCAG contrast ---------------------------------------------------------

function channel(v: number): number {
  const s = v / 255
  return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
}

function luminance(hex: string): number {
  const h = hex.replace('#', '')
  const r = parseInt(h.slice(0, 2), 16)
  const g = parseInt(h.slice(2, 4), 16)
  const b = parseInt(h.slice(4, 6), 16)
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

function contrast(a: string, b: string): number {
  const la = luminance(a)
  const lb = luminance(b)
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

// --- CSS parsing -----------------------------------------------------------

/** The body of the first `{ ... }` block that follows `selector`. */
function cssBlock(css: string, selector: string): string {
  const at = css.indexOf(selector)
  expect(at, `${selector} not found in tokens.css`).toBeGreaterThan(-1)
  const open = css.indexOf('{', at)
  let depth = 0
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++
    else if (css[i] === '}') {
      depth--
      if (depth === 0) return css.slice(open + 1, i)
    }
  }
  throw new Error(`unterminated block for ${selector}`)
}

/** Every `--name: value` declaration in a block, lower-cased hex values. */
function cssVars(block: string): Record<string, string> {
  const vars: Record<string, string> = {}
  for (const m of block.matchAll(/(--[a-z-]+)\s*:\s*([^;]+);/gi)) {
    vars[m[1]] = m[2].trim().toLowerCase()
  }
  return vars
}

// --- The brand SSOT --------------------------------------------------------

interface Master {
  /** The seal's ground: the solid warm ink field. */
  ink: string
  /** The letter knocked out of the field: the off-white paper. */
  paper: string
}

async function readMaster(): Promise<Master> {
  // The master is an inverse seal (ADR-0050): a solid ink field rect with the
  // glyph path knocked out of it in paper. There is no hairline frame to read
  // any more — the two brand colours are the field's fill and the glyph's.
  const svg = await readRepoFile('..', 'build', 'icon.svg')
  const ink = /<rect width="512" height="512" fill="(#[0-9A-Fa-f]{6})"/.exec(svg)
  const paper = /<path[^>]*\sfill="(#[0-9A-Fa-f]{6})"/.exec(svg)
  expect(ink, 'brand master: could not read the ink field').not.toBeNull()
  expect(paper, 'brand master: could not read the paper glyph').not.toBeNull()
  return { ink: ink![1].toUpperCase(), paper: paper![1].toUpperCase() }
}

// --- The tokens ------------------------------------------------------------

interface Theme {
  name: string
  vars: Record<string, string>
}

async function readThemes(): Promise<{ light: Theme; dark: Theme }> {
  const css = await readRepoFile('src', 'styles', 'design-system', 'tokens.css')
  return {
    light: { name: 'light', vars: cssVars(cssBlock(css, ':root {')) },
    dark: { name: 'dark', vars: cssVars(cssBlock(css, '[data-theme="dark"] {')) },
  }
}

/** Surfaces that carry text in the app. */
const TEXT_SURFACES = ['--bg-primary', '--bg-secondary', '--bg-tertiary']

describe('brand palette', () => {
  it('declares the master colours as the brand tokens', async () => {
    const master = await readMaster()
    const { light, dark } = await readThemes()
    expect(light.vars['--brand-paper'], '--brand-paper').toBe(master.paper.toLowerCase())
    expect(light.vars['--brand-ink'], '--brand-ink').toBe(master.ink.toLowerCase())
    // The dark theme must NOT redefine them. The brand's two colours are the
    // same in both themes — the themes swap which one is ink and which is
    // paper — so there must be exactly ONE place in the stylesheet where
    // they are written down.
    expect(dark.vars['--brand-paper'], 'the dark theme re-declares --brand-paper').toBeUndefined()
    expect(dark.vars['--brand-ink'], 'the dark theme re-declares --brand-ink').toBeUndefined()
  })

  it('wears the brand ink as its text, and the brand paper as its dark-theme text', async () => {
    // The two themes are the same two brand colours, swapped: ink on paper,
    // paper on ink. If either drifts to a third colour the product stops
    // reading as one object with its own mark.
    const master = await readMaster()
    const { light, dark } = await readThemes()
    expect(light.vars['--text-primary']).toBe(master.ink.toLowerCase())
    expect(dark.vars['--text-primary']).toBe(master.paper.toLowerCase())
  })

  it('no longer wears the cold blue it used to wear', async () => {
    // The old accent was Tailwind blue-600 with a pale blue twin, chosen
    // because it was the default — not because it belonged to this brand.
    // Leaving them in is what made the app read as "a neutral-blue tool
    // with a cream sticker in the corner".
    const { light, dark } = await readThemes()
    expect(light.vars['--accent']).not.toBe('#2563eb')
    expect(light.vars['--accent-soft']).not.toBe('#eff6ff')
    expect(dark.vars['--accent']).not.toBe('#60a5fa')
    expect(dark.vars['--accent-soft']).not.toBe('#1e3a5f')
  })

  it('draws the mark from the same colours as the master', async () => {
    // The chain the ADR wants unbroken: master → tokens → component. If the
    // component is edited with a hand-picked cream, the app and the icon in
    // the dock become two products again.
    const master = await readMaster()
    const src = await readRepoFile('src', 'components', 'BrandMark.tsx')
    expect(src).toContain(master.paper)
    expect(src).toContain(master.ink)
  })
})

describe('palette contrast', () => {
  it('keeps the accent readable on every surface it lands on', async () => {
    // ADR-0037's hard gate: the accent carries text, chips, borders and the
    // primary button, and it must clear AA against all of them — in BOTH
    // themes. Asserted as a property, so re-tuning the palette does not
    // mean editing this file.
    const { light, dark } = await readThemes()
    for (const theme of [light, dark]) {
      const accent = theme.vars['--accent']
      const surfaces = [...TEXT_SURFACES, '--bg-hover', '--bg-active', '--accent-soft']
      for (const surface of surfaces) {
        const bg = theme.vars[surface]
        expect(bg, `${theme.name}: ${surface} is not defined`).toBeTruthy()
        expect(
          contrast(accent, bg),
          `${theme.name}: --accent on ${surface} is below WCAG AA 4.5:1`,
        ).toBeGreaterThanOrEqual(4.5)
      }
      // ...and as a background, with the card surface as the text on top.
      expect(
        contrast(theme.vars['--bg-secondary'], accent),
        `${theme.name}: --bg-secondary on --accent is below 4.5:1`,
      ).toBeGreaterThanOrEqual(4.5)
      expect(
        contrast(theme.vars['--bg-secondary'], theme.vars['--accent-light']),
        `${theme.name}: --bg-secondary on --accent-light is below 4.5:1`,
      ).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('keeps the text ramp readable on every surface it lands on', async () => {
    const { light, dark } = await readThemes()
    for (const theme of [light, dark]) {
      for (const ink of ['--text-primary', '--text-secondary', '--text-tertiary']) {
        for (const surface of TEXT_SURFACES) {
          expect(
            contrast(theme.vars[ink], theme.vars[surface]),
            `${theme.name}: ${ink} on ${surface} is below WCAG AA 4.5:1`,
          ).toBeGreaterThanOrEqual(4.5)
        }
      }
    }
  })

  it('keeps control borders visible enough to identify a control', async () => {
    // --border-default draws button and input boundaries, so WCAG 1.4.11's
    // 3:1 non-text contrast applies to it. The cool grey it replaced
    // managed only 1.52:1 — visible as a hairline, not as a boundary.
    const { light, dark } = await readThemes()
    for (const theme of [light, dark]) {
      for (const surface of TEXT_SURFACES) {
        expect(
          contrast(theme.vars['--border-default'], theme.vars[surface]),
          `${theme.name}: --border-default on ${surface} is below 3:1`,
        ).toBeGreaterThanOrEqual(3)
      }
    }
  })

  it('keeps the semantic colours readable on their own soft fields', async () => {
    const { light, dark } = await readThemes()
    for (const theme of [light, dark]) {
      for (const sem of ['danger', 'warning', 'info', 'success']) {
        expect(
          contrast(theme.vars[`--${sem}`], theme.vars[`--${sem}-soft`]),
          `${theme.name}: --${sem} on --${sem}-soft is below WCAG AA 4.5:1`,
        ).toBeGreaterThanOrEqual(4.5)
      }
    }
  })
})

describe('preview palette', () => {
  // The appearance tab draws each option as a miniature of the app so the
  // three themes can be told apart by eye. It cannot read the LIVE theme for
  // that: a swatch has to be able to show a theme that is not the active one.
  // So the preview palette is a catalogue of both, and the catalogue is only
  // honest as long as every value in it equals the live token it depicts.
  const PAIRS: Array<[preview: string, live: string, theme: 'light' | 'dark']> = [
    ['--preview-light-page', '--bg-primary', 'light'],
    ['--preview-light-card', '--bg-secondary', 'light'],
    ['--preview-light-edge', '--border-subtle', 'light'],
    ['--preview-light-line', '--text-tertiary', 'light'],
    ['--preview-light-accent', '--accent', 'light'],
    ['--preview-dark-page', '--bg-primary', 'dark'],
    ['--preview-dark-card', '--bg-secondary', 'dark'],
    ['--preview-dark-edge', '--border-subtle', 'dark'],
    ['--preview-dark-line', '--text-tertiary', 'dark'],
    ['--preview-dark-accent', '--accent', 'dark'],
  ]

  it('depicts the live palette, not a copy of it', async () => {
    const { light, dark } = await readThemes()
    for (const [preview, live, themeName] of PAIRS) {
      // The catalogue is declared once, in :root; the palette it depicts for
      // the dark theme lives in the dark block. Compare the catalogue entry
      // with the theme it claims to show.
      const catalogue = light.vars
      const depicted = themeName === 'light' ? light : dark
      expect(catalogue[preview], `${preview} is not defined`).toBeTruthy()
      expect(
        catalogue[preview],
        `${preview} (${themeName}) no longer matches ${live} — the swatch would lie about the app`,
      ).toBe(depicted.vars[live])
    }
  })

  it('declares the catalogue once, in :root', async () => {
    // The catalogue holds both themes' values, so it must not be re-declared
    // per theme — one list, one place to change.
    const { dark } = await readThemes()
    for (const [preview] of PAIRS) {
      expect(dark.vars[preview], `the dark theme re-declares ${preview}`).toBeUndefined()
    }
  })
})

describe('legacy palette', () => {
  // The palette ADR-0037 replaced, one colour at a time: the cold Tailwind
  // blue the app wore as its accent, and the neutral greys it wore
  // everywhere else. Two components kept a hand-kept copy of the old
  // neutrals — the theme-picker swatches and the activity heatmap — and both
  // were showing the user a colour language the product no longer speaks.
  // A grep is the only honest guard against a copy creeping back in: the
  // tokens are the palette, and everything else must refer to them.
  const LEGACY_HEXES = [
    '#2563eb', '#3b82f6', '#60a5fa', '#eff6ff', '#1e3a5f', '#4c8dff',
    'rgba(88, 165, 255',
    '#f0f0ee', '#c8c8c3', '#9a9a94', '#5e5e58',
    '#1a1a1a', '#2a2a2a', '#3a3a3a', '#4a4a4a',
    '#e0ded8', '#6a6a6a', '#949494', '#e8e8e8', '#f5f5f3',
    '#e0e0e0', '#111', '#ddd',
  ]

  /** A literal, matched only when it is not the prefix of a longer hex. */
  function legacyPattern(literal: string): RegExp {
    const escaped = literal.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    return new RegExp(escaped + '(?![0-9a-f])', 'i')
  }

  async function listUiFiles(): Promise<string[]> {
    const fsName = 'node:' + 'fs'
    const pathName = 'node:' + 'path'
    const procName = 'node:' + 'process'
    const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
    const path = (await import(/* @vite-ignore */ pathName)) as unknown as NodePath
    const proc = (await import(/* @vite-ignore */ procName)) as unknown as NodeProcess
    const found: string[] = []
    const walk = (dir: string): void => {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name)
        if (entry.isDirectory()) walk(full)
        // Generated mark geometry: pure path data with no colour tokens.
        // check-brand.mjs gate 5 byte-compares it to the master instead.
        else if (entry.name !== 'brandMarkPaths.ts') found.push(full)
      }
    }
    walk(path.resolve(proc.cwd(), 'src'))
    found.push(path.resolve(proc.cwd(), 'index.html'))
    return found
  }

  // Walks src/ synchronously; under a full parallel run that I/O can exceed
  // vitest's 5s default even when the palette is clean.
  it('is not worn anywhere in the UI', async () => {
    const fsName = 'node:' + 'fs'
    const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
    const files = await listUiFiles()
    const offenders: string[] = []
    for (const file of files) {
      if (/\.test\.(ts|tsx)$/.test(file)) continue
      if (!/\.(css|tsx|ts|html)$/.test(file)) continue
      const text = (await fs.readFileSync(file, 'utf8')).toLowerCase()
      for (const hex of LEGACY_HEXES) {
        if (legacyPattern(hex).test(text)) offenders.push(`${file}: ${hex}`)
      }
    }
    expect(
      offenders,
      'the palette ADR-0037 replaced is back in the UI — use the tokens instead',
    ).toEqual([])
  }, 15000)
})
