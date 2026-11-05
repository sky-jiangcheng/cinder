// BrandMark.test.tsx — guards that the app UI uses the real brand mark.
//
// The regression this prevents: the navbar rendered a "▦" text glyph while
// every shipped artifact (app icon, favicon, VS Code extension) carried the
// mark from build/icon.svg. Nothing failed — the glyph just looked like a
// placeholder next to a real icon. These assertions make the mark a hard
// dependency, so swapping it back out breaks a test rather than shipping
// silently.
//
// Second regression these guard: the brand moved from a navy tile to the
// paper card (warm field + hairline ink frame). The UI is generated from
// the same SSOT as the shipped icons, so a half-applied move — card in the
// dock, navy tile in the navbar — would look like two products.
//
// Third regression (2026-10-24 UI sweep): the navbar put a 6px border-radius
// on the mark. The brand master's own comment says the card has "no rounded
// corners — the square edge and the hairline are the style", and at 26px the
// frame sits ~1.3px from the edge, so the radius crop ate the frame's four
// corners: the mark read as a torn sticker. The radius was left over from
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

  it('carries the paper-card field, not a gradient or a bevel', () => {
    const { container } = render(<BrandMark />)
    const html = container.innerHTML
    // Warm-paper field + near-black ink, the brand SSOT's palette. A blue
    // gradient and a white hairline were what made the old mark read as
    // stock app-icon chrome.
    expect(html).toContain('#F5F0E4')
    expect(html).toContain('#1B1B1B')
    expect(container.querySelector('linearGradient')).toBeNull()
    expect(container.querySelector('clipPath')).toBeNull()
    // The one stroke in the mark is the brand frame itself: the hairline
    // that makes the card read as printed. It is a rect outline, not the
    // old bevel rim on the glyph.
    const stroked = [...container.querySelectorAll('[stroke]')]
    expect(stroked).toHaveLength(1)
    expect(stroked[0].tagName.toLowerCase()).toBe('rect')
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

  it('re-weights the frame so the printed edge survives the rendering', () => {
    // The regression (ADR-0037): the master draws a 9-unit hairline on the
    // 512 grid. At the rail's 26px that is 0.46px — under one pixel, so the
    // browser smears the frame into a grey line. The frame is the mark's
    // only distinguishing feature ("a square warm card with a heavier ink
    // line around it"), so at UI sizes it was losing exactly the thing that
    // made the mark identifiable.
    //
    // The icon generator already re-weights its glyph domain for precisely
    // this reason (scripts/generate-icons.mjs, 34 units at 24px); the UI
    // was the one surface that never got the treatment.
    for (const size of [17, 20, 22, 26, 32, 64]) {
      const { container, unmount } = render(<BrandMark size={size} />)
      const frame = container.querySelector('[stroke]')
      expect(frame, `no frame drawn at ${size}px`).not.toBeNull()
      const stroke = Number(frame!.getAttribute('stroke-width'))
      expect(
        (stroke * size) / 512,
        `frame at ${size}px must render at least 1px wide`,
      ).toBeGreaterThanOrEqual(1)
      // The paper margin around the frame has to survive too. A frame with
      // no paper around it is not a printed edge, it is a dark border — and
      // the master inset (26 units) cannot hold once the stroke is heavier.
      const inset = Number(frame!.getAttribute('x'))
      expect(
        ((inset - stroke / 2) * size) / 512,
        `paper margin at ${size}px must be at least 1px`,
      ).toBeGreaterThanOrEqual(1)
      unmount()
    }
  })

  it('reproduces the master exactly at the master size', () => {
    // Re-weighting must not creep upward: at 512 the geometry has to be the
    // master's geometry, or the app icon and the UI mark stop being the
    // same drawing.
    const { container } = render(<BrandMark size={512} />)
    const frame = container.querySelector('[stroke]')!
    expect(Number(frame.getAttribute('stroke-width'))).toBe(9)
    expect(frame.getAttribute('x')).toBe('26')
    expect(frame.getAttribute('width')).toBe('460')
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
