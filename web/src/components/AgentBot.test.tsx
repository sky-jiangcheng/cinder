// @vitest-environment jsdom
//
// The panel is a conversation and nothing else. These tests pin what that
// means: open/close, drag, the streamed answer, the tool lines that show the
// model working, the references the answer was built from, and the one place
// it can be filed. The assertions about module chips, the scope picker and the
// hand-built cards went away with the elements themselves.
import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
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
    getProjects: vi.fn(),
  }
})

import {
  askAIWithEvidenceStream,
  createNoteWithMeta,
  fileAnswerAsPage,
  getConfig,
  getProjects,
} from '../api/client'
import type { Evidence, Project } from '../api/types'
import AgentBot from './AgentBot'

const POS_KEY = 'repotory.agentBot.pos'
const onToast = vi.fn()

function projectFixture(id: number, name: string): Project {
  return {
    id, name, root_path: `/x/${name}`, level_override: 0, is_auto_grouped: false,
    is_starred: false, created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
    my_added: 0, my_deleted: 0, my_files: 0, my_added_7d: 0, is_workday: false, below_standard: false,
  }
}

function evidenceFixture(): Evidence {
  return {
    items: [
      { ref: 'P1', type: 'page', id: 3, title: '失败重试策略', kind: 'query', snippet: '', rank: 1 },
    ],
    dropped: 0,
    elapsed_ms: 12,
    truncated: false,
  }
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  vi.mocked(getConfig).mockResolvedValue({
    config: { ai_chat_base_url: 'http://localhost:11434', ai_chat_model: 'qwen' },
    scan_roots: [],
  })
  vi.mocked(getProjects).mockResolvedValue([])
})

