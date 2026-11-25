// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { Project } from '../../api/types'
import type { EvidenceItem } from '../../api/client'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_key: string, opts?: { defaultValue?: string }) => opts?.defaultValue ?? _key,
  }),
}))

import { AssistantReferences } from './HomeKnowledgeAssistant'

const project: Project = {
  id: 3,
  name: 'CBiPay',
  root_path: '/workspace/CBiPay',
  level_override: 0,
  is_auto_grouped: false,
  is_starred: false,
  created_at: '',
  repo_count: 2,
  total_added: 0,
  total_deleted: 0,
  my_added: 120,
  my_deleted: 0,
  my_files: 0,
  my_added_7d: 0,
  is_workday: true,
  below_standard: false,
}

const item: EvidenceItem = {
  ref: 'N1',
  type: 'note',
  id: 7,
  title: '重试策略',
  kind: 'knowledge',
  snippet: '支付链路的退避与幂等',
  rank: 1,
}

describe('AssistantReferences', () => {
  it('starts collapsed and keeps an expanded answer open across rerenders', () => {
    const props = {
      projectCards: [project],
      knowledgeCards: [item],
      onOpenProject: vi.fn(),
      onOpenKnowledge: vi.fn(),
    }
    const { rerender } = render(<AssistantReferences {...props} />)
    const details = screen.getByRole('group') as HTMLDetailsElement

    expect(details.open).toBe(false)
    fireEvent.click(screen.getByText('引用与相关项目 (2)'))
    expect(details.open).toBe(true)

    rerender(<AssistantReferences {...props} knowledgeCards={[{ ...item, snippet: '更新后的摘要' }]} />)
    expect(details.open).toBe(true)
    expect(screen.getByText('更新后的摘要')).toBeTruthy()
  })

  it('opens the selected card with pointer or keyboard', () => {
    const onOpenKnowledge = vi.fn()
    render(
      <AssistantReferences
        projectCards={[]}
        knowledgeCards={[item]}
        onOpenProject={vi.fn()}
        onOpenKnowledge={onOpenKnowledge}
      />,
    )
    fireEvent.click(screen.getByText('引用与相关项目 (1)'))

    const card = screen.getByRole('button', { name: /重试策略/ })
    fireEvent.click(card)
    fireEvent.keyDown(card, { key: ' ' })
    expect(onOpenKnowledge).toHaveBeenCalledTimes(2)
    expect(onOpenKnowledge).toHaveBeenLastCalledWith(item)
  })
})
