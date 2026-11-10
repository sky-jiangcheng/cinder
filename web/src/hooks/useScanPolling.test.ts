import { renderHook, act } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('../api/client', () => ({
  getScanStatus: vi.fn(),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key }),
}))

import { useScanPolling } from './useScanPolling'
import { getScanStatus } from '../api/client'

const mockGetScanStatus = vi.mocked(getScanStatus)

const idle = { running: false, backfilling: false, message: '', progress: 0, total: 0 }
const running = (msg: string) => ({ running: true, backfilling: false, message: msg, progress: 1, total: 3 })

describe('useScanPolling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mockGetScanStatus.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('starts with scanning=false', () => {
    mockGetScanStatus.mockResolvedValue(idle)
    const { result } = renderHook(() => useScanPolling(() => {}))
    expect(result.current.scanning).toBe(false)
    expect(result.current.message).toBe('')
  })

  it('start() sets scanning=true and begins polling', () => {
    mockGetScanStatus.mockResolvedValue(idle)
    const { result } = renderHook(() => useScanPolling(() => {}))

    act(() => { result.current.start('Scanning…') })

    expect(result.current.scanning).toBe(true)
    expect(result.current.message).toBe('Scanning…')
    expect(result.current.doneMessage).toBe('')
  })

  it('calls onFinish when scan completes', async () => {
    const onFinish = vi.fn()
    mockGetScanStatus
      .mockResolvedValueOnce(running('in progress'))
      .mockResolvedValueOnce(idle)

    const { result } = renderHook(() => useScanPolling(onFinish))

    act(() => { result.current.start('Scanning…') })

    await act(async () => { await vi.advanceTimersByTimeAsync(2100) })

    expect(result.current.scanning).toBe(false)
    expect(onFinish).toHaveBeenCalledTimes(1)
  })

  it('auto-detects an already-running scan on mount', async () => {
    mockGetScanStatus.mockResolvedValue(running('already running'))

    const { result } = renderHook(() => useScanPolling(() => {}))

    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    expect(result.current.scanning).toBe(true)
    expect(result.current.message).toBe('already running')
  })

  // A local service that is not running yet is a normal state, but polling it
  // every 2 seconds forever burns CPU and fills the log for nothing. The
  // interval backs off after each failure.
  it('backs off instead of hammering an unreachable backend', async () => {
    mockGetScanStatus.mockRejectedValue(new Error('backend down'))

    const { result } = renderHook(() => useScanPolling(() => {}))
    act(() => { result.current.start('Scanning…') })

    // The mount check (1) plus the first failed tick at 2s (2). The next tick
    // is planned for 2s + 4s = 6s, not for 4s — that doubling is the backoff.
    // A fixed 2s interval would have fired twice by now (3 calls).
    await act(async () => { await vi.advanceTimersByTimeAsync(4000) })
    expect(mockGetScanStatus).toHaveBeenCalledTimes(2)

    // The second failure lands at 6s, and backs off further from there.
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(mockGetScanStatus).toHaveBeenCalledTimes(3)
  })

  it('resets the backoff once the backend answers again', async () => {
    mockGetScanStatus
      .mockRejectedValueOnce(new Error('backend down'))
      .mockResolvedValueOnce(running('back'))
      .mockResolvedValue(idle)

    const { result } = renderHook(() => useScanPolling(() => {}))
    act(() => { result.current.start('Scanning…') })

    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })   // fails
    await act(async () => { await vi.advanceTimersByTimeAsync(4000) })   // succeeds
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })   // healthy interval again
    expect(result.current.scanning).toBe(false)
  })

  it('gives up after repeated failures instead of scanning forever', async () => {
    mockGetScanStatus.mockRejectedValue(new Error('backend down'))

    const { result } = renderHook(() => useScanPolling(() => {}))
    act(() => { result.current.start('Scanning…') })

    // Enough time to exhaust the failure budget through the growing backoff.
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })
    expect(result.current.scanning).toBe(false)
    expect(result.current.message).toBe('Scan failed')
  })
})
