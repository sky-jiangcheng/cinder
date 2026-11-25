import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'

vi.mock('../utils/markdown', async importOriginal => {
  const actual = await importOriginal<typeof import('../utils/markdown')>()
  return { ...actual, renderMarkdownAsync: vi.fn() }
})

import { renderMarkdownAsync } from '../utils/markdown'
import { useRenderedMarkdown } from './useRenderedMarkdown'

const asyncMock = vi.mocked(renderMarkdownAsync)
const diagramA = '```mermaid\ngraph TD\n  A --> B\n```'
const diagramB = '```mermaid\ngraph LR\n  C --> D\n```'

beforeEach(() => { asyncMock.mockReset() })

describe('useRenderedMarkdown', () => {
  it('skips the async pass when there is no diagram', () => {
    asyncMock.mockResolvedValue('<p>async</p>')
    const { result } = renderHook(() => useRenderedMarkdown('plain **text**'))
    expect(result.current).toContain('<strong>text</strong>')
    expect(asyncMock).not.toHaveBeenCalled()
  })

  it('shows the sync pass first, then swaps in the async SVG', async () => {
    let resolve!: (html: string) => void
    asyncMock.mockReturnValue(new Promise<string>(r => { resolve = r }))
    const { result } = renderHook(() => useRenderedMarkdown(diagramA))
    expect(result.current).toContain('<pre')
    resolve('<svg>mock A</svg>')
    await waitFor(() => expect(result.current).toBe('<svg>mock A</svg>'))
  })

  it('ignores a stale render that resolves after the source changed', async () => {
    const resolvers: Array<(html: string) => void> = []
    asyncMock.mockImplementation(() => new Promise<string>(r => { resolvers.push(r) }))
    const { result, rerender } = renderHook(
      ({ source }) => useRenderedMarkdown(source),
      { initialProps: { source: diagramA } },
    )
    rerender({ source: diagramB })
    resolvers[0]('<svg>stale A</svg>')
    await Promise.resolve()
    expect(result.current).not.toBe('<svg>stale A</svg>')
    resolvers[1]('<svg>fresh B</svg>')
    await waitFor(() => expect(result.current).toBe('<svg>fresh B</svg>'))
  })

  it('falls back to the sync pass when the async renderer fails', async () => {
    asyncMock.mockRejectedValue(new Error('mermaid unavailable'))
    const { result } = renderHook(() => useRenderedMarkdown(diagramA))
    await waitFor(() => expect(asyncMock).toHaveBeenCalled())
    expect(result.current).toContain('<pre')
  })
})
