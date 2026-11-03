import { describe, it, expect } from 'vitest'

// A <select> that sets `appearance: none` has thrown away the OS-drawn arrow,
// so the control has to draw its own. Two ways to get that wrong, and both of
// them shipped here:
//
//   1. the base rule sets no image at all -> the control renders NO arrow;
//   2. a theme rule sets only `background-image` -> `background-repeat` stays
//      at its initial `repeat`, and the 11x11 chevron is tiled across the
//      whole control (the "grid of arrows" the knowledge page's scope select
//      used to show in the dark theme).
//
// Neither failure is visible to the build, tsc or lint — they never read CSS.
// So the invariant is pinned here: for every class that strips the native
// appearance, the declarations that apply to that class must (a) carry an
// inline SVG background image and (b) pin the repeat to `no-repeat`.
//
// The analysis is per FILE on purpose. CSS-module class names are hashed per
// module, so `.select` in Knowledge.module.css has nothing to do with `.select`
// in Review.module.css; merging them would let one file's correct rule mask
// another's missing one.
//
// Files are read through node:fs rather than import.meta.glob('?raw'), which
// returns EMPTY strings for .css under vitest. The specifier is assembled at
// runtime so tsc does not require @types/node, which this project does not
// depend on; `npm run build` type-checks src/ and would otherwise fail on a
// test-only import.

const fsName = 'node:' + 'fs'
const pathName = 'node:' + 'path'
const procName = 'node:' + 'process'

interface NodeFs {
  readFileSync(p: string, enc: string): string
  readdirSync(
    p: string,
    opts: { withFileTypes: true },
  ): Array<{ name: string; isDirectory(): boolean }>
}
interface NodePath {
  resolve(...p: string[]): string
  join(...p: string[]): string
  relative(from: string, to: string): string
}
interface NodeProcess {
  cwd(): string
}

interface Rule {
  selector: string
  body: string
}

interface CssFile {
  file: string
  text: string
}

async function listCssFiles(): Promise<CssFile[]> {
  const fs = (await import(/* @vite-ignore */ fsName)) as unknown as NodeFs
  const path = (await import(/* @vite-ignore */ pathName)) as unknown as NodePath
  const proc = (await import(/* @vite-ignore */ procName)) as unknown as NodeProcess
  const root = path.resolve(proc.cwd(), 'src')
  const out: CssFile[] = []
  const walk = (dir: string) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) {
        walk(full)
        continue
      }
      if (!entry.name.endsWith('.css')) continue
      out.push({ file: path.relative(root, full), text: fs.readFileSync(full, 'utf8') })
    }
  }
  walk(root)
  return out
}

/**
 * Brace-aware rule extraction.
 *
 * A regex over `{...}` is not enough: inside `@media`, the prelude of the
 * inner rule would be swallowed by the at-rule, so a declaration block that
 * sets `appearance: none` under a breakpoint would go unnoticed.
 */
function parseRules(text: string): Rule[] {
  const rules: Rule[] = []
  let buf = ''
  let quote: string | null = null
  let i = 0
  while (i < text.length) {
    const ch = text[i]
    if (quote) {
      if (ch === '\\') {
        buf += ch + (text[i + 1] ?? '')
        i += 2
        continue
      }
      if (ch === quote) quote = null
      buf += ch
      i++
      continue
    }
    if (ch === '"' || ch === "'") {
      quote = ch
      buf += ch
      i++
      continue
    }
    if (ch === '{') {
      const selector = buf.trim()
      buf = ''
      let depth = 1
      let body = ''
      let j = i + 1
      while (j < text.length && depth > 0) {
        const c = text[j]
        if (c === '{') depth++
        else if (c === '}') {
          depth--
          if (depth === 0) break
        }
        body += c
        j++
      }
      if (selector.startsWith('@')) {
        // At-rule: descend into it so breakpoint-scoped rules are seen too.
        rules.push(...parseRules(body))
      } else if (selector) {
        rules.push({ selector, body })
      }
      i = j + 1
      continue
    }
    if (ch === '}') {
      buf = ''
      i++
      continue
    }
    buf += ch
    i++
  }
  return rules
}

