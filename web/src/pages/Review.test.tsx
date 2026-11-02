// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import { useLocation, useNavigate } from 'react-router-dom'
import { lagged, renderReady } from '../test/renderReady'

// Partial mock: override the backend-calling endpoints, keep everything else
// (getConnectionKind etc.) real — same shape as Home.test.tsx.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    getProjects: vi.fn(),
    listPendingWikiPages: vi.fn(),
    listRejectedWikiPages: vi.fn(),
    listApprovedWikiPages: vi.fn(),
    listCompileJobs: vi.fn(),
    findMergeCandidates: vi.fn(),
    mergeWikiPages: vi.fn(),
    runCommunitySummary: vi.fn(),
    listWikiPageVersions: vi.fn(),
    diffWikiPageVersion: vi.fn(),
    approveWikiPage: vi.fn(),
    rejectWikiPage: vi.fn(),
    startCompileJob: vi.fn(),
    startLintJob: vi.fn(),
    cancelCompileJob: vi.fn(),
    deleteCompiledPage: vi.fn(),
    promoteWikiPageToGlobal: vi.fn(),
    assignWikiPageProject: vi.fn(),
  }
})

// Same stable-t convention as Home.test.tsx: components pass defaultValue inline,
// so the mock returns it verbatim and every other call yields its own key —
// asserting on 'review.mergeAdopt' still works after the i18n gap closes.
const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import {
  approveWikiPage, assignWikiPageProject, cancelCompileJob, deleteCompiledPage, diffWikiPageVersion,
  findMergeCandidates, getProjects, listApprovedWikiPages, listCompileJobs, listPendingWikiPages,
  listRejectedWikiPages, listWikiPageVersions, mergeWikiPages, promoteWikiPageToGlobal,
  rejectWikiPage, runCommunitySummary, startCompileJob, startLintJob,
  type CompileJob, type MergeCandidate, type MergeCandidateReport, type WikiCommunityReport,
  type WikiPageVersion, type WikiPendingPage,
} from '../api/client'
import ReviewPage, { jobErrorText } from './Review'

const mockGetProjects = vi.mocked(getProjects)
const mockListPending = vi.mocked(listPendingWikiPages)
const mockListRejected = vi.mocked(listRejectedWikiPages)
const mockListApproved = vi.mocked(listApprovedWikiPages)
const mockListJobs = vi.mocked(listCompileJobs)
const mockFindCandidates = vi.mocked(findMergeCandidates)
const mockMerge = vi.mocked(mergeWikiPages)
const mockCommunity = vi.mocked(runCommunitySummary)
const mockListVersions = vi.mocked(listWikiPageVersions)
const mockDiff = vi.mocked(diffWikiPageVersion)
const mockPromote = vi.mocked(promoteWikiPageToGlobal)
const mockAssign = vi.mocked(assignWikiPageProject)

const pendingPage = (over: Partial<WikiPendingPage> = {}): WikiPendingPage => ({
  page: {
    id: 11, slug: 'payment-gateway', title: '支付网关', kind: 'concept', project_id: 4,
    content: '正文', updated_at: '2026-10-12 15:04:05', status: 'pending', source: 'compiler',
  },
  source_note_ids: [],
  out_links: [],
  in_links: [],
  ...over,
})

// An approved page, for the scope surface (ADR-0031). The scope feed carries
// the same row shape as the review feed — a page is a page.
const approvedPage = (over: Partial<WikiPendingPage> = {}): WikiPendingPage => ({
  page: {
    id: 21, slug: 'retry-policy', title: '重试策略', kind: 'concept', project_id: 4,
    content: '正文', updated_at: '2026-10-12 15:04:05', status: 'approved', source: 'manual',
  },
  source_note_ids: [],
  out_links: [],
  in_links: [],
  ...over,
})

// A replaced page. Rejecting replaces a page with an error placeholder rather
// than deleting it, so the row survives as a tombstone.
const rejectedPage = (over: Partial<WikiPendingPage> = {}): WikiPendingPage => ({
  page: {
    id: 41, slug: 'broken-page', title: '编译失败页', kind: 'concept', project_id: 4,
    content: '编译失败', updated_at: '2026-10-12 15:04:05', status: 'rejected', source: 'compiler',
  },
  source_note_ids: [],
  out_links: [],
  in_links: [],
  ...over,
})

