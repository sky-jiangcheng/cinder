import { act, render, type RenderResult } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { ReactElement } from 'react'

// The async-readiness floor for frontend component tests (ADR-0026).
//
// What this exists for: a page that fills state from a promise inside a
// useEffect is queryable one render before its data lands. `findBy*` on an
// element that is mounted unconditionally therefore resolves immediately and
// waits for nothing, so a test that then reads effect-filled state synchronously
// depends on who wins the ordering — and on a loaded CI runner that is not the
// test. Measured on `Review.test.tsx`: delaying the three load promises by 300ms
// reddens 3 of its 12 tests, none of them the select case that was fixed first.
//
// `settle()` closes that window for every mock that resolves as an ordinary
// promise: the microtask queue is drained completely before the next macrotask,
// so crossing one macrotask boundary inside `act()` lands all effect state
// before the test body runs. It changes no mock defaults, which is precisely why
// it does not disturb the empty-state tests — the alternative that seeded
// non-empty defaults was rejected on measurement (see ADR-0026).
//
// Two boundaries, both load-bearing:
//
// 1. `settle()` uses a REAL `setTimeout`. Under `vi.useFakeTimers()` it never
//    resolves and the test hangs until a `waitFor` timeout. Today that is safe
//    because fake timers appear only in the `renderHook` tests
//    (`useDebouncedCallback`, `useConfirmClick`, `useScanPolling`, `useApiData`),
//    none of which goes through `renderReady`. Never combine the two.
// 2. Scope is the files that share `MemoryRouter` + mocked i18n:
//    `Review`, `Home`, `Dashboard`, `Knowledge`. (`Search` was deleted by
//    ADR-0038 — the search page is now the console's search mode.) `AiTab`
//    (no router, required
//    props), `NotFound` and `App.smoke` (real i18n instance) are deliberately
//    OUT — they get no `settle()` floor, so an interaction test written there
//    must wait for its own data. The split is not merged into one helper because
//    the mocked `t` returns a key or `defaultValue` while the real one returns a
//    translation, and assertions in those files depend on which.
//
// It also cannot drain chains that wait on real timers (debounce, polling
// intervals). Where a test reads something produced by those, wait for the
// element that IS the data instead — `pickProject` in `Review.test.tsx` is that
// pattern.

// ADR-0026 promotion criterion 1: a re-runnable probe. Set the variable to a
// number of milliseconds and the three load promises named in the ADR resolve
// that much later, so the race can be re-opened on demand rather than waited
// for. `REPO_TEST_LAG_MS=300 npx vitest run src/pages/Review.test.tsx` must be
// green; `cross-env` is already a devDependency if a Windows box needs it.
//
// There is no @types/node in this project, so `process` is reached through an
// explicit shape rather than assumed to be typed.
const proc = (globalThis as {
  process?: { env?: Record<string, string | undefined> }
}).process
export const TEST_LAG_MS = Number(proc?.env?.REPO_TEST_LAG_MS ?? 0)

/** Wrap a stubbed value so it resolves `TEST_LAG_MS` later than the default.
 *  A no-op when the knob is unset, which is every ordinary test run. */
export function lagged<T>(value: T): Promise<T> {
  if (!(TEST_LAG_MS > 0)) return Promise.resolve(value)
  return new Promise(resolve => {
    setTimeout(() => { resolve(value) }, TEST_LAG_MS)
  })
}

/** Cross one macrotask boundary inside `act()`, draining every already-created
 *  promise chain. See the fake-timer warning above before reaching for this. */
export async function settle(): Promise<void> {
  await act(async () => {
    await new Promise<void>(resolve => { setTimeout(resolve, 0) })
  })
}

/** Render under `MemoryRouter` and return only once the effect-launched data has
 *  landed. `initialEntries` exists for routes that read the query string. */
export async function renderReady(
  ui: ReactElement,
  memory?: { initialEntries: string[] },
): Promise<RenderResult> {
  const result = memory
    ? render(<MemoryRouter initialEntries={memory.initialEntries}>{ui}</MemoryRouter>)
    : render(<MemoryRouter>{ui}</MemoryRouter>)
  await settle()
  return result
}
