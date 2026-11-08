import { useState, useEffect, useRef, useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { getConfig, type AppConfig } from '../api/client'
import { getStoredTheme, type ThemeMode } from '../utils/theme'
import ScanRootsTab from './settings/ScanRootsTab'
import StandardsTab from './settings/StandardsTab'
import AiTab from './settings/AiTab'
import AuthorsTab from './settings/AuthorsTab'
import AppearanceTab from './settings/AppearanceTab'
import PluginsTab from './settings/PluginsTab'
import ActionsTab from './settings/ActionsTab'
import UpdatesTab from './settings/UpdatesTab'
import ReviewTab from './settings/ReviewTab'
import ErrorBanner from '../components/ErrorBanner'
import PageShell from '../components/PageShell'
import s from './Settings.module.css'

// 预留钩子类：`settings` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

type TabKey = 'scan' | 'standards' | 'authors' | 'appearance' | 'plugins' | 'ai' | 'review' | 'actions' | 'updates'
const TAB_KEYS: TabKey[] = ['scan', 'standards', 'authors', 'appearance', 'plugins', 'ai', 'review', 'actions', 'updates']

const isTabKey = (v: string | null): v is TabKey => !!v && (TAB_KEYS as string[]).includes(v)

function Settings() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()
  const [data, setData] = useState<AppConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [themeMode, setThemeMode] = useState<ThemeMode>('system')
  const [message, setMessage] = useState('')
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  // The active tab lives in the URL (?tab=…) so a reload, a bookmark or a
  // shared link lands on the same section instead of silently resetting to the
  // first one. An unknown/missing value falls back to 'scan'.
  const rawTab = searchParams.get('tab')
  const tab: TabKey = isTabKey(rawTab) ? rawTab : 'scan'
  const selectTab = useCallback((key: TabKey) => {
    setSearchParams({ tab: key }, { replace: true })
  }, [setSearchParams])

  const showMessage = (msg: string) => {
    setMessage(msg)
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => setMessage(''), 3000)
  }

  // Single load path for the initial fetch and the retry button — previously
  // the inline retry duplicated the catch/finally body and forgot to reset
  // loading, so a failed retry left the page in a half-state.
  const loadConfig = () => {
    setLoading(true)
    setError('')
    getConfig()
      .then(setData)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : t('common.failed')))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    setThemeMode(getStoredTheme()) // eslint-disable-line react-hooks/set-state-in-effect
    loadConfig()
    return () => { if (timerRef.current) clearTimeout(timerRef.current) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t])

  if (loading) {
    return (
      <div className="settings">
        <div className="skeleton skeleton-text" style={{ width: 180, height: 26, marginBottom: 10 }} />
        <div className="skeleton skeleton-text" style={{ width: 280, height: 14, marginBottom: 28 }} />
        <div className="skeleton skeleton-text" style={{ width: '100%', height: 24, marginBottom: 12 }} />
        <div className="skeleton skeleton-text" style={{ width: '100%', height: 64, marginBottom: 8 }} />
        <div className="skeleton skeleton-text" style={{ width: '100%', height: 64, marginBottom: 8 }} />
      </div>
    )
  }

  if (error) {
    return (
      <div className="settings">
        <h1 className="visually-hidden">{t('settings.title')}</h1>
        <ErrorBanner message={error} onRetry={loadConfig} />
      </div>
    )
  }

  return (
    <PageShell
      header={
        <>
          {/* The nav bar already says "设置" and the strip below says which
              section is open, so a page title would be the same words twice. The
              h1 stays for screen readers and the document outline. */}
          <h1 className="visually-hidden">{t('settings.title')}</h1>

          {message && <div className="message-banner" role="status">{message}</div>}

          {/* Tabs are a real tablist: arrow-key roving focus + aria wiring, so the
              section switch is navigable without a pointer. The strip is chrome:
              it lives in the pinned header, so the section it switches stays
              reachable while a long panel scrolls underneath it. */}
          <div className={`${s.settingsTabs}`} role="tablist" aria-label={t('settings.title')}>
            {TAB_KEYS.map((key) => (
              <button
                key={key}
                role="tab"
                id={`settings-tab-${key}`}
                aria-selected={tab === key}
                aria-controls={`settings-panel-${key}`}
                tabIndex={tab === key ? 0 : -1}
                className={`tab-btn ${tab === key ? 'tab-active' : ''}`}
                onClick={() => selectTab(key)}
                onKeyDown={(e) => {
                  const i = TAB_KEYS.indexOf(key)
                  if (e.key === 'ArrowRight') selectTab(TAB_KEYS[(i + 1) % TAB_KEYS.length])
                  else if (e.key === 'ArrowLeft') selectTab(TAB_KEYS[(i - 1 + TAB_KEYS.length) % TAB_KEYS.length])
                  else if (e.key === 'Home') selectTab(TAB_KEYS[0])
                  else if (e.key === 'End') selectTab(TAB_KEYS[TAB_KEYS.length - 1])
                  else return
                  e.preventDefault()
                }}
              >
                {t(`settings.tabs.${key}`)}
              </button>
            ))}
          </div>
        </>
      }
    >
      {/* Only the active panel is mounted; the wrapper carries the a11y wiring
          for the tablist above. The one line under the strip says what the
          open section is for — the tab label names it, this explains it, and
          the two are adjacent so the pairing is unambiguous. */}
      <div role="tabpanel" id={`settings-panel-${tab}`} aria-labelledby={`settings-tab-${tab}`}>
        <p className="section-head-desc">
          {t(`settings.tabDesc.${tab}`, { defaultValue: t('settings.subtitle') })}
        </p>
        {tab === 'scan' && (
          <ScanRootsTab data={data} onChange={setData} showMessage={showMessage} />
        )}
        {tab === 'standards' && data && (
          <StandardsTab
            key="standards"
            initialStandard={data.config.daily_code_standard || '500'}
            initialDepth={data.config.scan_depth || '2'}
            showMessage={showMessage}
          />
        )}
        {tab === 'authors' && data && (
          <AuthorsTab
            key="authors"
            initialAuthor={data.config.git_author || ''}
            showMessage={showMessage}
          />
        )}
        {tab === 'appearance' && (
          <AppearanceTab themeMode={themeMode} onThemeChange={setThemeMode} showMessage={showMessage} />
        )}
        {tab === 'plugins' && data && (
          <PluginsTab key="plugins" initialAutoImport={data.config.auto_import !== '0'} initialClaudeCapture={data.config.claude_session_capture === '1'} initialTargets={{ openclaw_project: data.config.openclaw_project || '', hermes_project: data.config.hermes_project || '', codebuddy_project: data.config.codebuddy_project || '' }} initialConfig={data.config} showMessage={showMessage} />
        )}
        {tab === 'ai' && data && (
          <div role="tabpanel" id="settings-panel-ai" aria-labelledby="settings-tab-ai">
            <AiTab config={data.config} showMessage={showMessage} onSaved={loadConfig} />
          </div>
        )}

        {tab === 'review' && <ReviewTab showMessage={showMessage} />}

        {tab === 'actions' && data && <ActionsTab initialConfig={data.config} showMessage={showMessage} />}
        {tab === 'updates' && data && (
          <UpdatesTab initialConfig={data.config} showMessage={showMessage} />
        )}
      </div>
    </PageShell>
  )
}

export default Settings
