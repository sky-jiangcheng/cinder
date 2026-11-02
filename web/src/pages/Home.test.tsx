// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import { lagged, renderReady } from '../test/renderReady'

// Partial mock: override the backend-calling endpoints, keep everything else
// (getConnectionKind etc.) real so components like StatusBar keep working.
//
// Home is the console now (ADR-0038 decision 2): it loads the project list for
// the scope selector and the hot-project recommendation, and the console asks
// the backend for the config and the tag list.
vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    getProjects: vi.fn(),
    getConfig: vi.fn(),
    listAllTags: vi.fn(),
    searchAll: vi.fn(),
    listRecentHandoffs: vi.fn(),
    getSummary: vi.fn(),
    getTodoCounts: vi.fn(),
    getNoteCounts: vi.fn(),
    getHeatmapData: vi.fn(),
    getStatusBar: vi.fn(),
    getScanStatus: vi.fn(),
  }
})

// Same stable-t convention as useKnowledgePage.test.tsx: components pass
// defaultValue inline, so the mock returns it verbatim.
const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import {
  getProjects,
  getConfig,
  listAllTags,
  searchAll,
  type Project,
  type SearchHit,
} from '../api/client'
import Home from './Home'

const mockGetProjects = vi.mocked(getProjects)
const mockGetConfig = vi.mocked(getConfig)
const mockListAllTags = vi.mocked(listAllTags)
const mockSearch = vi.mocked(searchAll)

const project = (over: Partial<Project> = {}): Project => ({
  id: 7,
  name: 'Alpha',
  root_path: '/x',
  level_override: 0,
  is_auto_grouped: false,
  is_starred: false,
  created_at: '',
  repo_count: 1,
  total_added: 0,
  total_deleted: 0,
  my_added: 0,
  my_deleted: 0,
  my_files: 0,
  my_added_7d: 0,
  is_workday: false,
  below_standard: false,
  ...over,
})

const hit = (over: Partial<SearchHit>): SearchHit => ({
  type: 'note',
  id: 5,
  project_id: 2,
  project_name: 'Beta',
  title: '登录排查笔记',
  snippet: '关于登录的记录',
  updated_at: '2026-10-12 10:00:00',
  ...over,
})

const renderHome = async () => { await renderReady(<Home />) }

const flush = async () => { await waitFor(() => {}) }

beforeEach(() => {
  vi.clearAllMocks()
  mockGetProjects.mockResolvedValue([])
  mockGetConfig.mockResolvedValue({ config: {}, scan_roots: [] })
  mockListAllTags.mockResolvedValue([])
  mockSearch.mockResolvedValue([])
})

describe('Home console (ADR-0038 decision 2)', () => {
  it('opens on the idle state: one question box plus the merged recommendation area', async () => {
    mockListAllTags.mockResolvedValue(['网关', '鉴权'])
    mockGetProjects.mockResolvedValue([project({ name: 'Alpha' })])
    await renderHome()

    // One textbox — the console has a single input, not a search box next to
    // a chat box.
    expect(screen.getAllByRole('textbox')).toHaveLength(1)
    // The three recommendation sources live in one place.
    expect(screen.getByText('从这些开始')).toBeTruthy()
    expect(screen.getByText('快捷提问')).toBeTruthy()
    expect(screen.getByText('热门项目')).toBeTruthy()
    expect(screen.getByText('热门标签')).toBeTruthy()
    expect(await screen.findByText('#网关')).toBeTruthy()
  })

  it('keeps the numbers off the home screen — the dashboard is the only authority', async () => {
    await renderHome()
    await flush()
    // The project quick cards and the today card moved to the dashboard
    // (ADR-0038 decision 5): two screens must not answer "how much today"
    // with two different numbers.
    expect(screen.queryByText('重点项目速览')).toBeNull()
    expect(screen.queryByText('今日开发动态')).toBeNull()
    expect(screen.queryByRole('link', { name: /进入完整仪表盘/ })).toBeNull()
  })

  // ADR-0026 promotion criterion 2. The knowledge-scope selector has the
  // exact shape that used to flake in Review — a static option keeps the
  // <select> mounted on the first render while the project options land later —
  // and until this test nothing in the suite had ever touched it. Written after
  // the floor existed, it waits for the option rather than the combobox, so it
  // also holds with REPO_TEST_LAG_MS set; the await-the-combobox pattern it
  // replaces could not.
  it('offers late-arriving projects in the knowledge scope selector', async () => {
    mockGetProjects.mockImplementation(() => lagged([project()]))
    await renderHome()
    // Wait for the option — it is the data — and only then take the select by
    // role, so this file needs no DOM traversal at all (ADR-0026 条件 4).
    await screen.findByRole('option', { name: 'Alpha' })
    const select = (await screen.findByRole('combobox', {
      name: '检索范围',
    })) as HTMLSelectElement
    fireEvent.change(select, { target: { value: '7' } })
    await waitFor(() => expect(select.value).toBe('7'))
  })
})