function renderBot() {
  return render(
    <MemoryRouter initialEntries={['/dashboard']}>
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

/** Answer a question the way the transport does: an optional tool, then text. */
function answerWith(text: string, opts: { tool?: string; evidence?: Evidence } = {}) {
  vi.mocked(askAIWithEvidenceStream).mockImplementation((_pid, _q, h) => {
    if (opts.tool) {
      h.onToolCall('call-1', opts.tool)
      h.onToolResult('call-1', { ok: true })
    }
    h.onDelta(text)
    h.onDone({
      reply: text,
      evidence: opts.evidence ?? { items: [], dropped: 0, elapsed_ms: 1, truncated: false },
    })
    return () => {}
  })
}

async function ask(question: string) {
  const box = screen.getByLabelText('问题')
  fireEvent.change(box, { target: { value: question } })
  fireEvent.click(screen.getByRole('button', { name: '直接发送' }))
  await waitFor(() => expect(askAIWithEvidenceStream).toHaveBeenCalled())
}

describe('AgentBot', () => {
  it('renders the floating bot, clear of the chat composer', () => {
    renderBot()
    const style = bot().style
    expect(style.left).not.toBe('')
    expect(parseFloat(style.left)).toBeLessThan(window.innerWidth / 4)
    expect(style.right).toBe('')
  })

  it('opens on a click and closes on Escape', () => {
    renderBot()
    clickBot()
    expect(screen.getByRole('dialog')).toBeTruthy()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens on the conversation, not on a wall of chrome', () => {
    renderBot()
    clickBot()
    const panel = screen.getByRole('dialog')
    expect(panel.textContent).toContain('Cinder 助手')
    expect(screen.getByText('问点什么，答案来自你的记忆库。')).toBeTruthy()
    expect(screen.getByLabelText('问题')).toBeTruthy()
    // Nothing from the old panel survives: the only buttons are "close" and
    // "send", and there is no module row, scope picker or chip group left.
    expect(within(panel).getAllByRole('button').map(b => b.textContent || b.getAttribute('aria-label')))
      .toEqual(['关闭', '直接发送'])
  })

  it('streams the answer under the question it came from', async () => {
    answerWith('重试最多三次，然后上报。')
    renderBot()
    clickBot()
    await ask('失败重试策略是什么？')

    // Project 0 is the whole library: the bot is a global assistant.
    expect(vi.mocked(askAIWithEvidenceStream).mock.calls[0][0]).toBe(0)
    expect(vi.mocked(askAIWithEvidenceStream).mock.calls[0][1]).toBe('失败重试策略是什么？')
    expect(await screen.findByText('重试最多三次，然后上报。')).toBeTruthy()
    // The question stays on screen: an answer with no question above it is a
    // fact with no provenance.
    expect(within(screen.getByRole('log')).getByText('失败重试策略是什么？')).toBeTruthy()
  })

  it('shows the tool the model ran, then marks it done', async () => {
    answerWith('今天 12 次提交。', { tool: 'query_daily_stats' })
    renderBot()
    clickBot()
    await ask('今天提交了多少？')
    // The tool line names the tool the way the locale does, not the wire name,
    // and it closes out when the tool returns.
    expect(await screen.findByText('查询昨日代码量')).toBeTruthy()
    expect(await screen.findByText('已完成')).toBeTruthy()
  })

  it('falls back to the raw name for a tool the locale does not know', async () => {
    answerWith('好。', { tool: 'brand_new_tool' })
    renderBot()
    clickBot()
    await ask('试试')
    expect(await screen.findByText('brand_new_tool')).toBeTruthy()
  })

  it('lists the references the answer was built from', async () => {
    answerWith('三次 [P1]。', { evidence: evidenceFixture() })
    renderBot()
    clickBot()
    await ask('重试几次？')
    expect(await screen.findByText('失败重试策略')).toBeTruthy()
    expect(screen.getByText('[P1]')).toBeTruthy()
  })

  it('files the answer as a page with the refs it cited', async () => {
    vi.mocked(getProjects).mockResolvedValue([projectFixture(12, '支付')])
    vi.mocked(fileAnswerAsPage).mockResolvedValue(77)
    answerWith('三次 [P1]。', { evidence: evidenceFixture() })
    renderBot()
    clickBot()
    await ask('重试几次？')

    const button = await screen.findByRole('button', { name: /存为页面/ })
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(button)
    await waitFor(() => expect(fileAnswerAsPage).toHaveBeenCalled())
    const call = vi.mocked(fileAnswerAsPage).mock.calls[0]
    expect(call[0]).toBe(12)
    expect(call[4]).toEqual(['P1'])
  })

  it('saves the answer as a note', async () => {
    vi.mocked(getProjects).mockResolvedValue([projectFixture(12, '支付')])
    vi.mocked(createNoteWithMeta).mockResolvedValue({
      id: 1, project_id: 12, kind: 'knowledge', title: '重试几次？', source: 'ai',
    } as Awaited<ReturnType<typeof createNoteWithMeta>>)
    answerWith('三次。')
    renderBot()
    clickBot()
    await ask('重试几次？')

    const button = await screen.findByRole('button', { name: /保存为笔记/ })
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(button)
    await waitFor(() => expect(createNoteWithMeta).toHaveBeenCalled())
  })

  it('a drag moves the bot and does not open the panel', () => {
    renderBot()
    const b = bot()
    const before = { x: parseFloat(b.style.left), y: parseFloat(b.style.top) }
    fireEvent.pointerDown(b, { pointerId: 1, button: 0, clientX: 100, clientY: 100 })
    fireEvent.pointerMove(b, { pointerId: 1, clientX: 300, clientY: 200 })
    fireEvent.pointerUp(b, { pointerId: 1, clientX: 300, clientY: 200 })

    expect(screen.queryByRole('dialog')).toBeNull()
    const after = { x: parseFloat(b.style.left), y: parseFloat(b.style.top) }
    expect(after.x).not.toBe(before.x)
    const saved = JSON.parse(window.localStorage.getItem(POS_KEY) || '{}')
    expect(saved.x).toBeCloseTo(after.x)
    expect(saved.y).toBeCloseTo(after.y)
  })

  it('keeps the position the user chose', () => {
    window.localStorage.setItem(POS_KEY, JSON.stringify({ x: 321, y: 123 }))
    renderBot()
    expect(parseFloat(bot().style.left)).toBe(321)
    expect(parseFloat(bot().style.top)).toBe(123)
  })

  it('stays inside the window when it is resized', async () => {
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

  it('points at settings instead of sending when no model is configured', async () => {
    vi.mocked(getConfig).mockResolvedValue({ config: {}, scan_roots: [] })
    renderBot()
    clickBot()
    expect(await screen.findByText(/尚未配置/)).toBeTruthy()
    const box = screen.getByLabelText('问题')
    fireEvent.change(box, { target: { value: '在吗' } })
    expect((screen.getByRole('button', { name: '直接发送' }) as HTMLButtonElement).disabled).toBe(true)
  })
})
