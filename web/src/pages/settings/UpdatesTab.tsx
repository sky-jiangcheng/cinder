import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import {
  beginUpdateDownload,
  getAppVersion,
  getInstallInfo,
  getUpdateState,
  openBrowserURL,
  runUpdateCheck,
  subscribeUpdateState,
  updateConfig,
  type InstallInfo,
  type UpdateState,
} from '../../api/client'
import s from './UpdatesTab.module.css'

interface Props {
  // Full config map from the settings page: the update card's two switches
  // (auto-check, proxy) live under `update_*` keys, so the whole map is the
  // least-brittle way to seed them.
  initialConfig: Record<string, string>
  showMessage: (msg: string) => void
}

/**
 * Version update card.
 *
 * The check is advisory: it reports what GitHub published and nothing more.
 * The DOWNLOAD it can start is a different thing, and the difference matters:
 * the app fetches the bundle into a local file and verifies its SHA256
 * against the digest the release published, so the user gets progress
 * feedback and a checksum verdict instead of a browser download with
 * neither.
 *
 * What the app still does NOT do is install anything. Nothing here writes
 * into the directory the running binary lives in; the verified file is
 * handed to the OS installer (mount the .dmg, reveal the .zip) and the
 * user performs the install with the same consent they give a browser
 * download. The button that says "download" says exactly that.
 *
 * The verdict comes from the shared store (api/updateStatus.ts), the same one
 * the navigation badge reads. One check, one answer: a badge saying
 * "up to date" next to a card saying "2.13.0 available" would be worse than
 * either alone.
 */