const candidate = (over: Partial<MergeCandidate> = {}): MergeCandidate => ({
  winner_id: 11, winner_slug: 'payment-gateway', loser_id: 3, loser_slug: 'gateway-payment',
  score: 7, signals: ['shared-notes', 'title-overlap'], shared_notes: 2, project_id: 4,
  ...over,
})

const report = (over: Partial<MergeCandidateReport> = {}): MergeCandidateReport => ({
  project_id: 4, pages: 12, pairs_scored: 9, pairs_possible: 66,
  candidates: [candidate()], truncated: false,
  ...over,
})

const job = (over: Partial<CompileJob> = {}): CompileJob => ({
  id: 31, project_id: 4, kind: 'compile', requested_notes: 3, status: 'succeeded',
  notes_total: 3, notes_done: 3, pages_created: 2, pages_updated: 0, links_created: 1,
  attachments: 0, revision_todos: 0, rejected_ops: 0, findings: 0,
  created_at: '2026-10-12 15:04:05',
  ...over,
})

const pageVersion = (over: Partial<WikiPageVersion> = {}): WikiPageVersion => ({
  id: 71, page_id: 11, title: '支付网关', content: 'old', kind: 'concept',
  source: 'compiler', created_at: '2026-10-11 09:30:00',
  ...over,
})

const communityReport = (over: Partial<WikiCommunityReport> = {}): WikiCommunityReport => ({
  project_id: 4, approved_pages: 20, edges: 31, communities_found: 3, dropped_small: 1,
  dropped_large: 0, isolated: 2, rounds: 4, converged: true,
  pages_created: 2, pages_updated: 1, documents_linked: 9, notes_attached: 5,
  revision_todos: 1, empty_bodies: 0, summarized_slugs: [], skipped_by_budget: [],
  outbound_bytes: 4321, calls: 3, community_gate_off: false, run_at: '2026-10-12T15:04:05Z',
  elapsed_ms: 812,
  ...over,
})

// The URL is part of the contract (?view= is the deep-link surface), so the
// tests need to read it — and to walk it, the way the browser's back button
// does.
function LocationProbe() {
  const loc = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <span data-testid="location">{loc.pathname + loc.search}</span>
      <button type="button" data-testid="back" onClick={() => navigate(-1)}>back</button>
    </>
  )
}

// renderReady (ADR-0026) — the page fills its queues from a Promise.all inside a
// useEffect, so returning before that settles is how this file used to flake.
const renderReview = async (initial = '/review') => {
  await renderReady(
    <>
      <LocationProbe />
      <ReviewPage />
    </>,
    { initialEntries: [initial] },
  )
}

// The tab strip (ADR-0039). A tab's accessible name is its label plus its
// count badge, so the tab is found by the label prefix; the badge is the one
// element inside the button whose own text is a number.
const TAB_LABELS: Record<string, string> = {
  pending: 'review.tabPending',
  merge: 'review.tabMerge',
  scope: 'review.tabScope',
  jobs: 'review.tabJobs',
  rejected: 'review.tabRejected',
}

function tabName(key: string): RegExp {
  return new RegExp(`^${TAB_LABELS[key].replace(/\./g, '\\.')}`)
}

/** The tab of a queue. */
function tab(key: string): HTMLElement {
  return screen.getByRole('tab', { name: tabName(key) })
}

/** The tab of a queue, or null when that queue has no tab at all. */
function queryTab(key: string): HTMLElement | null {
  return screen.queryByRole('tab', { name: tabName(key) })
}

/** The count badge of a tab. */
function tabCount(key: string): string {
  return within(tab(key)).getByText(/^\d+$/).textContent ?? ''
}

/** The open panel — only the selected tab's panel is rendered. */
function openPanel(): HTMLElement | null {
  return screen.queryByRole('tabpanel')
}

/** The id of the open panel, e.g. "review-panel-merge". */
function openPanelId(): string | undefined {
  return openPanel()?.id
}

/** The rows of the open panel. */
function rows(): HTMLElement[] {
  const p = openPanel()
  return p ? within(p).queryAllByRole('listitem') : []
}

/** Open a tab and wait for its panel. The tab itself may only appear once its
 *  queue has loaded, so both are awaited. */
