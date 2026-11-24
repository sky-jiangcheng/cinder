// @vitest-environment jsdom
// HandoffTimeline.test.tsx — the knowledge page's handoff timeline.
//
// What this pins:
//   - entries group by day with 今天/昨天 headers (the timeline's whole point:
//     "what happened, when, in order");
//   - a card click opens the handoff's OWN read-only detail panel inline —
//     NOT a navigation to the notes detail page (the notes page stays
//     reachable only through the panel's secondary link);
//   - the shared scope and query filters narrow the window client-side;
//   - the empty state speaks when no handoff exists;
//   - the load-more affordance appears once the fetched window is full.

import { describe, expect, it, vi, beforeEach, beforeAll } from 'vitest'
import { screen, render, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import i18n from '../i18n' // real i18next instance — assertions read user wording

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    listHandoffTimeline: vi.fn(),
    getNote: vi.fn(),
  }
})

import { listHandoffTimeline, getNote, type HandoffTimelineEntry, type Project, type Note } from '../api/client'
import HandoffTimelineBody from './HandoffTimeline'
import { getToday, getYesterday } from '../utils/dates'

const mockList = vi.mocked(listHandoffTimeline)
const mockGetNote = vi.mocked(getNote)

const entry = (over: Partial<HandoffTimelineEntry> = {}): HandoffTimelineEntry => ({
  note_id: 1,
  project_id: 7,
  project_name: 'Cinder',
  title: '会话交接：wiki 重构推进到 L3',
  excerpt: 'wiki 系列重构完成 service 子包拆分，下一步迁移查询路径。',
  created_at: `${getYesterday()} 09:00:00`,
  updated_at: `${getYesterday()} 09:00:00`,
  auto_captured: false,
  ...over,
})

const projects: Project[] = [
  { id: 7, name: 'Cinder' } as Project,
  { id: 9, name: 'Warden' } as Project,
]

const noop = () => {}

function renderBody(props: Partial<Parameters<typeof HandoffTimelineBody>[0]> = {}) {
  return render(
    <MemoryRouter initialEntries={['/knowledge']}>
      <HandoffTimelineBody
        scope={0}
        query=""
        projects={projects}
        onCount={noop}
        onError={noop}
        onLoading={noop}
        {...props}
      />
    </MemoryRouter>,
  )
}

beforeAll(async () => {
  await i18n.changeLanguage('zh-CN')
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('HandoffTimelineBody', () => {
  it('groups entries by day with 今天/昨天 headers', async () => {
    mockList.mockResolvedValue([
      entry({ note_id: 1, updated_at: `${getYesterday()} 09:00:00`, title: '昨天的交接' }),
      entry({ note_id: 2, updated_at: `${getToday()} 18:40:11`, title: '今天的交接' }),
      entry({ note_id: 3, updated_at: `${getToday()} 10:00:00`, title: '今天更早的交接' }),
    ])
    renderBody()
    await screen.findByText('今天')
    expect(screen.getByText('昨天')).toBeTruthy()
    // Today's two entries share the day header; the day count says so.
    expect(screen.getAllByText('2 条').length).toBeGreaterThan(0)
    expect(screen.getByText('今天的交接')).toBeTruthy()
    expect(screen.getByText('昨天的交接')).toBeTruthy()
  })

  it("a card click opens the handoff's OWN detail panel, not the notes page", async () => {
    mockList.mockResolvedValue([entry({ note_id: 42 })])
    const fullNote: Partial<Note> = {
      id: 42,
      title: '会话交接：wiki 重构推进到 L3',
      content: '## Summary\n\n重构完成了 L3 拆分。\n\n## Next\n\n迁移查询路径。',
    }
    mockGetNote.mockResolvedValue(fullNote as Note)
    renderBody()
    fireEvent.click(await screen.findByText('会话交接：wiki 重构推进到 L3'))
    // The detail renders the full body inline (markdown → HTML), inside the
    // timeline — no navigation to /note/42 happened.
    await screen.findByText('重构完成了 L3 拆分。')
    expect(mockGetNote).toHaveBeenCalledWith(42)
    // The notes page stays reachable only as the secondary link.
    const link = screen.getByRole('link', { name: '在笔记中打开' })
    expect(link.getAttribute('href')).toBe('/note/42')
  })

  it('clicking an open card collapses its detail', async () => {
    mockList.mockResolvedValue([entry({ note_id: 42 })])
    mockGetNote.mockResolvedValue({ id: 42, content: '正文' } as Note)
    renderBody()
    const card = await screen.findByText('会话交接：wiki 重构推进到 L3')
    fireEvent.click(card)
    await screen.findByText('正文')
    fireEvent.click(card)
    expect(screen.queryByText('在笔记中打开')).toBeNull()
  })

  it('the scope filter hides other projects', async () => {
    mockList.mockResolvedValue([
      entry({ note_id: 1, project_id: 7, project_name: 'Cinder', title: 'Cinder 的交接' }),
      entry({ note_id: 2, project_id: 9, project_name: 'Warden', title: 'Warden 的交接' }),
    ])
    renderBody({ scope: 9 })
    await screen.findByText('Warden 的交接')
    expect(screen.queryByText('Cinder 的交接')).toBeNull()
  })

  it('the query narrows by title and excerpt', async () => {
    mockList.mockResolvedValue([
      entry({ note_id: 1, title: 'wiki 重构交接', excerpt: '普通内容' }),
      entry({ note_id: 2, title: '无关笔记', excerpt: '提到了发版流水线' }),
    ])
    renderBody({ query: '重构' })
    await screen.findByText('wiki 重构交接')
    expect(screen.queryByText('无关笔记')).toBeNull()
  })

  it('the empty state speaks when there are no handoffs', async () => {
    mockList.mockResolvedValue([])
    renderBody()
    await screen.findByText(/还没有 Handoff 笔记/)
  })

  it('offers load-more once the fetched window is full', async () => {
    mockList.mockResolvedValue(Array.from({ length: 50 }, (_, i) => entry({ note_id: i + 1 })))
    renderBody()
    await screen.findByText('加载更早的 Handoff')
    expect(screen.getByText('加载更早的 Handoff')).toBeTruthy()
  })

  it('marks auto-captured entries', async () => {
    mockList.mockResolvedValue([entry({ auto_captured: true })])
    renderBody()
    await screen.findByText('自动捕获')
  })
})
