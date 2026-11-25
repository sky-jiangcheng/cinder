// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { render, screen } from '@testing-library/react'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => {
      const dict: Record<string, string> = {
        'project.relatedLabel': 'Related',
        'project.relatedShared': '共同：{{terms}}',
        'project.relatedSameTopic': '同一主题',
        'project.relatedSameProject': '同一项目',
        'project.relatedSimilar': '主题相近',
        'common.listSeparator': '、',
      }
      const template = dict[key] ?? key
      return template.replace(/\{\{(\w+)\}\}/g, (_m, k) => String(opts?.[k] ?? ''))
    },
  }),
}))

import RelatedNoteChips, { relatedReason } from './RelatedNoteChips'
import type { RelatedNote } from '../../api/client'

const related = (over: Partial<RelatedNote>): RelatedNote => ({
  id: 1, project_id: 2, title: '另一条笔记', score: 0.5, ...over,
})

const t = (key: string, opts?: Record<string, unknown>) => {
  const dict: Record<string, string> = {
    'project.relatedShared': '共同：{{terms}}',
    'project.relatedSameTopic': '同一主题',
    'project.relatedSameProject': '同一项目',
    'project.relatedSimilar': '主题相近',
    'common.listSeparator': '、',
  }
  return (dict[key] ?? key).replace(/\{\{(\w+)\}\}/g, (_m, k) => String(opts?.[k] ?? ''))
}

describe('RelatedNoteChips', () => {
  it('renders nothing when there is no relation', () => {
    const { container } = render(<MemoryRouter><RelatedNoteChips related={[]} /></MemoryRouter>)
    expect(container.innerHTML).toBe('')
  })

  it('caps the chips and links each to its note', () => {
    render(
      <MemoryRouter>
        <RelatedNoteChips
          related={[related({ id: 7 }), related({ id: 8 }), related({ id: 9 }), related({ id: 10 })]}
          max={2}
        />
      </MemoryRouter>,
    )
    const links = screen.getAllByRole('link')
    expect(links).toHaveLength(2)
    expect(links[0].getAttribute('href')).toBe('/note/7')
  })

  it('names the shared terms in the tooltip, and the topic when there is one', () => {
    render(
      <MemoryRouter>
        <RelatedNoteChips related={[related({ shared: ['迁移', 'sqlite'], same_topic: true })]} />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link').getAttribute('title')).toBe('共同：迁移、sqlite、同一主题')
  })

  it('falls back to the relation kind when no terms are shared', () => {
    render(
      <MemoryRouter>
        <RelatedNoteChips related={[related({ shared: [], same_project: true, same_topic: true })]} />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link').getAttribute('title')).toBe('同一主题')
  })
})

describe('relatedReason', () => {
  it('prefers shared terms over the weaker flags', () => {
    expect(relatedReason(related({ shared: ['a'], same_project: true }), t as never)).toBe('共同：a')
  })

  it('never returns an empty sentence', () => {
    expect(relatedReason(related({}), t as never)).toBe('主题相近')
  })
})
