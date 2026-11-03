// components/agent/agentContext.test.ts — what the agent knows, per page.
//
// The point of these tests is the REASON the module exists: the questions
// offered must be the ones the current page can actually answer, and the
// answers must come from the same data — and the same SCOPE — the page
// itself reads. A card that says "12 commits today" while the dashboard says
// 9 is worse than no card.

import { describe, expect, it, vi, beforeEach } from 'vitest'

vi.mock('../../api/client', () => ({
  getHeatmapData: vi.fn(),
  getInstallInfo: vi.fn(),
  getNoteCounts: vi.fn(),
  getProjectOverview: vi.fn(),
  getProjects: vi.fn(),
  getStatusBar: vi.fn(),
  getSummary: vi.fn(),
  getTodoCounts: vi.fn(),
  listPendingWikiPages: vi.fn(),
  listRecentHandoffs: vi.fn(),
}))

import {
  getHeatmapData,
  getInstallInfo,
  getNoteCounts,
  getProjectOverview,
  getProjects,
  getStatusBar,
  getSummary,
  getTodoCounts,
  listPendingWikiPages,
  listRecentHandoffs,
} from '../../api/client'
import { buildAgentContext, todayCard, todayISO, weekCard } from './agentContext'
import { daysAgo, getYesterday } from '../../utils/dates'

// The days are relative to the wall clock on purpose: the card's title
// depends on whether the date IS the dashboard's day, and a hard-coded date
// would make that comparison mean something different every day.
const Y = getYesterday()
const D2 = daysAgo(2)
const D1 = daysAgo(1)
const T0 = daysAgo(0)

// The translator here echoes the key, so a test asserts WHICH key was used
// rather than a localized string — the wording is the locale file's job.
const t = (key: string, opts?: Record<string, unknown>) =>
  opts ? `${key}|${JSON.stringify(opts)}` : key

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getSummary).mockResolvedValue({
    date: Y, repo_count: 5, total_files: 12, total_added: 340,
    total_deleted: 90, my_added: 120, my_deleted: 30, my_files: 4, is_workday: true,
  })
  vi.mocked(getHeatmapData).mockResolvedValue({
    days: [
      { date: D2, lines_added: 10, lines_deleted: 2, commits: 3 },
      { date: D1, lines_added: 20, lines_deleted: 4, commits: 5 },
      { date: T0, lines_added: 30, lines_deleted: 6, commits: 7 },
    ],
  })
  vi.mocked(getTodoCounts).mockResolvedValue([{ project_id: 1, count: 2, total: 4 }])
  vi.mocked(getNoteCounts).mockResolvedValue([{ project_id: 1, count: 9 }])
  vi.mocked(getProjects).mockResolvedValue([
    { id: 1, name: 'Script', root_path: '/x', last_commit: '', stars: false } as never,
  ])
  vi.mocked(getStatusBar).mockResolvedValue({
    current_time: '2026-11-03T10:00:00', last_commit_time: '2026-11-03T09:00:00',
    last_commit_repo: 'Script', last_commit_branch: 'main', last_commit_msg: 'fix: thing',
  })
  vi.mocked(getProjectOverview).mockResolvedValue({
    readme_excerpt: '', tech_stack: [{ name: 'Go', category: 'language' }],
    languages: [{ language: 'TypeScript', count: 1200 }],
    dependencies: [], top_contributors: [],
    activity: { total_commits: 42, active_days: 9, last_commit_date: '2026-11-03', commit_rate_30d: 3, active_months: 2 },
    recent_commits: [{ time: '09:00', message: 'chore: x', author: 'me', repo: 'Script', branch: 'main' }],
    cached: false,
  })
  vi.mocked(listRecentHandoffs).mockResolvedValue([])
  vi.mocked(listPendingWikiPages).mockResolvedValue([])
  vi.mocked(getInstallInfo).mockResolvedValue({
    version: '2.25.0', platform: 'darwin', install_path: '/Applications/Repotory.app',
    executable_path: '/Applications/Repotory.app/Contents/MacOS/repotory',
    download_dir: '/Users/me/Downloads',
  })
})

describe('buildAgentContext', () => {
  it('offers the dashboard its own questions', () => {
    const ctx = buildAgentContext('/dashboard', t)
    expect(ctx.label).toBe('agent.context.dashboard')
    expect(ctx.projectId).toBe(0)
    expect(ctx.actions.map(a => a.key)).toEqual(['today', 'week', 'pending', 'last'])
  })

  it('scopes a project page to that project', () => {
    const ctx = buildAgentContext('/project/12', t)
    expect(ctx.projectId).toBe(12)
    expect(ctx.label).toBe('agent.context.project')
    expect(ctx.actions.map(a => a.key)).toEqual(['overview', 'today', 'commits', 'stack'])
  })

  it('offers the review page its pending pages', () => {
    const ctx = buildAgentContext('/review', t)
    expect(ctx.actions[0].key).toBe('pending')
  })

  it('falls back to a single question anywhere else', () => {
    const ctx = buildAgentContext('/nowhere', t)
    expect(ctx.actions.map(a => a.key)).toEqual(['today'])
  })
})

