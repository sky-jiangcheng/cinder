import { beforeEach, describe, expect, it } from 'vitest'
import {
  cardFromSearchHit,
  knowledgeHref,
  serializeDialogue,
  loadDialogue,
  type DialogueMessage,
} from './HomeKnowledgeAssistant'
import type { EvidenceItem, SearchHit } from '../../api/client'

function hit(over: Partial<SearchHit>): SearchHit {
  return {
    type: 'note',
    id: 7,
    project_id: 3,
    project_name: 'CBiPay',
    title: '重试策略',
    snippet: '…',
    updated_at: '2026-10-20T00:00:00Z',
    ...over,
  }
}

function item(over: Partial<EvidenceItem>): EvidenceItem {
  return {
    ref: 'N1',
    type: 'note',
    id: 7,
    title: '重试策略',
    kind: 'knowledge',
    snippet: '…',
    rank: 1,
    ...over,
  }
}

describe('cardFromSearchHit', () => {
  it('keeps a note hit as a note card', () => {
    const card = cardFromSearchHit(hit({ type: 'note' }), 0, 'CBiPay')
    expect(card).toMatchObject({ ref: 'N1', type: 'note', kind: 'knowledge', project_name: 'CBiPay', rank: 1 })
  })

  it('keeps a todo hit as a todo card instead of flattening it to a note', () => {
    const card = cardFromSearchHit(hit({ type: 'todo', id: 12 }), 0)
    expect(card).toMatchObject({ ref: 'T1', type: 'todo', kind: 'todo', id: 12 })
  })
})

// ADR-0056: the console is a lazy route, so a top-level tab switch unmounts it.
// Pinned literal on purpose — renaming the key without a migration silently
// abandons every existing user's saved conversation.
const HISTORY_KEY = 'repotory.home.dialogue.v1'

function msg(over: Partial<DialogueMessage> = {}): DialogueMessage {
  return { id: 'm1', role: 'user', content: 'hello', timestamp: '10:00', ...over }
}

describe('dialogue snapshot (ADR-0056)', () => {
  beforeEach(() => localStorage.clear())

  it('survives a remount: what was serialized is what comes back', () => {
    const messages = [
      msg(),
      msg({ id: 'm2', role: 'assistant', content: 'answer', knowledgeCards: [], kind: 'ask' }),
    ]
    localStorage.setItem(HISTORY_KEY, serializeDialogue(messages))
    expect(loadDialogue()).toEqual(messages)
  })

  it('never persists a streaming draft', () => {
    const json = serializeDialogue([
      msg(),
      msg({ id: 'm2', role: 'assistant', content: 'partial', isStreaming: true }),
    ])
    const saved = JSON.parse(json)
    expect(saved).toHaveLength(1)
    localStorage.setItem(HISTORY_KEY, json)
    expect(loadDialogue()).toEqual([msg()])
    // A snapshot written by an older build may still contain a draft.
    localStorage.setItem(HISTORY_KEY, JSON.stringify([msg({ id: 'm2', role: 'assistant', isStreaming: true })]))
    expect(loadDialogue()).toEqual([])
  })

  it('caps the snapshot so evidence cards cannot grow it without bound', () => {
    const many = Array.from({ length: 80 }, (_, i) => msg({ id: `m${i}` }))
    const saved = JSON.parse(serializeDialogue(many))
    expect(saved).toHaveLength(50)
    expect(saved[0].id).toBe('m30')
    expect(saved[49].id).toBe('m79')
  })

  it('ignores corrupt, non-array and malformed snapshots', () => {
    localStorage.setItem(HISTORY_KEY, '{not json')
    expect(loadDialogue()).toEqual([])
    localStorage.setItem(HISTORY_KEY, JSON.stringify({ nope: true }))
    expect(loadDialogue()).toEqual([])
    localStorage.setItem(HISTORY_KEY, JSON.stringify([{ id: 1 }, null, msg(), { id: 'x', role: 'bot' }]))
    expect(loadDialogue()).toEqual([msg()])
  })
})

describe('knowledgeHref', () => {
  it('deep-links a note to its row in the notes tab', () => {
    expect(knowledgeHref(item({ project_id: 3, id: 7 }), 0)).toBe('/project/3?tab=notes&noteId=7')
  })

  it('lands a todo on the todos tab — a todo id is not a note id', () => {
    const href = knowledgeHref(item({ type: 'todo', kind: 'todo', project_id: 3, id: 12 }), 0)
    expect(href).toBe('/project/3?tab=todos')
    expect(href).not.toContain('noteId')
  })

  it('falls back to the given project only for note and todo cards', () => {
    expect(knowledgeHref(item({ project_id: 0 }), 9)).toBe('/project/9?tab=notes&noteId=7')
    expect(knowledgeHref(item({ type: 'todo', kind: 'todo', project_id: 0 }), 9)).toBe('/project/9?tab=todos')
  })

  it('sends a page to its wiki entry, not to the project list', () => {
    expect(knowledgeHref(item({ type: 'page', project_id: 5 }), 9)).toBe('/wiki/7')
    expect(knowledgeHref(item({ type: 'page', project_id: 0 }), 9)).toBe('/wiki/7')
  })
})
