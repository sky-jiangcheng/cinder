// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import NoteDelegate from './NoteDelegate'
import { generateWeeklyReport, type NoteOrganizeResult } from '../../api/client'

vi.mock('../../api/client', () => ({
  generateWeeklyReport: vi.fn(),
  findDuplicateNotes: vi.fn(),
  generateNoteInsights: vi.fn(),
}))

const DICT: Record<string, string> = {
  'project.aiDelegate': 'AI delegate',
  'project.aiSummaryAction': 'Summarize {{done}}/{{total}}',
  'project.aiGenerating': 'Generating…',
  'project.aiDuplicateAction': 'Find duplicates',
  'project.aiScanning': 'Scanning…',
  'project.aiDuplicateTitle': 'Possible duplicates',
  'project.aiDuplicateCount': '{{count}} pairs',
  'project.aiDuplicateEmpty': 'No obvious duplicates found',
  'project.weeklyAction': 'Weekly report',
  'project.weeklyDrafting': 'Drafting…',
  'project.weeklyThisWeek': 'This week',
  'project.weeklyLastWeek': 'Last week',
  'project.weeklyDone': 'Draft is in the composer',
  'project.weeklyFailed': 'Weekly report failed',
  'project.weeklyHint': 'Draft a weekly report',
  'common.listSeparator': ', ',
}
const mockT = (key: string, opts?: Record<string, unknown> & { defaultValue?: string }) => {
  const template = DICT[key] ?? opts?.defaultValue ?? key
  return template.replace(/\{\{(\w+)\}\}/g, (_m, k) => String(opts?.[k] ?? ''))
}
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mockT }) }))

const weekly = vi.mocked(generateWeeklyReport)

function renderStrip(onWeekly?: (p: NoteOrganizeResult) => void) {
  return render(
    <MemoryRouter>
      <NoteDelegate projectId={7} total={3} summarized={0} onGenerated={() => {}} onWeekly={onWeekly} />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  weekly.mockReset()
})

// Without a composer to land in, the action must not exist: a button whose
// only outcome is a discarded draft is worse than no button.
describe('the weekly action needs somewhere to land', () => {
  it('is absent when no onWeekly handler is given', () => {
    renderStrip()
    expect(screen.queryByText('Weekly report')).toBeNull()
  })
})

describe('the weekly action', () => {
  it('asks which week before spending a model call', () => {
    renderStrip(() => {})
    fireEvent.click(screen.getByText('Weekly report'))
    expect(screen.getByText('This week')).toBeTruthy()
    expect(screen.getByText('Last week')).toBeTruthy()
    // Nothing has been requested yet: choosing the week IS the request.
    expect(weekly).not.toHaveBeenCalled()
  })

  it('hands last week\'s proposal to the caller and says where it went', async () => {
    weekly.mockResolvedValue({
      note_id: 0,
      title: 'x 周报 · 11-16 ~ 11-22',
      content: '## 本周做了什么',
      tags: '周报',
    })
    const onWeekly = vi.fn()
    renderStrip(onWeekly)

    fireEvent.click(screen.getByText('Weekly report'))
    fireEvent.click(screen.getByText('Last week'))

    await waitFor(() => expect(onWeekly).toHaveBeenCalledTimes(1))
    expect(weekly).toHaveBeenCalledWith(7, -1)
    expect(onWeekly.mock.calls[0][0].title).toBe('x 周报 · 11-16 ~ 11-22')
    expect(await screen.findByText('Draft is in the composer')).toBeTruthy()
  })

  it('reports a failure instead of swallowing it', async () => {
    weekly.mockRejectedValue(new Error('boom'))
    const onWeekly = vi.fn()
    renderStrip(onWeekly)

    fireEvent.click(screen.getByText('Weekly report'))
    fireEvent.click(screen.getByText('This week'))

    expect(await screen.findByText(/Weekly report failed/)).toBeTruthy()
    expect(onWeekly).not.toHaveBeenCalled()
  })
})
