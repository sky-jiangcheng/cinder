import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The double-click-to-zoom contract: two fast presses toggle once, the two
// detection paths never double-toggle (two toggles land the window back where
// it was, i.e. no visible change), and interactive children keep their own
// double clicks.

// The press/cooldown timestamps live at module level, so every test imports a
// fresh copy — without the reset, test 1's toggle would put test 2 inside the
// cooldown and the whole suite would order-depend.
let { stripDoubleClick, stripMouseDown } = await import('./windowTitlebar')

describe('windowTitlebar', () => {
  let maximise: ReturnType<typeof vi.fn>

  beforeEach(async () => {
    vi.resetModules()
    ;({ stripDoubleClick, stripMouseDown } = await import('./windowTitlebar'))
    vi.useFakeTimers()
    maximise = vi.fn()
    ;(window as unknown as { runtime?: unknown }).runtime = { WindowToggleMaximise: maximise }
  })

  afterEach(() => {
    vi.useRealTimers()
    delete (window as unknown as { runtime?: unknown }).runtime
  })

  const press = (target: EventTarget | null = null) => stripMouseDown({ target })

  it('toggles once on two fast presses', () => {
    press()
    vi.advanceTimersByTime(100)
    press()
    expect(maximise).toHaveBeenCalledTimes(1)
  })

  it('does not toggle when the two presses are far apart', () => {
    press()
    vi.advanceTimersByTime(600)
    press()
    expect(maximise).not.toHaveBeenCalled()
  })

  it('the dblclick fallback alone still toggles', () => {
    stripDoubleClick({ target: null })
    expect(maximise).toHaveBeenCalledTimes(1)
  })

  it('the two paths never double-toggle in the same burst', () => {
    // The mousedown pair toggles; the browser-assembled dblclick arrives
    // milliseconds later and must be absorbed by the cooldown.
    press()
    vi.advanceTimersByTime(100)
    press()
    stripDoubleClick({ target: null })
    expect(maximise).toHaveBeenCalledTimes(1)
  })

  it('a second double click after the cooldown restores', () => {
    press()
    vi.advanceTimersByTime(100)
    press()
    vi.advanceTimersByTime(1000)
    press()
    vi.advanceTimersByTime(100)
    press()
    expect(maximise).toHaveBeenCalledTimes(2)
  })

  it('interactive children keep their own double clicks', () => {
    const button = document.createElement('button')
    press(button)
    vi.advanceTimersByTime(100)
    press(button)
    expect(maximise).not.toHaveBeenCalled()
    // And the fallback path honours the same guard.
    stripDoubleClick({ target: button } as never)
    expect(maximise).not.toHaveBeenCalled()
  })

  it('is a no-op without the wails runtime (browser dev)', () => {
    delete (window as unknown as { runtime?: unknown }).runtime
    press()
    vi.advanceTimersByTime(100)
    press()
    expect(maximise).not.toHaveBeenCalled()
  })
})
