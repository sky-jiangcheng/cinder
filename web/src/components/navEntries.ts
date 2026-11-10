// components/navEntries.ts — the app's six top-level entries.
//
// The list lives here, not inside SideRail, so the active predicates stay
// pure data that can be asserted without a browser.

import type { IconName } from './Icon'

export interface NavEntry {
  to: string
  labelKey: string
  icon: IconName
  active: (pathname: string) => boolean
}

export const NAV_ENTRIES: NavEntry[] = [
  { to: '/', labelKey: 'nav.home', icon: 'home', active: p => p === '/' },
  { to: '/dashboard', labelKey: 'nav.dashboard', icon: 'grid', active: p => p.startsWith('/dashboard') || p.startsWith('/project') },
  { to: '/knowledge', labelKey: 'nav.knowledge', icon: 'book', active: p => p.startsWith('/knowledge') || p.startsWith('/wiki') },
  { to: '/review', labelKey: 'nav.review', icon: 'check', active: p => p.startsWith('/review') },
  { to: '/code-review', labelKey: 'nav.codeReview', icon: 'shield', active: p => p.startsWith('/code-review') },
  { to: '/settings', labelKey: 'nav.settings', icon: 'settings', active: p => p.startsWith('/settings') },
]

/**
 * The entry a path belongs to.
 *
 * A path that belongs to no entry falls back to the home entry: the bot
 * still has to answer something, and the console is the one place that can
 * answer anything.
 */
export function entryForPath(pathname: string): NavEntry {
  return NAV_ENTRIES.find(e => e.active(pathname)) ?? NAV_ENTRIES[0]
}
