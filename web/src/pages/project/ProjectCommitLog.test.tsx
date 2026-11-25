// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    searchCommits: vi.fn(),
    getCommitDiff: vi.fn(),
    getCommitFile: vi.fn(),
  }
})

vi.mock('../../hooks/useToast', () => ({
  useToast: () => vi.fn(),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { defaultValue?: string; count?: number; page?: number; pages?: number }) => {
      if (key === 'project.commitCount') return `${opts?.count ?? 0} commits`
      if (key === 'project.pageIndicator') return `Page ${opts?.page ?? 1} / ${opts?.pages ?? 1}`
      return opts?.defaultValue ?? key
    },
  }),
}))

import { searchCommits, getCommitDiff, getCommitFile, type CommitDiff, type CommitPage, type RepoInfo } from '../../api/client'
import ProjectCommitLog from './ProjectCommitLog'

const commit = (hash: string, message: string) => ({
  hash, message, time: '2026-11-19 10:00:00', author: 'jiangcheng',
  repo: '/code/MiddlePlatform/midplatform-data-monitor', branch: 'main',
})

const repos: RepoInfo[] = [{
  id: 42,
  path: '/code/MiddlePlatform/midplatform-data-monitor',
  project_id: 137,
  last_scanned_at: '',
  stats: [],
  web_url: '',
}]

const patch: CommitDiff = {
  sha: 'abcdef123456',
  repo: repos[0].path,
  files: [{
    path: 'internal/service/a.go',
    status: 'modified',
    binary: false,
    added: 1,
    deleted: 1,
    hunks: [{
      header: '', old_start: 2, new_start: 2,
      lines: [
        { kind: 'context' as const, text: 'unchanged', old_no: 2, new_no: 2 },
        { kind: 'del' as const, text: 'old line', old_no: 3 },
        { kind: 'add' as const, text: 'new line', new_no: 3 },
      ],
    }],
  }],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(searchCommits).mockResolvedValue({
    items: [commit('abcdef123456', 'fix: explorer')],
    total: 60,
    offset: 0,
    limit: 20,
  })
  vi.mocked(getCommitDiff).mockResolvedValue(patch)
  vi.mocked(getCommitFile).mockImplementation(async (_repoId, _sha, _path, before) => ({
    path: 'internal/service/a.go',
    content: before ? 'old line\n' : 'new line\n',
    exists: true,
  }))
})

describe('ProjectCommitLog', () => {
  it('asks for a page instead of a hard-coded 50-commit list', async () => {
    render(<ProjectCommitLog projectId={137} repos={repos} />)
    await waitFor(() => expect(searchCommits).toHaveBeenCalledWith(137, 42, '', 0, 20))
    await screen.findByText('fix: explorer')

    fireEvent.click(screen.getByText('project.nextPage'))
    await waitFor(() => expect(searchCommits).toHaveBeenCalledWith(137, 42, '', 20, 20))
    expect(screen.getByText('Page 2 / 3')).toBeTruthy()
  })

  it('paints the timeline shape instead of a blank loading string', async () => {
    let resolve!: (page: CommitPage) => void
    vi.mocked(searchCommits).mockImplementation(() => new Promise(r => { resolve = r }))
    render(<ProjectCommitLog projectId={137} repos={repos} />)
    // The skeleton is aria-hidden on purpose (placeholders are not content);
    // the test id is the seam that lets the test see it without DOM digging.
    expect(screen.getByTestId('commit-skeleton')).toBeTruthy()
    resolve({ items: [commit('aaa111', 'after resolve')], total: 1, offset: 0, limit: 20 })
    expect(await screen.findByText('after resolve')).toBeTruthy()
  })

  it('keeps the current page on screen while the next page loads', async () => {
    // offset 0 answers immediately; offset 20 stays pending until we release it.
    let resolveNext!: (page: CommitPage) => void
    const nextPage = new Promise<CommitPage>(r => { resolveNext = r })
    vi.mocked(searchCommits).mockImplementation((_pid, _repo, _term, offset) =>
      offset === 0
        ? Promise.resolve({ items: [commit('abcdef123456', 'fix: explorer')], total: 60, offset: 0, limit: 20 })
        : nextPage)

    render(<ProjectCommitLog projectId={137} repos={repos} />)
    await screen.findByText('fix: explorer')

    fireEvent.click(screen.getByText('project.nextPage'))
    await waitFor(() => expect(searchCommits).toHaveBeenLastCalledWith(137, 42, '', 20, 20))

    // The outgoing page is still there — no blank pane, no scroll reset.
    expect(screen.getByText('fix: explorer')).toBeTruthy()
    resolveNext({ items: [commit('bbb222', 'page two')], total: 60, offset: 20, limit: 20 })
    expect(await screen.findByText('page two')).toBeTruthy()
    expect(screen.queryByText('fix: explorer')).toBeNull()
  })

  it('opens the selected commit patch on the right page', async () => {
    render(<ProjectCommitLog projectId={137} repos={repos} />)
    fireEvent.click(await screen.findByText('fix: explorer'))

    await waitFor(() => expect(getCommitDiff).toHaveBeenCalledWith(42, 'abcdef123456'))
    expect(await screen.findByText('internal/service/a.go')).toBeTruthy()
    expect(screen.getByText('new line')).toBeTruthy()
    expect(screen.getByText('old line')).toBeTruthy()
  })

  it('opens a full-file before/after comparison from a diff file', async () => {
    render(<ProjectCommitLog projectId={137} repos={repos} />)
    fireEvent.click(await screen.findByText('fix: explorer'))
    const fileButton = await screen.findByText('internal/service/a.go')
    fireEvent.click(fileButton)

    await waitFor(() => expect(getCommitFile).toHaveBeenCalledTimes(2))
    expect(getCommitFile).toHaveBeenCalledWith(42, 'abcdef123456', 'internal/service/a.go', true)
    expect(getCommitFile).toHaveBeenCalledWith(42, 'abcdef123456', 'internal/service/a.go', false)
    expect(screen.getAllByText('internal/service/a.go').length).toBeGreaterThan(1)
    expect(screen.getByText('project.before')).toBeTruthy()
    expect(screen.getByText('project.after')).toBeTruthy()
  })
})
