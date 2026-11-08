// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'

const callMock = vi.fn()
vi.mock('./transport', () => ({ call: (...args: unknown[]) => callMock(...args) }))

import { latestReviewReport, reviewNotifyCount, runReviewNow } from './codeReviewEndpoints'

beforeEach(() => {
  callMock.mockReset()
  callMock.mockResolvedValue(null)
})

describe('code review endpoint routing contract', () => {
  it('latestReviewReport routes to the LatestReviewReport binding', async () => {
    callMock.mockResolvedValue({ run: null, findings: [] })
    await latestReviewReport()
    expect(callMock).toHaveBeenCalledWith({
      method: 'LatestReviewReport',
      args: [],
      path: '/review/latest',
    })
  })

  it('latestReviewReport unwraps a null payload to an empty snapshot', async () => {
    const got = await latestReviewReport()
    expect(got).toEqual({ findings: [] })
  })

  it('reviewNotifyCount routes to the ReviewNotifyCount binding', async () => {
    callMock.mockResolvedValue(3)
    const got = await reviewNotifyCount()
    expect(callMock).toHaveBeenCalledWith({
      method: 'ReviewNotifyCount',
      args: [],
      path: '/review/notify',
    })
    expect(got).toBe(3)
  })

  // The rail dot must never be drawn from a bad payload: a missing count, a
  // negative number or a string all mean "do not disturb".
  it('reviewNotifyCount clamps anything that is not a positive number to zero', async () => {
    for (const bad of [null, undefined, -2, '5', {}]) {
      callMock.mockResolvedValueOnce(bad)
      expect(await reviewNotifyCount()).toBe(0)
    }
  })

  // A Wails build that predates the binding throws *synchronously* (the
  // transport checks `typeof app[method] !== 'function'` before any promise
  // exists), so a downstream .catch() never sees it. The dot must still be
  // zero rather than an exception in the middle of a render.
  it('reviewNotifyCount survives a transport that throws synchronously', async () => {
    callMock.mockImplementationOnce(() => {
      throw new Error('Wails method not found: ReviewNotifyCount')
    })
    await expect(reviewNotifyCount()).resolves.toBe(0)
  })

  it('runReviewNow sends positional args and a body the RPC bridge can read', async () => {
    await runReviewNow({ date: '2026-01-02', force: true, noAI: true })
    const arg = callMock.mock.calls[0][0]
    expect(arg.method).toBe('RunReviewNow')
    expect(arg.args).toEqual(['2026-01-02', '', true, true, false])
    expect(JSON.parse(arg.init.body)).toEqual({
      date: '2026-01-02',
      scope: '',
      force: true,
      no_ai: true,
      dry_run: false,
    })
  })
})