async function openTab(key: string) {
  const btn = await waitFor(() => {
    const el = queryTab(key)
    expect(el).toBeTruthy()
    return el as HTMLElement
  })
  fireEvent.click(btn)
  await waitFor(() => expect(openPanelId()).toBe(`review-panel-${key}`))
}

// The project filter renders one <option> per entry of `projects`, and that
// state only lands after the load effect's getProjects() promise resolves. So
// the combobox is queryable a render before the option it needs to accept:
// jsdom matches an assigned value against the options present at that moment,
// finds none, and hands the change handler an empty value — Number('') is 0,
// so projectFilter never leaves 0 and React writes '0' straight back onto the
// controlled node. Every project-gated button then stays disabled, and the
// failure surfaces far from its cause as `expected true to be false` on a
// button assertion. Which render wins that race tracked runner load, not
// product behaviour, so await the option first; the value check keeps a
// recurrence pointing at the select instead of at a downstream timeout.
async function pickProject(id: number, name: string) {
  await screen.findByRole('option', { name })
  const select = (await screen.findByRole(
    'combobox', { name: 'review.projectFilter' },
  )) as HTMLSelectElement
  fireEvent.change(select, { target: { value: String(id) } })
  await waitFor(() => expect(select.value).toBe(String(id)))
}

beforeEach(() => {
  vi.clearAllMocks()
  // jsdom has no layout engine, so scrollIntoView is missing. The page calls
  // it when a tab switch returns the reader to the top of the new panel — a
  // no-op is enough for the test.
  Element.prototype.scrollIntoView = vi.fn()
  // The three promises ADR-0026 names as this page's load path. Under
  // REPO_TEST_LAG_MS they land late on purpose — that is the promotion-criterion-1
  // probe, and it is a no-op on every ordinary run.
  mockGetProjects.mockImplementation(() => lagged([]))
  mockListPending.mockImplementation(() => lagged([]))
  mockListRejected.mockResolvedValue([])
  mockListApproved.mockResolvedValue([])
  mockListJobs.mockResolvedValue([])
  mockFindCandidates.mockImplementation(() => lagged(report({ candidates: [] })))
  mockMerge.mockResolvedValue(null)
  mockCommunity.mockResolvedValue(communityReport())
  mockListVersions.mockResolvedValue([])
  mockDiff.mockResolvedValue('')
  mockPromote.mockResolvedValue(undefined)
  mockAssign.mockResolvedValue(undefined)
})

