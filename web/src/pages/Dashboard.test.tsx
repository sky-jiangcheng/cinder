// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
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

beforeEach(() => {
  vi.clearAllMocks()
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
    await renderReady(<Dashboard />)
    expect(await screen.findByText('proj-alpha')).toBeTruthy()
    expect(screen.getByText('proj-beta')).toBeTruthy()
  })

  it('keeps the workday goal state visible for a workday summary', async () => {
    mockGetProjects.mockResolvedValue([proj(1, 'proj-alpha', { is_starred: true })])
    mockGetSummary.mockResolvedValue(summary({ is_workday: true, my_added: 500 }))
    await renderReady(<Dashboard />)
    await waitFor(() => expect(mockGetSummary).toHaveBeenCalled())
    // The name renders in more than one spot (card + dropdown matches), so
    // assert presence rather than uniqueness.
    expect((await screen.findAllByText('proj-alpha')).length).toBeGreaterThan(0)
    expect(screen.getByText('dashboard.title')).toBeTruthy()
  })

  it('shows the weekend state instead of the goal when the day is not a workday', async () => {
    mockGetProjects.mockResolvedValue([])
    mockGetSummary.mockResolvedValue(summary({ is_workday: false }))
    await renderReady(<Dashboard />)
    await waitFor(() => expect(mockGetSummary).toHaveBeenCalled())
    // Dashboard.tsx swaps the goal for the weekend hint on non-workdays
    // (dashboard.weekend, defaultValue "Happy weekend").
    expect(await screen.findByText('Happy weekend')).toBeTruthy()
  })
})

// ADR-0038 决策 5：记忆环（最近交接记录 + 写一条交接记录）从首页搬进仪表盘。
// 首页已经是整页对话台，仪表盘是记忆环的唯一一处——所以这里钉住「它在仪表盘里」。
describe('Dashboard (memory ring, ADR-0038 decision 5)', () => {
  it('shows the recent handoffs in the ring at the top of the dashboard', async () => {
    mockListRecentHandoffs.mockResolvedValue([
      entry({ note_id: 1, auto_captured: true }),
      entry({ note_id: 2, project_name: 'Beta', title: 'Session Handoff 2026-10-11 09:30', auto_captured: false }),
    ])
    await renderReady(<Dashboard />)

    const ring = screen.getByRole('region', { name: '记忆环' })
    expect(await screen.findByText('Session Handoff 2026-10-12 15:04')).toBeTruthy()
    expect(ring.textContent).toContain('Alpha')
    expect(screen.getByText('Session Handoff 2026-10-11 09:30')).toBeTruthy()
    // 徽标只给自动捕捉的那条：手写记录不该被标成「自动」。
    expect(screen.getAllByText('自动')).toHaveLength(1)
  })

  it('offers the empty hint when no handoff exists yet', async () => {
    await renderReady(<Dashboard />)
    expect(await screen.findByText('还没有交接记录——写一条，或等会话结束后出现在这里')).toBeTruthy()
  })

  it('opens the handoff composer from the ring', async () => {
    await renderReady(<Dashboard />)

    const write = await screen.findByRole('button', { name: /写一条交接记录/ })
    expect(screen.queryByRole('dialog')).toBeNull()
    fireEvent.click(write)

    // HandoffComposer 打开后就是一个 dialog——「写一条交接记录」这条零配置
    // 路径因此是可达的，不需要 MCP 客户端、模型或任何配置。
    expect(await screen.findByRole('dialog')).toBeTruthy()
  })
})
