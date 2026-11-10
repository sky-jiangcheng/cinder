import { useTranslation } from 'react-i18next'
import { useEffect, useState } from 'react'
import { getStatusBar, type StatusBarData } from '../api/client'
import { getConnectionKind, subscribeConnection } from '../api/client'
import s from './StatusBar.module.css'

// 预留钩子类：`status-warn` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

export default function StatusBar() {
  const { t, i18n } = useTranslation()
  const [data, setData] = useState<StatusBarData | null>(null)
  const [connKind, setConnKind] = useState(getConnectionKind())
  const [loadFailed, setLoadFailed] = useState(false)

  useEffect(() => {
    let cancelled = false
    const fetch = () => {
      getStatusBar()
        .then(d => { if (!cancelled) { setData(d); setLoadFailed(false) } })
        .catch((e: unknown) => {
          if (cancelled) return
          console.error('[StatusBar] fetch failed:', e)
          setLoadFailed(true)
        })
    }
    fetch()
    const timer = setInterval(fetch, 30000)
    return () => { cancelled = true; clearInterval(timer) }
  }, [])

  useEffect(() => {
    return subscribeConnection(setConnKind)
  }, [])

  // Update current time every second locally
  const [now, setNow] = useState(new Date())
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(timer)
  }, [])

  // The clock follows the interface language. The language instance comes
  // from the translation hook, so the clock follows a language switch too.
  const locale = i18n?.resolvedLanguage || i18n?.language || 'zh-CN'
  const currentTime = now.toLocaleString(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })

  const statusText = connKind === 'offline'
    ? t('status.offline', { defaultValue: 'Offline' })
    : connKind === 'backend-down'
      ? t('status.backendDown', { defaultValue: 'Backend unavailable' })
      : null

  return (
    <div className={s.statusBar}>
      <div className={s.statusLeft}>
        <span className={s.statusItem}>
          <span className={`${s.statusDot} ${connKind !== 'ok' ? s.statusDotError : ''}`} />
          <span className="status-warn" aria-live="polite">{statusText ?? (loadFailed ? t('common.loadFailed') : '')}</span>
          {t('status.time')}{currentTime}
        </span>
      </div>
      <div className={s.statusRight}>
        {data?.last_commit_time ? (
          <>
            <span className={s.statusItem} title={data.last_commit_msg}>
              {t('status.lastCommit')}<strong>{data.last_commit_time}</strong>
            </span>
            <span className={s.statusSeparator}>|</span>
            <span className={s.statusItem}>
              {t('status.project')}<strong>{data.last_commit_repo}</strong>
            </span>
            <span className={s.statusSeparator}>|</span>
            <span className={s.statusItem}>
              {t('status.branch')}<strong>{data.last_commit_branch || 'unknown'}</strong>
            </span>
          </>
        ) : (
          <span className={`${s.statusItem} muted`}>{t('status.noCommits')}</span>
        )}
      </div>
    </div>
  )
}
