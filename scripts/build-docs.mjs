#!/usr/bin/env node
// scripts/build-docs.mjs — 文档站生成器（双语）。
//
// 内容源分两个 locale：
//   en  docs/en/**/*.md   → 输出到 docs/ 根路径（默认语言，/repotory/ 即英文）
//   zh  docs/**/*.md（不含 docs/en/）→ 输出到 docs/zh/ 子路径
//
// 两个树的相对结构互为镜像；英文页链接到尚未翻译的页面（如 ADR 详情）时
// 自动回退到中文版输出路径。侧栏标签取 docs/sidebar.json 的 label/label_en。
// 生成物不入库（.gitignore 忽略 docs/**/*.html），GitHub Pages 部署时由
// .github/workflows/pages.yml 先执行本脚本。
//
// 依赖 marked + mermaid + jsdom（复用 web/node_modules），运行方式：
//   node scripts/build-docs.mjs
//
// mermaid 图在构建时渲染为内联 SVG（write-time render）：```mermaid 代码块
// 经 mermaid.render() 产出 SVG 直写进 HTML，页面零运行时依赖、离线可用，
// 与 GitHub 对 ```mermaid 的原生渲染保持同源可读。渲染需要 jsdom 环境
// （mermaid 依赖 DOM + getBBox 布局量测），polyfill 见 renderMermaid()。
// 若渲染器不可用则自动降级为「保留源码文本」的 <pre>，构建不失败。

import { readFileSync, writeFileSync, existsSync, readdirSync, statSync, rmSync, mkdirSync } from 'node:fs'
import { dirname, join, relative, resolve, basename } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
import { createHash } from 'node:crypto'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const docsDir = join(root, 'docs')