export default function UpdatesTab({ initialConfig, showMessage }: Props) {
  const { t } = useTranslation()
  const [state, setState] = useState<UpdateState>(getUpdateState())
  const [autoCheck, setAutoCheck] = useState(initialConfig.update_check_enabled !== '0')
  const [proxy, setProxy] = useState(initialConfig.update_proxy ?? '')
  const [saving, setSaving] = useState(false)
  const [startingDownload, setStartingDownload] = useState(false)
  // The running build's version, read locally. A card that could only state
  // your version AFTER a round trip to GitHub would sit blank on a machine
  // that cannot reach it — which is exactly the machine that needs the proxy
  // field below.
  const [currentVersion, setCurrentVersion] = useState('')
  // Where this build is installed and where update bundles are written.
  // Both questions used to have no answer inside the app.
  const [installInfo, setInstallInfo] = useState<InstallInfo | null>(null)

  useEffect(() => subscribeUpdateState(setState), [])
  useEffect(() => {
    getAppVersion().then(setCurrentVersion)
  }, [])
  useEffect(() => {
    getInstallInfo().then(setInstallInfo).catch(() => undefined)
  }, [])

  const checking = state.phase === 'checking'
  const info = state.info
  const error = state.phase === 'error' ? state.error : ''

  const handleCheck = async () => {
    try {
      await runUpdateCheck()
    } catch {
      // The store already carries the message; the card renders it below.
    }
  }

  const handleAutoCheckToggle = (on: boolean) => {
    setAutoCheck(on)
    void persist({ update_check_enabled: on ? '1' : '0' },
      on ? t('settings.updates.autoCheckOn') : t('settings.updates.autoCheckOff'))
  }

  const handleProxySave = () => {
    void persist({ update_proxy: proxy.trim() }, t('settings.updates.proxySaved'))
  }

  const persist = async (kv: Record<string, string>, ok: string) => {
    setSaving(true)
    try {
      for (const [k, v] of Object.entries(kv)) {
        await updateConfig(k, v)
      }
      showMessage(ok)
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setSaving(false)
    }
  }

  // The platform bundle when the release ships one for this machine, the
  // release page otherwise — a release without a build for this platform is
  // not an error, the page is the right target then.
  const handleDownload = async () => {
    if (!info?.download_url || !info.download_name) {
      // No bundle for this platform: the release page is the only target.
      if (info?.release_url) {
        try {
          await openBrowserURL(info.release_url)
        } catch (e: unknown) {
          showMessage(t('settings.updates.openFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
        }
      }
      return
    }
    setStartingDownload(true)
    try {
      // The version travels with the request: it goes into the file name
      // (so the bundle in Downloads says which release it is) and into the
      // progress snapshot (so the dialog names it too).
      await beginUpdateDownload(info.download_url, info.download_name, info.download_sha256 ?? '', info.latest)
      showMessage(t('settings.updates.downloadStarted'))
    } catch (e: unknown) {
      showMessage(t('settings.updates.downloadStartFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setStartingDownload(false)
    }
  }

  return (
    <div className="settings-section">
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.updates.title')}</h2>
        <p className="section-desc">{t('settings.updates.desc')}</p>

        <div className={s.actionRow}>
          <button className="btn btn-primary" onClick={handleCheck} disabled={checking}>
            {checking ? t('settings.updates.checking') : t('settings.updates.check')}
          </button>
          {currentVersion && (
            <span className={s.versionLine}>
              {t('settings.updates.currentVersion', { version: currentVersion })}
              {info?.has_update && (
                <span className={s.latestBadge}>{t('settings.updates.latestVersion', { version: info.latest })}</span>
              )}
            </span>
          )}
        </div>

        {error && (
          <p className={s.error} role="alert">
            {t('settings.updates.checkFailed', { msg: error })}
          </p>
        )}

        {state.phase === 'current' && !error && (
          <p className={s.upToDate}>{t('settings.updates.upToDate')}</p>
        )}

        {info?.has_update && (
          <div className={s.updateCard}>
            <div className={s.cardHead}>
              <span className={s.cardTitle}>
                {t('settings.updates.newVersion', { version: info.latest })}
              </span>
              {info.published_at && (
                <span className={s.publishedAt}>
                  {t('settings.updates.publishedAt', { date: formatDate(info.published_at) })}
                </span>
              )}
            </div>

            {info.notes && (
              <details className={s.notes}>
                <summary>{t('settings.updates.notesTitle')}</summary>
                {/* Release notes are Markdown from a GitHub API response — the
                    same renderer the knowledge cards use, which sanitizes the
                    HTML. */}
                <div className={`${s.notesBody} markdown-body`} dangerouslySetInnerHTML={{ __html: renderNotes(info.notes) }} />
              </details>
            )}

            <div className={s.actionRow}>
              <button className="btn btn-primary" onClick={handleDownload} disabled={startingDownload}>
                {startingDownload ? t('settings.updates.downloadStarting') : t('settings.updates.download')}
              </button>
              {info.download_name && (
                <span className={s.assetName}>{info.download_name}</span>
              )}
              <button
                className="btn btn-secondary"
                onClick={() => info.release_url && openBrowserURL(info.release_url)}
                disabled={!info.release_url}
              >
                {t('settings.updates.viewRelease')}
              </button>
            </div>
            {info.download_name && (
              <p className={s.downloadNote}>{t('settings.updates.downloadNote')}</p>
            )}
          </div>
        )}
      </section>

      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.updates.prefsTitle')}</h2>
        <div className="form-group">
          <label id="settings-update-autocheck-label">{t('settings.updates.autoCheckLabel')}</label>
          <div className="toggle-row">
            <button
              className={`${s.toggle} ${autoCheck ? s.toggleOn : ''}`}
              onClick={() => handleAutoCheckToggle(!autoCheck)}
              disabled={saving}
              aria-pressed={autoCheck}
              aria-labelledby="settings-update-autocheck-label"
            >
              <span className={s.toggleKnob} />
            </button>
            <span className="form-hint" style={{ marginTop: 0 }}>
              {autoCheck ? t('settings.updates.autoCheckOnHint') : t('settings.updates.autoCheckOffHint')}
            </span>
          </div>
        </div>
        <div className="form-group">
          <label htmlFor="update-proxy">{t('settings.updates.proxyLabel')}</label>
          <div className="input-row">
            <input
              id="update-proxy"
              className="form-input"
              type="text"
              placeholder="http://127.0.0.1:7890"
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
              disabled={saving}
            />
            <button className="btn btn-secondary btn-sm" onClick={handleProxySave} disabled={saving}>
              {saving ? t('settings.saving') : t('settings.save')}
            </button>
          </div>
          <p className="form-hint">{t('settings.updates.proxyHint')}</p>
        </div>
      </section>

      {/* Where this build lives and where bundles are written. A user who
          installed from a DMG and then asked "where did it install?" had no
          answer to look at; the app knew and never said. */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.updates.installInfoTitle')}</h2>
        <div className={s.pathList}>
          <div className={s.pathItem}>
            <span className={s.pathLabel}>{t('settings.updates.installPathLabel')}</span>
            <span className={s.pathValue}>{installInfo?.install_path || t('settings.updates.pathUnknown')}</span>
          </div>
          <div className={s.pathItem}>
            <span className={s.pathLabel}>{t('settings.updates.downloadDirLabel')}</span>
            <span className={s.pathValue}>{installInfo?.download_dir || t('settings.updates.pathUnknown')}</span>
          </div>
        </div>
      </section>
    </div>
  )
}

/** RFC 3339 timestamp -> YYYY-MM-DD. Falls back to the raw string. */
function formatDate(raw: string): string {
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// The release body is Markdown from the GitHub API. marked + DOMPurify are
// used directly rather than the app's full Markdown pipeline (utils/markdown):
// that one also pulls in KaTeX and highlight.js, which a release note has no
// use for and which would land in this tab's lazy chunk.
function renderNotes(notes: string): string {
  const raw = marked.parse(notes, { async: false, gfm: true, breaks: false })
  const html = typeof raw === 'string' ? raw : ''
  return DOMPurify.sanitize(html)
}
