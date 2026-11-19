// hooks/assistantCards.ts — the latest settled answer's evidence cards, made
// available to the desktop shell's right-hand context rail.
//
// Why a module store instead of props or an event: the rail lives in App's
// shell and the cards are produced deep in the lazy Home route. A CustomEvent
// would be missed on mount (rail subscribes before/after the answer lands,
// depending on navigation) and a localStorage round-trip would duplicate the
// dialogue snapshot this module already has. A one-slot store with subscribe
// gives the rail the cards on first render and on every later answer.

import type { Project, EvidenceItem } from '../api/client'

export interface AssistantCards {
  projectCards: Project[]
  knowledgeCards: EvidenceItem[]
  /** The answer these cards belong to, so the rail can tell "current" from stale. */
  messageId: string
}

const EMPTY: AssistantCards = { projectCards: [], knowledgeCards: [], messageId: '' }

let current: AssistantCards = EMPTY
const listeners = new Set<() => void>()

export function publishAssistantCards(cards: AssistantCards): void {
  current = cards
  for (const listener of listeners) listener()
}

export function subscribeAssistantCards(listener: () => void): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

export function getAssistantCards(): AssistantCards {
  return current
}