// The five queues are five tabs (ADR-0039 decision 2). What matters is not
// that the markup changed but that a reviewer can now see, at a glance, which
// queues have something in them — and reach each one without scrolling past
// the others.
describe('Review tabs (ADR-0039 decision 2)', () => {
  it('lists the five queues with their counts', async () => {
    mockListPending.mockResolvedValue([pendingPage()])
    mockFindCandidates.mockResolvedValue(report())
    mockListApproved.mockResolvedValue([approvedPage()])
    mockListJobs.mockResolvedValue([job()])
    mockListRejected.mockResolvedValue([rejectedPage()])
    await renderReview()
    await screen.findByText('支付网关')
    expect(tab('pending').textContent).toContain('review.tabPending')
    expect(tab('merge').textContent).toContain('review.tabMerge')
    expect(tab('scope').textContent).toContain('review.tabScope')
    expect(tab('jobs').textContent).toContain('review.tabJobs')
    expect(tab('rejected').textContent).toContain('review.tabRejected')
    // The badge is the number that says whether a tab is worth clicking.
    expect(tabCount('pending')).toBe('1')
    expect(tabCount('merge')).toBe('1')
    expect(tabCount('scope')).toBe('1')
    expect(tabCount('jobs')).toBe('1')
    expect(tabCount('rejected')).toBe('1')
  })

  it('lands on the pending queue by default', async () => {
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    expect(tab('pending').getAttribute('aria-selected')).toBe('true')
    expect(openPanelId()).toBe('review-panel-pending')
  })

  it('gives an empty queue no tab', async () => {
    // An empty tab is a click that leads nowhere. Only the two queues that
    // answer a question ("what is waiting?" / "did the scan find duplicates?")
    // stay when they have nothing to show.
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    expect(queryTab('pending')).toBeTruthy()
    expect(queryTab('merge')).toBeTruthy()
    expect(queryTab('scope')).toBeNull()
    expect(queryTab('jobs')).toBeNull()
    expect(queryTab('rejected')).toBeNull()
  })

  it('switches to the merge panel and stops rendering the queue', async () => {
    mockListPending.mockResolvedValue([pendingPage()])
    mockFindCandidates.mockResolvedValue(report())
    await renderReview()
    await screen.findByText('支付网关')
    fireEvent.click(tab('merge'))
    await waitFor(() => expect(openPanelId()).toBe('review-panel-merge'))
    // One queue at a time: the pending cards are gone, not merely hidden
    // behind the tab.
    expect(screen.queryByText('支付网关')).toBeNull()
    expect(await screen.findByText('review.mergeAdopt')).toBeTruthy()
  })

  it('writes the tab into the URL', async () => {
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    fireEvent.click(tab('merge'))
    await waitFor(
      () => expect(screen.getByTestId('location').textContent).toContain('view=merge'),
      { timeout: 2000 },
    )
  })

  it('keeps the back button able to walk the tab switch', async () => {
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    fireEvent.click(tab('merge'))
    await waitFor(
      () => expect(screen.getByTestId('location').textContent).toContain('view=merge'),
      { timeout: 2000 },
    )
    // Back: the queue comes back. A REPLACE write would have eaten the step
    // and left the back button on the page before the tab.
    fireEvent.click(screen.getByTestId('back'))
    await waitFor(
      () => expect(screen.getByTestId('location').textContent).not.toContain('view=merge'),
      { timeout: 2000 },
    )
    expect(tab('pending').getAttribute('aria-selected')).toBe('true')
    expect(openPanelId()).toBe('review-panel-pending')
  })

  it('lands on the merge panel when the URL says so', async () => {
    mockFindCandidates.mockImplementation(() => lagged(report()))
    await renderReview('/review?view=merge')
    expect(await screen.findByText('review.mergeAdopt')).toBeTruthy()
    expect(tab('merge').getAttribute('aria-selected')).toBe('true')
    expect(openPanelId()).toBe('review-panel-merge')
  })

  it('falls back to the pending queue for an unknown view', async () => {
    await renderReview('/review?view=nope')
    await screen.findByText('review.emptyPendingTitle')
    expect(tab('pending').getAttribute('aria-selected')).toBe('true')
    expect(openPanelId()).toBe('review-panel-pending')
  })

  it('returns the reader to the top of the new panel', async () => {
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    const scroll = vi.mocked(Element.prototype.scrollIntoView)
    scroll.mockClear()
    fireEvent.click(tab('merge'))
    await waitFor(() => expect(scroll).toHaveBeenCalled())
    expect(scroll).toHaveBeenCalledWith({ block: 'start' })
    // The scrolled element is the strip, not the panel: the strip is what the
    // reader should see, and its CSS scroll-margin-top keeps it clear of the
    // pinned toolbar.
    const ctx = scroll.mock.contexts[0] as HTMLElement | undefined
    expect(ctx).toBeTruthy()
    expect(within(ctx as HTMLElement).getByRole('tablist')).toBeTruthy()
  })

  it('wires the tabs to their panels for assistive tech', async () => {
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    const selected = tab('pending')
    expect(selected.getAttribute('role')).toBe('tab')
    expect(selected.getAttribute('aria-selected')).toBe('true')
    expect(selected.getAttribute('aria-controls')).toBe('review-panel-pending')
    // Only the selected tab is in the tab order; the rest are reached with the
    // arrow keys.
    expect(tab('merge').getAttribute('tabindex')).toBe('-1')
    const p = openPanel() as HTMLElement
    expect(p.getAttribute('role')).toBe('tabpanel')
    expect(p.getAttribute('aria-labelledby')).toBe('review-tab-pending')
  })

  it('keeps the selected tab in place when its queue empties', async () => {
    // The last replaced page is deleted while the rejected tab is open. The
    // tab must NOT vanish under the reader: a tab that disappears mid-review
    // moves the panel without being asked — and the panel would jump back the
    // moment another replaced page showed up.
    mockListRejected.mockResolvedValue([rejectedPage()])
    await renderReview()
    await openTab('rejected')
    expect(openPanelId()).toBe('review-panel-rejected')

    mockListRejected.mockResolvedValue([])
    fireEvent.click(screen.getByText('review.reload'))
    await screen.findByText('review.emptyTab')
    expect(queryTab('rejected')).toBeTruthy()
    expect(tab('rejected').getAttribute('aria-selected')).toBe('true')
    expect(openPanelId()).toBe('review-panel-rejected')
  })

  it('takes the reader to the report after a community synthesis run', async () => {
    // The report lives in the jobs tab. The run was paid for and it just
    // finished — the reader has to see the counters without having to know
    // which tab they are in and go looking.
    mockGetProjects.mockResolvedValue([{
      id: 4, name: 'Alpha', root_path: '/x', level_override: 0, is_auto_grouped: false,
      is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
      my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
    }])
    mockCommunity.mockResolvedValue(communityReport())
    await renderReview()
    await pickProject(4, 'Alpha')
    const button = (await screen.findByText('review.startCommunity')) as HTMLButtonElement
    await waitFor(() => expect(button.disabled).toBe(false))
    fireEvent.click(button)
    await waitFor(() => expect(openPanelId()).toBe('review-panel-jobs'))
    expect(await screen.findByText('review.communityHeading')).toBeTruthy()
  })

  it('keeps the default queue out of the URL', async () => {
    // Same convention as the knowledge page's ?view=: "/review" says "the
    // queue", "?view=merge" says which one.
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    await openTab('merge')
    expect(screen.getByTestId('location').textContent).toContain('view=merge')
    fireEvent.click(tab('pending'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).not.toContain('view='))
  })
})

