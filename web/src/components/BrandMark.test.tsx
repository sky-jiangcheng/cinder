// BrandMark.test.tsx — guards that the app UI uses the real brand mark.
//
// The regression this prevents: the navbar rendered a "▦" text glyph while
// every shipped artifact (app icon, favicon, VS Code extension) carried the
// mark from build/icon.svg. Nothing failed — the glyph just looked like a
// placeholder next to a real icon. These assertions make the mark a hard
// dependency, so swapping it back out breaks a test rather than shipping
// silently.
//
// v6 / ADR-0052: the knot is a woven band whose under-strand is cut at
// each crossing, so the master holds THREE filled paths. The geometry lives
// in generated brandMarkPaths.ts and check-brand.mjs gate 5 byte-compares
// every path against the master. This test guards the component's rendering
// of that data — not the letter's shape (a previous version pinned "one M"
// and a scale(0.941176) transform, both of which belonged to the retired
// IBM Plex Sans "C").
//
// Third regression (2026-10-24 UI sweep): the navbar put a 6px border-radius
// on the mark, cropping the ink field's corners into a torn sticker. That
// rule still holds, but for a different reason: the master now carries its
// own rounded seal (rx on the field rect, matching Warden's rounded PNG), so
// a CSS radius would round the corners a SECOND time. A CSS module rule is
// invisible to a DOM assertion, so the stylesheet and the master are both
// read through node:fs. The old "square edge is the style" rationale died
// with the hairline frame (ADR-0050); the no-crop guard did not.

// 本文件的 DOM 访问是**显式登记**而非疏漏（ADR-0026 条件 4）：被测对象是装饰性
// SVG——`aria-hidden` 正是设计意图，所以没有任何 role/name 可查。断言的是
// `<rect>/<path>` 的几何数量与 `aria-hidden` 本身，两者都只能从 DOM 拿；换成
// role 查询等于假装 logo 有可访问语义，会把要测的东西测没。testing-library
// 其余规则在本文件仍然生效。
/* eslint-disable testing-library/no-container, testing-library/no-node-access */
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import BrandMark from './BrandMark'
import { BRAND_MARK_PATHS, BRAND_MARK_TRANSFORM } from './brandMarkPaths'

const fsName = 'node:' + 'fs'
const pathName = 'node:' + 'path'
const procName = 'node:' + 'process'

interface NodeFs {
  readFileSync(p: string, enc: string): string
}
interface NodePath {
  resolve(...p: string[]): string
  join(...p: string[]): string
}
interface NodeProcess {
  cwd(): string
}

async function readRepoFile(rel: string): Promise<string> {
  const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
  const path = (await import(/* @vite-ignore */ pathName)) as unknown as NodePath
  const proc = (await import(/* @vite-ignore */ procName)) as unknown as NodeProcess
  return fs.readFileSync(path.resolve(proc.cwd(), rel), 'utf8')
}

