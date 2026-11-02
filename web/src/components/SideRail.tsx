// components/SideRail.tsx — the narrow left rail of the desktop shell.
//
// The shell is left / middle / right: this rail owns navigation, the window
// controls' corner (on macOS the title bar is hidden and the traffic lights
// land on top of the rail's top band) and the two app-wide switches.
//
// It is an icon rail on purpose. The old navbar spent its width on seven text
// labels plus a wordmark, three of which were the same word in two languages
// ("搜索" appeared twice: as the nav entry and as the search pill). At 72px the
// rail carries the brand mark, the section icons, and the theme/language
// switches — and the label of every item moves to a tooltip, which costs
// nothing until it is needed.
//
// The five entries are the whole top level (ADR-0038 decision 1): Home is the
// console that both asks and searches, Knowledge holds notes and pages, and
// the dashboard owns every number. The search page and the wiki list are not
// entries any more — they are a mode and a view inside those two.

import { useEffect, useRef, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon, { type IconName } from './Icon'
import BrandMark from './BrandMark'
import { getUpdateState, subscribeUpdateState } from '../api/updateStatus'
import { getCurrentLanguage, setLanguage } from '../i18n'
import type { ThemeMode } from '../utils/theme'
import s from './SideRail.module.css'

interface NavEntry {
  to: string
  labelKey: string
  icon: IconName
  active: (pathname: string) => boolean
}

const NAV_ENTRIES: NavEntry[] = [
  { to: '/', labelKey: 'nav.home', icon: 'home', active: p => p === '/' },
  { to: '/dashboard', labelKey: 'nav.dashboard', icon: 'grid', active: p => p === '/dashboard' },
  { to: '/knowledge', labelKey: 'nav.knowledge', icon: 'book', active: p => p.startsWith('/knowledge') },
  { to: '/review', labelKey: 'nav.review', icon: 'check', active: p => p.startsWith('/review') },
  { to: '/settings', labelKey: 'nav.settings', icon: 'settings', active: p => p.startsWith('/settings') },
]

const THEME_OPTIONS: { value: ThemeMode; labelKey: string; icon: IconName }[] = [
  { value: 'light', labelKey: 'nav.themes.light', icon: 'sun' },
  { value: 'dark', labelKey: 'nav.themes.dark', icon: 'moon' },
  { value: 'system', labelKey: 'nav.themes.system', icon: 'monitor' },
]

interface Props {
  themeMode: ThemeMode
  onThemeChange: (mode: ThemeMode) => void
}

export default function SideRail({ themeMode, onThemeChange }: Props) {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  const [langOpen, setLangOpen] = useState(false)
  const [themeOpen, setThemeOpen] = useState(false)
  const langRef = useRef<HTMLDivElement>(null)
  const themeRef = useRef<HTMLDivElement>(null)
  const currentLang = getCurrentLanguage()

  // A newer release exists -> a dot on the Settings icon. The dot is a
  // pointer, not a nag: the Settings → Updates tab carries the detail.
  const [updateState, setUpdateState] = useState(getUpdateState())

  useEffect(() => subscribeUpdateState(setUpdateState), [])

  useEffect(() => {
    if (!langOpen) return
    const handler = (e: MouseEvent) => {
      if (langRef.current && !langRef.current.contains(e.target as Node)) setLangOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [langOpen])

  useEffect(() => {
    if (!themeOpen) return
    const handler = (e: MouseEvent) => {
      if (themeRef.current && !themeRef.current.contains(e.target as Node)) setThemeOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [themeOpen])

  const themeIcon: IconName = themeMode === 'light' ? 'sun' : themeMode === 'dark' ? 'moon' : 'monitor'

  return (
    <nav className={s.rail} aria-label={t('nav.main')}>
      {/* The brand mark doubles as the rail's top edge. The window's drag
          surface is the strip above the centre column, so the corner that
          holds the native window buttons stays free of webview handles. */}
      <Link to="/" className={s.brand} aria-label={t('nav.home')}>
        <BrandMark size={26} className={s.brandMark} />
      </Link>

      <div className={s.navGroup}>
        {NAV_ENTRIES.map(entry => {
          const label = t(entry.labelKey)
          const isActive = entry.active(pathname)
          return (
            <Link
              key={entry.to}
              to={entry.to}
              className={`${s.item} ${isActive ? s.itemActive : ''}`}
              aria-label={label}
              aria-current={isActive ? 'page' : undefined}
              data-tip={label}
            >
              <Icon name={entry.icon} size={18} />
              {entry.to === '/settings' && updateState.phase === 'available' && (
                <span
                  className={s.updateDot}
                  title={t('nav.updateAvailable', { version: updateState.info?.latest ?? '' })}
                />
              )}
            </Link>
          )
        })}
      </div>

      <div className={s.railFoot}>
        <div className={s.switcher} ref={themeRef}>
          <button
            type="button"
            className={s.item}
            onClick={() => setThemeOpen(v => !v)}
            aria-label={t('nav.theme')}
            aria-expanded={themeOpen}
            data-tip={t('nav.theme')}
          >
            <Icon name={themeIcon} size={18} />
          </button>
          {themeOpen && (
            <div className={s.menu} role="menu">
              {THEME_OPTIONS.map(opt => (
                <button
                  key={opt.value}
                  type="button"
                  role="menuitemradio"
                  aria-checked={themeMode === opt.value}
                  className={`${s.menuItem} ${themeMode === opt.value ? s.menuItemActive : ''}`}
                  onClick={() => { onThemeChange(opt.value); setThemeOpen(false) }}
                >
                  <Icon name={opt.icon} size={14} />
                  <span>{t(opt.labelKey)}</span>
                </button>
              ))}
            </div>
          )}
        </div>

        <div className={s.switcher} ref={langRef}>
          <button
            type="button"
            className={s.item}
            onClick={() => setLangOpen(v => !v)}
            aria-label={t('nav.language')}
            aria-expanded={langOpen}
            data-tip={t('nav.language')}
          >
            <Icon name="globe" size={18} />
          </button>
          {langOpen && (
            <div className={s.menu} role="menu">
              <button
                type="button"
                role="menuitemradio"
                aria-checked={currentLang === 'zh-CN'}
                className={`${s.menuItem} ${currentLang === 'zh-CN' ? s.menuItemActive : ''}`}
                onClick={() => { setLanguage('zh-CN'); setLangOpen(false) }}
              >
                <span>中文</span>
              </button>
              <button
                type="button"
                role="menuitemradio"
                aria-checked={currentLang === 'en'}
                className={`${s.menuItem} ${currentLang === 'en' ? s.menuItemActive : ''}`}
                onClick={() => { setLanguage('en'); setLangOpen(false) }}
              >
                <span>English</span>
              </button>
            </div>
          )}
        </div>
      </div>
    </nav>
  )
}
