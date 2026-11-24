// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'

// The version panel loads its data through a dynamic import of api/client, so
// the mock has to carry the three version calls as well as the note list.
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
  listNoteVersions: vi.fn(),
  restoreNoteVersion: vi.fn(),
  diffNoteVersions: vi.fn(),
}))

const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import NoteSection from './NoteSection'
import {
  listNotes, listNoteVersions, listTopics, restoreNoteVersion, type Note, type NoteVersion,
} from '../api/client'

const mockListNotes = vi.mocked(listNotes)
const mockListVersions = vi.mocked(listNoteVersions)
const mockListTopics = vi.mocked(listTopics)
const mockRestore = vi.mocked(restoreNoteVersion)

const note: Note = {
  id: 345,
  project_id: 12,
  title: '网关重试与幂等',
  content: '当前正文：第二版。',
  tags: 'gateway,retry',
  kind: 'knowledge',
  pinned: false,
  source: 'manual',
  topic_id: 0,
  sort_order: 0,
  created_at: '2026-10-20T00:00:00Z',
  updated_at: '2026-10-20T00:00:00Z',
}

const version: NoteVersion = {
  id: 7,
  note_id: 345,
  title: '网关重试与幂等',
  content: '旧正文：第一版。',
  tags: 'gateway,retry',
  kind: 'knowledge',
  created_at: '2026-10-19T00:00:00Z',
}

beforeEach(() => {
  // jsdom has no layout engine, so scrollIntoView is missing. The panel calls
  // it on open to bring itself into view — a no-op is enough for the test.
  Element.prototype.scrollIntoView = vi.fn()
  mockListNotes.mockReset().mockResolvedValue([note])
  mockListVersions.mockReset().mockResolvedValue([version])
  mockListTopics.mockReset().mockResolvedValue([])
  mockRestore.mockReset().mockResolvedValue(undefined)
})

describe('NoteSection 版本恢复', () => {
  it('恢复成功后重新拉取笔记列表，卡片显示恢复回来的正文', async () => {
    // The restore writes the old content back into the note behind the list's
    // back. Without a refetch the card keeps rendering the content that was
    // just replaced, and the restore reads as a no-op.
    mockRestore.mockImplementation(async () => {
      mockListNotes.mockResolvedValue([{ ...note, content: version.content }])
    })

    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await waitFor(() => expect(mockListNotes).toHaveBeenCalledTimes(1))

    fireEvent.click(screen.getByTitle('project.versionHistory'))
    await waitFor(() => expect(mockListVersions).toHaveBeenCalledWith(345))

    fireEvent.click(screen.getByTitle('恢复到此版本'))
    await waitFor(() => expect(mockRestore).toHaveBeenCalledWith(345, 7))

    // The list is refetched…
    await waitFor(() => expect(mockListNotes).toHaveBeenCalledTimes(2))
    // …and the card shows the restored content, not the one it replaced.
    expect(await screen.findByText(version.content)).toBeTruthy()
  })
})
