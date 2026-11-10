import { useCallback, useEffect, useRef, useState } from 'react'

interface ApiDataState<T> {
  data: T | null
  loading: boolean
  error: string | null
}

type Fetcher<T> = () => Promise<T>

// Minimal module-level cache shared by every useApiData consumer. Entries are
// keyed by an arbitrary cache key; a TTL avoids stale data across long
// sessions. `invalidate` clears entries so mutations can force a refetch.
const cache = new Map<string, { data: unknown; at: number }>()
const inflight = new Map<string, Promise<unknown>>()

// Consumers waiting on one shared request. Each owns its own state, so the
// owner must broadcast the outcome (success or failure) to all of them.
type InflightResult = { ok: true; data: unknown } | { ok: false; error: string }
const waiters = new Map<string, Set<(r: InflightResult) => void>>()

function settleWaiters(key: string, result: InflightResult) {
  const set = waiters.get(key)
  if (!set) return
  waiters.delete(key)
  for (const cb of set) cb(result)
}

const listeners = new Set<() => void>()
function notify() { listeners.forEach(l => l()) }

/** Drop cached entries. With a prefix, only matching keys are removed. */
export function invalidateCache(prefix?: string) {
  if (!prefix) { cache.clear() } else {
    for (const k of [...cache.keys()]) if (k.startsWith(prefix)) cache.delete(k)
  }
  notify()
}

export interface UseApiDataOptions {
  /** Cache key; when omitted no caching happens. */
  cacheKey?: string
  /** Cache lifetime in ms (default 30s). */
  ttl?: number
}

/**
 * Fetches data from a fetcher with loading/error state and an optional
 * shared cache. `refetch` bypasses the cache. When `cacheKey` is set, two
 * mounted components using the same key share one request and one cache
 * entry (e.g. the projects list used by Dashboard, NoteSection and the
 * command palette).
 */
export function useApiData<T>(
  fetcher: Fetcher<T>,
  deps: unknown[] = [],
  options: UseApiDataOptions = {}
): ApiDataState<T> & { refetch: () => void } {
  const { cacheKey, ttl = 30_000 } = options
  const [state, setState] = useState<ApiDataState<T>>({ data: null, loading: true, error: null })
  const fetcherRef = useRef(fetcher)
  const requestSeq = useRef(0)
  useEffect(() => { fetcherRef.current = fetcher }, [fetcher])
  // Stringify deps for cache-busting comparisons.
  const depsKey = JSON.stringify(deps)
  const fullKey = cacheKey ? `${cacheKey}:${depsKey}` : undefined

  const load = useCallback(async (bypassCache: boolean, token = ++requestSeq.current) => {
    const isCurrent = () => token === requestSeq.current
    if (fullKey && !bypassCache) {
      const hit = cache.get(fullKey)
      if (hit && Date.now() - hit.at < ttl) {
        if (isCurrent()) setState({ data: hit.data as T, loading: false, error: null })
        return
      }
      if (inflight.has(fullKey)) {
        // Register instead of awaiting: the owner broadcasts the outcome to
        // every waiter, so a failure clears loading here too, not only in the
        // owner's own component.
        let set = waiters.get(fullKey)
        if (!set) { set = new Set(); waiters.set(fullKey, set) }
        set.add((r) => {
          if (!isCurrent()) return
          if (r.ok) setState({ data: r.data as T, loading: false, error: null })
          else setState({ data: null, loading: false, error: r.error })
        })
        return
      }
    }
    if (isCurrent()) setState(s => ({ ...s, loading: true, error: null }))
    const p = Promise.resolve().then(() => fetcherRef.current())
    if (fullKey) inflight.set(fullKey, p as Promise<unknown>)
    try {
      const data = await p
      if (fullKey) {
        cache.set(fullKey, { data, at: Date.now() })
        inflight.delete(fullKey)
        settleWaiters(fullKey, { ok: true, data })
      }
      if (isCurrent()) setState({ data, loading: false, error: null })
    } catch (e) {
      const error = e instanceof Error ? e.message : 'request failed'
      if (fullKey) {
        inflight.delete(fullKey)
        settleWaiters(fullKey, { ok: false, error })
      }
      if (isCurrent()) setState({ data: null, loading: false, error })
    }
  }, [fullKey, ttl])

  useEffect(() => {
    const token = ++requestSeq.current
    load(false, token)
    // Invalidate this effect's request when dependencies change or the
    // component unmounts; late results are ignored by the sequence guard.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    return () => { requestSeq.current++ }
  }, [fullKey, depsKey, load])

  // React to external invalidations while mounted.
  useEffect(() => {
    const onChange = () => { if (fullKey) load(true) }
    listeners.add(onChange)
    return () => { listeners.delete(onChange) }
  }, [fullKey, load])

  const refetch = useCallback(() => load(true), [load])
  return { ...state, refetch }
}
