// @vitest-environment jsdom
// ContextRail.test.tsx — the right column of the desktop shell (ADR-0034).
//
// What this pins:
//   - the rail has NO global search input any more (ADR-0038 决策 6): the home
//     page is a full-page conversation console with its own input, and ⌘K still
//     opens the command palette. A second search box in the rail made "search"
//     look like a rail feature when it is a top-level action;
//   - the two blocks the rail is actually for — the review queue and the
//     recently updated pages — are still there.
//
// The real i18n instance is used (like Wiki.test.tsx) so the assertions read
// the wording a user reads, not translation keys.

import { describe, expect, it, vi, beforeEach, beforeAll, afterEach } from 'vitest'
import { screen, waitFor, render, fireEvent, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import i18n from '../i18n' // real i18next instance — otherwise t() returns raw keys
import { lagged } from '../test/renderReady' // ADR-0026 probe: REPO_TEST_LAG_MS re-opens the window

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    browseWikiPages: vi.fn(),
    listPendingWikiPages: vi.fn(),
    getProjects: vi.fn(),
  }
})

import {
  browseWikiPages,
  getProjects,
  listPendingWikiPages,
  type WikiPageSummary,
  type WikiPendingPage,
} from '../api/client'
import ContextRail from './ContextRail'
import { publishAssistantCards } from '../hooks/assistantCards'
import type { Project, EvidenceItem } from '../api/client'

const mockBrowse = vi.mocked(browseWikiPages)
const mockPending = vi.mocked(listPendingWikiPages)
const mockProjects = vi.mocked(getProjects)

const pageSummary = (over: Partial<WikiPageSummary> = {}): WikiPageSummary => ({
  id: 11,
  slug: 'gateway-design',
  title: '网关设计',
  kind: 'concept',
  project_id: 0,
  updated_at: '2026-10-21 10:00:00',
  snippet: '网关的总体设计。',
  ...over,
})

const pendingPage = (over: Partial<WikiPendingPage['page']> = {}): WikiPendingPage => ({
  page: {
    id: 21,
    slug: 'pending-a',
    title: '待审核页 A',
    kind: 'concept',
    project_id: 7,
    content: '',
    updated_at: '2026-10-22 09:00:00',
    status: 'pending',
    source: 'local',
    ...over,
  },
  source_note_ids: [],
  out_links: [],
  in_links: [],
})

function renderRail(collapsed = false, initialEntries: string[] = ['/']) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <ContextRail collapsed={collapsed} onToggle={() => {}} />
    </MemoryRouter>,
  )
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  mockBrowse.mockImplementation(() => lagged([]))
  mockPending.mockImplementation(() => lagged([]))
  mockProjects.mockImplementation(() => lagged([]))
  act(() => publishAssistantCards({ projectCards: [], knowledgeCards: [], messageId: '' }))
})

// The module store outlives a test; leaving cards behind would make unrelated
// tests render stale names.
afterEach(() => {
  act(() => publishAssistantCards({ projectCards: [], knowledgeCards: [], messageId: '' }))
})

describe('ContextRail routing focus — opening /project/:id pins the rail to it', () => {
  const cbimoneyProject = {
    id: 137, name: 'CBiMoney', root_path: '/w/CBiMoney', level_override: 0,
    is_auto_grouped: false, is_starred: true, created_at: '', repo_count: 1,
    total_added: 0, total_deleted: 0, my_added: 0, my_deleted: 0, my_files: 0,
    my_added_7d: 0, is_workday: true, below_standard: false,
  }

  it('scopes queue + recent to the routed project and names the scope badge', async () => {
    // 拉取 → 下拉出现（供给选择）
    mockProjects.mockImplementation(() => lagged([cbimoneyProject]))
    mockPending.mockImplementation(() => lagged([pendingPage()]))
    mockBrowse.mockImplementation(() => lagged([pageSummary()]))

    renderRail(false, ['/project/137'])
    // 队列与最近更新都按路由项目过滤……
    await waitFor(() => expect(mockPending).toHaveBeenCalledWith(137))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(137, 6))
    // ……徽标名字由项目列表解析（路由只有 id）。
    expect(await screen.findAllByText('CBiMoney')).toBeTruthy()
  })

  it('falls back to the whole-library scope on non-project routes', async () => {
    renderRail(false, ['/'])
    await waitFor(() => expect(mockPending).toHaveBeenCalledWith(0))
    expect(mockPending).toHaveBeenCalledTimes(1)
  })

  it('hides the chat evidence blocks on a project-detail route', async () => {
    // 有上一轮对话的引用卡片，但用户已导航到项目详情——"本轮提到…"是
    // 对话台的语境，在这里是噪音。
    mockPending.mockImplementation(() => lagged([pendingPage()]))
    mockBrowse.mockImplementation(() => lagged([pageSummary()]))
    act(() => publishAssistantCards({
      projectCards: [cbimoneyProject], knowledgeCards: [], messageId: 'ai-cbimoney',
    }))
    renderRail(false, ['/project/137'])
    await waitFor(() => expect(mockPending).toHaveBeenCalledWith(137))
    expect(screen.queryByText('本轮涉及项目')).toBeNull()
    expect(screen.queryByText('本轮引用知识')).toBeNull()
    // 项目上下文区块照常
    expect(await screen.findByText('待审核')).toBeTruthy()
    expect(await screen.findByText('最近更新')).toBeTruthy()
  })

  it('pins an empty queue message to the routed project name', async () => {
    mockPending.mockImplementation(() => lagged([]))
    mockProjects.mockImplementation(() => lagged([cbimoneyProject]))
    renderRail(false, ['/project/137'])
    await waitFor(() => expect(mockPending).toHaveBeenCalledWith(137))
    expect(await screen.findByText('CBiMoney 暂无待审核页')).toBeTruthy()
  })
})