describe('Review merge suggestions (ADR-0019)', () => {
  it('renders each candidate as a directed pair, loser first', async () => {
    mockFindCandidates.mockImplementation(() => lagged(report({
      candidates: [
        candidate(),
        candidate({
          winner_id: 12, winner_slug: 'quota-sync', loser_id: 9, loser_slug: 'quota-refresh',
          score: 4, signals: ['linked'], shared_notes: 0,
        }),
      ],
    })))
    await renderReview()
    await openTab('merge')
    // A1 (ADR-0026): the tab is mounted unconditionally, so `await`ing it
    // waits for nothing — the rows are the data. With REPO_TEST_LAG_MS set,
    // the old synchronous read here ran one render early and saw zero rows.
    await waitFor(() => expect(rows()).toHaveLength(2))
    const list = rows()
    expect(list).toHaveLength(2)
    // Direction is the row's whole payload: [[loser]] → [[winner]]. The loser's
    // slug must come BEFORE the winner's, or a reviewer reading top-down would
    // absorb the wrong page.
    expect(list[0].textContent).toMatch(/gateway-payment.*→.*payment-gateway/)
    expect(list[1].textContent).toMatch(/quota-refresh.*→.*quota-sync/)
    // Signals and score are the "why" the structural pass is allowed to claim.
    expect(list[0].textContent).toContain('shared-notes')
    expect(list[0].textContent).toContain('title-overlap')
    expect(list[1].textContent).not.toContain('title-overlap')
    // shared_notes is emitted only when non-zero — a "0 shared notes" chip is noise.
    expect(list[0].textContent).toContain('review.mergeSharedNotes')
    expect(list[1].textContent).not.toContain('review.mergeSharedNotes')
    expect(screen.getAllByText('review.mergeAdopt')).toHaveLength(2)
  })

  it('asks for confirmation before merging: the first click fires no call', async () => {
    mockFindCandidates.mockResolvedValue(report())
    mockMerge.mockResolvedValue({
      loser_id: 3, loser_slug: 'gateway-payment', winner_id: 11, winner_slug: 'payment-gateway',
      edges_redirected: 2, edges_dropped: 0, notes_reattached: 3, loser_previous_status: 'approved',
    })
    await renderReview()
    await openTab('merge')
    const adopt = await screen.findByText('review.mergeAdopt')
    fireEvent.click(adopt)
    // Armed, not executed — a merge destroys the loser's content, so one click
    // is never enough (and no modal library is involved).
    expect(mockMerge).not.toHaveBeenCalled()
    expect(adopt.textContent).toBe('review.mergeConfirm')
    fireEvent.click(screen.getByText('review.mergeConfirm'))
    await waitFor(() => expect(mockMerge).toHaveBeenCalledWith(3, 11))
    // Loser first: the binding's argument order is (loserID, winnerID) and the
    // Wails layer resolves positionally.
    expect(mockMerge.mock.calls[0][0]).toBe(3)
    expect(mockMerge.mock.calls[0][1]).toBe(11)
  })

  it('hides a dismissed pair locally without calling anything', async () => {
    mockFindCandidates.mockImplementation(() => lagged(report()))
    await renderReview()
    await openTab('merge')
    // A1 (ADR-0026): the tab is mounted unconditionally, so waiting for it
    // proves nothing about the rows — wait for the row itself.
    await waitFor(() => expect(rows()).toHaveLength(1))
    fireEvent.click(within(openPanel() as HTMLElement).getByText('review.mergeIgnore'))
    await waitFor(() => expect(rows()).toHaveLength(0))
    // Ignoring is a view-local filter: nothing is sent anywhere, and nothing is
    // stored — no endpoint exists to record it, by design.
    expect(mockMerge).not.toHaveBeenCalled()
    expect((openPanel() as HTMLElement).textContent).toContain('review.emptyMergeTitle')
  })

  it('degrades a null candidate report to the empty state instead of throwing', async () => {
    // The endpoint normalises null to an empty report, but the page is the last
    // line: a report that arrives null must still render the queue, not a crash.
    mockFindCandidates.mockImplementation(() => lagged(null as unknown as MergeCandidateReport))
    mockListPending.mockImplementation(() => lagged([pendingPage()]))
    await renderReview()
    await openTab('merge')
    expect(await screen.findByText('review.emptyMergeTitle')).toBeTruthy()
    // The rest of the page survived the null too.
    expect(screen.getByText('review.tabPending')).toBeTruthy()
  })

  it('says when the list was truncated', async () => {
    mockFindCandidates.mockResolvedValue(report({ truncated: true }))
    await renderReview()
    await openTab('merge')
    expect(await screen.findByText('review.mergeTruncated')).toBeTruthy()
  })
})