describe('BrandMark', () => {
  it('renders an inline SVG rather than a text glyph', () => {
    const { container } = render(<BrandMark />)
    const svg = container.querySelector('svg')
    expect(svg).not.toBeNull()
    // The old navbar used a bare "▦" character, which inherits font metrics
    // and has no geometry of its own.
    expect(svg!.querySelectorAll('rect, path').length).toBeGreaterThan(1)
  })

  it('carries the inverse-seal field, not a gradient or a bevel', () => {
    const { container } = render(<BrandMark />)
    const html = container.innerHTML
    // Solid warm-ink field + off-white paper glyph, the brand SSOT's palette.
    expect(html).toContain('#34322D')
    expect(html).toContain('#F7F4EE')
    expect(container.querySelector('linearGradient')).toBeNull()
    expect(container.querySelector('clipPath')).toBeNull()
    // The inverse seal has NO frame: recognition comes from the reversed
    // figure, not from a stroke that has to be re-weighted at small sizes.
    expect(container.querySelectorAll('[stroke]')).toHaveLength(0)
    const field = container.querySelector('rect')!
    expect(field.getAttribute('width')).toBe('512')
    expect(field.getAttribute('fill')).toBe('#34322D')
    expect(container.querySelector('path')!.getAttribute('stroke')).toBeNull()
  })

  it('draws every generated path as a filled shape', () => {
    const { container } = render(<BrandMark />)
    const marks = container.querySelectorAll('g path')
    // The woven knot is cut at each crossing, so it is several filled paths,
    // not one stroke bundle and not one letter with a counter.
    expect(marks).toHaveLength(BRAND_MARK_PATHS.length)
    expect(BRAND_MARK_PATHS.length).toBeGreaterThan(1)
    marks.forEach((mark, i) => {
      expect(mark.getAttribute('d')).toBe(BRAND_MARK_PATHS[i])
      expect(mark.getAttribute('fill')).toBe('#F7F4EE')
      expect(mark.getAttribute('fill-rule')).toBe('nonzero')
      expect(mark.getAttribute('stroke')).toBeNull()
    })
  })

  it('uses the master group transform verbatim', () => {
    const { container } = render(<BrandMark />)
    const g = container.querySelector('g')!
    // Generated alongside the paths; gate 5 compares it to build/icon.svg.
    expect(g.getAttribute('transform')).toBe(BRAND_MARK_TRANSFORM)
  })

  it('is hidden from assistive tech unless given a title', () => {
    const { container, rerender } = render(<BrandMark />)
    // Adjacent text already names the product, so the mark is decorative by
    // default — announcing it would double the name for screen readers.
    expect(container.querySelector('svg')!.getAttribute('aria-hidden')).toBe('true')

    rerender(<BrandMark title="Cinder" />)
    const named = screen.getByRole('img', { name: 'Cinder' })
    expect(named).toBeTruthy()
    expect(named.getAttribute('aria-hidden')).toBeNull()
  })

  it('keeps the master rounded field — no CSS crop on top of it', async () => {
    // The load-bearing CSS rule. A border-radius on the mark is a crop, not
    // a style: the field is already rounded in the SVG, so a second radius
    // eats its corners and a shadow draws a square halo behind them.
    const css = await readRepoFile('src/components/SideRail.module.css')
    const rule = /\.brandMark\s*\{([^}]*)\}/.exec(css)
    expect(rule, '.brandMark rule not found in SideRail.module.css').not.toBeNull()
    expect(rule![1]).not.toMatch(/border-radius/)
    expect(rule![1]).not.toMatch(/box-shadow/)

    // Assert the radius as a property of the SSOT, not a magic number: if
    // the master is re-rounded, the rail must follow or this goes red.
    const master = await readRepoFile('../build/icon.svg')
    const masterRx = /<rect[^>]*\sfill="#34322D"[^>]*\srx="(\d+)"/.exec(master)?.[1]
    expect(masterRx, 'build/icon.svg field has no rx').toBeTruthy()
    const { container } = render(<BrandMark />)
    expect(container.querySelector('rect')!.getAttribute('rx')).toBe(masterRx)
  })

  it('the mark is the app identity, not a per-section ornament', async () => {
    // The left rail is the ONE place the brand mark belongs. The assistant
    // header used to render it too, so a single home page carried the logo
    // three times (rail, section header, AI message avatar).
    const src = await readRepoFile('src/pages/home/HomeKnowledgeAssistant.tsx')
    expect(src).not.toMatch(/BrandMark/)
  })

  it('draws the solid seal at every size above the glyph-only floor', () => {
    // Above GLYPH_ONLY_MAX the mark is the full inverse seal: a flat ink
    // field with the paper knot knocked out of it. There is no stroke to
    // re-weight, so the same geometry holds at 17px and at 512px alike.
    for (const size of [17, 20, 22, 26, 32, 64, 512]) {
      const { container, unmount } = render(<BrandMark size={size} />)
      expect(container.querySelector('[stroke]'), `frame at ${size}px`).toBeNull()
      const field = container.querySelector('rect')!
      expect(field.getAttribute('width'), `field at ${size}px`).toBe('512')
      expect(field.getAttribute('fill')).toBe('#34322D')
      expect(container.querySelectorAll('g path')).toHaveLength(BRAND_MARK_PATHS.length)
      expect(container.querySelector('path')!.getAttribute('fill')).toBe('#F7F4EE')
      unmount()
    }
  })

  it('collapses to the bare knot at glyph-domain sizes', () => {
    // At or below 16px a solid field reads as a dark square rather than a
    // shape. Every path takes currentColor so it belongs to its surface.
    const { container } = render(<BrandMark size={16} />)
    expect(container.querySelector('[stroke]')).toBeNull()
    expect(container.querySelector('rect')).toBeNull()
    const marks = container.querySelectorAll('g path')
    expect(marks).toHaveLength(BRAND_MARK_PATHS.length)
    marks.forEach((mark) => expect(mark.getAttribute('fill')).toBe('currentColor'))
  })
})
