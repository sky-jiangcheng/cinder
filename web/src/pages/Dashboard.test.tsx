// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import { useLocation, useNavigate } from 'react-router-dom'
import { renderReady } from '../test/renderReady'

// Partial mock: override the backend-calling endpoints, keep everything else
// (getConnectionKind etc.) real so components like StatusBar keep working.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    getProjects: vi.fn(),
    getSummary: vi.fn(),
    getTodoCounts: vi.fn(),
    getNoteCounts: vi.fn(),
    getConfig: vi.fn(),
    getHeatmapData: vi.fn(),
    getStatusBar: vi.fn(),
    getScanStatus: vi.fn(),
    searchProjects: vi.fn(),
    searchAll: vi.fn(),
    triggerScan: vi.fn(),
    listRecentHandoffs: vi.fn(),
  }
})

const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import {
  getProjects,
  getSummary,
  getTodoCounts,
  getNoteCounts,
  getConfig,
  getHeatmapData,
  getStatusBar,
  getScanStatus,
  searchProjects,
  searchAll,
  listRecentHandoffs,
  type HandoffEntry,
  type Project,
  type Summary,
} from '../api/client'
import { invalidateCache } from '../hooks/useApiData'
import Dashboard from './Dashboard'

const mockGetProjects = vi.mocked(getProjects)
const mockGetSummary = vi.mocked(getSummary)
const mockGetTodoCounts = vi.mocked(getTodoCounts)
const mockGetNoteCounts = vi.mocked(getNoteCounts)
const mockGetConfig = vi.mocked(getConfig)
const mockGetHeatmapData = vi.mocked(getHeatmapData)
const mockGetStatusBar = vi.mocked(getStatusBar)
const mockGetScanStatus = vi.mocked(getScanStatus)
const mockSearchProjects = vi.mocked(searchProjects)
const mockSearchAll = vi.mocked(searchAll)
const mockListRecentHandoffs = vi.mocked(listRecentHandoffs)

// First Dashboard component test (D25): the page previously only had App.smoke
// shell coverage, while the ADR-0017 promotion gate requires functional tests
// for both first-screen halves.
const proj = (id: number, name: string, over: Partial<Project> = {}): Project => ({
  id,
  name,
  root_path: '/tmp/' + name,
  level_override: 0,
  is_auto_grouped: true,
  is_starred: false,
  created_at: '2026-10-06T00:00:00Z',
  repo_count: 1,
  total_added: 0,
  total_deleted: 0,
  my_added: 0,
  my_deleted: 0,
  my_files: 0,
  my_added_7d: 0,
  is_workday: true,
  below_standard: false,
  ...over,
})

// 记忆环的一行：最近交接记录（ADR-0038 决策 5 把它从首页搬到了仪表盘）。
const entry = (over: Partial<HandoffEntry> = {}): HandoffEntry => ({
  note_id: 1,
  project_id: 2,
  project_name: 'Alpha',
  title: 'Session Handoff 2026-10-12 15:04',
  updated_at: '2026-10-12 15:04:05',
  auto_captured: false,
  ...over,
})

const summary = (over: Partial<Summary> = {}): Summary => ({
  date: '2026-10-12',
  repo_count: 1,
  total_files: 3,
  total_added: 120,
  total_deleted: 40,
  my_added: 120,
  my_deleted: 40,
  my_files: 3,
  is_workday: true,
  ...over,
})

// The tab is part of the deep-link surface (?view=projects|sessions), so the
// tests have to be able to read the URL — and to walk it, the way the browser's
// back button does.
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

async function renderDashboard(initial = '/dashboard') {
  return renderReady(
    <>
      <LocationProbe />
      <Dashboard />
    </>,
    { initialEntries: [initial] },
  )
}

/** The tab strip's buttons, found by the label they carry. */
function tabButton(label: '项目' | '会话'): HTMLElement {
  return screen.getByRole('tab', { name: new RegExp(`^${label}`) })
}

