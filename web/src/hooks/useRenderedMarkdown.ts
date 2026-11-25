import { useEffect, useMemo, useState } from 'react'
import { renderMarkdown, renderMarkdownAsync } from '../utils/markdown'

/**
 * Renders markdown through the async renderer so ```mermaid blocks become SVG.
 *
 * The sync pass stays as the immediate fallback: it shows the newest text
 * while the async render is in flight, and prevents another document's body
 * from flashing when the caller switches content (note navigation, stream
 * deltas). Only the async result — the one that can carry diagrams — is
 * cached, and it is tagged with the exact source it was rendered from so a
 * late resolution can never paint stale content.
 *
 * `debounceMs` is for streaming sources: every token replaces the source, so
 * re-parsing the whole answer per delta is wasteful. Static bodies leave it 0.
 */
export function useRenderedMarkdown(source: string, debounceMs = 0): string {
  const hasDiagram = source.includes('```mermaid')
  const [asyncRender, setAsyncRender] = useState<{ source: string; html: string } | null>(null)
  const syncHtml = useMemo(() => renderMarkdown(source), [source])

  useEffect(() => {
    // No diagram → the async renderer would produce byte-identical HTML after
    // re-parsing the whole body (note lists can hold dozens of cards).
    if (!hasDiagram) return undefined
    let cancelled = false
    const run = () => {
      renderMarkdownAsync(source)
        .then(html => { if (!cancelled) setAsyncRender({ source, html }) })
        .catch(() => { if (!cancelled) setAsyncRender(null) })
    }
    if (debounceMs > 0) {
      const timer = setTimeout(run, debounceMs)
      return () => { cancelled = true; clearTimeout(timer) }
    }
    run()
    return () => { cancelled = true }
  }, [source, debounceMs, hasDiagram])

  if (!hasDiagram) return syncHtml
  return asyncRender?.source === source ? asyncRender.html : syncHtml
}
