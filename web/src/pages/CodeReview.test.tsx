// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, fireEvent, within } from '@testing-library/react'
import { renderReady } from '../test/renderReady'

// Partial mock, same shape as the other page tests: override the backend calls
// this page makes, keep everything else real.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    latestReviewReport: vi.fn(),
    runReviewNow: vi.fn(),
  }
})

// Same stable-t convention as Home/Review: the page passes no defaultValue, so
// the key comes back verbatim and assertions can name it.
const mockT = (key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? key

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import {
  latestReviewReport, runReviewNow,
  type ReviewFinding, type ReviewRun, type ReviewSnapshot,
} from '../api/client'
import CodeReview from './CodeReview'

const mockLatest = vi.mocked(latestReviewReport)
const mockRun = vi.mocked(runReviewNow)

const finding = (over: Partial<ReviewFinding> = {}): ReviewFinding => ({
  id: 1,
  run_id: 1,
  repo_path: '/dev/pay',
  file_path: 'src/PayClient.java',
  line: 12,
  rule: 'feign_missing_body',
  severity: 'high',
  verdict: 'confirmed',
  evidence: 'Result<PayInfo> query(PayQueryReq req);',
  message: 'parameter has no @RequestBody',
  suggestion: 'add @RequestBody',
  ...over,
})

const run = (over: Partial<ReviewRun> = {}): ReviewRun => ({
  id: 1,
  run_date: '2026-11-07',
  scope: 'collected',
  status: 'done',
  started_at: '2026-11-08T09:30:00',
  repos: 1,
  commits: 3,
  files: 2,
  candidates: 1,
  findings: 1,
  skipped: 1,
  ai_calls: 2,
  ...over,
})

const snapshot = (findings: ReviewFinding[], over: Partial<ReviewRun> = {}): ReviewSnapshot => ({
  run: run({ ...over, candidates: findings.length, findings: findings.length }),
  findings,
})

beforeEach(() => {
  mockLatest.mockReset()
  mockRun.mockReset()
})

describe('CodeReview page', () => {
  // "Never reviewed" and "reviewed, found nothing" are different answers, and
  // the page must not collapse them: the first one has to point at the setting.
  it('points a first-time user at the setting instead of an empty report', async () => {
    mockLatest.mockResolvedValue({ findings: [] })
    renderReady(<CodeReview />)
    expect(await screen.findByText('codeReview.neverRan')).toBeTruthy()
    expect(screen.queryByText('codeReview.nothingFound')).toBeNull()
  })

  it('reports "reviewed, nothing found" without suggesting the feature is off', async () => {
    mockLatest.mockResolvedValue(snapshot([]))
    renderReady(<CodeReview />)
    expect(await screen.findByText('codeReview.nothingFound')).toBeTruthy()
    expect(screen.queryByText('codeReview.neverRan')).toBeNull()
  })

  // Older Go binaries marshaled an empty findings slice as JSON null (a nil
  // slice), and findings.length on null crashed the whole page — the report
  // behind "null is not an object (evaluating 'c?.findings.length')". The page
  // normalizes the payload, so a null list renders as the empty state.
  it('survives a legacy backend that serialized zero findings as null', async () => {
    mockLatest.mockResolvedValue({
      run: run({ findings: 0, candidates: 0 }),
      findings: null,
    } as unknown as Awaited<ReturnType<typeof latestReviewReport>>)
    renderReady(<CodeReview />)
    expect(await screen.findByText('codeReview.nothingFound')).toBeTruthy()
    // The toolbar still renders — the null list is normalized to [], so the
    // "all" filter shows a zero count instead of the page crashing.
    expect(screen.getByRole('button', { name: 'codeReview.severityAll' })).toBeTruthy()
  })

  it('groups by file, worst file first', async () => {
    mockLatest.mockResolvedValue(
      snapshot([
        finding({ id: 1, file_path: 'src/Low.java', severity: 'low', line: 3 }),
        finding({ id: 2, file_path: 'src/High.java', severity: 'high', line: 9 }),
        finding({ id: 3, file_path: 'src/High.java', severity: 'medium', line: 4 }),
      ]),
    )
    renderReady(<CodeReview />)

    const groups = await screen.findAllByRole('heading', { level: 2 })
    // A group inherits its most severe finding's rank, so the file holding the
    // high one comes first regardless of the order rows arrived in.
    expect(groups[0].textContent).toContain('src/High.java')
    expect(groups[1].textContent).toContain('src/Low.java')
  })

  it('severity filter narrows the report to the chosen level', async () => {
    mockLatest.mockResolvedValue(
      snapshot([
        finding({ id: 1, file_path: 'src/High.java', severity: 'high' }),
        finding({ id: 2, file_path: 'src/Low.java', severity: 'low' }),
      ]),
    )
    renderReady(<CodeReview />)
    await screen.findByText('src/High.java')

    // The filter buttons are the three severity tabs plus "all"; clicking the
    // low one leaves exactly one file group on screen.
    fireEvent.click(screen.getByRole('button', { name: /codeReview\.severity\.low/ }))
    expect(screen.queryByText('src/High.java')).toBeNull()
    expect(screen.getByText('src/Low.java')).toBeTruthy()
  })

  // The word "unverified" is the one that gets misread as "broken": the page
  // must render the verdict as its own label, never fold it into severity.
  it('renders the verdict next to the rule, not into the severity', async () => {
    mockLatest.mockResolvedValue(
      snapshot([finding({ verdict: 'unverified', severity: 'medium' })]),
    )
    renderReady(<CodeReview />)

    // Scoped to the finding card: the severity tab above carries the same label.
    const card = within(await screen.findByRole('article'))
    expect(card.getByText('feign_missing_body')).toBeTruthy()
    expect(card.getByText('codeReview.verdict.unverified')).toBeTruthy()
    expect(card.getByText('codeReview.severity.medium')).toBeTruthy()
  })

  it('says so when the run was already done today', async () => {
    mockLatest.mockResolvedValue(snapshot([finding()]))
    mockRun.mockResolvedValue({ already_done: true, dry_run: false, run: run(), findings: [], stored: [] })

    renderReady(<CodeReview />)
    await screen.findByText('feign_missing_body')
    fireEvent.click(screen.getByRole('button', { name: 'codeReview.runNow' }))

    // The toast is owned by App, so the page's visible proof is the reload it
    // performs afterwards: the second read is the one that has to happen.
    await screen.findByText('feign_missing_body')
    expect(mockRun).toHaveBeenCalledTimes(1)
    expect(mockLatest).toHaveBeenCalledTimes(2)
  })
})