describe('Review page scope (ADR-0031)', () => {
  // The scope surface shows where a page currently speaks. A project-scoped
  // page names its project; a global page says so outright, because "which
  // project does this belong to?" has no answer for it.
  it('labels each approved page with the scope it speaks from', async () => {
    mockGetProjects.mockResolvedValue([{
      id: 4, name: 'Alpha', root_path: '/x', level_override: 0, is_auto_grouped: false,
      is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
      my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
    }])
    mockListApproved.mockImplementation(() => lagged([
      approvedPage(),
      approvedPage({
        page: {
          id: 22, slug: 'global-rule', title: '全局约定', kind: 'synthesis', project_id: 0,
          content: '正文', updated_at: '2026-10-12 15:04:05', status: 'approved', source: 'manual',
        },
      }),
    ]))
    await renderReview()
    await openTab('scope')
    const list = rows()
    expect(list).toHaveLength(2)
    // A project page names its project; a global page says "global".
    expect(list[0].textContent).toContain('Alpha')
    expect(list[0].textContent).toContain('review.scopePromote')
    expect(list[1].textContent).toContain('review.scopeGlobal')
    // A global page offers the reverse move instead of the promotion.
    expect(list[1].textContent).toContain('review.scopeDemote')
    expect(list[1].textContent).not.toContain('review.scopePromote')
  })

  // The promotion changes what steers every future answer in every project,
  // so one click must never be enough — same two-click arm as the merge row.
  it('asks for confirmation before promoting a page to the global tier', async () => {
    mockListApproved.mockImplementation(() => lagged([approvedPage()]))
    await renderReview()
    await openTab('scope')
    const promote = await screen.findByText('review.scopePromote')
    fireEvent.click(promote)
    expect(mockPromote).not.toHaveBeenCalled()
    expect(promote.textContent).toBe('review.scopePromoteConfirm')
    fireEvent.click(screen.getByText('review.scopePromoteConfirm'))
    await waitFor(() => expect(mockPromote).toHaveBeenCalledWith(21))
  })

  // Moving a page back needs an explicit target: once a page is global, the
  // project it came from is not knowable from the row.
  it('moves a global page back into a project the reviewer picks', async () => {
    mockGetProjects.mockResolvedValue([{
      id: 4, name: 'Alpha', root_path: '/x', level_override: 0, is_auto_grouped: false,
      is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
      my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
    }])
    mockListApproved.mockImplementation(() => lagged([
      approvedPage({
        page: {
          id: 22, slug: 'global-rule', title: '全局约定', kind: 'synthesis', project_id: 0,
          content: '正文', updated_at: '2026-10-12 15:04:05', status: 'approved', source: 'manual',
        },
      }),
    ]))
    await renderReview()
    await openTab('scope')
    const demote = (await screen.findByText('review.scopeDemote')) as HTMLButtonElement
    // No project picked yet: the button says so by being disabled.
    await waitFor(() => expect(demote.disabled).toBe(true))
    const select = (await screen.findByLabelText('review.scopeTarget')) as HTMLSelectElement
    fireEvent.change(select, { target: { value: '4' } })
    await waitFor(() => expect(demote.disabled).toBe(false))
    fireEvent.click(demote)
    await waitFor(() => expect(mockAssign).toHaveBeenCalledWith(22, 4))
  })

  // No approved pages, no scope tab: a surface about a move nobody can make
  // is noise.
  it('gives the scope queue no tab when there is nothing to scope', async () => {
    await renderReview()
    await screen.findByText('review.emptyPendingTitle')
    expect(queryTab('scope')).toBeNull()
  })
})

