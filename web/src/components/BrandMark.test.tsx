// BrandMark.test.tsx — guards that the app UI uses the real brand mark.
//
// The regression this prevents: the navbar rendered a "▦" text glyph while
// every shipped artifact (app icon, favicon, VS Code extension) carried the
// mark from build/icon.svg. Nothing failed — the glyph just looked like a
// placeholder next to a real icon. These assertions make the mark a hard
// dependency, so swapping it back out breaks a test rather than shipping
// silently.
//
// Second regression these guard: the brand moved to the inverse seal (a
// solid warm-ink field with the letter knocked out in paper — ADR-0050, no
// hairline frame). The UI is generated from the same SSOT as the shipped
// icons, so a half-applied move — seal in the dock, framed card in the
// navbar — would look like two products.
//
// Third regression (2026-10-24 UI sweep): the navbar put a 6px border-radius
// on the mark. The brand master is a square seal — "no rounded corners, the
// square edge is the style" — so a radius crop ate the ink field's four
// corners and the mark read as a torn sticker. The radius was left over from
// the navy-tile era, when the tile had a gradient of its own. A CSS module
// rule is invisible to a DOM assertion, so the stylesheet is read through
// node:fs and the radius rule asserted to be gone.

// 本文件的 DOM 访问是**显式登记**而非疏漏（ADR-0026 条件 4）：被测对象是装饰性
// SVG——`aria-hidden` 正是设计意图，所以没有任何 role/name 可查。断言的是
// `<rect>/<path>` 的几何数量与 `aria-hidden` 本身，两者都只能从 DOM 拿；换成
// role 查询等于假装 logo 有可访问语义，会把要测的东西测没。testing-library
// 其余规则在本文件仍然生效。
/* eslint-disable testing-library/no-container, testing-library/no-node-access */
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import BrandMark from './BrandMark'

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

async function readRailCss(): Promise<string> {
  const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
  const path = (await import(/* @vite-ignore */ pathName)) as unknown as NodePath
  const proc = (await import(/* @vite-ignore */ procName)) as unknown as NodeProcess
  return fs.readFileSync(
    path.resolve(proc.cwd(), 'src/components/SideRail.module.css'),
    'utf8',
  )
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
    // A blue gradient and a white hairline were what made the old mark read
    // as stock app-icon chrome.
    expect(html).toContain('#34322D')
    expect(html).toContain('#F7F4EE')
    expect(container.querySelector('linearGradient')).toBeNull()
    expect(container.querySelector('clipPath')).toBeNull()
    // The inverse seal has NO frame: the recognition comes from the reversed
    // figure, not from a stroke that has to be re-weighted to survive small
    // sizes. Any stroke here means the framed-card era crept back in.
    expect(container.querySelectorAll('[stroke]')).toHaveLength(0)
    // The field is a full-bleed rect, not an inset framed card.
    const field = container.querySelector('rect')!
    expect(field.getAttribute('width')).toBe('512')
    expect(field.getAttribute('fill')).toBe('#34322D')
    expect(container.querySelector('path')!.getAttribute('stroke')).toBeNull()
  })

  it('draws the mark as one filled path', () => {
    const { container } = render(<BrandMark />)
    const mark = container.querySelector('path')!
    // The letter is a fill, not a bundle of strokes: strokes at this size are what
    // made the previous mark lose its white and pinch at 16px. Nonzero winding
    // is the typeface's own.
    expect(mark.getAttribute('fill-rule')).toBe('nonzero')
    expect(mark.getAttribute('stroke')).toBeNull()
    // One closed subpath. The R this replaced had two (silhouette + counter);
    // the C's aperture opens to the outside, so the letter has no hole — and
    // the count also pins WHICH letter the mark carries.
    expect(mark.getAttribute('d')!.match(/M/g)).toHaveLength(1)
  })

  it('keeps the glyph inside the tile', () => {
    const { container } = render(<BrandMark />)
    const mark = container.querySelector('path')!
    // The letter is scaled and centred by its own transform; without it the
    // glyph renders at its original 340-unit cap and crowds the tile edge.
    const g = mark.closest('g')!
    const scale = Number(/scale\(([\d.]+)\)/.exec(g.getAttribute('transform')!)![1])
    expect(scale).toBeCloseTo(320 / 340, 5)
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

  it('is not cropped in the rail — the card keeps its square edge', async () => {
    // The load-bearing CSS rule. A border-radius on the mark is a crop, not a
    // style: the brand master is a square card whose hairline frame sits
    // ~1.3px from the edge at the rail's 26px, so any visible radius
    // swallows the frame's four corners and the mark reads as torn.
    const css = await readRailCss()
    const rule = /\.brandMark\s*\{([^}]*)\}/.exec(css)
    expect(rule, '.brandMark rule not found in SideRail.module.css').not.toBeNull()
    expect(rule![1]).not.toMatch(/border-radius/)
    // The navy-tile era also added a drop shadow; the paper card has none.
    expect(rule![1]).not.toMatch(/box-shadow/)
  })

  it('the mark is the app identity, not a per-section ornament', async () => {
    // The left rail is the ONE place the brand mark belongs. The assistant
    // header used to render it too, so a single home page carried the logo
    // three times (rail, section header, AI message avatar). The assistant
    // header and the message avatar now use text instead.
    const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
    const path = (await import(/* @vite-ignore */ pathName)) as unknown as NodePath
    const proc = (await import(/* @vite-ignore */ procName)) as unknown as NodeProcess
    const src = fs.readFileSync(
      path.resolve(proc.cwd(), 'src/pages/home/HomeKnowledgeAssistant.tsx'),
      'utf8',
    )
    expect(src).not.toMatch(/BrandMark/)
  })

  it('draws the solid seal at every size above the glyph-only floor', () => {
    // Above GLYPH_ONLY_MAX the mark is the full inverse seal: a flat ink
    // field with the paper letter knocked out of it. There is no stroke to
    // re-weight (the whole point of dropping the frame), so the field is the
    // same full-bleed rect and the glyph the same paper fill at 17px and at
    // 512px alike.
    for (const size of [17, 20, 22, 26, 32, 64, 512]) {
      const { container, unmount } = render(<BrandMark size={size} />)
      expect(container.querySelector('[stroke]'), `frame at ${size}px`).toBeNull()
      const field = container.querySelector('rect')!
      expect(field.getAttribute('width'), `field at ${size}px`).toBe('512')
      expect(field.getAttribute('fill')).toBe('#34322D')
      expect(container.querySelector('path')!.getAttribute('fill')).toBe('#F7F4EE')
      unmount()
    }
  })

  it('collapses to the bare letter at glyph-domain sizes', () => {
    // At or below 16px a framed card reads as a bright square rather than a
    // letter — the exact failure the icon generator's glyph domain exists
    // to prevent. The letter takes currentColor so it belongs to whatever
    // surface it lands on; a fixed near-black would vanish on a dark ground.
    const { container } = render(<BrandMark size={16} />)
    expect(container.querySelector('[stroke]')).toBeNull()
    expect(container.querySelector('rect')).toBeNull()
    expect(container.querySelector('path')!.getAttribute('fill')).toBe('currentColor')
  })
})
