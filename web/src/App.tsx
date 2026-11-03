import { lazy, Suspense, type CSSProperties, type ReactNode, useEffect, useState } from 'react'
import { BrowserRouter, HashRouter, Navigate, Routes, Route, useLocation, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import CommandPalette from './components/CommandPalette'
import ToastHost, { type ToastItem } from './components/Toast'
import { listenImportCompleted } from './api/transport'
import { applyTheme, getStoredTheme, listenSystemTheme, storeTheme, type ThemeMode } from './utils/theme'
import ErrorBoundary from './components/ErrorBoundary'
import { getConnectionKind, subscribeConnection, startHealthPoll, isDesktopRuntime } from './api/transport'
import { startUpdateCheck } from './api/updateStatus'
import AgentBot from './components/AgentBot'
import UpdateDownloadUI from './components/UpdateDownloadUI'
import SideRail from './components/SideRail'
import ContextRail from './components/ContextRail'
import { ToastContext } from './hooks/useToast'
import s from './App.module.css'

// Lazy-loaded pages. The initial route (Home, the ADR-0038 full-page console:
// one question box that also searches) is still eagerly loaded by the browser,
// but subsequent navigations only fetch the chunks that are needed.
const Home = lazy(() => import('./pages/Home'))
const Dashboard = lazy(() => import('./pages/Dashboard'))
const ProjectDetail = lazy(() => import('./pages/ProjectDetail'))
const Settings = lazy(() => import('./pages/Settings'))
const Knowledge = lazy(() => import('./pages/Knowledge'))
const WikiDetail = lazy(() => import('./pages/wiki/WikiDetail'))
const Review = lazy(() => import('./pages/Review'))
const NotFound = lazy(() => import('./pages/NotFound'))

/**
 * The old top-level search page is now the console's "search" mode
 * (ADR-0038 decision 1). The route stays as a redirect so the deep links
 * `/search?q=x` keep working — the query travels along to `/?q=x`, where the
 * console picks it up and runs the search.
 */
function SearchRedirect() {
  const [searchParams] = useSearchParams()
  const q = searchParams.get('q')
  return <Navigate to={q ? `/?q=${encodeURIComponent(q)}` : '/'} replace />
}

/**
 * The old top-level wiki page is now the "pages" view of the Knowledge tab
 * (ADR-0038 decision 4). The route stays as a redirect: `/wiki?project=3`
 * becomes `/knowledge?view=pages&project=3`, so the scope survives the jump.
 */
function WikiRedirect() {
  const [searchParams] = useSearchParams()
  const params = new URLSearchParams(searchParams)
  params.set('view', 'pages')
  return <Navigate to={`/knowledge?${params.toString()}`} replace />
}

// Minimal page loader shown while a lazy chunk is being fetched.
function PageLoader() {
  return (
    <div className="panel-section">
      <div className="skeleton skeleton-text" style={{ height: 20, width: 180, marginBottom: 12 }} />
      <div className="skeleton skeleton-text" style={{ height: 14, width: '60%', marginBottom: 24 }} />
      <div className="skeleton skeleton-text" style={{ height: 120, width: '100%' }} />
    </div>
  )
}

// The macOS build hides the window's title bar (see main.go), so the traffic
// lights are drawn over the top-left of the webview and the left rail has to
// leave room for them. Everywhere else the native frame keeps them out of
// the content entirely.
const MAC_DESKTOP =
  isDesktopRuntime() && typeof navigator !== 'undefined' && /Macintosh|Mac OS X/i.test(navigator.userAgent)

// The right rail remembers whether it was folded away. The state lives here
// rather than inside the rail because the floating action button anchors
// itself to the edge of the centre column, and that depends on the rail's
// width.
const CONTEXT_RAIL_KEY = 'repotory:context-rail-collapsed'

function readContextRailCollapsed(): boolean {
  try {
    return localStorage.getItem(CONTEXT_RAIL_KEY) === '1'
  } catch {
    return false
  }
}

function AppRouter({ children }: { children: ReactNode }) {
  // Wails WebViews can reject BrowserRouter's history/location mutations on
  // custom origins. Keep normal browser URLs when served in a browser, but
  // use hash URLs in the desktop shell so navigation never throws a
  // DOMException.
  const wails = typeof window !== 'undefined' && !!(window as unknown as { go?: { app?: { App?: unknown } } }).go?.app?.App
  const Router = wails ? HashRouter : BrowserRouter
  return <Router>{children}</Router>
}

function App() {
  const { t } = useTranslation()
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [toasts, setToasts] = useState<ToastItem[]>([])
  const [connKind, setConnKind] = useState(getConnectionKind())
  const [themeMode, setThemeMode] = useState<ThemeMode>(getStoredTheme())
  const [contextCollapsed, setContextCollapsed] = useState(readContextRailCollapsed)

  // Start periodic health polling on mount.
  useEffect(() => {
    return startHealthPoll()
  }, [])

  // React to connection state changes.
  useEffect(() => {
    return subscribeConnection(setConnKind)
  }, [])

  // One background update check per launch (desktop only, and only when the
  // user left it enabled). Failure is silent — see api/updateStatus.ts.
  useEffect(() => {
    startUpdateCheck()
  }, [])

  // Focus main without touching location (the active router owns navigation).
  const skipToContent = (e: React.MouseEvent<HTMLAnchorElement>) => {
    e.preventDefault()
    const main = document.getElementById('main-content')
    main?.focus()
    main?.scrollIntoView({ block: 'start' })
  }

  // pushToast owns the per-toast auto-dismiss timer (the only scheduling
  // site — ToastHost renders purely).
  const pushToast = (item: Omit<ToastItem, 'id'>) => {
    const id = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    setToasts(prev => [...prev, { ...item, id }])
    setTimeout(() => setToasts(prev => prev.filter(x => x.id !== id)), item.duration ?? 5000)
  }

  useEffect(() => {
    const mode = getStoredTheme()
    applyTheme(mode)
    return listenSystemTheme(() => {
      if (getStoredTheme() === 'system') applyTheme('system')
    })
  }, [])

  const changeTheme = (mode: ThemeMode) => {
    setThemeMode(mode)
    storeTheme(mode)
    applyTheme(mode)
  }

  const toggleContextRail = (collapsed: boolean) => {
    setContextCollapsed(collapsed)
    try {
      localStorage.setItem(CONTEXT_RAIL_KEY, collapsed ? '1' : '0')
    } catch {
      // A rail that forgets its state is not worth an error.
    }
  }

  useEffect(() => {
    // Surface knowledge import results as toasts (issue #36). The listener
    // is really unsubscribed now that transport returns the cancel function.
    const unsubscribe = listenImportCompleted((data) => {
      if (data.error) {
        pushToast({ kind: 'error', title: `${t('common.importFailed', { defaultValue: 'Import failed' })} ${data.source}`, message: data.error })
        return
      }
      if (data.created === 0 && data.updated === 0 && data.skipped === 0) {
        return
      }
      pushToast({
        kind: 'success',
        title: `${t('common.imported', { defaultValue: 'Imported' })} ${data.source}`,
        message: `${t('common.add', { defaultValue: '+' })}${data.created} · ${t('common.edit', { defaultValue: '~' })}${data.updated} · ${t('common.remove', { defaultValue: '-' })}${data.skipped}`,
      })
    })
    return unsubscribe
  }, [t])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen(o => !o)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  const connBanner = connKind === 'offline'
    ? t('status.offline', { defaultValue: 'Offline' })
    : connKind === 'backend-down'
      ? t('status.backendDown', { defaultValue: 'Backend unavailable' })
      : null

  return (
    <AppRouter>
      {/* Lets any descendant confirm a side effect (copy path, save AI answer)
          without App having to thread a callback down to it. */}
      <ToastContext.Provider value={pushToast}>
        <div
          className="app"
          style={{ '--context-rail-offset': contextCollapsed ? 'var(--context-rail-collapsed-width)' : 'var(--context-rail-width)' } as CSSProperties}
        >
          {connBanner && (
            <div className="conn-banner" role="alert" aria-live="polite">{connBanner}</div>
          )}
          <a className={s.skipLink} href="#main-content" onClick={skipToContent}>{t('common.show', { defaultValue: 'Skip to main content' })}</a>
          <div className={`${s.shell} ${MAC_DESKTOP ? s.shellWithControls : ''}`}>
            <SideRail
              themeMode={themeMode}
              onThemeChange={changeTheme}
            />
            <div className={s.mainColumn}>
              <div className={s.titleStrip} aria-hidden="true" />
              <main id="main-content" className={s.mainContent} tabIndex={-1}>
                <RoutedApp />
              </main>
            </div>
            <ContextRail collapsed={contextCollapsed} onToggle={toggleContextRail} />
          </div>
          <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
          <ToastHost toasts={toasts} onDismiss={(id) => setToasts(prev => prev.filter(x => x.id !== id))} />
          {/* The floating agent: a draggable bot that knows the page it is on.
              It replaces the old capture button, which sat on top of the chat
              page's send button. */}
          <AgentBot onToast={pushToast} />
          {/* The update download's progress surfaces live at the app level:
              the download survives navigation, and a progress indicator that
              disappears when the user leaves the settings page is not a
              progress indicator. */}
          <UpdateDownloadUI />
        </div>
      </ToastContext.Provider>
    </AppRouter>
  )
}

// RoutedApp wraps the routes in an ErrorBoundary keyed to the current path, so a
// crash in a destination page (e.g. clicking a scan result) shows a recoverable
// error instead of a black screen, and recovers when the route changes.
// Suspense is placed OUTSIDE the ErrorBoundary so a failed chunk load is also
// caught and displayed as a recoverable error rather than a blank page.
function RoutedApp() {
  const { pathname } = useLocation()
  return (
    <ErrorBoundary resetKey={pathname}>
      <Suspense fallback={<PageLoader />}>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/dashboard" element={<Dashboard />} />
          {/* Search and the wiki list are no longer top-level tabs (ADR-0038).
              Both routes survive as redirects so old bookmarks and the IDE
              plugins' deep links keep landing on the right content. */}
          <Route path="/search" element={<SearchRedirect />} />
          <Route path="/project/:id" element={<ProjectDetail />} />
          <Route path="/knowledge" element={<Knowledge />} />
          {/* The approved knowledge layer, read by a person (ADR-0032). The
              detail route is separate so a page has an address of its own:
              /wiki/:id can be deep-linked and opened from the IDE plugins. */}
          <Route path="/wiki" element={<WikiRedirect />} />
          <Route path="/wiki/:id" element={<WikiDetail />} />
          <Route path="/review" element={<Review />} />
          <Route path="/settings" element={<Settings />} />
          {/* Catch-all: without it an unmatched URL renders nothing at all —
              a blank page below the navbar that reads like a crash. */}
          <Route path="*" element={<NotFound />} />
        </Routes>
      </Suspense>
    </ErrorBoundary>
  )
}

export default App
