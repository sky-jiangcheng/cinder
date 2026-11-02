// @vitest-environment jsdom
// Knowledge.test.tsx — the knowledge page after ADR-0038 decision 4 and
// ADR-0039 decision 3.
//
// Two levels of controls live on this page, and they must not blur into one
// another:
//
//   - the OUTER segmented control ("笔记 / 知识页") — one object, two faces
//     (ADR-0038 decision 4). It stays a segmented control, a pill.
//   - the INNER tab strip ("全部笔记 / 待整理") — two places inside the notes
//     drawer (ADR-0039 decision 3). It is an underlined tab.
//
// What this pins:
//
//   - the ADR-0038 behaviour that still holds: one skeleton, one search
//     field, one project scope, the view switch writes ?view= and the back
//     button can walk it;
//   - the inner tabs: both are there, the default is the note list, the
//     organize tab only takes up room when there is a backlog, its badge is
//     the backlog size, and the URL can address it (?sub=organize);
//   - the "待整理 N" jump button is GONE. It existed only to point at
//     something that sat at the bottom of a ~10,000px page; with the tab in
//     place the patch has no reason to exist, and its return would be a
//     regression.

import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import { useLocation, useNavigate } from 'react-router-dom'
import { renderReady } from '../test/renderReady'

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

// Same stable-t convention as Review.test.tsx / Home.test.tsx: the mock
// returns the defaultValue verbatim, so every assertion reads the string the
// component asked for. The tab labels keep their Chinese wording because
// they carry one.
const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import {
  listAllNotes, listAllTags, searchAll, getProjects,
  browseWikiPages, searchWikiPages, listOrganizeGroups,
  type NoteWithProject, type Project, type WikiPageSummary, type OrganizeGroup,
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

/** A landing bucket still holding notes — the backlog the tab counts. */
const group = (over: Partial<OrganizeGroup> = {}): OrganizeGroup => ({
  project_id: 9,
  name: 'ghost',
  root_path: '/code/ghost',
  kind: 'import_placeholder',
  note_count: 14,
  repo_bound: true,
  ...over,
})

// The URL is part of the contract (?view= / ?sub= / ?q= / ?project= are the
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

async function renderKnowledge(initial = '/knowledge') {
  return renderReady(
    <>
      <LocationProbe />
      <Knowledge />
    </>,
    { initialEntries: [initial] },
  )
}

/** The segmented control's two buttons, found by the view they carry. */
function viewButton(view: 'notes' | 'pages'): HTMLElement {
  const btn = screen.getAllByRole('button').find(b => b.getAttribute('data-view') === view)
  if (!btn) throw new Error(`no view button for ${view}`)
  return btn
}

/** The note grid — the card grid, not the "recent notes" list above it. */
function noteGrid(container: HTMLElement): Element | null {
  return container.querySelector('[class*="noteGrid"]')
}

beforeEach(() => {
  vi.clearAllMocks()
  mockNotes.mockResolvedValue([note()])
  mockTags.mockResolvedValue([])
  mockSearchAll.mockResolvedValue([])
  mockProjects.mockResolvedValue([project()])
  mockBrowse.mockResolvedValue([page()])
  mockSearchWiki.mockResolvedValue([])
  // No backlog by default: the organize tab must not take up room when
  // there is nothing to file.
  mockGroups.mockResolvedValue([])
})

describe('the knowledge page (ADR-0038 decision 4)', () => {
  it('opens on the notes view', async () => {
    await renderKnowledge()

    expect(viewButton('notes').getAttribute('aria-pressed')).toBe('true')
    expect(viewButton('pages').getAttribute('aria-pressed')).toBe('false')
    // The notes body is up, and it reported its count to the shared count
    // bar — one count bar for the tab, whatever the body underneath is.
    expect(await screen.findByText('knowledge.resultCount')).toBeTruthy()
  })

  it('deep-links into the pages view with ?view=pages', async () => {
    await renderKnowledge('/knowledge?view=pages')

    expect(viewButton('pages').getAttribute('aria-pressed')).toBe('true')
    expect(await screen.findByText('wiki.resultCount')).toBeTruthy()
    expect(mockBrowse).toHaveBeenCalledWith(0, 100)
  })

  it('switches the view and writes it into the URL', async () => {
    await renderKnowledge()
    await screen.findByText('knowledge.resultCount')

    fireEvent.click(viewButton('pages'))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('view=pages'), { timeout: 2000 })
    expect(viewButton('pages').getAttribute('aria-pressed')).toBe('true')
    expect(mockBrowse).toHaveBeenCalledWith(0, 100)
  })

  it('keeps the back button able to walk the view switch', async () => {
    await renderKnowledge()
    await screen.findByText('knowledge.resultCount')

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
    await renderKnowledge()
    await screen.findByText('knowledge.resultCount')
    expect(screen.getByRole('searchbox')).toBeTruthy()

    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalled(), { timeout: 2000 })

    // The field is still there — it is the skeleton, not the body.
    expect(screen.getByRole('searchbox')).toBeTruthy()
  })

  it('gives the notes view the project scope it never had', async () => {
    // The whole point of the merge: the notes view is cross-project, so it
    // needs the same scope dropdown the pages view had.
    await renderKnowledge()
    await screen.findByText('knowledge.resultCount')

    const scope = screen.getByRole('combobox')
    expect(scope).toBeTruthy()
    expect((scope as HTMLSelectElement).value).toBe('0')
  })

  it('keeps the project scope in both views and writes it into the URL', async () => {
    await renderKnowledge()
    await screen.findByText('knowledge.resultCount')

    fireEvent.change(screen.getByRole('combobox'), { target: { value: '7' } })
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('project=7'), { timeout: 2000 })

    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(7, 100), { timeout: 2000 })
    // The scope survives the view switch — it is the skeleton's.
    expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe('7')
  })

  it('carries the shared query into the pages view', async () => {
    await renderKnowledge()
    await screen.findByText('knowledge.resultCount')

    fireEvent.change(screen.getByRole('searchbox'), { target: { value: '网关' } })
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('q='), { timeout: 2000 })

    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(mockSearchWiki).toHaveBeenCalledWith(0, '网关', 50), { timeout: 2000 })
  })
})