beforeEach(() => {
  vi.clearAllMocks()
  // The project list is shared through the module-level cache in useApiData;
  // an earlier test in this file would otherwise serve its own projects to the
  // next one and the scope assertions below would never see a second fetch.
  invalidateCache()
  mockGetProjects.mockResolvedValue([])
  mockGetSummary.mockResolvedValue(summary())
  mockGetTodoCounts.mockResolvedValue([])
  mockGetNoteCounts.mockResolvedValue([])
  mockGetConfig.mockResolvedValue({ config: {}, scan_roots: [] })
  mockGetHeatmapData.mockResolvedValue({ days: [] })
  mockGetStatusBar.mockResolvedValue({
    current_time: '',
    last_commit_time: '',
    last_commit_repo: '',
    last_commit_branch: '',
    last_commit_msg: '',
  })
  mockGetScanStatus.mockResolvedValue({ running: false, backfilling: false, message: '', progress: 0, total: 0 })
  mockSearchProjects.mockResolvedValue([])
  mockSearchAll.mockResolvedValue([])
  // 记忆环：默认没有交接记录。
  mockListRecentHandoffs.mockResolvedValue([])
})

describe('Dashboard (first-screen project status half)', () => {
  it('renders project cards from the project list', async () => {
    // The dashboard defaults to the starred-only filter (showStarredOnly=true),
    // so the seeded projects must be starred to reach the card grid.
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true }), proj(2, 'proj-beta', { is_starred: true })])
    await renderDashboard()
    expect(await screen.findByText('proj-alpha')).toBeTruthy()
    expect(screen.getByText('proj-beta')).toBeTruthy()
  })

  it('keeps the workday goal state visible for a workday summary', async () => {
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true })])
    mockGetSummary.mockResolvedValue(summary({ is_workday: true, my_added: 500 }))
    await renderDashboard()
    await waitFor(() => expect(mockGetSummary).toHaveBeenCalled())
    // The name renders in more than one spot (card + dropdown matches), so
    // assert presence rather than uniqueness.
    expect((await screen.findAllByText('proj-alpha')).length).toBeGreaterThan(0)
    expect(screen.getByText('dashboard.title')).toBeTruthy()
  })

  it('shows the weekend state instead of the goal when the day is not a workday', async () => {
    mockGetProjects.mockResolvedValue([])
    mockGetSummary.mockResolvedValue(summary({ is_workday: false }))
    await renderDashboard()
    await waitFor(() => expect(mockGetSummary).toHaveBeenCalled())
    // Dashboard.tsx swaps the goal for the weekend hint on non-workdays
    // (dashboard.weekend, defaultValue "Happy weekend").
    expect(await screen.findByText('Happy weekend')).toBeTruthy()
  })
})

