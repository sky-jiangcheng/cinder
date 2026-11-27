import { describe, it, expect, vi } from 'vitest'

// Mock mermaid to avoid DOM dependency in unit tests
vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn().mockResolvedValue({ svg: '<svg>mock</svg>' }),
  },
}))

// Mock katex to avoid CSS import issues
vi.mock('katex', () => ({
  default: {
    renderToString: vi.fn().mockReturnValue('<span class="katex-mock">math</span>'),
  },
}))

// Mock highlight.js
vi.mock('highlight.js', () => ({
  default: {
    highlight: vi.fn().mockReturnValue({ value: 'highlighted' }),
    highlightAuto: vi.fn().mockReturnValue({ value: 'auto-highlighted' }),
  },
}))

import { renderMarkdown, stripMarkdown, stripThinking, parseTags, joinTags } from './markdown'

describe('renderMarkdown', () => {
  it('returns empty string for empty input', () => {
    expect(renderMarkdown('')).toBe('')
  })

  it('renders basic markdown to HTML', () => {
    const result = renderMarkdown('**bold** and *italic*')
    expect(result).toContain('<strong>bold</strong>')
    expect(result).toContain('<em>italic</em>')
  })

  it('strips YAML frontmatter', () => {
    const result = renderMarkdown('---\ntitle: test\n---\n# Hello')
    expect(result).not.toContain('title: test')
    expect(result).toContain('Hello')
  })

  it('renders GFM callout blocks', () => {
    const result = renderMarkdown('> [!NOTE]\n> This is a note')
    expect(result).toContain('callout')
    expect(result).toContain('callout-note')
    expect(result).toContain('This is a note')
  })

  // Regression: the old placeholder regex (>\s*\[!TYPE\]\s*[\s\S]*?(?=\n>|$))
  // swallowed the blank line after the callout and then ran to the end of the
  // document — everything after the callout silently vanished from the render.
  it('keeps content that follows a callout followed by a blank line', () => {
    const result = renderMarkdown('> [!NOTE]\n> callout content\n\nNormal paragraph after.')
    expect(result).toContain('callout content')
    expect(result).toContain('Normal paragraph after.')
  })

  // Regression: the old regex stopped at the first \n> boundary, leaving the
  // tail lines of the callout in the source to render a second time as an
  // ordinary blockquote.
  it('does not duplicate callout tail lines when followed by plain text', () => {
    const result = renderMarkdown('> [!NOTE]\n> c1\n> c2\nText after.')
    expect(result.match(/c2/g)?.length).toBe(1)
    expect(result).toContain('Text after.')
  })

  // Two callouts of the same type must each render their own block (the
  // placeholder is index-scoped, not type-scoped).
  it('renders two same-type callouts independently', () => {
    const result = renderMarkdown('> [!NOTE]\n> first\n\nbetween\n\n> [!NOTE]\n> second\n\ntail')
    expect(result.match(/callout-note/g)?.length).toBe(2)
    expect(result).toContain('first')
    expect(result).toContain('between')
    expect(result).toContain('second')
    expect(result).toContain('tail')
    expect(result).not.toContain('%%CALLOUT')
  })

  // Unknown callout types stay ordinary blockquotes and must not swallow
  // following content.
  it('leaves unknown callout types as blockquotes', () => {
    const result = renderMarkdown('> [!FOO]\n> mystery\n\nafter')
    expect(result).not.toContain('callout-foo')
    expect(result).toContain('mystery')
    expect(result).toContain('after')
  })

  // Regression: injectCallouts used to pass the rendered callout as a STRING
  // replacement, and String.prototype.replace interprets `$` patterns in a
  // string replacement. The callout body is user content, so `$$` collapsed to
  // `$` (a note saying `echo $$` rendered as `echo $`) and `$&` expanded to the
  // matched placeholder — leaking the internal `%%CALLOUT_0_xxx%%` token into
  // the visible output.
  //
  // The dollars sit inside a code span because the inline-math renderer runs
  // AFTER the callout injection and would otherwise read `$...$` as a math
  // expression. A code span is stashed before math rendering — which is also
  // where these patterns realistically appear (a note quoting shell syntax).
  it('keeps dollar patterns in callout content literal', () => {
    const result = renderMarkdown('> [!NOTE]\n> run `echo $$` and perl `$&` match')
    expect(result).toContain('echo $$')
    expect(result).toContain('$&amp;')
    expect(result).not.toContain('%%CALLOUT')
  })

  // `$'` splices the text AFTER the match into the replacement — so a callout
  // containing it duplicated the rest of the document into the callout body.
  it('does not splice the following document into a callout body', () => {
    const result = renderMarkdown('> [!NOTE]\n> literal `$`\'` token\n\nTAILMARKER')
    expect(result.match(/TAILMARKER/g)?.length).toBe(1)
    expect(result).not.toContain('%%CALLOUT')
  })

  it('renders code blocks with syntax highlighting', () => {
    const result = renderMarkdown('```js\nconsole.log("hi")\n```')
    expect(result).toContain('<pre><code')
    expect(result).toContain('hljs')
    // The language badge must read a clean name, not the library's class list.
    expect(result).toContain('data-language="js"')
  })

  it('renders inline code', () => {
    const result = renderMarkdown('use `npm install`')
    expect(result).toContain('<code>npm install</code>')
  })

  // Regression: $...$ replacement used to run over the whole HTML string,
  // including <pre>/<code> content — a code block containing `$1 + $2$` was
  // rewritten into a KaTeX span and the code became garbage.
  it('leaves dollar signs inside code blocks untouched', () => {
    const result = renderMarkdown('```js\nconst price = $1 + $2$\n```\n\nafter')
    expect(result).toContain('$1 + $2$')
    expect(result).not.toContain('katex-mock')
    expect(result).toContain('after')
  })

  it('leaves dollar signs inside inline code untouched', () => {
    const result = renderMarkdown('run `export $HOME/bin` now')
    expect(result).toContain('$HOME/bin')
    expect(result).not.toContain('katex-mock')
  })

  it('still renders math outside code', () => {
    const result = renderMarkdown('E = $mc^2$ and `cost $5`')
    expect(result).toContain('katex-mock')
    expect(result).toContain('$5')
  })

  it('renders links', () => {
    const result = renderMarkdown('[Google](https://google.com)')
    expect(result).toContain('href="https://google.com"')
    expect(result).toContain('Google')
  })

  it('renders task lists', () => {
    const result = renderMarkdown('- [x] done\n- [ ] todo')
    expect(result).toContain('done')
    expect(result).toContain('todo')
  })
})

