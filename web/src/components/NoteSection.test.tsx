// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'

// The API surface NoteSection touches. organizeNote is the one under test:
// a PROPOSAL that must land in the editor, and only the user's save may
// persist it.
vi.mock('../api/client', () => ({
  listNotes: vi.fn(),
  createNoteWithMeta: vi.fn(),
  updateNoteFull: vi.fn(),
  deleteNote: vi.fn(),
  pinNote: vi.fn(),
  moveNote: vi.fn(),
  organizeNote: vi.fn(),
  getProjects: vi.fn(),
  // The topic layer (schema v27): the section loads the project's topics and
  // files notes under them.
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
  listNotes, organizeNote, updateNoteFull, listTopics,
  type Note,
} from '../api/client'

const mockListNotes = vi.mocked(listNotes)
const mockOrganize = vi.mocked(organizeNote)
const mockUpdate = vi.mocked(updateNoteFull)
const mockListTopics = vi.mocked(listTopics)

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
  // The optimistic-lock counter the editor must post back on save (ADR-0057).
  version: 7,
  sort_order: 0,
  created_at: '2026-10-20T00:00:00Z',
  updated_at: '2026-10-20T00:00:00Z',
}

beforeEach(() => {
  mockListNotes.mockReset().mockResolvedValue([note])
  mockOrganize.mockReset()
  mockUpdate.mockReset().mockResolvedValue(undefined)
  mockListTopics.mockReset().mockResolvedValue([])
})

const flush = async () => { await waitFor(() => expect(mockListNotes).toHaveBeenCalled()) }

describe('NoteSection AI tidy', () => {
  it('opens the editor with the AI proposal and saves only on the user’s click', async () => {
    mockOrganize.mockResolvedValue({
      note_id: 345,
      title: '支付网关：重试与幂等',
      content: '# 支付网关\n\n## 目录\n\n- 重试策略\n\n## 重试策略\n\n最多重试三次。',
      tags: 'gateway,retry,idempotency',
    })
    render(<NoteSection projectId={12} />)
    await flush()

    fireEvent.click(screen.getByTitle('project.organizeHint'))
    await waitFor(() => expect(mockOrganize).toHaveBeenCalledWith(345))

    // The proposal is in the editor: title and body are the model's, the
    // review banner says so, and nothing has been saved yet.
    expect(await screen.findByDisplayValue('支付网关：重试与幂等')).toBeTruthy()
    expect(screen.getByText('project.organizeProposal')).toBeTruthy()
    const textarea = screen.getByPlaceholderText('project.contentPlaceholder') as HTMLTextAreaElement
    expect(textarea.value).toContain('## 目录')
    expect(mockUpdate).not.toHaveBeenCalled()

    // Save is the user's act: the proposal content goes through the normal
    // full-update path, with kind/pinned carried from the stored note.
    fireEvent.click(screen.getByText('project.save'))
    await waitFor(() =>
      expect(mockUpdate).toHaveBeenCalledWith(345, expect.stringContaining('## 目录'), '支付网关：重试与幂等', 'gateway,retry,idempotency', 'knowledge', false, 7),
    )
  })

  it('keeps the stored note when the tidy call fails', async () => {
    mockOrganize.mockRejectedValue(new Error('AI chat is not configured — set the endpoint and model in Settings → AI'))
    render(<NoteSection projectId={12} />)
    await flush()

    fireEvent.click(screen.getByTitle('project.organizeHint'))
    await waitFor(() => expect(mockOrganize).toHaveBeenCalled())

    // A failed tidy must not open the editor (no half-state) and must say
    // why — a silent no-op teaches users the button is broken.
    expect(await screen.findByText(/project.organizeFailed/)).toBeTruthy()
    expect(screen.queryByDisplayValue('支付网关：重试与幂等')).toBeNull()
    expect(mockUpdate).not.toHaveBeenCalled()
  })

  it('surfaces a proposal warning (truncated reply) inside the editor', async () => {
    mockOrganize.mockResolvedValue({
      note_id: 345,
      title: 't',
      content: '半截正文……',
      tags: '',
      warning: '（回复被输出上限截断）',
    })
    render(<NoteSection projectId={12} />)
    await flush()

    fireEvent.click(screen.getByTitle('project.organizeHint'))
    expect(await screen.findByText('（回复被输出上限截断）')).toBeTruthy()
    expect(mockUpdate).not.toHaveBeenCalled()
  })
})
