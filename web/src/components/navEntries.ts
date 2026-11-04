// components/navEntries.ts — the app's five top-level entries.
//
// The list lives here, not in SideRail, because TWO consumers need it: the
// left rail that draws it and the agent bot's module chips (ADR-0045
// decision 3). Two copies would drift, and a drifted list means the bot
// and the rail point at different modules — the bot would offer a module
// the app has no route for.
//
// It is pure data + a predicate, so it can be asserted without a browser.

import type { IconName } from './Icon'

export interface NavEntry {
  to: string
  labelKey: string
  icon: IconName
  active: (pathname: string) => boolean
}

export const NAV_ENTRIES: NavEntry[] = [
  { to: '/', labelKey: 'nav.home', icon: 'home', active: p => p === '/' },
  { to: '/dashboard', labelKey: 'nav.dashboard', icon: 'grid', active: p => p.startsWith('/dashboard') },
  { to: '/knowledge', labelKey: 'nav.knowledge', icon: 'book', active: p => p.startsWith('/knowledge') },
  { to: '/review', labelKey: 'nav.review', icon: 'check', active: p => p.startsWith('/review') },
  { to: '/settings', labelKey: 'nav.settings', icon: 'settings', active: p => p.startsWith('/settings') },
]

/**
 * The entry a path belongs to.
 *
 * A path that belongs to no entry (a project page, say) falls back to the
 * home entry: the bot still has to answer something, and the console is
 * the one place that can answer anything.
 */
export function entryForPath(pathname: string): NavEntry {
  return NAV_ENTRIES.find(e => e.active(pathname)) ?? NAV_ENTRIES[0]
}