describe("the notes drawer's inner tabs (ADR-0039 decision 3)", () => {
  it('gives the notes drawer two tabs: all notes and to file', async () => {
    mockGroups.mockResolvedValue([group()])
    await renderKnowledge()

    const allNotes = await screen.findByRole('tab', { name: /全部笔记/ })
    const organize = screen.getByRole('tab', { name: /待整理/ })
    expect(allNotes.getAttribute('aria-selected')).toBe('true')
    expect(organize.getAttribute('aria-selected')).toBe('false')
  })

  it('opens on all notes, with the note grid rendered', async () => {
    const { container } = await renderKnowledge()

    expect((await screen.findByRole('tab', { name: /全部笔记/ })).getAttribute('aria-selected')).toBe('true')
    expect(noteGrid(container)).not.toBeNull()
    // The workbench is behind its tab, not on the screen: an inactive panel
    // is not exposed to the accessibility tree.
    expect(screen.queryByRole('tabpanel', { name: /待整理/ })).toBeNull()
  })

  it('switches to the organize workbench and leaves the note grid behind', async () => {
    mockGroups.mockResolvedValue([group()])
    const { container } = await renderKnowledge()
    await screen.findByRole('tab', { name: /待整理/ })

    fireEvent.click(screen.getByRole('tab', { name: /待整理/ }))

    // The workbench is up ...
    expect(await screen.findByRole('tabpanel', { name: /待整理/ })).toBeTruthy()
    // ... and the note list is not on the screen any more.
    expect(screen.queryByRole('tabpanel', { name: /全部笔记/ })).toBeNull()
    expect(noteGrid(container)).toBeNull()
  })

  it('does not offer the organize tab when there is nothing to file', async () => {
    mockGroups.mockResolvedValue([])
    await renderKnowledge()

    await screen.findByRole('tab', { name: /全部笔记/ })
    expect(screen.queryByRole('tab', { name: /待整理/ })).toBeNull()
  })

  it('badges the organize tab with the number of notes waiting', async () => {
    mockGroups.mockResolvedValue([group({ note_count: 14 })])
    await renderKnowledge()

    const organize = await screen.findByRole('tab', { name: /待整理/ })
    expect(within(organize).getByText('14')).toBeTruthy()
  })

  it('has no "待整理 N" jump button any more', async () => {
    // The patch that pointed at the bottom of a 10,000px page is gone: the
    // tab is the way in now. A real button named 待整理 would mean it came
    // back.
    mockGroups.mockResolvedValue([group({ note_count: 14 })])
    await renderKnowledge()

    await screen.findByRole('tab', { name: /待整理/ })
    expect(screen.queryByRole('button', { name: /待整理/ })).toBeNull()
  })

  it('deep-links into the organize workbench with ?sub=organize', async () => {
    mockGroups.mockResolvedValue([group({ note_count: 14 })])
    await renderKnowledge('/knowledge?view=notes&sub=organize')

    const organize = await screen.findByRole('tab', { name: /待整理/ })
    expect(organize.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByRole('tabpanel', { name: /待整理/ })).toBeTruthy()
    expect(screen.queryByRole('tabpanel', { name: /全部笔记/ })).toBeNull()
  })

  it('writes the inner tab into the URL', async () => {
    mockGroups.mockResolvedValue([group({ note_count: 14 })])
    await renderKnowledge()
    await screen.findByRole('tab', { name: /待整理/ })

    fireEvent.click(screen.getByRole('tab', { name: /待整理/ }))

    await waitFor(
      () => expect(screen.getByTestId('location').textContent).toContain('view=notes&sub=organize'),
      { timeout: 2000 },
    )
  })

  it('keeps the back button able to walk the inner tab switch', async () => {
    mockGroups.mockResolvedValue([group({ note_count: 14 })])
    await renderKnowledge()
    await screen.findByRole('tab', { name: /待整理/ })

    fireEvent.click(screen.getByRole('tab', { name: /待整理/ }))
    await waitFor(
      () => expect(screen.getByTestId('location').textContent).toContain('sub=organize'),
      { timeout: 2000 },
    )

    // The inner switch is a navigation step (PUSH), not a keystroke, so the
    // back button walks it like it walks the outer view switch.
    fireEvent.click(screen.getByTestId('back'))
    await waitFor(
      () => expect(screen.getByTestId('location').textContent).not.toContain('sub=organize'),
      { timeout: 2000 },
    )
    expect(screen.getByRole('tab', { name: /全部笔记/ }).getAttribute('aria-selected')).toBe('true')
  })

  it('clears the inner tab when the outer view switches', async () => {
    mockGroups.mockResolvedValue([group({ note_count: 14 })])
    await renderKnowledge()
    await screen.findByRole('tab', { name: /待整理/ })

    fireEvent.click(screen.getByRole('tab', { name: /待整理/ }))
    await waitFor(
      () => expect(screen.getByTestId('location').textContent).toContain('sub=organize'),
      { timeout: 2000 },
    )

    // Out to the pages drawer: the workbench is not a page of the knowledge
    // base, so the inner tab goes with it.
    fireEvent.click(viewButton('pages'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toContain('view=pages'), { timeout: 2000 })
    expect(screen.getByTestId('location').textContent).not.toContain('sub=')

    // And back: the notes drawer must not still be standing on the
    // workbench the user left two steps ago.
    fireEvent.click(viewButton('notes'))
    await waitFor(() => expect(viewButton('notes').getAttribute('aria-pressed')).toBe('true'))
    expect(screen.getByTestId('location').textContent).not.toContain('sub=')
    expect(screen.getByRole('tab', { name: /全部笔记/ }).getAttribute('aria-selected')).toBe('true')
  })
})