describe('renderMarkdown with math', () => {
  it('renders inline math with KaTeX', () => {
    const result = renderMarkdown('E = $mc^2$')
    expect(result).toContain('katex-mock')
  })

  it('renders block math with KaTeX', () => {
    const result = renderMarkdown('$$\nx^2 + y^2 = z^2\n$$')
    expect(result).toContain('katex-mock')
  })
})

describe('stripMarkdown', () => {
  it('returns empty string for empty input', () => {
    expect(stripMarkdown('')).toBe('')
  })

  it('strips markdown formatting', () => {
    expect(stripMarkdown('**bold** and *italic*')).toBe('bold and italic')
  })

  it('strips code blocks', () => {
    expect(stripMarkdown('```js\ncode\n``` other')).toBe('other')
  })

  it('strips inline code', () => {
    const result = stripMarkdown('text `code` end')
    expect(result).toContain('end')
  })

  it('truncates to max length', () => {
    const long = 'a'.repeat(200)
    const result = stripMarkdown(long, 50)
    expect(result).toHaveLength(51) // 50 + '…'
    expect(result).toMatch(/…$/)
  })

  it('strips frontmatter', () => {
    expect(stripMarkdown('---\ntitle: x\n---\nHello')).toBe('Hello')
  })

  it('strips callout lines', () => {
    expect(stripMarkdown('> [!NOTE]\n> content\nNormal')).toBe('Normal')
  })

  it('strips headings markers', () => {
    expect(stripMarkdown('# Title')).toBe('Title')
  })

  // Regression (2026-10-21 knowledge-grid smoke test): the old character-class
  // strip mangled every date and leaked link URLs into the card excerpts.
  it('keeps hyphens inside dates and identifiers', () => {
    expect(stripMarkdown('日期：2026-09-01（实现于 1.7.0）')).toBe('日期：2026-09-01（实现于 1.7.0）')
    expect(stripMarkdown('字段 lines_added 与 daily_stats')).toBe('字段 lines_added 与 daily_stats')
  })

  it('drops the URL but keeps the label of a link', () => {
    expect(stripMarkdown('关联：[ADR-0002](0002-c-end-repositioning.md)')).toBe('关联：ADR-0002')
    expect(stripMarkdown('![架构图](docs/arch.png)')).toBe('架构图')
  })

  it('keeps bullet text while removing the marker', () => {
    expect(stripMarkdown('- 状态：Accepted\n- 日期：2026-08-17')).toBe('状态：Accepted 日期：2026-08-17')
  })

  it('does not treat a table rule as a list bullet', () => {
    expect(stripMarkdown('| a | b |\n|---|---|\n| 1 | 2 |')).toBe('a b 1 2')
  })
})

describe('parseTags', () => {
  it('parses comma-separated tags', () => {
    expect(parseTags('go, react, sqlite')).toEqual(['go', 'react', 'sqlite'])
  })

  it('trims whitespace', () => {
    expect(parseTags('  go ,  react  ')).toEqual(['go', 'react'])
  })

  it('filters empty strings', () => {
    expect(parseTags('go,,react,')).toEqual(['go', 'react'])
  })

  it('returns empty array for empty string', () => {
    expect(parseTags('')).toEqual([])
  })
})

describe('joinTags', () => {
  it('joins tags with comma and space', () => {
    expect(joinTags(['go', 'react'])).toBe('go, react')
  })

  it('returns empty string for empty array', () => {
    expect(joinTags([])).toBe('')
  })
})

describe('stripThinking', () => {
  it('removes a closed reasoning block', () => {
    expect(stripThinking('<think>plan: read the note first</think>The retry budget was raised.'))
      .toBe('The retry budget was raised.')
  })

  it('recognises both spellings, any case, and reasoning', () => {
    expect(stripThinking('<thinking>x</thinking>A.')).toBe('A.')
    expect(stripThinking('<THINK>x</THINK>A.')).toBe('A.')
    expect(stripThinking('<reasoning>x</reasoning>A.')).toBe('A.')
  })

  it('drops an unterminated block to the end (a streaming answer)', () => {
    expect(stripThinking('Answer so far.\n<think>still thinking')).toBe('Answer so far.')
  })

  it('removes a stray tag and keeps the text around it', () => {
    expect(stripThinking('Answer.</think>')).toBe('Answer.')
  })

  it('keeps the notes out of an excerpt', () => {
    expect(stripMarkdown('<think>notes</think>The answer.')).toBe('The answer.')
  })
})