// ADR-0039 决策 4：仪表盘的滚动区收成「项目 / 会话」两个 tab。
// 记忆环（上次会话）与项目列表（今天的产出）是两种不相干的对象，平级堆在
// 一个滚动区里正是 ADR-0038 要消灭的形状——这次是它换了个位置复现。
describe('Dashboard (project / session tabs, ADR-0039 decision 4)', () => {
  it('shows both tabs with their labels', async () => {
    await renderDashboard()

    expect(screen.getByRole('tablist', { name: '仪表盘视图' })).toBeTruthy()
    expect(tabButton('项目').textContent).toContain('项目')
    expect(tabButton('会话').textContent).toContain('会话')
  })

  it('opens on the projects tab: the project sections render, the memory ring does not', async () => {
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true })])
    await renderDashboard()

    expect(tabButton('项目').getAttribute('aria-selected')).toBe('true')
    expect(tabButton('会话').getAttribute('aria-selected')).toBe('false')
    expect(await screen.findByText('proj-alpha')).toBeTruthy()
    // 记忆环在「会话」tab 里，默认看不见——这正是 tab 化的代价，徽标负责回答
    // 「该不该点进去」。
    expect(screen.queryByRole('region', { name: '记忆环' })).toBeNull()
    expect(screen.getByRole('tabpanel').id).toBe('dashboard-panel-projects')
  })

  it('switches to the sessions tab: the memory ring renders, the project sections do not', async () => {
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true })])
    await renderDashboard()
    await screen.findByText('proj-alpha')

    fireEvent.click(tabButton('会话'))

    expect(await screen.findByRole('region', { name: '记忆环' })).toBeTruthy()
    expect(screen.queryByText('proj-alpha')).toBeNull()
    expect(screen.getByRole('tabpanel').id).toBe('dashboard-panel-sessions')
    expect(tabButton('会话').getAttribute('aria-selected')).toBe('true')
    expect(tabButton('项目').getAttribute('aria-selected')).toBe('false')
  })

  it('badges the handoff count on the sessions tab and the project count on the projects tab', async () => {
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true }), proj(2, 'proj-beta', { is_starred: true })])
    mockListRecentHandoffs.mockResolvedValue([
      entry({ note_id: 1, auto_captured: true }),
      entry({ note_id: 2, project_name: 'Beta', title: 'Session Handoff 2026-10-11 09:30' }),
    ])
    await renderDashboard()

    // 徽标是「有 2 条没看」一眼可见的唯一手段（ADR-0038 决策 5 要的是首屏可达）。
    await waitFor(() => expect(within(tabButton('会话')).getByText('2')).toBeTruthy())
    expect(within(tabButton('项目')).getByText('2')).toBeTruthy()
  })

  it('deep-links into the sessions tab with ?view=sessions', async () => {
    mockListRecentHandoffs.mockResolvedValue([entry({ note_id: 1 })])
    await renderDashboard('/dashboard?view=sessions')

    // 首屏就落在「会话」：记忆环不用多点一次。
    expect(await screen.findByRole('region', { name: '记忆环' })).toBeTruthy()
    expect(tabButton('会话').getAttribute('aria-selected')).toBe('true')
    expect(screen.getByRole('tabpanel').id).toBe('dashboard-panel-sessions')
  })

  it('falls back to the projects tab for an unknown ?view= value', async () => {
    await renderDashboard('/dashboard?view=bogus')

    expect(tabButton('项目').getAttribute('aria-selected')).toBe('true')
    expect(screen.getByRole('tabpanel').id).toBe('dashboard-panel-projects')
  })

  it('writes the tab switch into the URL', async () => {
    await renderDashboard()

    fireEvent.click(tabButton('会话'))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('view=sessions'), { timeout: 2000 })
  })

  it('keeps the back button able to walk the tab switch', async () => {
    await renderDashboard()

    fireEvent.click(tabButton('会话'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('view=sessions'), { timeout: 2000 })

    // Back: the view returns to the projects tab and the URL says so. A
    // REPLACE write would have eaten the step and left the back button on the
    // page before the tab.
    fireEvent.click(screen.getByTestId('back'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).not.toContain('view=sessions'), { timeout: 2000 })
    expect(tabButton('项目').getAttribute('aria-selected')).toBe('true')
    expect(screen.getByRole('tabpanel').id).toBe('dashboard-panel-projects')
  })

  it('wires the tabs and the panel for assistive tech', async () => {
    await renderDashboard()

    const projects = tabButton('项目')
    const sessions = tabButton('会话')
    expect(projects.getAttribute('aria-controls')).toBe('dashboard-panel-projects')
    expect(sessions.getAttribute('aria-controls')).toBe('dashboard-panel-sessions')

    const panel = screen.getByRole('tabpanel')
    expect(panel.id).toBe('dashboard-panel-projects')
    expect(panel.getAttribute('aria-labelledby')).toBe('dashboard-tab-projects')
  })

  it('sends the scroll area back to the top when the tab changes', async () => {
    // jsdom has no layout engine and no scrollTo of its own, so the page calls
    // it defensively; here we assert that the scroller was asked to go home.
    const scrollTo = vi.fn()
    const proto = Element.prototype as unknown as { scrollTo?: unknown }
    const original = proto.scrollTo
    proto.scrollTo = scrollTo
    try {
      await renderDashboard()

      fireEvent.click(tabButton('会话'))

      await waitFor(() => expect(scrollTo).toHaveBeenCalledWith({ top: 0 }))
    } finally {
      if (original === undefined) delete proto.scrollTo
      else proto.scrollTo = original
    }
  })
})