/** Class tokens used in a selector list, e.g. `[data-theme='dark'] .select`. */
function classTokens(selector: string): string[] {
  const tokens: string[] = []
  const re = /\.(-?[_a-zA-Z][\w-]*)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(selector)) !== null) {
    const after = selector[m.index + m[0].length]
    if (after && /[\w-]/.test(after)) continue
    tokens.push(m[1])
  }
  return tokens
}

const APPEARANCE_NONE = /(^|[;{])\s*(-webkit-)?appearance\s*:\s*none\s*;/

/** Declarations that apply to `className` anywhere in this file. */
function declarationsFor(rules: Rule[], className: string): string {
  return rules
    .filter(r => r.selector.split(',').some(part => classTokens(part).includes(className)))
    .map(r => r.body)
    .join('\n')
}

describe('select arrow rendering', () => {
  it('every appearance-less select draws its arrow exactly once', async () => {
    const files = await listCssFiles()
    const broken: string[] = []

    for (const { file, text } of files) {
      const rules = parseRules(text)
      const stripped = new Set<string>()
      for (const rule of rules) {
        if (!APPEARANCE_NONE.test(rule.body)) continue
        for (const token of classTokens(rule.selector)) stripped.add(token)
      }
      for (const className of stripped) {
        const decls = declarationsFor(rules, className)
        const drawsImage = /background(-image)?\s*:[^;]*url\(/.test(decls)
        const pinsRepeat = /no-repeat/.test(decls)
        if (!drawsImage || !pinsRepeat) {
          broken.push(
            `${file} .${className} (image=${drawsImage ? 'yes' : 'no'}, no-repeat=${pinsRepeat ? 'yes' : 'no'})`,
          )
        }
      }
    }

    expect(
      broken,
      'A <select> with `appearance: none` must carry an inline SVG background image AND pin `no-repeat`; ' +
        'otherwise the light theme loses the arrow and a theme override that only swaps `background-image` tiles it.',
    ).toEqual([])
  })

  it('the tiled-arrow regression itself is still caught', async () => {
    // Proves the check above is not vacuous: re-introduce the exact defect
    // (base rule with no image, theme rule with `background-image` only) and
    // confirm the invariant would report it.
    const rules = parseRules(
      `.select {
         appearance: none;
         background-color: var(--bg-secondary);
       }
       [data-theme='dark'] .select {
         background-image: url("data:image/svg+xml,%3Csvg%3E%3C/svg%3E");
       }`,
    )
    const decls = declarationsFor(rules, 'select')
    const drawsImage = /background(-image)?\s*:[^;]*url\(/.test(decls)
    const pinsRepeat = /no-repeat/.test(decls)
    expect(drawsImage && pinsRepeat).toBe(false)
  })

  it('the knowledge scope select keeps its single arrow in both themes', async () => {
    // The control the user reported. It is the one whose dark theme tiled the
    // chevron across the whole box, so pin the shape of the fix here: the
    // base rule draws the arrow, and the theme rule only swaps its ink.
    const files = await listCssFiles()
    const knowledge = files.find(f => f.file.endsWith('Knowledge.module.css'))
    expect(knowledge).toBeDefined()
    const text = knowledge!.text
    const base = parseRules(text).find(r => /^\.select\s*$/.test(r.selector))
    expect(base, 'Knowledge.module.css lost its .select rule').toBeDefined()
    expect(base!.body).toMatch(/background\s*:[^;]*url\([^;]*no-repeat/)
    expect(base!.body).toMatch(/appearance\s*:\s*none/)
    const dark = parseRules(text).find(r => /\[data-theme=['"]dark['"]\]\s*\.select/.test(r.selector))
    expect(dark, 'Knowledge.module.css lost its dark-theme .select rule').toBeDefined()
    expect(dark!.body).toMatch(/background-image\s*:[^;]*url\(/)
  })
})
