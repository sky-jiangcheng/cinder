// api/codeReviewEndpoints.ts — the incremental code review surface (ADR-0053).
//
// Its own module rather than an addition to endpoints.ts, following the same
// pattern as memoryEndpoints.ts: a feature slice with one contract test is
// easier to keep honest than a growing list of functions in one file, and
// every caller imports through ../api/client, so the location is invisible to
// the app.
//
// The review is READ-mostly: two read paths (today's snapshot, one day's
// snapshot), one settings pair, and one trigger. The trigger is idempotent per
// day+scope on the backend, which is why the button can be pressed twice
// without a "are you sure" dialog.

import { call } from './transport'
import type { ReviewConfig, ReviewResult, ReviewSeverity, ReviewSnapshot } from './types'

/**
 * The most recent completed review, with its findings.
 *
 * A missing `run` means "never reviewed" — an empty finding list with a run
 * means "reviewed, nothing found". The page must tell those two apart, so the
 * distinction survives in the payload instead of being collapsed here.
 */
export function latestReviewReport(severities: ReviewSeverity[] = []): Promise<ReviewSnapshot> {
  return call<ReviewSnapshot>({
    method: 'LatestReviewReport',
    args: severities.length > 0 ? [severities] : [],
    path: '/review/latest',
  }).then(s => s ?? { findings: [] })
}

/** The daily-review settings (master switch, time, scope, AI, notify, budget). */
export function getReviewConfig(): Promise<ReviewConfig> {
  return call<ReviewConfig>({ method: 'GetReviewConfig', path: '/review/config' })
}

/** Persists the daily-review settings. */
export function updateReviewConfig(cfg: ReviewConfig): Promise<void> {
  return call<void>({ method: 'UpdateReviewConfig', args: [cfg], path: '/review/config' })
}

/**
 * How many findings the configured threshold considers worth attention.
 *
 * This is the rail dot's input (ADR-0053 D7). Zero is the common answer and
 * means "do not disturb" — including the case where a review ran and found
 * nothing.
 *
 * Failure is 0, and caught here rather than at the caller: the transport can
 * throw *synchronously* (Wails mode with a missing binding throws before any
 * promise exists), which a `.catch()` downstream would never see — and a
 * decorative dot must not take the navigation down with it.
 */
export async function reviewNotifyCount(): Promise<number> {
  try {
    const n = await call<number>({ method: 'ReviewNotifyCount', args: [], path: '/review/notify' })
    return typeof n === 'number' && n > 0 ? n : 0
  } catch {
    return 0
  }
}

/**
 * Runs a review now.
 *
 * `date` is the day being reviewed (empty = yesterday). `force` re-runs a day
 * that already completed; without it the backend returns the existing report
 * and sets `already_done`.
 */
export function runReviewNow(opts: {
  date?: string
  scope?: string
  force?: boolean
  noAI?: boolean
  dryRun?: boolean
} = {}): Promise<ReviewResult> {
  return call<ReviewResult>({
    method: 'RunReviewNow',
    args: [opts.date ?? '', opts.scope ?? '', !!opts.force, !!opts.noAI, !!opts.dryRun],
    path: '/review/run',
    init: {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        date: opts.date ?? '',
        scope: opts.scope ?? '',
        force: !!opts.force,
        no_ai: !!opts.noAI,
        dry_run: !!opts.dryRun,
      }),
    },
  })
}
