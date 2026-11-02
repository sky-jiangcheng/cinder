// @vitest-environment jsdom
// Component-level race test for the no-AI fallback branch. The pure-logic
// contracts (cardFromSearchHit / knowledgeHref) live in
// HomeKnowledgeAssistant.test.ts; this file exists because the cancelled flag
// cannot be pinned without rendering: it guards state writes after an await,
// so the failure mode is only observable through the mounted component.
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, fireEvent, act } from '@testing-library/react'
import { renderReady } from '../../test/renderReady'

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    getConfig: vi.fn(),
    searchAll: vi.fn(),
    listAllTags: vi.fn(),
  }
})

// Same stable-t convention as Search.test.tsx: defaultValue verbatim.
const mockT = (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: mockT }),
}))

import { getConfig, searchAll, listAllTags, type SearchHit } from '../../api/client'
import HomeKnowledgeAssistant from './HomeKnowledgeAssistant'

const mockGetConfig = vi.mocked(getConfig)
const mockSearch = vi.mocked(searchAll)

const hit = (over: Partial<SearchHit>): SearchHit => ({
  type: 'note',
  id: 5,
  project_id: 2,
  project_name: 'Alpha',
  title: '登录排查笔记',
  snippet: '关于登录的记录',
  updated_at: '2026-10-12 10:00:00',
  ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  // No AI endpoint configured → handleSend takes the fallback branch.
  mockGetConfig.mockResolvedValue({ config: {}, scan_roots: [] })
  // The idle stage's hot tags. Unmocked it would reach the transport.
  vi.mocked(listAllTags).mockResolvedValue([])
})

describe('HomeKnowledgeAssistant fallback race (stop vs in-flight searchAll)', () => {
  it('a stopped fallback search writes nothing and does not steal the next question asking slot', async () => {
    let resolveFirst!: (v: SearchHit[]) => void
    let resolveSecond!: (v: SearchHit[]) => void
    mockSearch.mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve }))
    mockSearch.mockReturnValueOnce(new Promise(resolve => { resolveSecond = resolve }))

    await renderReady(
      <HomeKnowledgeAssistant projects={[]} onToast={vi.fn()} onGoToSettings={vi.fn()} />,
    )

    // Q1 → in-flight fallback; the stop button replaces the send button.
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '登录' } })
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    expect(screen.getByText('登录')).toBeTruthy()
    expect(screen.getByTitle('停止')).toBeTruthy()

    // Stop Q1. The pending searchAll cannot be aborted, only ignored.
    fireEvent.click(screen.getByTitle('停止'))
    expect(screen.getByTitle('发送')).toBeTruthy()

    // Q2 takes over the asking slot while Q1's promise is still pending.
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '支付' } })
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    expect(screen.getByText('支付')).toBeTruthy()
    expect(screen.getByTitle('停止')).toBeTruthy()

    // The stale Q1 promise settles now. Before the cancelled flag, this wrote
    // its cards into Q1's message and — worse — setAsking(false) in finally,
    // killing Q2's spinner and elapsed timer mid-flight.
    await act(async () => {
      resolveFirst([hit({ title: '过期的登录笔记' })])
    })
    expect(screen.queryByText('过期的登录笔记')).toBeNull()
    expect(screen.getByTitle('停止')).toBeTruthy()

    // Q2 completes normally: asking released, fallback notice rendered.
    await act(async () => {
      resolveSecond([])
    })
    expect(await screen.findByText(/未配置大模型端点/)).toBeTruthy()
    expect(screen.getByTitle('发送')).toBeTruthy()
    expect(screen.queryByTitle('停止')).toBeNull()
  })

  it('a fallback search error after stop stays silent too', async () => {
    let rejectFirst!: (e: Error) => void
    mockSearch.mockReturnValueOnce(new Promise((_resolve, reject) => { rejectFirst = reject }))

    await renderReady(
      <HomeKnowledgeAssistant projects={[]} onToast={vi.fn()} onGoToSettings={vi.fn()} />,
    )

    fireEvent.change(screen.getByRole('textbox'), { target: { value: '登录' } })
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    expect(screen.getByTitle('停止')).toBeTruthy()

    fireEvent.click(screen.getByTitle('停止'))

    await act(async () => {
      rejectFirst(new Error('boom'))
    })
    // The failure text must not land in the stopped question's bubble.
    expect(screen.queryByText(/boom/)).toBeNull()
  })
})