// The brand mark, inlined from the brand SSOT (build/icon.svg) — the one
// master that also generates the app icons, the favicons, the VS Code /
// JetBrains icons and the README banners. Read at build time rather than
// copied by hand so the docs sidebar and the shipped artifacts cannot
// drift apart: change the SSOT, re-run generate-icons.mjs, rebuild the
// docs, and every surface shows the same mark.
function brandMarkSvg(size) {
  const svgPath = join(root, 'build', 'icon.svg')
  if (!existsSync(svgPath)) {
    console.warn('build-docs: build/icon.svg missing — sidebar falls back to wordmark only')
    return ''
  }
  let svg = readFileSync(svgPath, 'utf8')
  // Keep only the geometry: drop the outer <svg> wrapper, the <title> (the
  // sidebar h1 already names the product, so a second title makes screen
  // readers announce "Cinder" twice) and the XML preamble. The <title>
  // sits *after* the opening <svg> tag, so the wrapper strip cannot catch it.
  svg = svg
    .replace(/<\?xml[^>]*\?>/g, '')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/^[\s\S]*?<svg[^>]*>/, '')
    .replace(/<\/svg>\s*$/, '')
    .replace(/<title>[\s\S]*?<\/title>/g, '')
    .trim()
  // Gradient ids are global to the document. The docs page inlines many SVGs
  // (mermaid diagrams too), so a collision would repaint this mark with
  // another diagram's gradient.
  const uid = `docs-brand-${size}`
  svg = svg.replace(/id="rp-([a-z]+)"/g, (_, n) => `id="${uid}-${n}"`)
  svg = svg.replace(/url\(#rp-([a-z]+)\)/g, (_, n) => `url(#${uid}-${n})`)
  return `<svg width="${size}" height="${size}" viewBox="0 0 512 512" aria-hidden="true" focusable="false">${svg}</svg>`
}

// Resolve marked from web/node_modules (the project keeps a single dep tree).
const requireFromWeb = createRequire(join(root, 'web', 'noop.js'))
const { marked } = requireFromWeb('marked')

// --- Build-time mermaid renderer -------------------------------------------------
//
// mermaid 11 needs a DOM (plus SVG text-metrics hooks jsdom does not provide).
// The polyfills below are the minimum that makes mermaid.render() work headless:
// CSSStyleSheet (adoptedStyleSheets path) and getBBox/getComputedTextLength
// (label measurement — 8px/char is a rough but stable approximation; layout
// differences vs a real browser are cosmetic, not structural).
//
// getBBox must be UNION-OF-CHILDREN for container elements, not a textContent
// estimate: mermaid reads the root <svg>'s bbox to size the viewBox, and a
// textContent-based estimate scales with total characters (a 4000-char diagram
// → a 32000px-wide viewBox with height 36), which flattens every flowchart into
// a hairline. Container tags recurse into children, accumulating their
// transform/x/y offsets; only text-bearing leaves get the 8px/char estimate.
let mermaidRender = null
const mermaidPending = []  // in-flight render promises, resolved before writeFileSync
try {
  const { JSDOM } = requireFromWeb('jsdom')
  const dom = new JSDOM('<!DOCTYPE html><body><div id="container"></div></body>', {
    pretendToBeVisual: true,
    url: 'http://localhost/',
  })
  const w = dom.window
  w.CSSStyleSheet = class {
    constructor() { this.cssRules = [] }
    replaceSync() {}
    insertRule() { return 0 }
  }
  // Container tags: union of children's boxes, offsets applied.
  // Text-bearing leaves: per-char width estimate (stable, cosmetic-only drift).
  const BBOX_CONTAINERS = new Set([
    'svg', 'g', 'a', 'div', 'p', 'span', 'foreignObject', 'marker', 'pattern', 'defs',
  ])
  // Text width estimate: CJK glyphs are full-width (~16px at 16px font), latin
  // ~8px. A flat 8px/char halves CJK label widths and overflows the boxes.
  const textWidthOf = (s) => {
    let w = 0
    for (const ch of s || '') w += ch.codePointAt(0) > 0x2e7f ? 16 : 8
    return Math.max(w, 40)
  }
  const translateOf = (el) => {
    const t = el.getAttribute && el.getAttribute('transform')
    const m = t && /translate\(\s*([-\d.]+)[ ,]+([-\d.]+)/.exec(t)
    return m ? [parseFloat(m[1]), parseFloat(m[2])] : [0, 0]
  }
  w.SVGElement.prototype.getBBox = function () {
    const kids = this.children ? Array.from(this.children) : []
    if (BBOX_CONTAINERS.has(this.tagName) && kids.length) {
      let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity, any = false
      for (const k of kids) {
        const b = typeof k.getBBox === 'function' ? k.getBBox() : null
        if (!b || !isFinite(b.width) || !isFinite(b.height)) continue
        const [tx, ty] = translateOf(k)
        const ax = parseFloat((k.getAttribute && k.getAttribute('x')) || 0)
        const ay = parseFloat((k.getAttribute && k.getAttribute('y')) || 0)
        const ox = tx + ax, oy = ty + ay
        any = true
        if (b.x + ox < x0) x0 = b.x + ox
        if (b.y + oy < y0) y0 = b.y + oy
        if (b.x + ox + b.width > x1) x1 = b.x + ox + b.width
        if (b.y + oy + b.height > y1) y1 = b.y + oy + b.height
      }
      if (any) {
        const bb = { x: x0, y: y0, width: Math.max(x1 - x0, 1), height: Math.max(y1 - y0, 1) }
        return bb
      }
    }
    // Shapes with explicit geometry (rect/foreignObject/…) report their own
    // box — ignoring x/y/width/height shrank every node to 40×20, which
    // clipped the root viewBox at the bottom.
    const wAttr = parseFloat(this.getAttribute && this.getAttribute('width'))
    const hAttr = parseFloat(this.getAttribute && this.getAttribute('height'))
    if (!isNaN(wAttr) && !isNaN(hAttr)) {
      return {
        x: parseFloat(this.getAttribute('x')) || 0,
        y: parseFloat(this.getAttribute('y')) || 0,
        width: wAttr,
        height: hAttr,
      }
    }
    // Cylinder / shape paths: reconstruct a generous box from the first arc
    // (a rx,ry) plus the body segment — over-measuring just pads the viewBox.
    if (this.tagName === 'path') {
      const d = this.getAttribute('d') || ''
      const arc = /a\s*([\d.]+)\s*,\s*([\d.]+)/.exec(d)
      const body = /l\s*0\s*,\s*([\d.]+)/.exec(d)
      if (arc) {
        const rx = parseFloat(arc[1])
        const ry = parseFloat(arc[2])
        const half = ry + (body ? parseFloat(body[1]) : 0)
        return { x: -rx, y: -half, width: 2 * rx, height: 2 * half }
      }
    }
    // <text> with row tspans: width = widest row, height = rows × line-height.
    // Concatenating textContent measured a 2-line label as one long line,
    // which pushed cylinder labels half a label-width outside the shape.
    if (this.tagName === 'text' && kids.length) {
      let w = 0
      let rows = 0
      for (const row of kids) {
        w = Math.max(w, textWidthOf(row.textContent))
        rows++
      }
      return { x: 0, y: 0, width: w, height: Math.max(rows * 17.6, 17.6) }
    }
    return { x: 0, y: 0, width: textWidthOf(this.textContent), height: 20 }
  }
  w.SVGElement.prototype.getComputedTextLength = function () {
    return textWidthOf(this.textContent)
  }
  for (const k of ['document', 'window', 'CSSStyleSheet', 'DOMParser', 'XMLSerializer',
    'Element', 'Node', 'HTMLElement', 'SVGElement', 'Text', 'CustomEvent', 'location']) {
    // navigator is a getter-only global in newer Node — skip it rather than fail.
    try { if (w[k] !== undefined) globalThis[k] = w[k] } catch { /* getter-only global */ }
  }
  if (w.navigator) {
    try { Object.defineProperty(globalThis, 'navigator', { value: w.navigator, configurable: true }) } catch { /* keep host navigator */ }
  }
  globalThis.getComputedStyle = w.getComputedStyle.bind(w)
  // mermaid lives in web/node_modules; import it via the same dep tree as marked.
  const { default: mermaid } = await import(requireFromWeb.resolve('mermaid'))
  // htmlLabels:false is load-bearing under mermaid 12: with html labels the
  // flowchart renderer sizes <foreignObject> from the div's layout height,
  // which jsdom (no layout engine) always reports as 0 — every label ships
  // with height="0" and the diagram renders as empty boxes. SVG-text labels
  // are measured through getComputedTextLength/getBBox above instead.
  mermaid.initialize({
    startOnLoad: false,
    theme: 'neutral',
    themeVariables: { fontSize: '17px' },
    securityLevel: 'strict',
    htmlLabels: false,
    flowchart: { htmlLabels: false, wrappingWidth: 320, nodeSpacing: 64, rankSpacing: 72 },
  })
  let seq = 0
  // mermaid 12 samples the root svg's bbox mid-render (before dagre has
  // placed everything) and ships that as the final viewBox, which pads some
  // diagrams with hundreds of pixels of dead space. Recompute the box from
  // the finished SVG instead: walk every shape with its accumulated
  // translate() and rebuild the viewBox from real geometry.
  const fixViewBox = (svgStr) => {
    const doc = new JSDOM(svgStr).window.document
    const svgEl = doc.querySelector('svg')
    if (!svgEl) return svgStr
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
    const grow = (ax, ay, bx, by) => {
      x0 = Math.min(x0, ax); y0 = Math.min(y0, ay)
      x1 = Math.max(x1, bx); y1 = Math.max(y1, by)
    }
    const walk = (el, tx, ty) => {
      const t = /translate\(\s*([-\d.]+)[ ,]+([-\d.]+)/.exec(el.getAttribute && el.getAttribute('transform') || '')
      let nx = tx, ny = ty
      if (t) { nx += parseFloat(t[1]); ny += parseFloat(t[2]) }
      const tag = el.tagName
      if (tag === 'defs' || tag === 'marker') return
      if (tag === 'rect' || tag === 'foreignObject') {
        const w = parseFloat(el.getAttribute('width') || '0')
        const h = parseFloat(el.getAttribute('height') || '0')
        if (w > 0 && h > 0) {
          const x = parseFloat(el.getAttribute('x') || '0')
          const y = parseFloat(el.getAttribute('y') || '0')
          grow(x + nx, y + ny, x + nx + w, y + ny + h)
        }
      } else if (tag === 'text') {
        const rows = el.children && el.children.length ? Array.from(el.children) : [el]
        let w = 0
        for (const r of rows) w = Math.max(w, textWidthOf(r.textContent))
        const yy = parseFloat(el.getAttribute('y') || '0')
        const h = Math.max(rows.length * 17.6, 17.6)
        grow(nx, ny + yy, nx + w, ny + yy + h)
      } else if (tag === 'path') {
        const d = el.getAttribute('d') || ''
        if (/^[\sMLQCAZ\d.,\s-]+$/.test(d)) {
          const nums = d.match(/-?\d+\.?\d*/g) || []
          for (let i = 0; i + 1 < nums.length; i += 2) {
            grow(parseFloat(nums[i]) + nx, parseFloat(nums[i + 1]) + ny, parseFloat(nums[i]) + nx, parseFloat(nums[i + 1]) + ny)
          }
        }
      }
      for (const c of (el.children ? Array.from(el.children) : [])) walk(c, nx, ny)
    }
    walk(svgEl, 0, 0)
    if (isFinite(x0)) {
      const pad = 8
      svgEl.setAttribute('viewBox', `${x0 - pad} ${y0 - pad} ${x1 - x0 + 2 * pad} ${y1 - y0 + 2 * pad}`)
    }
    return svgEl.outerHTML
  }
  mermaidRender = async (source) => {
    const { svg } = await mermaid.render(`mmd-${Date.now()}-${seq++}`, source)
    return fixViewBox(svg)
  }
  // smoke-test the pipeline once; on failure fall back to source-text output
  await mermaidRender('flowchart LR\n  A --> B')
  console.log('✓ mermaid 构建时渲染已启用（jsdom polyfill）')
} catch (err) {
  console.warn(`⚠ mermaid 渲染器不可用（${String(err).slice(0, 80)}），图将降级为源码文本`)
}

const sidebar = JSON.parse(readFileSync(join(docsDir, 'sidebar.json'), 'utf8'))
const version = JSON.parse(readFileSync(join(root, 'web', 'package.json'), 'utf8')).version
const REPO = 'https://github.com/sky-jiangcheng/cinder'

// --- Locale registry -------------------------------------------------------------
// srcRoot: where the locale's .md sources live; outRoot: where its .html goes.
// English is the default language: en pages land at the site root, zh under /zh/.

const locales = {
  en: {
    srcRoot: join(docsDir, 'en'),
    outRoot: docsDir,
    htmlLang: 'en',
    titleSuffix: 'Cinder Docs',
    brandSmall: `Docs · v${version}`,
    backLabel: '← GitHub repository',
    switcher: { self: 'English', other: '中文' },
    landingRedirect: true, // root landing only: send zh browsers to /zh/ once per session
  },
  zh: {
    srcRoot: docsDir,
    outRoot: join(docsDir, 'zh'),
    htmlLang: 'zh-CN',
    titleSuffix: 'Cinder 文档',
    brandSmall: `用户文档 · v${version}`,
    backLabel: '← 返回 GitHub 仓库',
    switcher: { self: '中文', other: 'English' },
    landingRedirect: false,
  },
}

const isEnSource = (rel) => rel === 'en' || rel.startsWith('en/')

// Source file for `base` in a locale; null when that locale has no translation.
const sourceOf = (locale, base) => {
  const p = join(locales[locale].srcRoot, base + '.md')
  return existsSync(p) ? p : null
}

// Output file for `base` in a locale (independent of source existence).
const outputFileOf = (locale, base) => join(locales[locale].outRoot, base + '.html')

// --- Markdown helpers ---------------------------------------------------------

function parseDoc(mdPath) {
  const raw = readFileSync(mdPath, 'utf8')
  let title = ''
  let body = raw
  const fm = raw.match(/^---\n([\s\S]*?)\n---\n/)
  if (fm) {
    body = raw.slice(fm[0].length)
    const t = fm[1].match(/^title:\s*(.+)$/m)
    if (t) title = t[1].trim().replace(/^["']|["']$/g, '')
  }
  if (!title) {
    const h1 = body.match(/^#\s+(.+)$/m)
    if (h1) title = h1[1].trim()
  }
  return { title, body }
}

// Rewrite relative .md links for one page. Links stay inside the page's locale
// when a same-locale source exists; otherwise they fall back to the other
// locale's output (e.g. an English page linking an untranslated ADR detail
// lands on the Chinese page instead of 404ing). Links leaving docs/ (README,
// packaging, TODO...) become GitHub blob URLs — no .html is generated for them.
function rewriteLinks(html, mdPath, locale, pageOutDir) {
  const other = locale === 'en' ? 'zh' : 'en'
  return html.replace(/href="([^"#]*?)\.(md|go)(#[^"]*)?"/g, (m, path, ext, hash) => {
    if (path === '' || path.startsWith('http')) return m
    const target = resolve(dirname(mdPath), path + '.' + ext)
    const relDocs = relative(docsDir, target)
    if (relDocs.startsWith('..') || ext === 'go') {
      // Outside docs/: point at the repo blob. Chinese pages link the
      // Chinese README companion.
      let relRepo = relative(root, target).split('\\').join('/')
      if (locale === 'zh' && relRepo === 'README.md') relRepo = 'README.zh-CN.md'
      return `href="${REPO}/blob/master/${relRepo}${hash ?? ''}"`
    }
    const base = relDocs.replace(/^en\//, '').replace(/\.md$/, '')
    // Prefer the page's own locale; fall back to the other one when this page
    // links something not yet translated; if neither has it, link the source
    // on GitHub instead of shipping a guaranteed 404.
    const targetLocale = sourceOf(locale, base) ? locale : sourceOf(other, base) ? other : null
    if (!targetLocale) {
      console.warn(`⚠ 未解析的 .md 链接（${locale} ${mdPath}）: ${path}.md`)
      const relRepo = relative(root, target).split('\\').join('/')
      return `href="${REPO}/blob/master/${relRepo}${hash ?? ''}"`
    }
    return `href="${relative(pageOutDir, outputFileOf(targetLocale, base)).split('\\').join('/')}${hash ?? ''}"`
  })
}

function renderMarkdown(mdPath, locale, pageOutDir) {
  const { title, body } = parseDoc(mdPath)
  let html = marked.parse(body, { gfm: true })
  // ```mermaid fenced blocks → inline SVG at build time (mermaidRender, above);
  // when the renderer is unavailable, keep the source as a readable <pre>.
  // GitHub renders ```mermaid natively, so the same sources stay readable on
  // github.com either way.
  html = html.replace(/<pre><code class="language-mermaid">([\s\S]*?)<\/code><\/pre>/g, (m, code) => {
    const decoded = code
      .replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"')
    if (mermaidRender) {
      try {
        // Queue the render now; the build loop awaits mermaidPending and splices
        // each SVG into the slot before writing the file.
        const idx = mermaidPending.length
        mermaidPending.push(
          mermaidRender(decoded).then(
            (svg) => ({ svg }),
            (err) => { console.warn(`⚠ 一张 mermaid 图渲染失败：${String(err).slice(0, 80)}`); return { svg: null } },
          ),
        )
        return '<div class="mermaid-slot" data-idx="' + idx + '"></div>'
      } catch {
        /* fall through to source text */
      }
    }
    return `<pre class="mermaid-fallback">${decoded}</pre>`
  })
  html = rewriteLinks(html, mdPath, locale, pageOutDir)
  return { title, html }
}

// --- Nav & switcher --------------------------------------------------------------

const labelFor = (locale, obj) => (locale === 'en' ? (obj.label_en ?? obj.label) : obj.label)
const titleFor = (locale, sec) => (locale === 'en' ? (sec.title_en ?? sec.title) : sec.title)

// href from a page's output dir to `base` rendered in `locale`'s tree,
// falling back to the other locale when this one has no translation.
function hrefFor(locale, base, pageOutDir) {
  const target = sourceOf(locale, base) ? locale : (locale === 'en' ? 'zh' : 'en')
  return relative(pageOutDir, outputFileOf(target, base)).split('\\').join('/')
}

const navHtml = (locale, pageOutDir) => sidebar.sections.map(sec => `
      <h3>${titleFor(locale, sec)}</h3>
${sec.items.map(it => `      <a href="${hrefFor(locale, it.file, pageOutDir)}" data-page="${it.file}">${labelFor(locale, it)}</a>`).join('\n')}
`).join('')

// English | 中文 — links to the same page in the other locale when it exists.
// Rendered as two fixed pill buttons (current = solid, other = outline) so the
// pair never wraps asymmetrically or orphans the separator — the old
// `English · 中文` inline text used to leave a dangling separator on narrow
// viewports.
function switcherHtml(locale, base, pageOutDir) {
  const loc = locales[locale]
  const other = locale === 'en' ? 'zh' : 'en'
  if (!sourceOf(other, base)) {
    return `<span class="lang-switch"><span class="lang-pill lang-current">${loc.switcher.self}</span></span>`
  }
  return `<span class="lang-switch"><span class="lang-pill lang-current">${loc.switcher.self}</span><a class="lang-pill lang-other" href="${relative(pageOutDir, outputFileOf(other, base)).split('\\').join('/')}">${loc.switcher.other}</a></span>`
}

// --- Template -------------------------------------------------------------------

function page(title, activeFile, contentHtml, locale, base, extraHead = '') {
  const loc = locales[locale]
  const outPath = outputFileOf(locale, base)
  const pageOutDir = dirname(outPath)
  const active = activeFile
    ? `document.querySelectorAll('.sidebar a[data-page]').forEach(a => { if (a.dataset.page === ${JSON.stringify(activeFile)}) a.classList.add('active') })`
    : ''
  const landingScript = loc.landingRedirect && base === 'index'
    ? `try{if(!sessionStorage.getItem('rn-lang')&&(navigator.language||'').toLowerCase().startsWith('zh')&&document.referrer.indexOf(location.host)===-1){sessionStorage.setItem('rn-lang','zh');location.replace('zh/index.html')}}catch(e){}`
    : ''
  // Explicit favicon declaration (the pages never had one — browsers fell
  // back to /favicon.ico, the most cache-stubborn URL on the web, so a
  // rebrand looked "not applied" for days). SVG first for crisp tabs, ICO
  // as the alternate for old browsers. Both carry a content-hash query:
  // the mark changes, the URL changes, the browser cache cannot serve the
  // stale icon. Relative to the page's own depth, like every other link.
  const iconHash = () => {
    try {
      return createHash('sha256').update(readFileSync(join(root, 'docs', 'favicon.svg'))).digest('hex').slice(0, 8)
    } catch {
      return ''
    }
  }
  const faviconSvgHref = relative(pageOutDir, join(root, 'docs', 'favicon.svg')).split('\\').join('/')
  const faviconIcoHref = relative(pageOutDir, join(root, 'docs', 'favicon.ico')).split('\\').join('/')
  const iconV = iconHash()
  return `<!DOCTYPE html>
<html lang="${loc.htmlLang}">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <link rel="icon" type="image/svg+xml" href="${faviconSvgHref}${iconV ? `?v=${iconV}` : ''}">
  <link rel="alternate icon" href="${faviconIcoHref}${iconV ? `?v=${iconV}` : ''}">
  <title>${title && !title.includes('Cinder') ? title + ' · ' : ''}${loc.titleSuffix}</title>
${extraHead}  <style>
    /* The accent is the brand's, not the framework's: the docs site used to
       wear Material Green 500, a leftover with no relation to anything else
       in the product, while the app wore a Tailwind blue and the icons wore
       warm paper + near-black — three colour languages for one brand
       (ADR-0037). The accent now comes from the same family as the app's:
       warm bronze on the light reading surface, warm amber on the dark
       sidebar (a dark bronze would be unreadable there). The neutrals below
       stay the docs site's own — re-theming them is a design project, not a
       brand-identity fix. */
    :root { --bg: #f8f9fa; --text: #1a1a2e; --muted: #6c757d; --accent: #7A3E12; --accent-on-dark: #E09155; --accent-soft: #F2E4CD; --border: #e2e8f0; --sidebar-w: 240px; }
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: var(--bg); color: var(--text); display: flex; min-height: 100vh; }
    a { color: var(--accent); text-decoration: none; }
    a:hover { text-decoration: underline; }
    .sidebar { width: var(--sidebar-w); background: #1a1a2e; color: #fff; padding: 24px 0; flex-shrink: 0; overflow-y: auto; position: fixed; height: 100vh; }
    .sidebar-brand { padding: 0 20px 20px; border-bottom: 1px solid rgba(255,255,255,0.1); margin-bottom: 12px; }
    .sidebar-brand-row { display: flex; align-items: center; gap: 10px; }
    .sidebar-brand-row svg { display: block; flex-shrink: 0; }
    .sidebar-brand h1 { font-size: 18px; font-weight: 700; }
    .sidebar-brand span { color: var(--accent-on-dark); }
    .sidebar-brand small { display: block; font-size: 11px; color: rgba(255,255,255,0.5); margin-top: 4px; }
    .lang-switch { display: inline-flex; gap: 0; margin-top: 8px; font-size: 11px; border: 1px solid rgba(255,255,255,0.25); border-radius: 6px; overflow: hidden; }
    .lang-switch .lang-pill { display: block; padding: 3px 10px; color: rgba(255,255,255,0.75); background: transparent; }
    .lang-switch .lang-pill.lang-current { font-weight: 700; color: #fff; background: rgba(255,255,255,0.12); }
    .lang-switch a.lang-pill.lang-other:hover { color: #fff; background: rgba(255,255,255,0.08); text-decoration: none; }
    .sidebar nav { padding: 8px 0; }
    .sidebar h3 { font-size: 10px; text-transform: uppercase; letter-spacing: 0.08em; color: rgba(255,255,255,0.4); padding: 12px 20px 6px; }
    .sidebar a { display: block; padding: 6px 20px; color: rgba(255,255,255,0.75); font-size: 13px; }
    .sidebar a:hover { background: rgba(255,255,255,0.08); color: #fff; text-decoration: none; }
    .sidebar a.active { color: var(--accent-on-dark); font-weight: 600; }
    .main { margin-left: var(--sidebar-w); flex: 1; padding: 40px 48px; max-width: 860px; }
    .main h1 { font-size: 28px; margin-bottom: 8px; }
    .main .subtitle { color: var(--muted); margin-bottom: 32px; font-size: 15px; }
    .main h2 { font-size: 20px; margin-top: 36px; margin-bottom: 12px; padding-bottom: 8px; border-bottom: 1px solid var(--border); }
    .main h3 { font-size: 16px; margin-top: 24px; margin-bottom: 8px; }
    .main p { line-height: 1.75; margin-bottom: 16px; font-size: 14px; color: #334155; }
    .main ul, .main ol { margin-bottom: 16px; padding-left: 24px; font-size: 14px; color: #334155; }
    .main li { margin-bottom: 6px; }
    .main code { background: #eef2f7; padding: 1px 5px; border-radius: 3px; font-size: 13px; font-family: 'SF Mono', Menlo, monospace; }
    .main pre { background: #1a1a2e; color: #e2e8f0; padding: 16px; border-radius: 8px; overflow-x: auto; margin-bottom: 16px; font-size: 13px; line-height: 1.6; }
    .main pre code { background: none; color: inherit; padding: 0; }
    .main table { width: 100%; border-collapse: collapse; margin-bottom: 16px; font-size: 13px; }
    .main th, .main td { border: 1px solid var(--border); padding: 8px 12px; text-align: left; }
    .main th { background: #f1f5f9; font-weight: 600; }
    .main blockquote { background: var(--accent-soft); border-left: 3px solid var(--accent); padding: 12px 16px; border-radius: 0 6px 6px 0; margin-bottom: 16px; font-size: 13px; color: #334155; }
    .main blockquote.warning { background: #fef3c7; border-left-color: #f59e0b; }
    .main .nav-links { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 32px; }
    .main .nav-links a { background: var(--bg); border: 1px solid var(--border); padding: 6px 14px; border-radius: 6px; font-size: 13px; color: var(--text); }
    .main .nav-links a:hover { border-color: var(--accent); color: var(--accent); text-decoration: none; }
    .main hr { border: none; border-top: 1px solid var(--border); margin: 32px 0; }
    .mermaid-container { background: #ffffff; border: 1px solid var(--border); border-radius: 8px; padding: 16px; margin-bottom: 16px; overflow-x: auto; text-align: center; }
    .mermaid-container svg { max-width: 100%; height: auto; }
    .mermaid-fallback { background: #eef2f7; color: #334155; padding: 16px; border-radius: 8px; font-size: 12px; white-space: pre-wrap; }
    .main .back-to-top { display: inline-block; font-size: 13px; color: var(--muted); margin-top: 32px; }
    @media (max-width: 768px) { .sidebar { display: none; } .main { margin-left: 0; padding: 24px; } }
  </style>
</head>
<body>
  <aside class="sidebar">
    <div class="sidebar-brand">
      <div class="sidebar-brand-row">
        ${brandMarkSvg(28)}
        <h1>Repo<span>tory</span></h1>
      </div>
      <small>${loc.brandSmall}</small>
      ${switcherHtml(locale, base, pageOutDir)}
    </div>
    <nav>${navHtml(locale, pageOutDir)}
    </nav>
  </aside>
  <main class="main">
${contentHtml}
    <hr>
    <a href="${REPO}" class="back-to-top">${loc.backLabel}</a>
  </main>
  <script>${active}</script>
  <script>${landingScript}</script>
</body>
</html>
`
}

// --- Build -----------------------------------------------------------------------

function collectMdFiles(dir, skip) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (skip && p === skip) continue
    if (statSync(p).isDirectory()) out.push(...collectMdFiles(p, skip))
    else if (name.endsWith('.md')) out.push(p)
  }
  return out
}

// Generated HTML is never committed; wipe last build's output so pages whose
// sources moved or were removed can't linger at stale URLs.
function cleanGeneratedHtml(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) { cleanGeneratedHtml(p); continue }
    if (name.endsWith('.html')) rmSync(p)
  }
}

// Sanity gates: every sidebar entry needs a source in BOTH locales, otherwise
// the missing tree ships a broken nav by construction.
const sidebarFiles = new Set(sidebar.sections.flatMap(s => s.items.map(it => it.file)))
let failed = false
for (const locale of Object.keys(locales)) {
  const missing = [...sidebarFiles].filter(f => !sourceOf(locale, f))
  if (missing.length > 0) {
    for (const f of missing) console.error(`✗ sidebar 条目缺失 ${locale} 源文件: ${relative(root, join(loc.srcRoot, f + '.md'))}`)
    failed = true
  }
}
if (failed) { process.exitCode = 1 } else {

cleanGeneratedHtml(docsDir)
mkdirSync(join(docsDir, 'zh'), { recursive: true })

const built = []
for (const locale of Object.keys(locales)) {
  const loc = locales[locale]
  const sources = locale === 'en' ? collectMdFiles(loc.srcRoot) : collectMdFiles(docsDir, join(docsDir, 'en'))
  for (const mdPath of sources) {
    const rel = relative(loc.srcRoot, mdPath).replace(/\.md$/, '').split('\\').join('/')
    const base = rel
    const outPath = outputFileOf(locale, base)
    const pageOutDir = dirname(outPath)
    const { title, html } = renderMarkdown(mdPath, locale, pageOutDir)
    // Wait out any queued mermaid renders, then splice each SVG into its slot.
    let finalHtml = html
    if (mermaidPending.length > 0) {
      const svgs = await Promise.all(mermaidPending.splice(0))
      finalHtml = html.replace(/<div class="mermaid-slot" data-idx="(\d+)"><\/div>/g, (m, idx) => {
        const rendered = svgs[Number(idx)]
        return rendered?.svg
          ? `<div class="mermaid-container">${rendered.svg}</div>`
          : '<pre class="mermaid-fallback">diagram rendering failed — see the mermaid source in docs/</pre>'
      })
    }
    mkdirSync(pageOutDir, { recursive: true })
    if (base === 'index') {
      // Landing page: inject quick nav links into the <!--NAV_LINKS--> slot.
      const quick = sidebar.sections.flatMap(s => s.items).slice(0, 7)
        .map(it => `      <a href="${hrefFor(locale, it.file, pageOutDir)}">${labelFor(locale, it)}</a>`).join('\n')
      writeFileSync(outPath, page(title || loc.titleSuffix, '', finalHtml.replace('<!--NAV_LINKS-->', quick), locale, base))
    } else {
      writeFileSync(outPath, page(title, base, finalHtml, locale, base))
    }
    built.push(relative(root, outPath))
  }
}

console.log(`✓ 生成 ${built.length} 个页面（v${version}，en 默认 / zh 在 zh/）：\n  ${built.join('\n  ')}`)
}
