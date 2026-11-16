import { describe, it, expect } from 'vitest'

// The reading surface (ADR-0050 §⑤ 加注 2026-11-16, the Claude-desktop
// reference): rendered documents — markdown bodies, chat answers, note
// bodies — set the serif prose face (--font-display / --text-prose /
// --leading-prose); UI chrome keeps the compact sans ramp. Elements carry
// BOTH the global .markdown-body and a module class at the same specificity
// (0,1,0), so which one wins depends on bundle order — the exact hazard
// semanticInk.test.ts pins for the colour ramp. The reading surfaces resolve
// it by doubling their selector (.body.body); the chrome surfaces opt out the
// same way. This test reads the shipped CSS as text and pins both halves:
// a rule that silently loses its doubling reintroduces an order-dependent
// font, which is invisible in CI and visible on every page.

const fsName = 'node:' + 'fs'
const pathName = 'node:' + 'path'
const procName = 'node:' + 'process'

interface NodeFs {
  readFileSync(p: string, enc: string): string
}
interface NodePath {
  resolve(...p: string[]): string
}
interface NodeProcess {
  cwd(): string
}

async function read(rel: string): Promise<string> {
  const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
  const path = (await import(/* @vite-ignore */ pathName)) as unknown as NodePath
  const proc = (await import(/* @vite-ignore */ procName)) as unknown as NodeProcess
  return fs.readFileSync(path.resolve(proc.cwd(), rel), 'utf8')
}

describe('prose reading surface', () => {
  it('the global markdown-body carries the serif prose tokens', async () => {
    const css = await read('src/styles/design-system/typography.css')
    const block = css.match(/\.markdown-body\s*\{([^}]*)\}/)?.[1] ?? ''
    expect(block).toContain('var(--font-display)')
    expect(block).toContain('var(--text-prose)')
    expect(block).toContain('var(--leading-prose)')
  })

  it('the prose tokens sit in the reference band (16-18px, 1.6-1.8)', async () => {
    const css = await read('src/styles/design-system/tokens.css')
    const size = Number(css.match(/--text-prose:\s*(\d+(?:\.\d+)?)px/)?.[1])
    const leading = Number(css.match(/--leading-prose:\s*(\d+(?:\.\d+)?)/)?.[1])
    expect(size).toBeGreaterThanOrEqual(16)
    expect(size).toBeLessThanOrEqual(18)
    expect(leading).toBeGreaterThanOrEqual(1.6)
    expect(leading).toBeLessThanOrEqual(1.8)
  })

  it('every markdown-body carrier resolves the cascade by doubling', async () => {
    // name -> the doubled selector each file must contain
    const carriers: Array<[string, RegExp]> = [
      // reading surfaces: serif prose wins, deterministically
      ['src/pages/wiki/WikiDetail.module.css', /^\.body\.body\s*\{/m],
      ['src/components/NoteSection.module.css', /^\.body\.body\s*\{/m],
      ['src/pages/home/HomeKnowledgeAssistant.module.css', /^\.markdownText\.markdownText\s*\{/m],
      // chrome surfaces: the compact sans wins, deterministically
      ['src/pages/knowledge/KnowledgeCard.module.css', /^\.snippet\.snippet\s*\{/m],
      ['src/pages/project/ProjectOverviewSection.module.css', /^\.readme\.readme\s*\{/m],
      ['src/pages/Review.module.css', /^\.body\.body\s*\{/m],
    ]
    for (const [file, pattern] of carriers) {
      const css = await read(file)
      expect(pattern.test(css), `${file} must double the selector that shares an element with .markdown-body`).toBe(true)
    }
  })
})
