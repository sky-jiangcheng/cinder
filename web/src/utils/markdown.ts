import { marked } from 'marked'
import DOMPurify from 'dompurify'
// 'highlight.js/lib/common' bundles ~37 common languages instead of all ~190.
// The full entry point alone was ~1 MB and dragged the knowledge chunk past
// Vite's 500 kB warning threshold; unknown languages still fall back to
// auto-detection below.
import hljs from 'highlight.js/lib/common'
import katex from 'katex'
import 'katex/dist/katex.min.css'
import 'highlight.js/styles/github.css'
import 'highlight.js/styles/github-dark.css'
import { iconMarkup, type IconName } from '../components/Icon'

// Mermaid is by far the heaviest dependency (its diagram engines are several
// MB on their own), so it is loaded on demand: notes without a ```mermaid
// fence never download it.
let mermaidModule: typeof import('mermaid')['default'] | null = null

async function loadMermaid() {
  if (!mermaidModule) {
    const { default: mermaid } = await import('mermaid')
    // securityLevel 'strict' (the default) blocks click callbacks and HTML in
    // diagram definitions; the rendered SVG is also passed through DOMPurify
    // afterwards.
    mermaid.initialize({
      startOnLoad: false,
      theme: 'default',
      securityLevel: 'strict',
      // DOMPurify (the last step of the render chain) strips foreignObject and
      // its HTML children, which is where Mermaid puts labels by default.
      // SVG <text> labels survive sanitization, so diagrams keep their text.
      htmlLabels: false,
    })
    mermaidModule = mermaid
  }
  return mermaidModule
}

// marked.parse is synchronous by default, but its type union includes a Promise
// form. Pin the options so we always get a string, then sanitize for safe HTML.
marked.setOptions({
  async: false,
  gfm: true,
  breaks: false,
})

// Callout mapping: > [!TYPE] message
const CALLOUT_TYPES = ['NOTE', 'TIP', 'IMPORTANT', 'WARNING', 'CAUTION', 'QUESTION'] as const
type CalloutType = typeof CALLOUT_TYPES[number]

interface CalloutBlock {
  type: CalloutType
  title: string
  content: string
}

interface CalloutExtraction {
  callouts: CalloutBlock[]
  // cleaned is the source with every callout's consumed lines replaced by one
  // unique placeholder line each.
  cleaned: string
}

// extractCallouts does in ONE line-based pass what parseCallouts plus the
// placeholder regex used to do in two: it collects the callout blocks and
// rewrites the source with each block's exact lines spliced out. The old
// regex (>\s*\[!TYPE\]\s*[\s\S]*?(?=\n>|$)) either swallowed every line after
// a callout (the \s* + $ lookahead ate a trailing blank line and ran to the
// end of the document) or stopped early and left residue lines that rendered
// a second time. Splicing the very line ranges the parser consumed makes both
// failure modes impossible.
function extractCallouts(content: string): CalloutExtraction {
  const lines = content.split('\n')
  const callouts: CalloutBlock[] = []
  const out: string[] = []
  let current: CalloutBlock | null = null
  for (const line of lines) {
    const m = line.match(/^>\s*\[!(\w+)\]\s*(.*)$/)
    if (m) {
      if (current) callouts.push(current)
      const type = m[1].toUpperCase() as CalloutType
      if (!CALLOUT_TYPES.includes(type)) {
        // Unknown callout type: not a block we own, leave the line as-is.
        current = null
        out.push(line)
        continue
      }
      current = { type, title: m[2].trim() || type.toLowerCase(), content: '' }
      out.push(`%%CALLOUT_${callouts.length}_${Math.random().toString(36).slice(2)}%%`)
    } else if (current && line.startsWith('>')) {
      current.content += line.slice(1).trimEnd() + '\n'
      // Consumed: emit nothing.
    } else {
      if (current) {
        callouts.push(current)
        current = null
      }
      out.push(line)
    }
  }
  if (current) callouts.push(current)
  return { callouts, cleaned: out.join('\n') }
}

