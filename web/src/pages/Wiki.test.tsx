// @vitest-environment jsdom
// Wiki.test.tsx — the PAGES BODY of the knowledge tab (ADR-0032 semantics,
// ADR-0038 decision 4).
//
// The page is no longer a route of its own: /knowledge?view=pages renders it
// inside the shared skeleton, which owns the search field, the project
// scope, the result count and the error banner. This file therefore tests
// the BODY: given a query, a scope and the project list, it lists the
// approved pages and says the right thing when there are none.
//
// What this pins:
//   - only approved pages are listed (the backend refuses anything else, and
//     this body never asks for anything else);
//   - a global page is LABELLED — it is visible inside a project scope
//     (ADR-0031), and without the label that reads as a filter that leaked;
//   - the scope carries the evidence path's scope, not the review queue's:
//     a project means "that project's pages PLUS the global ones";
//   - a failed load reports the failure instead of pretending the library is
//     empty.

import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { screen, waitFor, render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import i18n from '../i18n' // real i18next instance — otherwise t() returns raw keys

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    browseWikiPages: vi.fn(),
    searchWikiPages: vi.fn(),
  }
})

import { browseWikiPages, searchWikiPages, type Project, type WikiPageSummary } from '../api/client'
import WikiPagesView from './Wiki'

const mockBrowse = vi.mocked(browseWikiPages)
const mockSearch = vi.mocked(searchWikiPages)

const summary = (over: Partial<WikiPageSummary> = {}): WikiPageSummary => ({
  id: 1,
  slug: 'gateway-design',
  title: '网关设计',
  kind: 'concept',
  project_id: 0,
  updated_at: '2026-10-21 10:00:00',
  snippet: '网关的总体设计。',
  ...over,
})

const project = (over: Partial<Project> = {}): Project => ({
  id: 1,
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

// The body is a plain component: the skeleton hands the query and the scope
// down, so the tests hand them in.
function renderBody(props: {
  query?: string
  scope?: number
  projects?: Project[]
  onCount?: (count: number, countKey: string) => void
  onError?: (message: string, retry: () => void) => void
} = {}) {
  return render(
    <MemoryRouter>
      <WikiPagesView
        query={props.query ?? ''}
        scope={props.scope ?? 0}
        projects={props.projects ?? [project({ id: 7, name: 'repotory' })]}
        onCount={props.onCount}
        onError={props.onError}
      />
    </MemoryRouter>,
  )
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  mockBrowse.mockResolvedValue([])
  mockSearch.mockResolvedValue([])
})

describe('the pages body of the knowledge tab (ADR-0032)', () => {
  it('lists the approved pages with the scope of each one', async () => {
    mockBrowse.mockResolvedValue([
      summary({ id: 2, slug: 'b', title: 'B 页', project_id: 7 }),
      summary({ id: 1, slug: 'a', title: 'A 页', project_id: 0 }),
    ])
    renderBody()

    expect(await screen.findByText('A 页')).toBeTruthy()
    expect(screen.getByText('B 页')).toBeTruthy()
    // The global page says so; the project page says which project.
    const rows = screen.getAllByRole('link')
    const globalRow = rows.find(l => l.textContent?.includes('A 页'))
    const projectRow = rows.find(l => l.textContent?.includes('B 页'))
    expect(globalRow?.textContent).toContain('全局')
    expect(projectRow?.textContent).toContain('repotory')
    expect(globalRow?.textContent).not.toContain('repotory')
    // No scope picked -> every approved page.
    expect(mockBrowse).toHaveBeenCalledWith(0, 100)
  })

  it('asks the backend for a search once a query is handed in', async () => {
    mockSearch.mockResolvedValue([summary({ id: 3, title: '命中页' })])
    renderBody({ query: '网关' })

    await waitFor(() => expect(mockSearch).toHaveBeenCalledWith(0, '网关', 50), { timeout: 2000 })
    expect(await screen.findByText('命中页')).toBeTruthy()
  })

  it('narrows to the scope the skeleton handed it', async () => {
    renderBody({ scope: 7 })
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(7, 100), { timeout: 2000 })
  })

  it('says the library is empty instead of showing a bare list', async () => {
    renderBody()
    expect(await screen.findByText('还没有已批准的知识页')).toBeTruthy()
  })

  it('distinguishes "no match" from "no pages"', async () => {
    mockSearch.mockResolvedValue([])
    renderBody({ query: '不存在的词' })

    await waitFor(() => expect(mockSearch).toHaveBeenCalled(), { timeout: 2000 })
    expect(await screen.findByText('没有匹配的知识页')).toBeTruthy()
    expect(screen.queryByText('还没有已批准的知识页')).toBeNull()
  })

  it('reports the failure instead of pretending the library is empty', async () => {
    mockBrowse.mockRejectedValue(new Error('network down'))
    const onError = vi.fn()
    renderBody({ onError })

    // The skeleton draws the error banner, so the body only reports it.
    await waitFor(() => expect(onError).toHaveBeenCalledWith('network down', expect.any(Function)), { timeout: 2000 })
    expect(screen.queryByText('还没有已批准的知识页')).toBeNull()
  })

  it('reports how many pages it has, with the count key the skeleton renders', async () => {
    mockBrowse.mockResolvedValue([summary({ id: 1 }), summary({ id: 2 })])
    const onCount = vi.fn()
    renderBody({ onCount })

    await waitFor(() => expect(onCount).toHaveBeenCalledWith(2, 'wiki.resultCount'), { timeout: 2000 })
  })

  it('links a page to its own address (/wiki/:id stays the detail route)', async () => {
    mockBrowse.mockResolvedValue([summary({ id: 42, title: 'A 页' })])
    renderBody()

    const link = await screen.findByRole('link', { name: /A 页/ })
    expect(link.getAttribute('href')).toBe('/wiki/42')
  })
})
