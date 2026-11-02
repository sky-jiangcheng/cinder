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

import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { screen, waitFor, render, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import i18n from '../i18n' // real i18next instance — otherwise t() returns raw keys

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    browseWikiPages: vi.fn(),
    listPendingWikiPages: vi.fn(),
  }
})

import {
  browseWikiPages,
  listPendingWikiPages,
  type WikiPageSummary,
  type WikiPendingPage,
} from '../api/client'
import ContextRail from './ContextRail'

const mockBrowse = vi.mocked(browseWikiPages)
const mockPending = vi.mocked(listPendingWikiPages)

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

function renderRail(collapsed = false) {
  return render(
    <MemoryRouter>
      <ContextRail collapsed={collapsed} onToggle={() => {}} />
    </MemoryRouter>,
  )
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
  mockBrowse.mockResolvedValue([])
  mockPending.mockResolvedValue([])
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
    mockPending.mockResolvedValue([pendingPage()])
    mockBrowse.mockResolvedValue([pageSummary()])
    renderRail()
    await waitFor(() => expect(mockBrowse).toHaveBeenCalled())

    expect(screen.getByText('待审核')).toBeTruthy()
    expect(await screen.findByText('待审核页 A')).toBeTruthy()
    expect(screen.getByText('最近更新')).toBeTruthy()
    expect(screen.getByText('网关设计')).toBeTruthy()
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