describe('ContextRail dynamic scope — the rail follows the newest answer', () => {
  const singleProject: Project = {
    id: 137, name: 'CBiMoney', root_path: '/workspace/CBiMoney',
    level_override: 0, is_auto_grouped: false, is_starred: true,
    created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
    my_added: 0, my_deleted: 0, my_files: 0,
    my_added_7d: 0, is_workday: true, below_standard: false,
  }
  const otherProject: Project = { ...singleProject, id: 42, name: 'CBiPay' }

  it('scopes the queue and recent list to the one project the answer involves', async () => {
    act(() => publishAssistantCards({
      projectCards: [singleProject], knowledgeCards: [], messageId: 'ai-cbimoney',
    }))
    renderRail()
    await waitFor(() => expect(mockPending).toHaveBeenCalledWith(137))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(137, 6))
    // The heading names the scope, so "whose queue is this" is never a guess.
    expect(screen.getAllByText('CBiMoney').length).toBeGreaterThan(0)
  })

  it('keeps the whole-library scope when the answer spans several projects', async () => {
    act(() => publishAssistantCards({
      projectCards: [singleProject, otherProject], knowledgeCards: [], messageId: 'ai-multi',
    }))
    renderRail()
    await waitFor(() => expect(mockPending).toHaveBeenCalledWith(0))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(0, 6))
    // Multi-project answers cannot be reduced to one project badge without
    // hiding the other projects' items — the global list is the honest view,
    // and the badge SAYS it's global so the unfiltered queue reads as intent.
    expect(screen.getByText('全库')).toBeTruthy()
  })

  it('re-reads the scope on every settled answer (compilations land while chatting)', async () => {
    renderRail()
    await waitFor(() => expect(mockPending).toHaveBeenCalledWith(0))
    act(() => publishAssistantCards({
      projectCards: [singleProject], knowledgeCards: [], messageId: 'ai-next',
    }))
    // Same project as before would not re-fetch; a NEW answer re-runs the load
    // so a page compiled by that very answer shows up without leaving chat.
    await waitFor(() => expect(mockPending).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledTimes(2))
    expect(mockPending).toHaveBeenLastCalledWith(137)
  })
})

describe('ContextRail (ADR-0038 decision 6 — the global search box is gone)', () => {
  it('has no global search input', async () => {
    renderRail()
    await waitFor(() => expect(mockBrowse).toHaveBeenCalled())

    // No searchbox role anywhere in the rail…
    expect(screen.queryByRole('searchbox')).toBeNull()
    // …and no other input either (a plain one would read as textbox).
    expect(screen.queryByRole('textbox')).toBeNull()
  })

  it('still shows the review queue and the recently updated pages', async () => {
    mockPending.mockImplementation(() => lagged([pendingPage()]))
    mockBrowse.mockImplementation(() => lagged([pageSummary()]))
    renderRail()
    await waitFor(() => expect(mockBrowse).toHaveBeenCalled())

    expect(screen.getByText('待审核')).toBeTruthy()
    expect(await screen.findByText('待审核页 A')).toBeTruthy()
    expect(screen.getByText('最近更新')).toBeTruthy()
    // The page title comes from the fetched list, so it waits for the data
    // instead of relying on the findBy above to have flushed the render.
    expect(await screen.findByText('网关设计')).toBeTruthy()
  })

  it('shows the newest answer\'s project and knowledge cards', () => {
    const project: Project = {
      id: 137, name: 'MiddlePlatform', root_path: '/workspace/MiddlePlatform',
      level_override: 0, is_auto_grouped: false, is_starred: false,
      created_at: '', repo_count: 1, total_added: 0, total_deleted: 0,
      my_added: 0, my_deleted: 0, my_files: 0,
      my_added_7d: 0, is_workday: true, below_standard: false,
    }
    const knowledge: EvidenceItem = {
      ref: 'N1', type: 'note', id: 8, title: '交接记录',
      kind: 'knowledge', snippet: '…', rank: 1, project_name: 'MiddlePlatform',
    }
    act(() => publishAssistantCards({
      projectCards: [project], knowledgeCards: [knowledge], messageId: 'ai-1',
    }))
    renderRail()

    expect(screen.getByText('本轮涉及项目')).toBeTruthy()
    expect(screen.getAllByText('MiddlePlatform').length).toBeGreaterThan(0)
    expect(screen.getByText('本轮引用知识')).toBeTruthy()
    expect(screen.getByText(/交接记录/)).toBeTruthy()
    expect(screen.getByText(/\[N1\]/)).toBeTruthy()
  })

  it('keeps the collapsed handle working', async () => {
    const onToggle = vi.fn()
    render(
      <MemoryRouter>
        <ContextRail collapsed onToggle={onToggle} />
      </MemoryRouter>,
    )

    fireEvent.click(screen.getByRole('button', { name: '展开上下文栏' }))
    expect(onToggle).toHaveBeenCalledWith(false)
  })
})
