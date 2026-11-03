// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import i18n from '../i18n' // real i18next instance — otherwise t() returns raw keys

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
  getConfig,
  getHeatmapData,
  getProjects,
  getSummary,
} from '../api/client'
import AgentBot from './AgentBot'
import { getDashboardScope, resetDashboardScope, setDashboardScope } from '../state/dashboardScope'
import { getYesterday } from '../utils/dates'

const POS_KEY = 'repotory.agentBot.pos'
const onToast = vi.fn()

// The card's day is the dashboard's day, so the mocks must speak the same
// day the app does — a hard-coded date would silently answer about a day
// nobody is looking at.
const Y = getYesterday()

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  resetDashboardScope()
  vi.mocked(getConfig).mockResolvedValue({
    config: { ai_chat_base_url: 'http://localhost:11434', ai_chat_model: 'qwen' },
    scan_roots: [],
  })
  vi.mocked(getSummary).mockResolvedValue({
    date: Y, repo_count: 5, total_files: 12, total_added: 340,
    total_deleted: 90, my_added: 120, my_deleted: 30, my_files: 4, is_workday: true,
  })
  vi.mocked(getHeatmapData).mockResolvedValue({
    days: [{ date: Y, lines_added: 30, lines_deleted: 6, commits: 7 }],
  })
  vi.mocked(getProjects).mockResolvedValue([])
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
    expect(screen.getByText('仪表盘')).toBeTruthy()
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
    fireEvent.click(await screen.findByRole('button', { name: '近 7 天' }))
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
