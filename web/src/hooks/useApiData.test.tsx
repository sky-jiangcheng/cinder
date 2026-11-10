// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useApiData, invalidateCache } from './useApiData'

// useApiData resolves through Promise microtasks; flushing with a nested act
// is enough to settle them without relying on timers.
const flush = async () => { await act(async () => { await Promise.resolve() }) }

beforeEach(() => {
  invalidateCache()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useApiData', () => {
  it('loads data once and shares the request across consumers with the same key', async () => {
    const fetcher = vi.fn().mockResolvedValue([1, 2, 3])
    const a = renderHook(() => useApiData(fetcher, [], { cacheKey: 'projects' }))
    const b = renderHook(() => useApiData(fetcher, [], { cacheKey: 'projects' }))
    await flush()

    expect(a.result.current.data).toEqual([1, 2, 3])
    expect(b.result.current.data).toEqual([1, 2, 3])
    expect(a.result.current.loading).toBe(false)
    expect(b.result.current.loading).toBe(false)
    // Two mounts, one cache key -> a single fetch.
    expect(fetcher).toHaveBeenCalledTimes(1)
    a.unmount()
    b.unmount()
  })

  it('reuses the cached value within the TTL without refetching', async () => {
    const fetcher = vi.fn().mockResolvedValue('cached')
    const a = renderHook(() => useApiData(fetcher, [], { cacheKey: 'notes' }))
    await flush()
    expect(a.result.current.data).toBe('cached')
    a.unmount()

    const b = renderHook(() => useApiData(fetcher, [], { cacheKey: 'notes' }))
    await flush()
    expect(b.result.current.data).toBe('cached')
    expect(fetcher).toHaveBeenCalledTimes(1)
    b.unmount()
  })

  it('refetches after the TTL expires', async () => {
    vi.useFakeTimers()
    const fetcher = vi.fn().mockResolvedValueOnce('v1').mockResolvedValueOnce('v2')
    const a = renderHook(() => useApiData(fetcher, [], { cacheKey: 'ttl', ttl: 10_000 }))
    await flush()
    expect(a.result.current.data).toBe('v1')
    a.unmount()

    act(() => vi.advanceTimersByTime(11_000))
    const b = renderHook(() => useApiData(fetcher, [], { cacheKey: 'ttl', ttl: 10_000 }))
    await flush()
    expect(b.result.current.data).toBe('v2')
    expect(fetcher).toHaveBeenCalledTimes(2)
    b.unmount()
  })

  it('refetch() bypasses the cache', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce('old').mockResolvedValueOnce('new')
    const { result } = renderHook(() => useApiData(fetcher, [], { cacheKey: 'rf' }))
    await flush()
    expect(result.current.data).toBe('old')

    await act(async () => { result.current.refetch() })
    await flush()
    expect(result.current.data).toBe('new')
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('invalidateCache(prefix) drops only matching cache entries', async () => {
    const fetcher = vi.fn().mockResolvedValue('data')
    const a = renderHook(() => useApiData(fetcher, [], { cacheKey: 'projects:list' }))
    const b = renderHook(() => useApiData(fetcher, [], { cacheKey: 'notes:list' }))
    await flush()
    expect(fetcher).toHaveBeenCalledTimes(2)
    a.unmount()
    b.unmount()

    act(() => { invalidateCache('projects') })
    const c = renderHook(() => useApiData(fetcher, [], { cacheKey: 'projects:list' }))
    const d = renderHook(() => useApiData(fetcher, [], { cacheKey: 'notes:list' }))
    await flush()
    expect(fetcher).toHaveBeenCalledTimes(3)
    c.unmount()
    d.unmount()
  })

  it('reports the error message and clears loading', async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error('backend down'))
    const { result } = renderHook(() => useApiData(fetcher))
    await flush()
    expect(result.current.error).toBe('backend down')
    expect(result.current.loading).toBe(false)
    expect(result.current.data).toBeNull()
  })

  it('deduplicates a concurrent in-flight request on the same key', async () => {
    let resolveFn: ((v: string) => void) | undefined
    const fetcher = vi.fn().mockImplementation(() => new Promise<string>(r => { resolveFn = r }))
    const a = renderHook(() => useApiData(fetcher, [], { cacheKey: 'inflight' }))
    const b = renderHook(() => useApiData(fetcher, [], { cacheKey: 'inflight' }))
    await flush()
    expect(resolveFn).toBeDefined()

    await act(async () => { resolveFn!('done') })
    await flush()
    expect(a.result.current.data).toBe('done')
    expect(b.result.current.data).toBe('done')
    expect(fetcher).toHaveBeenCalledTimes(1)
    a.unmount()
    b.unmount()
  })

  it('propagates a deduplicated failure to both consumers', async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error('backend down'))
    const a = renderHook(() => useApiData(fetcher, [], { cacheKey: 'shared-fail' }))
    const b = renderHook(() => useApiData(fetcher, [], { cacheKey: 'shared-fail' }))
    await flush()

    // Two mounts, one shared request, and BOTH must leave loading with the
    // error — the waiter used to stay stuck at loading:true forever.
    expect(fetcher).toHaveBeenCalledTimes(1)
    for (const r of [a, b]) {
      expect(r.result.current.error).toBe('backend down')
      expect(r.result.current.loading).toBe(false)
      expect(r.result.current.data).toBeNull()
    }
    a.unmount()
    b.unmount()
  })
})