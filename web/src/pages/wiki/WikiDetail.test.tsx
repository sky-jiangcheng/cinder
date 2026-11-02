// @vitest-environment jsdom
// WikiDetail.test.tsx — the /wiki/:id detail route (ADR-0032).
//
// What this pins:
//   - the body is rendered as markdown through the existing sanitize chain;
//   - a page that is not approved is refused by the backend, and this route
//     shows the refusal instead of rendering something;
//   - [[wikilinks]] become real links: the library's connective tissue
//     rendered as dead brackets reads as a broken page;
//   - the source notes and the edges are shown, so the page's provenance is
//     visible next to its claims.

import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { screen, waitFor, fireEvent, render } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import i18n from '../../i18n' // real i18next instance — otherwise t() returns raw keys

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    getWikiPage: vi.fn(),
    getProjects: vi.fn(),
  }
})

import { getWikiPage, getProjects, type PageEdge, type Project, type WikiPageDetail } from '../../api/client'
import WikiDetail from './WikiDetail'

const mockGetPage = vi.mocked(getWikiPage)
const mockProjects = vi.mocked(getProjects)

const edge = (over: Partial<PageEdge> = {}): PageEdge => ({
  page_id: 9,
  slug: 'other-page',
  title: '另一页',
  kind: 'concept',
  relation: 'relates',
  inbound: false,
  ...over,
})

const detail = (over: Partial<WikiPageDetail> = {}): WikiPageDetail => ({
  id: 1,
  slug: 'gateway-design',
  title: '网关设计',
  kind: 'concept',
  project_id: 7,
  updated_at: '2026-10-21 10:00:00',
  snippet: '网关的总体设计。',
  content: '## 小标题\n\n网关的总体设计，见 [[other-page]]。',
  status: 'approved',
  source: 'wiki-compile',
  out_links: [edge()],
  in_links: [],
  source_note_ids: [12, 13],
  ...over,
})

const project = (over: Partial<Project> = {}): Project => ({
  id: 7,
  name: 'repotory',
  root_path: '/code/repotory',
  level_override: 0,
  is_auto_grouped: false,
  is_starred: false,
  created_at: '2026-10-01',
  repo_count: 2,
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

function renderDetail(id = '1') {
  return render(
    <MemoryRouter initialEntries={[`/wiki/${id}`]}>
      <Routes>
        <Route path="/wiki/:id" element={<WikiDetail />} />
        <Route path="/wiki" element={<span data-testid="list">列表</span>} />
      </Routes>
    </MemoryRouter>,
  )
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  mockGetPage.mockResolvedValue(detail())
  mockProjects.mockResolvedValue([project()])
})

describe('the /wiki/:id detail route (ADR-0032)', () => {
  it('renders the title, the metadata and the body as markdown', async () => {
    renderDetail()

    expect(await screen.findByRole('heading', { level: 1, name: '网关设计' })).toBeTruthy()
    // The body went through the markdown pipeline, not through a <pre>.
    expect(screen.getByRole('heading', { level: 2, name: '小标题' })).toBeTruthy()
    expect(screen.getByText('repotory')).toBeTruthy()
    expect(screen.getByText(/2026-10-21/)).toBeTruthy()
  })

  it('turns a [[wikilink]] into a link that opens the target page', async () => {
    renderDetail()

    // The wikilink lives inside the rendered body, and the out-link chip
    // carries the same title — the href is what tells the two apart.
    const links = await screen.findAllByRole('link', { name: '另一页' })
    const wikilink = links.find(l => (l.getAttribute('href') ?? '').startsWith('#wikilink:'))
    expect(wikilink).toBeTruthy()
    expect(wikilink?.getAttribute('href')).toBe('#wikilink:other-page')

    fireEvent.click(wikilink as HTMLAnchorElement)
    await waitFor(() => expect(mockGetPage).toHaveBeenCalledWith(9))
  })

  it('lists the source notes and the outgoing links', async () => {
    renderDetail()

    expect(await screen.findByText('来源笔记')).toBeTruthy()
    expect(screen.getByText('笔记 12')).toBeTruthy()
    expect(screen.getByText('出链')).toBeTruthy()
    expect(screen.getAllByText('另一页').length).toBeGreaterThan(0)
  })

  it('refuses to render a page the backend did not approve', async () => {
    mockGetPage.mockRejectedValue(new Error('page 1 is "pending": only approved pages are readable here'))
    renderDetail()

    expect(await screen.findByText(/only approved pages are readable here/)).toBeTruthy()
    // The body is not rendered behind the refusal.
    expect(screen.queryByRole('heading', { level: 1, name: '网关设计' })).toBeNull()
  })

  it('sends the reader back to the list', async () => {
    renderDetail()

    const back = await screen.findByRole('link', { name: /返回知识页列表/ })
    // The wiki list is the "pages" view of the Knowledge tab now (ADR-0038
    // decision 4), so the back link goes straight there instead of bouncing
    // through the /wiki redirect.
    expect(back.getAttribute('href')).toBe('/knowledge?view=pages')
  })
})
