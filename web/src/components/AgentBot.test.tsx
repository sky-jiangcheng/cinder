// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import i18n from '../i18n' // real i18next instance — otherwise t() returns raw keys
import { lagged } from '../test/renderReady' // ADR-0026 probe: REPO_TEST_LAG_MS re-opens the window

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    askAIWithEvidenceStream: vi.fn(),
    createNoteWithMeta: vi.fn(),
    fileAnswerAsPage: vi.fn(),
    getConfig: vi.fn(),
    getHeatmapData: vi.fn(),
    getInstallInfo: vi.fn(),
    getNoteCounts: vi.fn(),
    getProjectDetail: vi.fn(),
    getProjectOverview: vi.fn(),
    getProjects: vi.fn(),
    getStatusBar: vi.fn(),
    getSummary: vi.fn(),
    getTodoCounts: vi.fn(),
    listPendingWikiPages: vi.fn(),
    listRecentHandoffs: vi.fn(),
  }
})

import {
  askAIWithEvidenceStream,
  getConfig,
  getHeatmapData,
  getProjectDetail,
  getProjectOverview,
  getProjects,
  getSummary,
} from '../api/client'
import type { Project, ProjectDetail, ProjectOverview } from '../api/types'
import AgentBot from './AgentBot'
import { getDashboardScope, resetDashboardScope, setDashboardScope } from '../state/dashboardScope'
import { getYesterday } from '../utils/dates'

const POS_KEY = 'repotory.agentBot.pos'
const onToast = vi.fn()

// The card's day is the dashboard's day, so the mocks must speak the same
// day the app does — a hard-coded date would silently answer about a day
// nobody is looking at.
const Y = getYesterday()

// The three project payloads are typed in full by the API layer, so the mocks
// must be too — a partial literal would not compile against `getProjects`.
// These helpers build the smallest value of each type that still says
// everything the panel reads.
function projectFixture(id: number, name: string): Project {
  return {
    id, name, root_path: `/x/${name}`, level_override: 0, is_auto_grouped: false,
    is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
    my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
  }
}

function projectDetailFixture(id: number, name: string): ProjectDetail {
  return {
    id, name, root_path: `/x/${name}`, level_override: 0, is_auto_grouped: false,
    repos: [],
  }
}

function projectOverviewFixture(): ProjectOverview {
  return {
    readme_excerpt: '', tech_stack: [], languages: [], dependencies: [],
    top_contributors: [], cached: false, recent_commits: [],
    activity: {
      total_commits: 12, commit_rate_30d: 3, active_days: 4,
      last_commit_date: Y, active_months: 1,
    },
  }
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  resetDashboardScope()
  vi.mocked(getConfig).mockImplementation(() => lagged({
    config: { ai_chat_base_url: 'http://localhost:11434', ai_chat_model: 'qwen' },
    scan_roots: [],
  }))
  vi.mocked(getSummary).mockImplementation(() => lagged({
    date: Y, repo_count: 5, total_files: 12, total_added: 340,
    total_deleted: 90, my_added: 120, my_deleted: 30, my_files: 4, is_workday: true,
  }))
  vi.mocked(getHeatmapData).mockImplementation(() => lagged({
    days: [{ date: Y, lines_added: 30, lines_deleted: 6, commits: 7 }],
  }))
  vi.mocked(getProjects).mockImplementation(() => lagged([]))
})

function renderBot(path = '/dashboard') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AgentBot onToast={onToast} />
    </MemoryRouter>
  )
}

function bot() {
  return screen.getByRole('button', { name: '打开 Agent' })
}

/** A press that does not move: the gesture that means "click". */
function clickBot() {
  const b = bot()
  fireEvent.pointerDown(b, { pointerId: 1, button: 0, clientX: 100, clientY: 100 })
  fireEvent.pointerUp(b, { pointerId: 1, button: 0, clientX: 100, clientY: 100 })
}

