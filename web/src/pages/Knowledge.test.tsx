// @vitest-environment jsdom
// Knowledge.test.tsx — the knowledge tab after ADR-0038 decision 4.
//
// The tab used to be two top-level tabs ("知识库" and "知识页") with almost
// the same skeleton and two different capabilities. Now it is ONE tab with
// two views over one skeleton.
//
// What this pins:
//   - the default view is the notes, and ?view=pages deep-links into the
//     approved pages;
//   - the view switcher writes the view into the URL, and the browser's back
//     button can walk it (a REPLACE write would swallow the step);
//   - the SKELETON is shared: the search field and the project scope are
//     there in both views — the scope used to exist only on the pages half,
//     even though the notes search was cross-project all along (the uneven
//     pair was the clearest sign that these were one thing split in two).

import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { screen, waitFor, fireEvent, render } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import i18n from '../i18n' // real i18next instance — otherwise t() returns raw keys

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    listAllNotes: vi.fn(),
    listAllTags: vi.fn(),
    searchAll: vi.fn(),
    getProjects: vi.fn(),
    browseWikiPages: vi.fn(),
    searchWikiPages: vi.fn(),
    listOrganizeGroups: vi.fn(),
    getScanStatus: vi.fn(),
  }
})

import {
  listAllNotes, listAllTags, searchAll, getProjects,
  browseWikiPages, searchWikiPages, listOrganizeGroups,
  type NoteWithProject, type Project, type WikiPageSummary,
} from '../api/client'
import Knowledge from './Knowledge'

const mockNotes = vi.mocked(listAllNotes)
const mockTags = vi.mocked(listAllTags)
const mockSearchAll = vi.mocked(searchAll)
const mockProjects = vi.mocked(getProjects)
const mockBrowse = vi.mocked(browseWikiPages)
const mockSearchWiki = vi.mocked(searchWikiPages)
const mockGroups = vi.mocked(listOrganizeGroups)

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

const note = (over: Partial<NoteWithProject> = {}): NoteWithProject => ({
  id: 1,
  project_id: 7,
  title: '网关设计',
  content: '',
  tags: '',
  kind: 'knowledge',
  pinned: false,
  source: '',
  topic_id: 0,
  sort_order: 0,
  created_at: '2026-10-06T00:00:00Z',
  updated_at: '2026-10-06T00:00:00Z',
  project_name: 'repotory',
  root_path: '/code/repotory',
  ...over,
})

const page = (over: Partial<WikiPageSummary> = {}): WikiPageSummary => ({
  id: 1,
  slug: 'gateway-design',
  title: '网关设计',
  kind: 'concept',
  project_id: 0,
  updated_at: '2026-10-21 10:00:00',
  snippet: '网关的总体设计。',
  ...over,
})

// The URL is part of the contract (?view= / ?q= / ?project= are the
// deep-link surface), so the tests need to be able to read it — and to walk
// it, the way the browser's back button does.
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

function renderKnowledge(initial = '/knowledge') {
  return render(
    <MemoryRouter initialEntries={[initial]}>
      <LocationProbe />
      <Knowledge />
    </MemoryRouter>,
  )
}

/** The segmented control's two buttons, found by the view they carry. */
function viewButton(view: 'notes' | 'pages'): HTMLElement {
  const btn = screen.getAllByRole('button').find(b => b.getAttribute('data-view') === view)
  if (!btn) throw new Error(`no view button for ${view}`)
  return btn
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  mockNotes.mockResolvedValue([note()])
  mockTags.mockResolvedValue([])
  mockSearchAll.mockResolvedValue([])
  mockProjects.mockResolvedValue([project()])
  mockBrowse.mockResolvedValue([page()])
  mockSearchWiki.mockResolvedValue([])
  mockGroups.mockResolvedValue([])
})

describe('the knowledge tab (ADR-0038 decision 4)', () => {
  it('opens on the notes view', async () => {
    renderKnowledge()

    expect(viewButton('notes').getAttribute('aria-pressed')).toBe('true')
    expect(viewButton('pages').getAttribute('aria-pressed')).toBe('false')
    // The notes body is up, and it reported its count to the shared count
    // bar — one count bar for the tab, whatever the body underneath is.
    expect(await screen.findByText('1 条笔记')).toBeTruthy()
  })

  it('deep-links into the pages view with ?view=pages', async () => {
    renderKnowledge('/knowledge?view=pages')

    expect(viewButton('pages').getAttribute('aria-pressed')).toBe('true')
    expect(await screen.findByText('1 张已批准的知识页')).toBeTruthy()
    expect(mockBrowse).toHaveBeenCalledWith(0, 100)
  })

  it('switches the view and writes it into the URL', async () => {
    renderKnowledge()
    await screen.findByText('1 条笔记')

    fireEvent.click(viewButton('pages'))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('view=pages'), { timeout: 2000 })
    expect(viewButton('pages').getAttribute('aria-pressed')).toBe('true')
    expect(mockBrowse).toHaveBeenCalledWith(0, 100)
  })

  it('keeps the back button able to walk the view switch', async () => {
    renderKnowledge()
    await screen.findByText('1 条笔记')

    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('view=pages'), { timeout: 2000 })

    // Back: the view returns to the notes, and the URL says so. A REPLACE
    // write would have eaten the step and left the back button on the page
    // before the tab.
    fireEvent.click(screen.getByTestId('back'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).not.toContain('view=pages'), { timeout: 2000 })
    expect(viewButton('notes').getAttribute('aria-pressed')).toBe('true')
  })

  it('shares one search field between the two views', async () => {
    renderKnowledge()
    await screen.findByText('1 条笔记')
    expect(screen.getByRole('searchbox')).toBeTruthy()

    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalled(), { timeout: 2000 })

    // The field is still there — it is the skeleton, not the body.
    expect(screen.getByRole('searchbox')).toBeTruthy()
  })

  it('gives the notes view the project scope it never had', async () => {
    // The whole point of the merge: the notes view is cross-project, so it
    // needs the same scope dropdown the pages view had.
    renderKnowledge()
    await screen.findByText('1 条笔记')

    const scope = screen.getByRole('combobox')
    expect(scope).toBeTruthy()
    expect((scope as HTMLSelectElement).value).toBe('0')
  })

  it('keeps the project scope in both views and writes it into the URL', async () => {
    renderKnowledge()
    await screen.findByText('1 条笔记')

    fireEvent.change(screen.getByRole('combobox'), { target: { value: '7' } })
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('project=7'), { timeout: 2000 })

    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(7, 100), { timeout: 2000 })
    // The scope survives the view switch — it is the skeleton's.
    expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe('7')
  })

  it('carries the shared query into the pages view', async () => {
    renderKnowledge()
    await screen.findByText('1 条笔记')

    fireEvent.change(screen.getByRole('searchbox'), { target: { value: '网关' } })
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('q='), { timeout: 2000 })

    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(mockSearchWiki).toHaveBeenCalledWith(0, '网关', 50), { timeout: 2000 })
  })
})