describe('todayCard', () => {
  it('takes the commit count from the heatmap and the churn from the summary', async () => {
    // The dashboard's day, not the wall-clock today: the daily-code dashboard
    // opens on the last finished day, and the card must agree with it.
    const card = await todayCard(t, Y)
    const metrics = Object.fromEntries((card.metrics ?? []).map(m => [m.label, m.value]))
    // The heatmap is the only place the per-day COMMIT count exists.
    expect(metrics['agent.metric.commits']).toBe('5')
    // The line counts are the user's OWN — the same ones the dashboard's
    // strip shows, not the library-wide totals.
    expect(metrics['agent.metric.myAdded']).toBe('+120')
    expect(metrics['agent.metric.myDeleted']).toBe('-30')
    expect(metrics['agent.metric.myFiles']).toBe('4')
    expect(metrics['agent.metric.repos']).toBe('5')
  })

  it('reads the summary in the dashboard\'s own scope', async () => {
    // The whole point of the shared scope: the card and the dashboard strip
    // must aggregate the SAME projects, or they answer two questions.
    await todayCard(t, Y, true)
    expect(getSummary).toHaveBeenCalledWith(Y, true)
    await todayCard(t, Y, false)
    expect(getSummary).toHaveBeenCalledWith(Y, false)
  })

  it('names the day it answers for', async () => {
    const card = await todayCard(t, Y)
    expect(card.title).toBe('agent.card.today.title')
    expect(card.subtitle).toBe(`agent.card.today.subtitle|${JSON.stringify({ date: Y })}`)
  })

  it('drops the word "today" when the dashboard is showing another day', async () => {
    const card = await todayCard(t, D2)
    expect(card.title).toBe('agent.card.codeVolume.title')
  })

  it('says which scope the line counts are in', async () => {
    const starred = await todayCard(t, Y, true)
    expect(starred.footer).toBe('agent.card.today.footerStarred')
    const all = await todayCard(t, Y, false)
    expect(all.footer).toBe('agent.card.today.footerAll')
  })

  it('reports zero rather than undefined when the day has no cell', async () => {
    vi.mocked(getHeatmapData).mockResolvedValue({ days: [] })
    const card = await todayCard(t, Y)
    const metrics = Object.fromEntries((card.metrics ?? []).map(m => [m.label, m.value]))
    expect(metrics['agent.metric.commits']).toBe('0')
  })

  it('says so when the data cannot be read', async () => {
    vi.mocked(getSummary).mockRejectedValue(new Error('boom'))
    vi.mocked(getHeatmapData).mockRejectedValue(new Error('boom'))
    const card = await todayCard(t, Y)
    expect(card.body).toBe('agent.error.load')
  })
})

describe('weekCard', () => {
  it('sums the last seven days', async () => {
    const card = await weekCard(t, T0)
    const metrics = Object.fromEntries((card.metrics ?? []).map(m => [m.label, m.value]))
    expect(metrics['agent.metric.commits7d']).toBe('15')
    expect(metrics['agent.metric.activeDays']).toBe('3')
  })
})

describe('the other cards', () => {
  it('reports the project overview', async () => {
    const ctx = buildAgentContext('/project/12', t)
    const card = await ctx.actions[0].load()
    const metrics = Object.fromEntries((card.metrics ?? []).map(m => [m.label, m.value]))
    expect(metrics['agent.metric.totalCommits']).toBe('42')
    expect(card.items?.[0]?.title).toBe('Go')
    expect(card.to).toBe('/project/12')
  })

  it('lists the todos by project name', async () => {
    const ctx = buildAgentContext('/dashboard', t)
    const card = await ctx.actions[2].load()
    expect(card.items?.[0]?.title).toBe('Script')
  })

  it('reports where the app is installed', async () => {
    const ctx = buildAgentContext('/settings', t)
    const card = await ctx.actions[0].load()
    expect(card.items?.some(i => i.meta === '/Applications/Repotory.app')).toBe(true)
  })
})

describe('the scope the context carries', () => {
  it('labels the quick question by the day it answers', () => {
    const today = buildAgentContext('/dashboard', t, undefined, { date: Y, starredOnly: true })
    expect(today.actions[0].label).toBe('agent.action.todayCommits')

    const other = buildAgentContext('/dashboard', t, undefined, { date: D2, starredOnly: true })
    expect(other.actions[0].label).toBe('agent.action.codeVolume')
  })

  it('labels the project question by the day it answers', () => {
    const today = buildAgentContext('/project/12', t, undefined, { date: Y, starredOnly: true })
    expect(today.actions[1].label).toBe('agent.action.projectToday')

    const other = buildAgentContext('/project/12', t, undefined, { date: D2, starredOnly: true })
    expect(other.actions[1].label).toBe('agent.action.projectVolume')
  })

  it('builds the day cards in that scope', async () => {
    const ctx = buildAgentContext('/dashboard', t, undefined, { date: D2, starredOnly: false })
    await ctx.actions[0].load()
    expect(getSummary).toHaveBeenCalledWith(D2, false)
  })
})

describe('todayISO', () => {
  it('formats the local date the way the app stores it', () => {
    expect(todayISO(new Date(2026, 10, 3))).toBe('2026-11-03')
    expect(todayISO(new Date(2026, 0, 1))).toBe('2026-01-01')
  })
})
