// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useDebouncedCallback } from './useDebouncedCallback'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('useDebouncedCallback', () => {
  it('invokes fn once after the delay of silence', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))

    act(() => {
      result.current('a')
      result.current('b')
      result.current('c')
    })
    expect(fn).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(299))
    expect(fn).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(1))
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('c')
  })

  it('passes the latest arguments from the last call', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 100))
    act(() => {
      result.current(1)
      result.current(2)
      result.current(3)
    })
    act(() => vi.advanceTimersByTime(100))
    expect(fn).toHaveBeenCalledWith(3)
  })

  it('clears the pending timer on unmount', () => {
    const fn = vi.fn()
    const { result, unmount } = renderHook(() => useDebouncedCallback(fn, 300))
    act(() => result.current('x'))
    unmount()
    act(() => vi.advanceTimersByTime(300))
    expect(fn).not.toHaveBeenCalled()
  })

  it('cancel() drops a pending call', () => {
    const fn = vi.fn()
    const { result } = renderHook(() => useDebouncedCallback(fn, 300))
    act(() => result.current('x'))
    act(() => result.current.cancel())
    act(() => vi.advanceTimersByTime(300))
    expect(fn).not.toHaveBeenCalled()
  })

  it('stays a stable reference across renders', () => {
    const fn = vi.fn()
    const { result, rerender } = renderHook(({ cb }) => useDebouncedCallback(cb, 100), {
      initialProps: { cb: fn },
    })
    const first = result.current
    rerender({ cb: fn })
    expect(result.current).toBe(first)
  })
})