function stripCalloutLines(content: string): string {
  const lines = content.split('\n')
  const filtered: string[] = []
  let inCallout = false
  for (const line of lines) {
    if (/^>\s*\[!\w+\]\s*/.test(line)) { inCallout = true; continue }
    if (inCallout && line.startsWith('>')) continue
    if (inCallout && !line.startsWith('>')) inCallout = false
    filtered.push(line)
  }
  return filtered.join('\n')
}

// renderCallout renders a single callout block to sanitized HTML.
function renderCallout(c: CalloutBlock): string {
  const inner = DOMPurify.sanitize(marked.parse(c.content) as string)
  const icon = getCalloutIcon(c.type)
  return `<div class="callout callout-${c.type.toLowerCase()}">
    <div class="callout-header">${icon} <span class="callout-title">${DOMPurify.sanitize(c.title)}</span></div>
    <div class="callout-content markdown-body">${inner}</div>
  </div>`
}

function getCalloutIcon(type: CalloutType): string {
  const icons: Record<CalloutType, IconName> = {
    NOTE: 'file-text',
    TIP: 'lightbulb',
    IMPORTANT: 'zap',
    WARNING: 'warning',
    CAUTION: 'warning',
    QUESTION: 'help',
  }
  return iconMarkup(icons[type])
}

