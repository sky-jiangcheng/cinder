// utils/windowTitlebar.ts — double-click-to-zoom for the CSS drag strips.
//
// macOS hides the native title bar (main.go mac.TitleBarHidden), so the
// window's top strips are webview elements carrying --wails-draggable: drag.
// Dragging works through Wails' runtime, but a double click on them used to
// do nothing at all — native title bars maximize on double click, and the
// absence of that behavior reads as a broken window (user report 2026-11-24).
//
// The maximize call is WindowToggleMaximise, NOT ToggleFullscreen: zoom fills
// the work area and keeps the menu bar and dock, which is the behavior macOS
// users expect from a title-bar double click. A second double click restores.
//
// Two detection paths, deliberately:
//   - two mousedowns inside the double-click window — works even when Wails'
//     drag interception swallows the synthetic dblclick event;
//   - the browser's dblclick event — works even when a drag session ate one
//     of the mousedowns.
// A short cooldown after a toggle keeps the two paths from double-firing (two
// toggles would land the window back where it started, i.e. no visible change).

const DOUBLE_PRESS_MS = 400
const TOGGLE_COOLDOWN_MS = 500

let lastPressAt = 0
let lastToggleAt = 0

function toggleMaximize(): void {
  const now = Date.now()
  if (now - lastToggleAt < TOGGLE_COOLDOWN_MS) return
  lastToggleAt = now
  // In the wails webview window.runtime is injected; outside it (plain
  // `npm run dev` in a browser) there is nothing to maximize — no-op, not a
  // crash, so the dev server keeps working. The cast follows transport.ts's
  // pattern: the runtime global is injected by the shell, not declared by
  // the DOM lib.
  try {
    const rt = (window as unknown as { runtime?: { WindowToggleMaximise?: () => void } }).runtime
    rt?.WindowToggleMaximise?.()
  } catch {
    // No window runtime in this context; nothing to maximize.
  }
}

function isInteractive(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false
  // The strips overlay real chrome (ContextRail's header holds the collapse
  // button): a double click ON a button must stay the button's, not turn
  // into a window toggle.
  return Boolean(target.closest('button, a, input, textarea, select, [role="button"]'))
}

/**
 * onMouseDown for a drag strip. Two presses inside the double-click window
 * toggle the window's zoom state.
 */
export function stripMouseDown(e: { target: EventTarget | null }): void {
  if (isInteractive(e.target)) return
  const now = Date.now()
  if (now - lastPressAt < DOUBLE_PRESS_MS) {
    lastPressAt = 0
    toggleMaximize()
  } else {
    lastPressAt = now
  }
}

/**
 * onDoubleClick for a drag strip. The fallback path: fires when the browser
 * assembles a dblclick even though one of the mousedowns went missing.
 */
export function stripDoubleClick(e: { target: EventTarget | null }): void {
  if (isInteractive(e.target)) return
  toggleMaximize()
}
