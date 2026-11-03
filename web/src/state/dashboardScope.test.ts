// state/dashboardScope.test.ts — the shared scope between the dashboard and
// the agent.
//
// The store is small, but it is load-bearing: when it and the dashboard
// disagree, the dashboard says "371 lines" and the agent says "10,327", and
// the user has no way to tell which one is the app's answer.

import { describe, expect, it, beforeEach, vi } from 'vitest'
import {
  getDashboardScope,
  resetDashboardScope,
  setDashboardScope,
  subscribeDashboardScope,
} from './dashboardScope'
import { getYesterday } from '../utils/dates'

beforeEach(() => {
  resetDashboardScope()
})

describe('dashboardScope', () => {
  it('starts on the dashboard\'s own defaults', () => {
    expect(getDashboardScope()).toEqual({ date: getYesterday(), starredOnly: true })
  })

  it('keeps the same object until something changes', () => {
    const before = getDashboardScope()
    setDashboardScope({ date: before.date })
    expect(getDashboardScope()).toBe(before)
  })

  it('notifies the subscribers when the scope changes', () => {
    const seen = vi.fn()
    const unsubscribe = subscribeDashboardScope(seen)

    setDashboardScope({ starredOnly: false })
    expect(seen).toHaveBeenCalledTimes(1)
    expect(getDashboardScope().starredOnly).toBe(false)

    setDashboardScope({ starredOnly: false })
    expect(seen).toHaveBeenCalledTimes(1)

    unsubscribe()
    setDashboardScope({ starredOnly: true })
    expect(seen).toHaveBeenCalledTimes(1)
  })
})
