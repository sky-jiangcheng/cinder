// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'

vi.mock('../api/client', () => ({
  listAllNotes: vi.fn(),
  listAllTags: vi.fn(),
  getProjects: vi.fn(),
  searchAll: vi.fn(),
  pinNote: vi.fn(),
  importClaudeMemory: vi.fn(),
  exportNoteAsMarkdown: vi.fn(),
}))

// A single stable `t` identity: the hook's fetchAll depends on [t], so a
// fresh function per render (what a naive mock produces) would retrigger the
// fetch effect forever. Real react-i18next memoizes t.
const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import { useKnowledgePage, invalidateKnowledgeCache } from './useKnowledgePage'
import { getProjects, listAllNotes, listAllTags, searchAll, type NoteWithProject, type Project } from '../api/client'

const mockListAllNotes = vi.mocked(listAllNotes)
const mockListAllTags = vi.mocked(listAllTags)
const mockGetProjects = vi.mocked(getProjects)
const mockSearchAll = vi.mocked(searchAll)

// The hook resolves through Promise microtasks; a nested act flush settles
// them without relying on timers.
const flush = async () => { await act(async () => { await Promise.resolve() }) }

const proj = (id: number, name: string): Project => ({
  id,
  name,
  root_path: '/tmp/' + name,
  level_override: 0,
  is_auto_grouped: true,
  is_starred: false,
  created_at: '2026-10-06T00:00:00Z',
  repo_count: 1,
  total_added: 0,
  total_deleted: 0,
  my_added: 0,
  my_deleted: 0,
  my_files: 0,
  my_added_7d: 0,
  is_workday: true,
  below_standard: false,
})

const note = (id: number, projectId: number, projectName: string): NoteWithProject => ({
  id,
  project_id: projectId,
  title: 'n' + id,
  content: '',
  tags: '',
  kind: 'knowledge',
  pinned: false,
  source: '',
  topic_id: 0,
  sort_order: 0,
  created_at: '2026-10-06T00:00:00Z',
  updated_at: '2026-10-06T00:00:00Z',
  project_name: projectName,
  root_path: '/tmp/' + projectName,
})

beforeEach(() => {
  invalidateKnowledgeCache()
  mockListAllNotes.mockReset().mockResolvedValue([])
  mockListAllTags.mockReset().mockResolvedValue([])
  mockGetProjects.mockReset().mockResolvedValue([])
  mockSearchAll.mockReset().mockResolvedValue([])
})

describe('useKnowledgePage', () => {
  it('derives projectNames from the scanned project list on a cold start (no notes)', async () => {
    // Regression: projectNames used to be derived from notes, so an empty
    // knowledge base yielded an empty list and Quick Note deadlocked on
    // "No projects found" even though projects existed.
    mockGetProjects.mockResolvedValue([
      proj(2, '.openclaw-autoclaw'),
      proj(3, '.cargo'),
      proj(5, 'jiangcheng'),
    ])
    const { result } = renderHook(() => useKnowledgePage())
    await flush()

    expect(result.current.projectNames).toEqual([
      ['.cargo', 3],
      ['.openclaw-autoclaw', 2],
      ['jiangcheng', 5],
    ])
    expect(result.current.loading).toBe(false)
    expect(result.current.error).toBe('')
  })

  it('keeps the no-projects guard accurate when nothing was scanned', async () => {
    const { result } = renderHook(() => useKnowledgePage())
    await flush()

    expect(result.current.projectNames).toEqual([])
  })

  it('loads notes, tags and projects together and keeps them in sync on fetchAll', async () => {
    mockListAllNotes.mockResolvedValue([note(1, 3, '.cargo')])
    mockListAllTags.mockResolvedValue(['arch'])
    mockGetProjects.mockResolvedValue([proj(3, '.cargo')])
    const { result } = renderHook(() => useKnowledgePage())
    await flush()

    expect(result.current.notes).toHaveLength(1)
    expect(result.current.tags).toEqual(['arch'])
    expect(result.current.projectNames).toEqual([['.cargo', 3]])

    // fetchAll runs again after an import; the projects list refreshes too.
    mockGetProjects.mockResolvedValue([proj(3, '.cargo'), proj(4, 'new-repo')])
    await act(async () => { result.current.fetchAll() })
    await flush()

    expect(result.current.projectNames).toEqual([['.cargo', 3], ['new-repo', 4]])
  })

  it('surfaces a fetch error instead of pretending the library is empty', async () => {
    mockListAllNotes.mockRejectedValue(new Error('backend down'))
    const { result } = renderHook(() => useKnowledgePage())
    await flush()

    expect(result.current.error).toBe('backend down')
    expect(result.current.loading).toBe(false)
  })

  it('narrows the notes to the project scope it is given', async () => {
    // The notes view is cross-project by nature; the scope is what turns it
    // into "this project's notes" (ADR-0038 decision 4).
    mockListAllNotes.mockResolvedValue([note(1, 3, '.cargo'), note(2, 7, 'repotory')])
    const { result } = renderHook(() => useKnowledgePage({ scopeProjectId: 7 }))
    await flush()

    expect(result.current.notes).toHaveLength(2)
    expect(result.current.filtered.map(n => n.id)).toEqual([2])
  })

  it('keeps every note when no scope is picked', async () => {
    mockListAllNotes.mockResolvedValue([note(1, 3, '.cargo'), note(2, 7, 'repotory')])
    const { result } = renderHook(() => useKnowledgePage())
    await flush()

    expect(result.current.filtered.map(n => n.id)).toEqual([1, 2])
  })

  it('uses the project list it is handed instead of fetching a second one', async () => {
    // The skeleton owns the project list (the scope dropdown lives there) —
    // two copies of the same list could disagree.
    mockGetProjects.mockResolvedValue([proj(9, 'fetched-anyway')])
    const { result } = renderHook(() => useKnowledgePage({ projects: [proj(3, '.cargo')] }))
    await flush()

    expect(mockGetProjects).not.toHaveBeenCalled()
    expect(result.current.projectNames).toEqual([['.cargo', 3]])
  })

  it('searches when the query is set', async () => {
    const { result } = renderHook(() => useKnowledgePage())
    await flush()

    act(() => { result.current.setQuery('网关') })
    // The search is debounced by 300ms: one request per pause in typing.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)) })

    expect(mockSearchAll).toHaveBeenCalledWith('网关')
    expect(result.current.hits).toEqual([])
  })
})
