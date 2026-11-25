// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { render, screen, waitFor } from '@testing-library/react'

vi.mock('../api/client', () => ({
  listNotes: vi.fn(),
  createNoteWithMeta: vi.fn(),
  updateNoteFull: vi.fn(),
  deleteNote: vi.fn(),
  pinNote: vi.fn(),
  moveNote: vi.fn(),
  organizeNote: vi.fn(),
  getProjects: vi.fn(),
  listTopics: vi.fn(),
  createTopic: vi.fn(),
  updateTopic: vi.fn(),
  deleteTopic: vi.fn(),
  setNoteTopic: vi.fn(),
}))

// Preserve interpolation so byline facts (lines / size / vN) are asserted on
// their real rendered values, not the untranslated key.
const DICT: Record<string, string> = {
  'project.createdAtPrefix': 'Created ',
  'project.updatedAtPrefix': 'Updated ',
  'project.noteFacts': '{{lines}} lines · {{size}}',
  'project.noteVersion': 'v{{version}}',
}
const mockT = (key: string, opts?: Record<string, unknown> & { defaultValue?: string }) => {
  const template = DICT[key] ?? opts?.defaultValue ?? key
  return template.replace(/\{\{(\w+)\}\}/g, (_m, k) => String(opts?.[k] ?? ''))
}
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mockT }) }))

import NoteSection from './NoteSection'
import { listNotes, listTopics, type Note } from '../api/client'

const mockListNotes = vi.mocked(listNotes)
const mockListTopics = vi.mocked(listTopics)

const base: Note = {
  id: 42,
  project_id: 12,
  title: 'Byline',
  content: 'line one\nline two',
  tags: '',
  kind: 'knowledge',
  pinned: false,
  source: 'manual',
  topic_id: 0,
  sort_order: 0,
  created_at: '2026-11-20T01:02:00Z',
  updated_at: '2026-11-20T01:02:00Z',
}

beforeEach(() => {
  mockListNotes.mockReset()
  mockListTopics.mockReset().mockResolvedValue([])
})

describe('NoteSection card byline', () => {
  it('shows line count and byte size', async () => {
    mockListNotes.mockResolvedValue([{ ...base }])
    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await waitFor(() => expect(mockListNotes).toHaveBeenCalled())
    // "line one\nline two" = 2 lines, 17 chars.
    expect(await screen.findByText('2 lines · 17 B')).toBeTruthy()
  })

  it('shows both created and updated stamps when they differ', async () => {
    mockListNotes.mockResolvedValue([{
      ...base,
      updated_at: '2026-11-21T03:04:00Z',
    }])
    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await waitFor(() => expect(mockListNotes).toHaveBeenCalled())
    const time = (await screen.findAllByText(/Created /)).find(n => n.textContent?.includes('Updated '))
    expect(time?.textContent).toContain('Updated ')
  })

  it('hides the version badge on v1, shows it above v1', async () => {
    mockListNotes.mockResolvedValue([{ ...base }])
    const { unmount } = render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await waitFor(() => expect(mockListNotes).toHaveBeenCalled())
    expect(screen.queryByText('v1')).toBeNull()
    unmount()

    mockListNotes.mockResolvedValue([{ ...base, version: 7 }])
    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await waitFor(() => expect(mockListNotes).toHaveBeenCalled())
    expect(await screen.findByText('v7')).toBeTruthy()
  })
})
