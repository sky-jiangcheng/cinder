// @vitest-environment jsdom
// NoteDetail.test.tsx — /note/:id (ADR-0078).
//
// What this pins, and why each piece is load-bearing:
//
//   - the page is addressed by the NOTE id alone. Before this route every link
//     to a note was the pair /project/<pid>?tab=notes&noteId=<n>, which carried
//     the project the note lived in at link time and broke as soon as MoveNote
//     filed the note elsewhere;
//   - "open in project" is still offered — the detail page is a reading
//     surface, not a replacement for managing notes in bulk;
//   - "back" follows `state.from` when the list that led here passed its own
//     path, and falls back to the notes view of the knowledge tab for a direct
//     deep link (a shared URL has no history to return to);
//   - a deleted note renders the not-found banner instead of an empty page.

import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import i18n from '../../i18n' // real i18next instance — t() must return copy, not keys
import NoteDetail from './NoteDetail'

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    getNote: vi.fn(),
    getProjects: vi.fn(),
    listTopics: vi.fn(),
    updateNoteFull: vi.fn(),
    deleteNote: vi.fn(),
    pinNote: vi.fn(),
    moveNote: vi.fn(),
    setNoteTopic: vi.fn(),
  }
})

import { getNote, getProjects, listTopics } from '../../api/client'

const NOTE = {
  id: 42,
  project_id: 25,
  title: 'Session Handoff 2026-11-24',
  content: '决定：交接记录写进 Cinder，不再写进仓库的 TODO。',
  tags: 'handoff,auto-captured',
  kind: 'knowledge',
  pinned: false,
  source: 'manual',
  topic_id: 0,
  sort_order: 0,
  version: 3,
  created_at: '2026-11-24 15:41:50',
  updated_at: '2026-11-24 15:41:50',
}

function renderAt(path: string, state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: path, state }]}>
      <Routes>
        <Route path="/note/:id" element={<NoteDetail />} />
        <Route path="/project/:id" element={<div>project page</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
  vi.mocked(getProjects).mockResolvedValue([{ id: 25, name: 'CBiPay' }] as never)
  vi.mocked(listTopics).mockResolvedValue([])
})

describe('NoteDetail', () => {
  it('renders one note addressed by its own id', async () => {
    vi.mocked(getNote).mockResolvedValue(NOTE as never)
    renderAt('/note/42')

    expect(await screen.findByRole('heading', { name: 'Session Handoff 2026-11-24' })).toBeTruthy()
    expect(vi.mocked(getNote)).toHaveBeenCalledWith(42)
    expect(document.body.textContent).toContain('决定：交接记录写进 Cinder')
    // The project is a label you can follow, not part of the address.
    expect(screen.getByRole('link', { name: 'CBiPay' }).getAttribute('href'))
      .toBe('/project/25?tab=notes')
    // A leaked key would render as "noteDetail.backToNotes".
    expect(document.body.textContent).not.toMatch(/noteDetail\./)
  })

  it('keeps a way back into the project, where notes are managed in bulk', async () => {
    vi.mocked(getNote).mockResolvedValue(NOTE as never)
    renderAt('/note/42')

    const open = await screen.findByRole('link', { name: '在项目中打开' })
    expect(open.getAttribute('href')).toBe('/project/25?tab=notes&noteId=42')
  })

  it('returns to the list that led here when one passed its own path', async () => {
    vi.mocked(getNote).mockResolvedValue(NOTE as never)
    renderAt('/note/42', { from: '/dashboard' })

    const back = await screen.findByRole('link', { name: '返回笔记列表' })
    expect(back.getAttribute('href')).toBe('/dashboard')
  })

  it('falls back to the notes view for a direct deep link', async () => {
    vi.mocked(getNote).mockResolvedValue(NOTE as never)
    renderAt('/note/42')

    const back = await screen.findByRole('link', { name: '返回笔记列表' })
    expect(back.getAttribute('href')).toBe('/knowledge?view=notes')
  })

  it('says the note is gone instead of rendering an empty page', async () => {
    vi.mocked(getNote).mockRejectedValue(new Error('note 42 not found'))
    renderAt('/note/42')

    expect(await screen.findByText(/笔记不存在（可能已被删除）/)).toBeTruthy()
  })
})