describe('Review community synthesis (ADR-0020)', () => {
  it('is disabled on "all projects", because project 0 is the global tier', async () => {
    await renderReview()
    const button = (await screen.findByText('review.startCommunity')) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    // The reason is stated, not implied by a greyed control.
    expect(button.getAttribute('title')).toBe('review.communityNeedsProject')
    fireEvent.click(button)
    expect(mockCommunity).not.toHaveBeenCalled()
  })

  it('runs for a selected project and reports the counters it got back', async () => {
    mockGetProjects.mockResolvedValue([{
      id: 4, name: 'Alpha', root_path: '/x', level_override: 0, is_auto_grouped: false,
      is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
      my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
    }])
    mockCommunity.mockResolvedValue(communityReport())
    await renderReview()
    await pickProject(4, 'Alpha')
    const button = (await screen.findByText('review.startCommunity')) as HTMLButtonElement
    await waitFor(() => expect(button.disabled).toBe(false))
    fireEvent.click(button)
    await waitFor(() => expect(mockCommunity).toHaveBeenCalledWith(4))
    // The report belongs to the jobs tab: it is "what the machine produced",
    // not a queue the reviewer works through.
    await openTab('jobs')
    expect(await screen.findByText('review.communityHeading')).toBeTruthy()
    // The counters stay on screen after the toast expires — a paid-for run whose
    // evidence vanishes in five seconds is not evidence.
    expect(screen.getByText('review.communityPagesCreated')).toBeTruthy()
    expect(screen.getByText('review.communityOutbound')).toBeTruthy()
  })

  it('surfaces the backend note verbatim when the gate is off', async () => {
    mockGetProjects.mockResolvedValue([{
      id: 4, name: 'Alpha', root_path: '/x', level_override: 0, is_auto_grouped: false,
      is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
      my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
    }])
    mockCommunity.mockResolvedValue(communityReport({
      community_gate_off: true, pages_created: 0, pages_updated: 0, documents_linked: 0,
      revision_todos: 0, empty_bodies: 0, outbound_bytes: 0,
      llm_note: '未开启（wiki_community）：只做了结构检测',
    }))
    await renderReview()
    await pickProject(4, 'Alpha')
    const button = (await screen.findByText('review.startCommunity')) as HTMLButtonElement
    await waitFor(() => expect(button.disabled).toBe(false))
    fireEvent.click(button)
    await openTab('jobs')
    await screen.findByText('review.communityHeading')
    expect(screen.getByText('未开启（wiki_community）：只做了结构检测')).toBeTruthy()
    expect(screen.queryByText('review.communityPagesCreated')).toBeNull()
    expect(screen.getByText('review.communityGateOffNote')).toBeTruthy()
  })
})

