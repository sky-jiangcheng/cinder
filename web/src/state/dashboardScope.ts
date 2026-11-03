// state/dashboardScope.ts — the dashboard's scope, shared with the agent.
//
// Why this file exists. The dashboard shows "today's code" as a set of
// numbers, and those numbers depend on TWO choices the user makes there: the
// DAY, and whether only starred projects count. The agent's card answers the
// same question ("今日代码提交量"), so the two must read the same choices.
//
// Before this file, they did not: the dashboard aggregated the starred
// projects (个人新增 371 行) while the agent aggregated the whole library
// (全库新增 20,689 行). Both numbers were correct; they answered different
// questions; and the user could see both at once and conclude that one of
// them was broken.
//
// So the scope is ONE fact with ONE owner. The dashboard writes it, the agent
// reads it, and neither can drift from the other. It is deliberately a tiny
// external store rather than React context: both consumers live in different
// parts of the tree (the dashboard page, and the floating bot that is mounted
// once at the app root), and a store keeps them from having to be siblings.

import { getYesterday } from '../utils/dates'

export interface DashboardScope {
  /** The day the dashboard is showing (YYYY-MM-DD). */
  date: string
  /** Whether the summary counts only the starred projects. */
  starredOnly: boolean
}

type Listener = () => void

const listeners = new Set<Listener>()

// The dashboard's own defaults: the last finished day, and starred projects
// only — exactly what the dashboard starts with.
let scope: DashboardScope = { date: getYesterday(), starredOnly: true }

/** The current scope. The returned object is stable until it changes. */
export function getDashboardScope(): DashboardScope {
  return scope
}

/**
 * Update the scope and notify the subscribers.
 *
 * A no-op when nothing actually changed: the store is read during render by
 * two components, and handing out a new object for an unchanged scope would
 * re-render both for nothing.
 */
export function setDashboardScope(patch: Partial<DashboardScope>): void {
  const next = { ...scope, ...patch }
  if (next.date === scope.date && next.starredOnly === scope.starredOnly) return
  scope = next
  listeners.forEach(fn => fn())
}

/** Subscribe to scope changes. Returns the unsubscribe function. */
export function subscribeDashboardScope(fn: Listener): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

/** Put the scope back to the dashboard's defaults (tests). */
export function resetDashboardScope(): void {
  setDashboardScope({ date: getYesterday(), starredOnly: true })
}