describe('AgentBot', () => {
  it('renders the floating bot', () => {
    renderBot()
    expect(bot()).toBeTruthy()
  })

  it('starts clear of the chat composer, which owns the bottom-right corner', () => {
    renderBot()
    const style = bot().style
    // The old capture button sat at the window's bottom-right — exactly on
    // top of the chat page's send button. The bot starts on the left.
    expect(style.left).not.toBe('')
    expect(parseFloat(style.left)).toBeLessThan(window.innerWidth / 4)
    expect(style.right).toBe('')
  })

  it('opens the panel on a click', () => {
    renderBot()
    clickBot()
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('names the page it is on', () => {
    renderBot('/dashboard')
    clickBot()
    // The module chip row repeats the module's name — that is the point of
    // the row — so the page label is the one OUTSIDE that group.
    const group = screen.getByRole('group', { name: '切换模块' })
    const labels = screen.getAllByText('仪表盘')
    expect(labels.some(node => !group.contains(node))).toBe(true)
  })

  it('opens with a card, not an empty box', async () => {
    renderBot('/dashboard')
    clickBot()
    // The panel answers the page's headline question on its own: an agent
    // that opens to an empty input has told the user nothing. The card
    // carries the number — today's commit count from the heatmap.
    expect(await screen.findByText('提交')).toBeTruthy()
    expect(await screen.findByText('7')).toBeTruthy()
  })

  it('shows the user\'s own line count, like the dashboard does', async () => {
    renderBot('/dashboard')
    clickBot()
    // The dashboard's strip says "个人新增 120" for the same day and scope;
    // the card must say the same number, not the library-wide total.
    expect(await screen.findByText('我的新增行')).toBeTruthy()
    expect(await screen.findByText('+120')).toBeTruthy()
  })

  it('reads the dashboard in the dashboard\'s own scope', async () => {
    renderBot('/dashboard')
    clickBot()
    await screen.findByText('提交')
    expect(getSummary).toHaveBeenCalledWith(Y, true)
  })

  it('follows the dashboard when its scope changes', async () => {
    renderBot('/dashboard')
    clickBot()
    await screen.findByText('提交')

    // The dashboard's starred filter is the user's choice; the agent's card
    // answers in the same scope, or the two numbers disagree.
    setDashboardScope({ starredOnly: false })
    await waitFor(() => {
      expect(getSummary).toHaveBeenCalledWith(Y, false)
    })
  })

  it('answers a quick action with its own card', async () => {
    renderBot('/dashboard')
    clickBot()
    const chip = await screen.findByRole('button', { name: '近 7 天' })
    // The chips are disabled while a card is loading, and the panel loads its
    // first card on open — so clicking before that load settles is a click on
    // a disabled button, which the browser (and jsdom) drops. Wait for the
    // chip to become clickable, or the test depends on how fast the first
    // load resolved: green locally, red on a loaded CI machine.
    await waitFor(() => expect((chip as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(chip)
    expect(await screen.findByText('近 7 天提交')).toBeTruthy()
  })

  it('dragging moves the bot and does NOT open the panel', () => {
    renderBot()
    const b = bot()
    const before = { x: parseFloat(b.style.left), y: parseFloat(b.style.top) }
    fireEvent.pointerDown(b, { pointerId: 1, button: 0, clientX: 100, clientY: 100 })
    fireEvent.pointerMove(b, { pointerId: 1, clientX: 300, clientY: 200 })
    fireEvent.pointerUp(b, { pointerId: 1, clientX: 300, clientY: 200 })

    expect(screen.queryByRole('dialog')).toBeNull()
    const after = { x: parseFloat(b.style.left), y: parseFloat(b.style.top) }
    expect(after.x).not.toBe(before.x)
    // The position is the user's from now on.
    const saved = JSON.parse(window.localStorage.getItem(POS_KEY) || '{}')
    expect(saved.x).toBeCloseTo(after.x)
    expect(saved.y).toBeCloseTo(after.y)
  })

  it('keeps the position the user chose', () => {
    window.localStorage.setItem(POS_KEY, JSON.stringify({ x: 321, y: 123 }))
    renderBot()
    const b = bot()
    expect(parseFloat(b.style.left)).toBe(321)
    expect(parseFloat(b.style.top)).toBe(123)
  })

  it('offers the five modules and marks the one it is on', () => {
    renderBot('/dashboard')
    clickBot()
    const group = screen.getByRole('group', { name: '切换模块' })
    const chips = within(group).getAllByRole('button')
    // The bot can lead the way: the same five entries the rail carries
    // (ADR-0045 decision 3), so the bot and the rail can never point at
    // different modules.
    expect(chips.map(c => c.textContent)).toEqual(['首页', '仪表盘', '知识库', '审核', '设置'])
    expect(chips[1].className).toContain('moduleChipActive')
  })

  it('asks about the whole library by default, even on a project page', async () => {
    vi.mocked(getProjects).mockImplementation(() => lagged([
      projectFixture(7, '支付'),
    ]))
    vi.mocked(getProjectDetail).mockImplementation(() => lagged(projectDetailFixture(7, '支付')))
    vi.mocked(getProjectOverview).mockImplementation(() => lagged(projectOverviewFixture()))
    renderBot('/project/7')
    clickBot()
    await screen.findAllByText('项目概览')
    // ADR-0045 decision 4: the desktop bot is a global assistant — the
    // page is only its current topic. The scope chip says so out loud.
    expect(screen.getByText('全库')).toBeTruthy()
  })

  it('scopes a free-form question to the library until the user picks a project', async () => {
    vi.mocked(getProjects).mockImplementation(() => lagged([
      projectFixture(7, '支付'),
    ]))
    vi.mocked(getProjectDetail).mockImplementation(() => lagged(projectDetailFixture(7, '支付')))
    vi.mocked(getProjectOverview).mockImplementation(() => lagged(projectOverviewFixture()))
    renderBot('/project/7')
    clickBot()
    const box = await screen.findByLabelText('问题')
    fireEvent.change(box, { target: { value: '这个项目最近踩了什么坑？' } })
    fireEvent.click(screen.getByRole('button', { name: '直接发送' }))
    await waitFor(() => expect(askAIWithEvidenceStream).toHaveBeenCalled())
    expect(vi.mocked(askAIWithEvidenceStream).mock.calls[0][0]).toBe(0)
  })

  it('lets the user lock the question to a project', async () => {
    vi.mocked(getProjects).mockImplementation(() => lagged([
      projectFixture(7, '支付'),
    ]))
    vi.mocked(getProjectDetail).mockImplementation(() => lagged(projectDetailFixture(7, '支付')))
    vi.mocked(getProjectOverview).mockImplementation(() => lagged(projectOverviewFixture()))
    renderBot('/project/7')
    clickBot()
    await screen.findAllByText('项目概览')

    // Open the scope picker and pick the project.
    fireEvent.click(screen.getByRole('button', { name: /当前上下文/ }))
    fireEvent.click(await screen.findByRole('menuitemradio', { name: '当前项目 · 支付' }))
    // The scope chip now names the locked project.
    await screen.findAllByText('支付')

    const box = await screen.findByLabelText('问题')
    fireEvent.change(box, { target: { value: '这个项目最近踩了什么坑？' } })
    fireEvent.click(screen.getByRole('button', { name: '直接发送' }))
    await waitFor(() => expect(askAIWithEvidenceStream).toHaveBeenCalled())
    expect(vi.mocked(askAIWithEvidenceStream).mock.calls[0][0]).toBe(7)
  })

  it('keeps the bot inside the window when it is resized', async () => {
    renderBot()
    const b = bot()
    fireEvent.pointerDown(b, { pointerId: 1, button: 0, clientX: 100, clientY: 100 })
    fireEvent.pointerMove(b, { pointerId: 1, clientX: 900, clientY: 700 })
    fireEvent.pointerUp(b, { pointerId: 1, clientX: 900, clientY: 700 })

    window.innerWidth = 600
    window.innerHeight = 400
    fireEvent(window, new Event('resize'))
    await waitFor(() => {
      expect(parseFloat(b.style.left)).toBeLessThanOrEqual(600)
      expect(parseFloat(b.style.top)).toBeLessThanOrEqual(400)
    })
    window.innerWidth = 1024
    window.innerHeight = 768
  })
})

describe('the shared scope', () => {
  it('starts on the dashboard\'s defaults', () => {
    expect(getDashboardScope()).toEqual({ date: Y, starredOnly: true })
  })
})