describe('Review page version history (ADR-0021)', () => {
  it('opens the history and renders the diff of a snapshot', async () => {
    mockListPending.mockResolvedValue([pendingPage()])
    mockListVersions.mockResolvedValue([
      pageVersion(),
      pageVersion({ id: 70, created_at: '2026-10-09 12:00:00' }),
    ])
    mockDiff.mockResolvedValue('--- a/payment-gateway\n+++ b/payment-gateway\n-旧结论\n+新结论')
    await renderReview()
    const toggle = await screen.findByText('review.versionHistory')
    fireEvent.click(toggle)
    await waitFor(() => expect(mockListVersions).toHaveBeenCalledWith(11))
    expect(await screen.findByText('review.versionListTitle')).toBeTruthy()
    expect(screen.getByText('2026-10-11 09:30:00')).toBeTruthy()
    fireEvent.click(screen.getByText('2026-10-09 12:00:00'))
    await waitFor(() => expect(mockDiff).toHaveBeenCalledWith(11, 70))
    // The shared DiffViewer colours the +/- lines; here the raw text is enough to
    // prove the page renders the backend's unified text rather than a summary.
    expect(await screen.findByText('+新结论')).toBeTruthy()
    expect(screen.getByText('-旧结论')).toBeTruthy()
  })

  it('says so when a page has no snapshots', async () => {
    mockListPending.mockResolvedValue([pendingPage()])
    mockListVersions.mockResolvedValue([])
    await renderReview()
    fireEvent.click(await screen.findByText('review.versionHistory'))
    expect(await screen.findByText('review.versionEmpty')).toBeTruthy()
    expect(mockDiff).not.toHaveBeenCalled()
  })
})

// The card list is the page's reason to exist; these two guard the wiring the
// new sections share with it (busy-gating and the approve call).
describe('Review queue wiring', () => {
  it('approves through the binding and reloads the queue', async () => {
    mockListPending.mockResolvedValue([pendingPage()])
    await renderReview()
    fireEvent.click(await screen.findByText('review.approve'))
    await waitFor(() => expect(mockListPending).toHaveBeenCalledWith(0))
    await waitFor(() => expect(approveWikiPage).toHaveBeenCalledWith(11))
    // A reload followed the approve: pending was called at least twice.
    await waitFor(() => expect(mockListPending.mock.calls.length).toBeGreaterThan(1))
  })

  it('starts a compile with the capped note budget', async () => {
    mockGetProjects.mockResolvedValue([{
      id: 4, name: 'Alpha', root_path: '/x', level_override: 0, is_auto_grouped: false,
      is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
      my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
    }])
    await renderReview()
    await pickProject(4, 'Alpha')
    const button = (await screen.findByText('review.startCompile')) as HTMLButtonElement
    await waitFor(() => expect(button.disabled).toBe(false))
    fireEvent.click(button)
    await waitFor(() => expect(startCompileJob).toHaveBeenCalledWith(4, 10))
    expect(startLintJob).not.toHaveBeenCalled()
    expect(cancelCompileJob).not.toHaveBeenCalled()
    expect(deleteCompiledPage).not.toHaveBeenCalled()
    expect(rejectWikiPage).not.toHaveBeenCalled()
  })
})

// The backend stores its fixed failure reasons as stable codes (db.CompileJobErr*)
// so this page can translate them; a prose message there shipped as an English
// sentence inside an otherwise Chinese panel (2026-10-21 review-page smoke test).
// Tested at the function boundary on purpose: this file's `t` mock returns the
// key and drops {{msg}}, so the rendered row cannot show the difference.
describe('jobErrorText', () => {
  const t = (k: string) => `T:${k}`

  it.each([
    ['interrupted_by_restart', 'review.errInterrupted'],
    ['worker_shutdown', 'review.errShutdown'],
    ['canceled_before_start', 'review.errCanceledEarly'],
  ])('maps the %s code to a localized string', (code, key) => {
    const out = jobErrorText(code, t)
    expect(out).toBe(`T:${key}`)
    expect(out).not.toContain(code)
  })

  it('passes a wrapped Go error through untouched', () => {
    // Diagnostic text is deliberately not localized — rewriting it would hide
    // what actually failed.
    expect(jobErrorText('wiki lint: connection refused', t)).toBe('wiki lint: connection refused')
  })
})