// highlightCodeBlocks applies hljs to <pre><code> blocks. An unknown language
// makes hljs.highlight throw, which would crash the whole note render, so we
// fall back to auto-detection (which never throws on arbitrary text).
//
// The try/catch alone is not enough: hljs.highlight LOGS "Could not find the
// language 'x'" to the console *before* throwing, so every ```mermaid block
// (a language the common bundle does not carry, and which renderMarkdownAsync
// renders via mermaid instead of hljs) painted one console error per block —
// 15 errors on a single screenful. Check support first and skip the call
// entirely: the fallback is the same, minus the noise.
function highlightCodeBlocks(html: string): string {
  return html.replace(
    /<pre><code(?:\s+class="language-(\w+)")?>([\s\S]*?)<\/code><\/pre>/g,
    (_match, lang: string | undefined, code: string) => {
      const decoded = code
        .replace(/&lt;/g, '<')
        .replace(/&gt;/g, '>')
        .replace(/&amp;/g, '&')
        .replace(/&#39;/g, "'")
        .replace(/&quot;/g, '"')
      let highlighted: string
      if (lang && hljs.getLanguage(lang)) {
        try {
          highlighted = hljs.highlight(decoded, { language: lang }).value
        } catch {
          highlighted = hljs.highlightAuto(decoded).value
        }
      } else {
        // No declared language, or one this bundle does not carry (mermaid):
        // auto-detect, without ever handing hljs a name it would reject.
        highlighted = hljs.highlightAuto(decoded).value
      }
      // The stylesheet's language badge reads data-language. attr(class) used
      // to print "hljs language-ts" verbatim — the library's own class names,
      // not a language label a reader asked for.
      const label = lang || 'text'
      return `<pre><code class="hljs${lang ? ` language-${lang}` : ''}" data-language="${label}">${highlighted}</code></pre>`
    }
  )
}

// renderInlineMath renders $...$ and $$...$$ in HTML text. The KaTeX output is
// injected after the main DOMPurify pass, so it is sanitized here to keep the
// final string free of raw HTML (e.g. from \href or \htmlClass).
//
// Code segments are stashed before the replacements and restored after them:
// <pre>/<code> content is full of literal dollar signs (shell $VAR, prices,
// regexes), and without the stash `` $1 + $2$ `` inside a code block was
// rewritten into a KaTeX span, corrupting the code.
function renderInlineMath(html: string): string {
  const stash: string[] = []
  const stashed = html.replace(/<(pre|code)\b[\s\S]*?<\/\1>/g, (m) => {
    stash.push(m)
    return `%%CODE_STASH_${stash.length - 1}%%`
  })
  const withMath = stashed
    .replace(/\$\$([\s\S]+?)\$\$/g, (_match, expr: string) => {
      try {
        return katex.renderToString(expr.trim(), { displayMode: true, throwOnError: false })
      } catch {
        return `<span class="math-error">$$${expr}$$</span>`
      }
    })
    .replace(/\$([^$\n]+?)\$/g, (_match, expr: string) => {
      try {
        return katex.renderToString(expr.trim(), { displayMode: false, throwOnError: false })
      } catch {
        return `$${expr}$`
      }
    })
  const restored = withMath.replace(/%%CODE_STASH_(\d+)%%/g, (_m, i: string) => stash[Number(i)] ?? '')
  return DOMPurify.sanitize(restored)
}

// injectCallouts swaps each extraction placeholder for its rendered callout
// HTML. Placeholders are index-scoped (`%%CALLOUT_<i>_<rand>%%`), so N callouts
// — even N of the same type — each get exactly their own block.
//
// The replacement MUST be a function, not a string. String.prototype.replace
// interprets `$` patterns in a string replacement: `$$` collapses to `$`, `$&`
// expands to the matched placeholder, and `` $` `` / `$'` splice in the text
// before/after the match. The callout body is USER CONTENT (a note can
// legitimately contain `echo $$`, a perl `$&`, or a `$'`), so a string
// replacement silently rewrote the user's text and leaked the internal
// placeholder into the visible output. A function replacement is literal.
function injectCallouts(html: string, callouts: CalloutBlock[]): string {
  for (let i = 0; i < callouts.length; i++) {
    const placeholderRegex = new RegExp(`%%CALLOUT_${i}_\\w+%%`)
    html = html.replace(placeholderRegex, () => renderCallout(callouts[i]))
  }
  return html
}

// A gateway that does not separate reasoning_content leaves the model's
// reasoning inline in content, wrapped in <think>…</think>. It is the model's
// notes, not the answer: the answer is rendered as usual and the notes fold
// behind a summary, the shape CodeBuddy and the other IDE agents use. An
// unterminated block folds too — streaming delivers the open tag long before
// the close.
const THINK_BLOCK = /<(think|thinking|reason|reasoning)>([\s\S]*?)(?:<\/\1>|$)/gi
const THINK_TAG = /<\/?(?:think|thinking|reason|reasoning)>/gi

export function splitThinking(content: string): { thoughts: string[]; body: string } {
  const thoughts: string[] = []
  const body = String(content ?? '')
    .replace(THINK_BLOCK, (_, _tag: string, inner: string) => {
      const text = inner.trim()
      if (text) thoughts.push(text)
      return ''
    })
    .replace(THINK_TAG, '')
    .replace(/^\s*\n+/, '')
    .trimEnd()
  return { thoughts, body }
}

/** The answer without its reasoning notes (excerpts never show them). */
export function stripThinking(content: string): string {
  return splitThinking(content).body
}


// renderMarkdown renders markdown to HTML synchronously (no mermaid).
// Suitable for card snippets, note bodies, and any non-preview context where
// mermaid diagrams are not expected.
export function renderMarkdown(content: string): string {
  if (!content) return ''

  const { callouts, cleaned } = extractCallouts(content)

  // Strip frontmatter, render, sanitize
  const stripped = cleaned.replace(/^---\n[\s\S]*?\n---\n?/, '')
  const raw = marked.parse(stripped)
  let html: string
  if (typeof raw === 'string') {
    html = raw
  } else {
    html = ''
  }
  html = DOMPurify.sanitize(html)

  html = injectCallouts(html, callouts)

  // Syntax highlight code blocks
  html = highlightCodeBlocks(html)

  // Render math
  html = renderInlineMath(html)

  return html
}

// renderMarkdownAsync is the full async renderer: supports mermaid diagrams in
// addition to syntax highlighting, callouts, and math. Use this for large note
// bodies or preview panes where mermaid is expected.
export async function renderMarkdownAsync(content: string): Promise<string> {
  if (!content) return ''

  const { callouts, cleaned } = extractCallouts(content)

  // Render mermaid blocks first (async)
  const withMermaid = await renderMermaidBlocks(cleaned)

  // Strip frontmatter, render, sanitize
  const stripped = withMermaid.replace(/^---\n[\s\S]*?\n---\n?/, '')
  const raw = marked.parse(stripped)
  let html: string
  if (typeof raw === 'string') {
    html = raw
  } else {
    html = ''
  }
  html = DOMPurify.sanitize(html)

  html = injectCallouts(html, callouts)

  // Syntax highlight code blocks
  html = highlightCodeBlocks(html)

  // Render math
  html = renderInlineMath(html)

  return html
}

// MERMAID_FENCE is a cheap pre-check: /g is intentionally omitted so .test
// does not advance lastIndex before the extraction regex runs below.
const MERMAID_FENCE = /```mermaid/

async function renderMermaidBlocks(text: string): Promise<string> {
  if (!MERMAID_FENCE.test(text)) return text

  const mermaid = await loadMermaid()
  const mermaidRegex = /```mermaid\s*\n([\s\S]*?)\n```/g
  let result = text
  let match
  const replacements: Array<{ original: string; svg: string }> = []

  while ((match = mermaidRegex.exec(text)) !== null) {
    try {
      const { svg } = await mermaid.render(
        'mermaid-' + Math.random().toString(36).slice(2),
        match[1].trim()
      )
      replacements.push({ original: match[0], svg })
    } catch {
      // Leave original on render failure
    }
  }
  for (const r of replacements) {
    // Function-form replacement: the string form interprets $&/$'/$$ patterns
    // inside `svg` (diagram labels with a dollar sign corrupted the output)
    // and only replaced the first occurrence of identical content.
    result = result.replace(r.original, () => r.svg)
  }
  return result
}

// stripMarkdown returns a single-line plain-text excerpt of markdown content.
//
// It strips markdown SYNTAX, never bare characters. The previous version used a
// character class (`[#>*_\-[\]()!]`), which quietly mangled the content it was
// summarizing: a knowledge-card excerpt showed "2026 09 01" for 2026-09-01 and
// leaked the URL out of every `[ADR-0002](0002-….md)` link (observed on the
// knowledge grid, 2026-10-21 smoke test). Every rule below is anchored to the
// construct it owns — a bullet marker is only a bullet at the start of a line,
// an emphasis `_` only when it is not intraword (so `lines_added` survives).
export function stripMarkdown(content: string, max = 140): string {
  const cleaned = stripCalloutLines(stripThinking(content))
    .replace(/^---\n[\s\S]*?\n---\n?/, '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')   // images → alt text
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')    // inline links → label, URL dropped
    .replace(/\[([^\]]*)\]\[[^\]]*\]/g, '$1')   // reference links → label
    .replace(/`([^`]*)`/g, '$1')
    .replace(/^\s{0,3}#{1,6}\s+/gm, ' ')         // ATX heading markers
    .replace(/^\s{0,3}>+\s?/gm, '')             // blockquote markers
    .replace(/^\s{0,3}[-*+]\s+/gm, '')          // bullet markers
    .replace(/^\s{0,3}\d+[.)]\s+/gm, '')        // ordered-list markers
    .replace(/^\s{0,3}(?:=+|-{3,}|_{3,})\s*$/gm, ' ') // thematic breaks / setext underlines
    // Table handling: the delimiter row goes first, otherwise the pipe rule
    // below turns "|---|---|" into "--- ---" and it survives as body text.
    .replace(/^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$/gm, ' ')
    .replace(/^\s*\|.*$/gm, row => row.replace(/\|/g, ' ')) // remaining pipes
    .replace(/(\*\*|__)(?=\S)([\s\S]*?\S)\1/g, '$2')      // strong
    .replace(/(^|[\s(（])([*_])(?=\S)([^*_]*?\S)\2/g, '$1$3') // emphasis, intraword-safe
    .replace(/~~(?=\S)([\s\S]*?\S)~~/g, '$1')   // strikethrough
    .replace(/\s+/g, ' ')
    .trim()
  return cleaned.length > max ? cleaned.slice(0, max) + '…' : cleaned
}

// tagsFromString / tagsFromString helpers: tags are stored comma-separated.
export function parseTags(tags: string): string[] {
  return tags.split(',').map(t => t.trim()).filter(Boolean)
}

export function joinTags(tags: string[]): string {
  return tags.join(', ')
}