// 「全部 / 关注」那个范围开关切的是**取数范围**（连 summary 一起变），不是
// 「看哪一块」——它是 chip，不是 tab（ADR-0039 决策 1 的三分法）。tab 化不该
// 碰它，回归断言钉住这一点。
describe('Dashboard (the scope switch stays a scope switch)', () => {
  it('keeps the all / starred switch on the fetch scope and the summary scope', async () => {
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true })])
    await renderDashboard()

    await waitFor(() => expect(mockGetProjects).toHaveBeenCalled())
    const date = mockGetProjects.mock.calls[0][0]
    // 默认就是「关注」：取数范围与 summary 口径都跟着 starred 走。
    expect(mockGetProjects).toHaveBeenCalledWith(date, true)
    expect(mockGetSummary).toHaveBeenCalledWith(date, true)

    // 切到「全部」：两个口径一起变。
    fireEvent.click(screen.getByRole('button', { name: 'All' }))
    await waitFor(() => expect(mockGetProjects).toHaveBeenCalledWith(date, false), { timeout: 2000 })
    await waitFor(() => expect(mockGetSummary).toHaveBeenCalledWith(date, false), { timeout: 2000 })
  })

  it('leaves the scope switch alone when the tab changes', async () => {
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true })])
    await renderDashboard()
    await waitFor(() => expect(mockGetProjects).toHaveBeenCalled())
    const date = mockGetProjects.mock.calls[0][0]

    fireEvent.click(tabButton('会话'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('view=sessions'), { timeout: 2000 })

    // 切 tab 不重新取数，也不改取数范围——tab 只是显示层。
    expect(mockGetProjects).toHaveBeenCalledTimes(1)
    expect(mockGetSummary).toHaveBeenCalledTimes(1)
    expect(mockGetProjects).toHaveBeenCalledWith(date, true)
  })
})

// ADR-0038 决策 5：记忆环（最近交接记录 + 写一条交接记录）从首页搬进仪表盘，
// ADR-0039 决策 4：它搬进仪表盘的「会话」tab。首页已经是整页对话台，仪表盘
// 是记忆环的唯一一处——所以这里钉住「它在仪表盘的会话 tab 里」。
describe('Dashboard (memory ring, ADR-0038 decision 5)', () => {
  it('shows the recent handoffs in the ring inside the sessions tab', async () => {
    mockListRecentHandoffs.mockResolvedValue([
      entry({ note_id: 1, auto_captured: true }),
      entry({ note_id: 2, project_name: 'Beta', title: 'Session Handoff 2026-10-11 09:30', auto_captured: false }),
    ])
    await renderDashboard()
    fireEvent.click(tabButton('会话'))

    const ring = await screen.findByRole('region', { name: '记忆环' })
    expect(await screen.findByText('Session Handoff 2026-10-12 15:04')).toBeTruthy()
    expect(ring.textContent).toContain('Alpha')
    expect(screen.getByText('Session Handoff 2026-10-11 09:30')).toBeTruthy()
    // 徽标只给自动捕捉的那条：手写记录不该被标成「自动」。
    expect(screen.getAllByText('自动')).toHaveLength(1)
  })

  it('offers the empty hint when no handoff exists yet', async () => {
    await renderDashboard()
    fireEvent.click(tabButton('会话'))

    expect(await screen.findByText('还没有交接记录——写一条，或等会话结束后出现在这里')).toBeTruthy()
  })

  it('keeps the write entry inside the sessions tab, next to the ring it belongs to', async () => {
    await renderDashboard()

    // 「写一条交接记录」按钮只长在记忆环这一侧：它属于记忆环，就跟记忆环待在
    // 一起（ADR-0039 决策 4 的取舍——内容不在当前 tab 里就看不见，徽标负责
    // 回答「该不该点进去」）。
    expect(screen.queryByRole('button', { name: /写一条交接记录/ })).toBeNull()

    fireEvent.click(tabButton('会话'))
    const write = await screen.findByRole('button', { name: /写一条交接记录/ })
    expect(screen.queryByRole('dialog')).toBeNull()
    fireEvent.click(write)

    // HandoffComposer 打开后就是一个 dialog——「写一条交接记录」这条零配置
    // 路径因此是可达的，不需要 MCP 客户端、模型或任何配置。
    expect(await screen.findByRole('dialog')).toBeTruthy()
  })
})
