// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'

// The API surface NoteSection touches. This file covers the topic layer's
// three consistency rules, all of which broke in the browser before they
// broke here:
//
//   1. the editor's filing selector is a CONTROLLED select — a write that
//      does not reach the edit draft snaps the selector back to the old
//      value, so the UI denies a change the server already accepted;
//   2. renaming a topic must rename it on the note cards too (the cards
//      carry a denormalized topic_name);
//   3. deleting a topic keeps the notes but drops their filing, so the
//      cards must lose the badge.
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

const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import NoteSection from './NoteSection'
import {
  listNotes, listTopics, setNoteTopic, updateTopic, deleteTopic,
  type Note, type NoteTopic,
} from '../api/client'

const mockListNotes = vi.mocked(listNotes)
const mockListTopics = vi.mocked(listTopics)
const mockSetNoteTopic = vi.mocked(setNoteTopic)
const mockUpdateTopic = vi.mocked(updateTopic)
const mockDeleteTopic = vi.mocked(deleteTopic)

const note: Note = {
  id: 345,
  project_id: 12,
  title: '网关重试与幂等',
  content: '最多重试三次，幂等键是 invoice id。',
  tags: 'gateway,retry',
  kind: 'knowledge',
  pinned: false,
  source: 'manual',
  topic_id: 0,
  sort_order: 0,
  created_at: '2026-10-20T00:00:00Z',
  updated_at: '2026-10-20T00:00:00Z',
}

const topic: NoteTopic = {
  id: 7,
  project_id: 12,
  name: '支付网关',
  description: '',
  sort_order: 0,
  note_count: 0,
  created_at: '2026-10-20T00:00:00Z',
  updated_at: '2026-10-20T00:00:00Z',
}

beforeEach(() => {
  mockListNotes.mockReset().mockResolvedValue([note])
  mockListTopics.mockReset().mockResolvedValue([topic])
  mockSetNoteTopic.mockReset().mockResolvedValue(undefined)
  mockUpdateTopic.mockReset().mockResolvedValue(undefined)
  mockDeleteTopic.mockReset().mockResolvedValue(undefined)
})

const flush = async () => { await waitFor(() => expect(mockListNotes).toHaveBeenCalled()) }

/** Opens the note's editor (the filing selector only exists there). */
const openEditor = async () => {
  fireEvent.click(await screen.findByText('project.edit'))
  return await screen.findByLabelText('project.topicLabel') as HTMLSelectElement
}

describe('NoteSection topic filing', () => {
  it('keeps the chosen topic in the editor after the write lands', async () => {
    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await flush()

    const select = await openEditor()
    expect(select.value).toBe('0')

    fireEvent.change(select, { target: { value: '7' } })
    await waitFor(() => expect(mockSetNoteTopic).toHaveBeenCalledWith(345, 7))

    // The selector is bound to the edit draft, not to the server's answer.
    // A write that does not reach the draft snaps the value back to "no
    // topic", which reads as "my filing did not save" even though it did.
    await waitFor(() => expect(select.value).toBe('7'))
  })

  it('renames the topic on the note cards as well as in the strip', async () => {
    mockListNotes.mockResolvedValue([{ ...note, topic_id: 7, topic_name: '支付网关' }])
    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await flush()

    expect(await screen.findByText('支付网关')).toBeTruthy()

    mockUpdateTopic.mockResolvedValue(undefined)
    fireEvent.click(screen.getByLabelText('project.topicRename'))
    const input = await screen.findByLabelText('project.topicRename')
    fireEvent.change(input, { target: { value: '支付网关重构' } })
    fireEvent.click(screen.getByText('common.save'))

    await waitFor(() => expect(mockUpdateTopic).toHaveBeenCalledWith(7, '支付网关重构'))
    // The cards carry a denormalized topic_name, so a rename that only
    // touched the topic list leaves the chip showing the old name next to a
    // filter bar showing the new one.
    expect(await screen.findByText('支付网关重构')).toBeTruthy()
    expect(screen.queryByText('支付网关')).toBeNull()
  })

  it('drops the filing from the note cards when the topic is deleted', async () => {
    mockListNotes.mockResolvedValue([{ ...note, topic_id: 7, topic_name: '支付网关' }])
    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    await flush()

    expect(await screen.findByText('支付网关')).toBeTruthy()

    fireEvent.click(screen.getByLabelText('project.topicDelete'))
    await waitFor(() => expect(mockDeleteTopic).toHaveBeenCalledWith(7))

    // The notes are kept, but they are unfiled now — the badge must go, or
    // the card claims a filing under a topic that no longer exists.
    await waitFor(() => expect(screen.queryByText('支付网关')).toBeNull())
    expect(screen.getByText('网关重试与幂等')).toBeTruthy()
  })
})
