// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { Link } from 'react-router-dom'
import { renderReady, settle } from './test/renderReady'

vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-i18next')>()
  return {
    ...actual,
    useTranslation: () => ({
      t: (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key,
    }),
  }
})

vi.mock('./pages/Knowledge', () => ({ default: () => <div>Knowledge destination</div> }))

vi.mock('./api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api/client')>()
  return {
    ...actual,
    getProjects: vi.fn(),
    getConfig: vi.fn(),
    listAllTags: vi.fn(),
    askAIWithEvidenceStream: vi.fn(),
  }
})

import { askAIWithEvidenceStream, getConfig, getProjects, listAllTags } from './api/client'
import { RoutedApp } from './App'

const mockAskStream = vi.mocked(askAIWithEvidenceStream)
const mockGetProjects = vi.mocked(getProjects)
const mockGetConfig = vi.mocked(getConfig)
const mockListAllTags = vi.mocked(listAllTags)

function RouteHarness() {
  return (
    <>
      <nav>
        <Link to="/">Home destination</Link>
        <Link to="/knowledge">Knowledge destination</Link>
      </nav>
      <RoutedApp />
    </>
  )
}

beforeEach(() => {
  localStorage.clear()
  mockGetProjects.mockResolvedValue([])
  mockGetConfig.mockResolvedValue({
    config: { ai_chat_base_url: 'https://ai.example/v1', ai_chat_model: 'test-model' },
    scan_roots: [],
  })
  mockListAllTags.mockResolvedValue([])
  mockAskStream.mockReset().mockReturnValue(vi.fn())
})

describe('Home route continuity', () => {
  it('keeps an in-flight answer when switching tabs and returning before the reply', async () => {
    await renderReady(<RouteHarness />)
    await screen.findByRole('heading', { name: '知识库对话台' })

    const question = '切换页签后继续回答'
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: question } })
    fireEvent.keyDown(input, { key: 'Enter' })
    await waitFor(() => expect(mockAskStream).toHaveBeenCalledTimes(1))
    expect(screen.getByTitle('停止')).toBeTruthy()

    fireEvent.click(screen.getByRole('link', { name: 'Knowledge destination' }))
    expect(await screen.findByText('Knowledge destination', { selector: 'div' })).toBeTruthy()

    fireEvent.click(screen.getByRole('link', { name: 'Home destination' }))
    await screen.findByRole('textbox')

    expect(screen.getByTitle('停止')).toBeTruthy()
    expect(screen.getAllByText(question)).toHaveLength(1)

    const handlers = mockAskStream.mock.calls[0][2]
    act(() => handlers.onDelta('已经'))
    expect(screen.getByText('已经')).toBeTruthy()

    act(() => handlers.onDone({
      reply: '已经继续生成的完整回答',
      evidence: { items: [], dropped: 0, elapsed_ms: 3, truncated: false },
      truncated: false,
    }))

    expect(await screen.findByText('已经继续生成的完整回答')).toBeTruthy()
    expect(screen.queryByTitle('停止')).toBeNull()
    expect(mockAskStream).toHaveBeenCalledTimes(1)
  })

  it('does not mount Home for an initial deep link to another tab', async () => {
    await renderReady(<RouteHarness />, { initialEntries: ['/knowledge'] })
    expect(await screen.findByText('Knowledge destination', { selector: 'div' })).toBeTruthy()
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(mockGetProjects).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('link', { name: 'Home destination' }))
    await screen.findByRole('heading', { name: '知识库对话台' })
    await waitFor(() => expect(mockGetProjects).toHaveBeenCalledTimes(1))
  })

  it('refreshes home data when returning from another tab', async () => {
    await renderReady(<RouteHarness />)
    await screen.findByRole('heading', { name: '知识库对话台' })
    await settle()
    expect(mockGetProjects).toHaveBeenCalledTimes(1)
    expect(mockGetConfig).toHaveBeenCalledTimes(1)
    expect(mockListAllTags).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByRole('link', { name: 'Knowledge destination' }))
    expect(await screen.findByText('Knowledge destination', { selector: 'div' })).toBeTruthy()
    fireEvent.click(screen.getByRole('link', { name: 'Home destination' }))
    await screen.findByRole('heading', { name: '知识库对话台' })

    await waitFor(() => expect(mockGetProjects).toHaveBeenCalledTimes(2))
    expect(mockGetConfig).toHaveBeenCalledTimes(2)
    expect(mockListAllTags).toHaveBeenCalledTimes(2)
  })
})
