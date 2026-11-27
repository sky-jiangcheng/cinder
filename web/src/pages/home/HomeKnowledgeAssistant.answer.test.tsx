// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key,
  }),
}))

import { AssistantAnswerBody, normalizeAnswerMarkdown, AnswerHead, type DialogueMessage } from './HomeKnowledgeAssistant'
import { renderMarkdown } from '../../utils/markdown'
import type { Project } from '../../api/client'

describe('normalizeAnswerMarkdown', () => {
  it('turns a leading warning paragraph into a real callout', () => {
    const out = normalizeAnswerMarkdown(
      '今日合计 17 次提交。\n\n⚠️ 注意:Live 只覆盖 30 个仓库。\n其余 60+ 仓库未统计。\n\n---\n',
      '注意',
    )
    expect(out).toContain('> [!WARNING] 注意\n> Live 只覆盖 30 个仓库。\n> 其余 60+ 仓库未统计。')
    expect(out).toContain('\n---\n')
    expect(renderMarkdown(out)).toContain('callout-warning')
  })

  it('leaves ordinary markdown alone', () => {
    const source = '**CBiMoney**\n\n- pay-common:8 提交\n- pay-customer:8 提交\n'
    expect(normalizeAnswerMarkdown(source, '注意')).toBe(source)
  })
})

describe('AnswerHead', () => {
  it('shows what the answer is and how much evidence it used', () => {
    const msg: DialogueMessage = {
      id: 'a',
      role: 'assistant',
      content: '12345',
      timestamp: '14:47',
      projectCards: Array.from({ length: 6 }, () => ({ id: 1 })) as unknown as Project[],
      knowledgeCards: Array.from({ length: 3 }, () => ({ ref: 'N1', id: 7 })) as never,
    }
    render(<AnswerHead msg={msg} />)
    expect(screen.getByText('回答')).toBeTruthy()
    expect(screen.getByText('6 项目')).toBeTruthy()
    expect(screen.getByText('3 知识')).toBeTruthy()
    expect(screen.getByText('5 字')).toBeTruthy()
  })
})

describe('AssistantAnswerBody', () => {
  it('folds the model reasoning above the answer', async () => {
    const { container } = render(
      <AssistantAnswerBody
        content={'<think>plan: read the note first</think>The retry budget was raised.'}
        warningTitle="注意"
      />,
    )
    const details = container.querySelector('details.think-block')
    expect(details).toBeTruthy()
    // The notes are still there, one click away — not deleted, not the answer.
    expect(details?.querySelector('summary')?.textContent).toBe('Thinking')
    expect(details?.textContent).toContain('plan: read the note first')
    expect(await screen.findByText(/The retry budget was raised/)).toBeTruthy()
  })

  it('does not draw a disclosure when there is no reasoning', async () => {
    const { container } = render(
      <AssistantAnswerBody content={'The retry budget was raised.'} warningTitle="注意" />,
    )
    expect(container.querySelector('details.think-block')).toBeNull()
  })
})
