// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'

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
  getNoteInsights: vi.fn(),
  generateNoteInsights: vi.fn(),
  findDuplicateNotes: vi.fn(),
}))

const DICT: Record<string, string> = {
  'project.createdAtPrefix': 'Created ',
  'project.aiDelegate': 'AI delegate',
  'project.aiSummaryAction': 'Summarize {{done}}/{{total}}',
  'project.aiSummaryDone': 'Generated {{count}} summaries',
  'project.aiGenerating': 'Generating…',
  'project.aiDuplicateAction': 'Find duplicates',
  'project.aiScanning': 'Scanning…',
  'project.aiDuplicateTitle': 'Possible duplicates',
  'project.aiDuplicateCount': '{{count}} pairs',
  'project.aiDuplicateEmpty': 'No obvious duplicates found',
  'project.aiSummaryStale': 'Edited since',
  'project.relatedLabel': 'Related',
  'project.relatedShared': '共同：{{terms}}',
  'common.listSeparator': '、',
}
const mockT = (key: string, opts?: Record<string, unknown> & { defaultValue?: string }) => {
  const template = DICT[key] ?? opts?.defaultValue ?? key
  return template.replace(/\{\{(\w+)\}\}/g, (_m, k) => String(opts?.[k] ?? ''))
}
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mockT }) }))

import NoteSection from './NoteSection'
import { invalidateCache } from '../hooks/useApiData'
import {
  findDuplicateNotes, generateNoteInsights, getNoteInsights, listNotes, listTopics,
  type Note,
} from '../api/client'

const mockListNotes = vi.mocked(listNotes)
const mockListTopics = vi.mocked(listTopics)
const mockInsights = vi.mocked(getNoteInsights)
const mockGenerate = vi.mocked(generateNoteInsights)
const mockDuplicates = vi.mocked(findDuplicateNotes)

const note = (id: number, title: string): Note => ({
  id,
  project_id: 12,
  title,
  content: `${title} 的正文内容，用来撑起卡片的预览区。`,
  tags: '',
  kind: 'knowledge',
  pinned: false,
  source: 'manual',
  topic_id: 0,
  sort_order: 0,
  created_at: '2026-11-20T01:02:00Z',
  updated_at: '2026-11-20T01:02:00Z',
})

beforeEach(() => {
  invalidateCache()
  mockListNotes.mockReset()
  mockListTopics.mockReset().mockResolvedValue([])
  mockInsights.mockReset().mockResolvedValue([])
  mockGenerate.mockReset()
  mockDuplicates.mockReset()
})

describe('NoteSection AI layer', () => {
  it('renders the cached summary and the related chips', async () => {
    mockListNotes.mockResolvedValue([note(42, '迁移复盘'), note(43, '列检测')])
    mockInsights.mockResolvedValue([
      {
        note_id: 42,
        summary: '迁移前先查 pragma table_info。',
        keywords: ['sqlite', '迁移'],
        cached: true,
        related: [{
          id: 43, project_id: 12, title: '列检测',
          score: 0.71, shared: ['迁移', 'sqlite'], same_project: true,
        }],
      },
    ])

    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)

    expect(await screen.findByText('迁移前先查 pragma table_info。')).toBeTruthy()
    // The title appears twice: as the second card's own title and as the
    // related chip on the first card. Only the chip carries the reason, so
    // finding it by that title is also the assertion that the reason is there.
    const chip = await screen.findByTitle('共同：迁移、sqlite')
    expect(chip.textContent).toContain('列检测')
  })

  it('flags a summary whose note was edited after it was written', async () => {
    mockListNotes.mockResolvedValue([note(42, '迁移复盘')])
    mockInsights.mockResolvedValue([{
      note_id: 42, summary: '旧摘要。', cached: true, stale: true,
    }])
    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    expect(await screen.findByText('Edited since')).toBeTruthy()
  })

  it('summarizes through the delegation strip and refreshes the cards', async () => {
    mockListNotes.mockResolvedValue([note(42, '迁移复盘'), note(43, '列检测')])
    mockGenerate.mockResolvedValue({
      insights: [{ note_id: 42, summary: '新摘要。', cached: true }],
      failed: 0,
    })

    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    // 0 of 2 notes have a summary, and the strip says so.
    const button = await screen.findByText('Summarize 0/2')
    fireEvent.click(button)

    await waitFor(() => expect(mockGenerate).toHaveBeenCalledWith(12, []))
    expect(await screen.findByText('Generated 1 summaries')).toBeTruthy()
    // A run that produced summaries re-reads the cache; a run that produced
    // none would not.
    await waitFor(() => expect(mockInsights.mock.calls.length).toBeGreaterThan(1))
  })

  it('lists duplicate pairs with their shared terms', async () => {
    mockListNotes.mockResolvedValue([note(42, '迁移复盘'), note(43, '迁移复盘（副本）')])
    mockDuplicates.mockResolvedValue([{
      a: { id: 42, project_id: 12, title: '迁移复盘' },
      b: { id: 43, project_id: 12, title: '迁移复盘（副本）' },
      score: 0.93,
      shared: ['迁移', 'sqlite'],
    }])

    render(<MemoryRouter><NoteSection projectId={12} /></MemoryRouter>)
    fireEvent.click(await screen.findByText('Find duplicates'))

    expect(await screen.findByText('Possible duplicates')).toBeTruthy()
    expect(screen.getByText('93%')).toBeTruthy()
    expect(screen.getAllByText('迁移复盘（副本）').length).toBeGreaterThan(0)
  })
})
