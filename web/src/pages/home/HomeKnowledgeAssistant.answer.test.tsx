// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key,
  }),
}))

import { normalizeAnswerMarkdown, AnswerHead, type DialogueMessage } from './HomeKnowledgeAssistant'
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
