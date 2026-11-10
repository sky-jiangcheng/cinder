// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'

const checkUpdateMock = vi.fn()
const getConfigMock = vi.fn()
const startUpdateDownloadMock = vi.fn()
const cancelUpdateDownloadMock = vi.fn()
const updateDownloadStateMock = vi.fn()

vi.mock('./endpoints', () => ({
  checkUpdate: (...args: unknown[]) => checkUpdateMock(...args),
  getConfig: (...args: unknown[]) => getConfigMock(...args),
  startUpdateDownload: (...args: unknown[]) => startUpdateDownloadMock(...args),
  cancelUpdateDownload: (...args: unknown[]) => cancelUpdateDownloadMock(...args),
  updateDownloadState: (...args: unknown[]) => updateDownloadStateMock(...args),
}))

import {
  getUpdateState,
  runUpdateCheck,
  startUpdateCheck,
  subscribeUpdateState,
  type UpdateState,
} from './updateStatus'
import { beginUpdateDownload, getDownloadSnapshot } from './downloadStatus'

// The store is module-level, so its state survives between tests in this
// file. Each test resets it by publishing a fresh verdict.
beforeEach(async () => {
  vi.clearAllMocks()
  checkUpdateMock.mockResolvedValue({
    current: '2.12.3',
    latest: '2.12.3',
    has_update: false,
    release_url: 'https://example.invalid/release',
    published_at: '2026-10-24T10:00:00Z',
    notes: '',
    assets: [],
    download_url: '',
    download_name: '',
  })
  getConfigMock.mockResolvedValue({ config: {}, scan_roots: [] })
  // Drive one successful check so the shared state starts from a known point,
  // then clear the mocks again — the call-count assertions below are about
  // what the test itself triggers, not about this warm-up.
  await runUpdateCheck()
  vi.clearAllMocks()
})

/** Pretend to run inside the Wails desktop webview. */
function installDesktopRuntime() {
  ;(window as unknown as { go: unknown }).go = { app: { App: {} } }
}

describe('update status store', () => {
  it('publishes "current" when the running build is the newest one', async () => {
    const state = await runUpdateCheck()
    expect(state.phase).toBe('current')
    expect(getUpdateState().phase).toBe('current')
  })

  it('publishes "available" when a newer release exists', async () => {
    checkUpdateMock.mockResolvedValue({
      current: '2.12.3',
      latest: '2.13.0',
      has_update: true,
      release_url: 'https://example.invalid/release',
      published_at: '2026-10-24T10:00:00Z',
      notes: 'shiny',
      assets: [],
      download_url: 'https://example.invalid/repotory-darwin-arm64.dmg',
      download_name: 'repotory-darwin-arm64.dmg',
    })
    const state = await runUpdateCheck()
    expect(state.phase).toBe('available')
    expect(state.info?.latest).toBe('2.13.0')
  })

  it('keeps the previous verdict when a later check fails', async () => {
    checkUpdateMock.mockResolvedValue({
      current: '2.12.3',
      latest: '2.13.0',
      has_update: true,
      release_url: 'https://example.invalid/release',
      published_at: '2026-10-24T10:00:00Z',
      notes: '',
      assets: [],
      download_url: '',
      download_name: '',
    })
    await runUpdateCheck()

    checkUpdateMock.mockRejectedValue(new Error('network down'))
    await expect(runUpdateCheck()).rejects.toThrow('network down')

    // The badge must not silently revert to "no information" because a
    // re-check failed — a known-newer version is still known.
    const state = getUpdateState()
    expect(state.phase).toBe('error')
    expect(state.error).toContain('network down')
    expect(state.info?.latest).toBe('2.13.0')
  })

  it('notifies subscribers on every state change', async () => {
    const seen: UpdateState[] = []
    const unsubscribe = subscribeUpdateState(s => seen.push(s))
    await runUpdateCheck()
    unsubscribe()
    expect(seen.length).toBeGreaterThanOrEqual(2)
    expect(seen[seen.length - 1].phase).toBe('current')
  })
})

describe('startUpdateCheck', () => {
  it('runs one check on startup', async () => {
    installDesktopRuntime()
    await startUpdateCheck()
    expect(checkUpdateMock).toHaveBeenCalled()
  })

  it('skips the check when the user turned it off', async () => {
    installDesktopRuntime()
    getConfigMock.mockResolvedValue({ config: { update_check_enabled: '0' }, scan_roots: [] })
    await startUpdateCheck()
    expect(checkUpdateMock).not.toHaveBeenCalled()
  })

  it('does not touch the network outside the desktop runtime', async () => {
    // A browser tab pointed at a headless server has no use for the check.
    delete (window as unknown as { go?: unknown }).go
    await startUpdateCheck()
    expect(getConfigMock).not.toHaveBeenCalled()
    expect(checkUpdateMock).not.toHaveBeenCalled()
  })
})

describe('download status polling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    startUpdateDownloadMock.mockResolvedValue(undefined)
    updateDownloadStateMock.mockRejectedValue(new Error('rpc down'))
  })

  afterEach(() => { vi.useRealTimers() })

  it('stops polling and publishes a failed state after repeated backend errors', async () => {
    await beginUpdateDownload('https://example.invalid/a.dmg', 'a.dmg', 'sha', '2.13.0')
    // One refresh runs before the timer starts, then the 400ms ticks take
    // over; ten ticks are more than the 5-failure budget.
    await vi.advanceTimersByTimeAsync(400 * 10)

    expect(getDownloadSnapshot().state.phase).toBe('failed')
    const calls = updateDownloadStateMock.mock.calls.length
    await vi.advanceTimersByTimeAsync(4000)
    // The interval is gone: no further polls after giving up.
    expect(updateDownloadStateMock.mock.calls.length).toBe(calls)
  })
})