describe('Home console — the ask / search modes (ADR-0038 decision 3)', () => {
  it('switches modes explicitly instead of guessing from Enter vs the button', async () => {
    await renderHome()
    const ask = screen.getByRole('button', { name: '问' })
    const search = screen.getByRole('button', { name: '搜' })
    expect(ask.getAttribute('aria-pressed')).toBe('true')
    expect(search.getAttribute('aria-pressed')).toBe('false')

    fireEvent.click(search)
    expect(search.getAttribute('aria-pressed')).toBe('true')
    expect(ask.getAttribute('aria-pressed')).toBe('false')
  })

  it('puts search hits in the stream grouped by project and offers "ask about this"', async () => {
    mockSearch.mockResolvedValue([
      hit({ id: 1, project_id: 2, project_name: 'Beta', title: '登录排查笔记' }),
      hit({ id: 2, project_id: 2, project_name: 'Beta', title: '登录超时笔记' }),
      hit({ id: 3, project_id: 3, project_name: 'Gamma', title: '登录网关笔记' }),
    ])
    await renderHome()

    fireEvent.click(screen.getByRole('button', { name: '搜' }))
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: '登录' } })
    fireEvent.keyDown(box, { key: 'Enter' })

    expect(await screen.findByText('登录排查笔记')).toBeTruthy()
    expect(screen.getByText('登录网关笔记')).toBeTruthy()
    // One group per project, with its own count.
    expect(screen.getByText('Beta')).toBeTruthy()
    expect(screen.getByText('Gamma')).toBeTruthy()

    // The one coupling point between search and ask: a hit can become the
    // next question.
    const askBtns = screen.getAllByRole('button', { name: '就这条追问' })
    expect(askBtns.length).toBe(3)
    fireEvent.click(askBtns[0])

    // It prefills the box and switches to the ask mode — it does not send.
    await waitFor(() => expect(screen.getByRole('button', { name: '问' }).getAttribute('aria-pressed')).toBe('true'))
    const next = screen.getByRole('textbox') as HTMLTextAreaElement
    expect(next.value).toContain('登录排查笔记')
  })

  it('clears the box after a search — the query stays in the transcript, not in the box', async () => {
    mockSearch.mockResolvedValue([hit({ title: '命中的那篇笔记' })])
    await renderHome()

    fireEvent.click(screen.getByRole('button', { name: '搜' }))
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: '登录' } })
    fireEvent.keyDown(box, { key: 'Enter' })

    expect(await screen.findByText('命中的那篇笔记')).toBeTruthy()
    // The box empties after a submit. react-router applies setSearchParams as
    // a transition, so there is a render in between where the URL still holds
    // the OLD value — treating that stale value as "someone else changed the
    // URL" used to refill the box with the query just submitted.
    await waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe(''))
  })

  it('runs the query behind a deep link: /?q=… opens the search mode and searches', async () => {
    mockSearch.mockResolvedValue([hit({ title: '深链命中的笔记' })])
    await renderReady(<Home />, { initialEntries: ['/?q=%E7%99%BB%E5%BD%95'] })

    expect(await screen.findByText('深链命中的笔记')).toBeTruthy()
    expect(screen.getByRole('button', { name: '搜' }).getAttribute('aria-pressed')).toBe('true')
  })
})
