// components/UpdateDownloadUI.tsx — the update download's progress surfaces.
//
// Two surfaces, one state:
//   - a MODAL progress dialog, the primary view (a 100 MB bundle takes long
//     enough that the percentage and the ETA need a home that cannot scroll
//     away);
//   - a CORNER CARD, the minimized form. The user can carry on with the app
//     while the bundle arrives, and still see that it is.
//
// Both read the same snapshot from api/downloadStatus.ts, so they can never
// disagree about the progress. The card is what the modal becomes when the
// user hits "minimize"; the modal reappears when they click the card.
//
// The card lives at the app level (App.tsx) rather than inside the settings
// tab: the download survives navigation, and a progress indicator that
// disappears when the user leaves the settings page is not a progress
// indicator.
//
// The finished state is the part that answers the questions a user actually
// has afterwards: WHERE the bundle went (the full path is shown), WHICH
// version it is (the version is in the file name and in the progress header),
// and how to get rid of the mounted installer volume (macOS mounts a disk
// image, and a mounted volume is a second "Repotory" in the Finder sidebar).

import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  abortUpdateDownload,
  ejectInstallerVolume,
  getDownloadSnapshot,
  getInstallInfo,
  isDownloading,
  minimizeDownloadModal,
  openDownloadedUpdate,
  openDownloadModal,
  revealDownloadedUpdate,
  subscribeDownloadState,
  syncDownloadState,
  type DownloadSnapshot,
} from '../api/client'
import Icon from './Icon'
import s from './UpdateDownloadUI.module.css'

/**
 * The download progress UI, mounted once at the app level.
 *
 * It reads the shared snapshot rather than owning any state of its own: the
 * settings tab can start a download, and this component picks it up.
 */
export default function UpdateDownloadUI() {
  const { t } = useTranslation()
  const [snapshot, setSnapshot] = useState<DownloadSnapshot>(getDownloadSnapshot())
  const [platform, setPlatform] = useState('')
  // The eject action's own report (nothing mounted / ejected / failed). It
  // lives here because the backend's verdict only reaches the frontend on the
  // next sync, and the user is waiting right here.
  const [ejectNote, setEjectNote] = useState('')

  useEffect(() => subscribeDownloadState(setSnapshot), [])

  // One read on mount, so a download started before this component mounted
  // (or in another window) is picked up.
  useEffect(() => { void syncDownloadState() }, [])

  // The platform decides whether the "eject the installer volume" action
  // exists at all — only macOS mounts a disk image. It is fetched when a
  // download finishes, not at startup, so the common case costs nothing.
  useEffect(() => {
    if (snapshot.state.phase !== 'done' || platform) return
    let cancelled = false
    void getInstallInfo()
      .then(info => { if (!cancelled) setPlatform(info.platform) })
      .catch(() => undefined)
    return () => { cancelled = true }
  }, [snapshot.state.phase, platform])

  const state = snapshot.state
  const active = isDownloading(state)
  // The card shows while a download runs and the modal is minimized, and
  // stays for a finished download until the user dismisses it — a completed
  // download with an "install" button must not vanish on its own.
  const showCard = !snapshot.modalOpen && (active || state.phase === 'done' || state.phase === 'failed')
  const showModal = snapshot.modalOpen && (active || state.phase === 'done' || state.phase === 'failed' || state.phase === 'canceled')

  const handleReveal = () => {
    void revealDownloadedUpdate(state.path).catch(() => undefined)
  }

  const handleEject = () => {
    void ejectInstallerVolume()
      .then(ejected => {
        // Two different answers, and the user needs to know which one they
        // got: the volume is gone, or there was never one to remove.
        setEjectNote(ejected
          ? t('settings.updates.downloadEjected')
          : t('settings.updates.downloadEjectNone'))
        // The backend records the eject in its own state; re-read it so the
        // button and the note agree.
        void syncDownloadState()
      })
      .catch((e: unknown) => {
        setEjectNote(e instanceof Error ? e.message : String(e))
      })
  }

  // Expanding the card re-reads the backend state: the app may have ejected
  // the installer volume on its own while the user was installing, and that
  // verdict only reaches the frontend on a sync.
  const handleExpand = () => {
    void syncDownloadState()
    openDownloadModal()
  }

  if (!showCard && !showModal) return null

  return (
    <>
      {showModal && (
        <Modal
          snapshot={snapshot}
          platform={platform}
          ejectNote={ejectNote}
          onMinimize={minimizeDownloadModal}
          onCancel={() => { void abortUpdateDownload() }}
          onOpen={() => { void openDownloadedUpdate(state.path) }}
          onReveal={handleReveal}
          onEject={handleEject}
          onDismiss={() => { void abortUpdateDownload() }}
        />
      )}
      {showCard && (
        <CornerCard
          snapshot={snapshot}
          onExpand={handleExpand}
          onCancel={() => { void abortUpdateDownload() }}
        />
      )}
    </>
  )
}

interface ModalProps {
  snapshot: DownloadSnapshot
  platform: string
  ejectNote: string
  onMinimize: () => void
  onCancel: () => void
  onOpen: () => void
  onReveal: () => void
  onEject: () => void
  onDismiss: () => void
}

/** The modal progress dialog. */
function Modal({ snapshot, platform, ejectNote, onMinimize, onCancel, onOpen, onReveal, onEject, onDismiss }: ModalProps) {
  const { t } = useTranslation()
  const state = snapshot.state
  const active = isDownloading(state)

  return (
    <div className={s.overlay} role="dialog" aria-modal="true" aria-label={t('settings.updates.downloadTitle')}>
      <div className={s.modal}>
        <div className={s.modalHead}>
          <div className={s.headIcon} aria-hidden="true">
            <Icon name="download" size={18} />
          </div>
          <div className={s.headText}>
            <div className={s.modalTitle}>{titleFor(t, state)}</div>
            {state.version && (
              <div className={s.modalVersion}>{t('settings.updates.downloadVersion', { version: state.version })}</div>
            )}
          </div>
        </div>

        <div className={s.progressTrack} role="progressbar" aria-valuenow={Math.round(percent(state))} aria-valuemin={0} aria-valuemax={100}>
          <div className={s.progressFill} style={{ width: `${percent(state)}%` }} />
        </div>

        <div className={s.statsRow}>
          <span className={s.percent}>{Math.round(percent(state))}%</span>
          <span className={s.bytes}>
            {t('settings.updates.downloadBytes', {
              done: formatBytes(state.downloaded),
              total: formatBytes(state.total),
            })}
          </span>
        </div>

        <div className={s.metaRow}>
          <span className={s.meta}>
            {t('settings.updates.downloadSpeed', { speed: formatBytes(state.speed) + '/s' })}
          </span>
          <span className={s.meta}>
            {state.remaining_seconds >= 0
              ? t('settings.updates.downloadRemaining', { time: formatDuration(t, state.remaining_seconds) })
              : t('settings.updates.downloadRemainingUnknown')}
          </span>
        </div>

        {state.phase === 'verifying' && (
          <p className={s.note}>{t('settings.updates.downloadVerifying')}</p>
        )}
        {state.phase === 'failed' && (
          <p className={s.error} role="alert">{state.error || t('settings.updates.downloadFailed')}</p>
        )}
        {state.phase === 'canceled' && (
          <p className={s.note}>{t('settings.updates.downloadCanceled')}</p>
        )}
        {state.phase === 'done' && (
          <>
            <p className={s.success}>
              {state.checksum_verified
                ? t('settings.updates.downloadDoneVerified')
                : t('settings.updates.downloadDoneUnverified')}
            </p>
            {/* The full path, because "the download" is a file the user has
                to go and install — and the folder it sits in is not a
                secret. */}
            {state.path && (
              <p className={s.pathLine}>{t('settings.updates.downloadSavedAt', { path: state.path })}</p>
            )}
            {state.installer_ejected && (
              <p className={s.note}>{t('settings.updates.downloadEjected')}</p>
            )}
            {ejectNote && (
              <p className={s.note}>{ejectNote}</p>
            )}
          </>
        )}

        {active && (
          <p className={s.hint}>{t('settings.updates.downloadHint')}</p>
        )}

        <div className={s.modalActions}>
          {state.phase === 'done' ? (
            <>
              <button className="btn btn-secondary" onClick={onDismiss}>{t('settings.updates.downloadLater')}</button>
              <button className="btn btn-secondary" onClick={onReveal}>{t('settings.updates.downloadReveal')}</button>
              {/* Only macOS mounts an installer disk image; on the other
                  platforms the button would be a no-op with no meaning. */}
              {platform === 'darwin' && !state.installer_ejected && (
                <button className="btn btn-secondary" onClick={onEject}>{t('settings.updates.downloadEject')}</button>
              )}
              <button className="btn btn-primary" onClick={onOpen}>{t('settings.updates.downloadOpenInstaller')}</button>
            </>
          ) : active ? (
            <>
              <button className="btn btn-secondary" onClick={onMinimize}>{t('settings.updates.downloadMinimize')}</button>
              <button className="btn btn-danger" onClick={onCancel}>{t('settings.updates.downloadCancel')}</button>
            </>
          ) : (
            <button className="btn btn-secondary" onClick={onDismiss}>{t('common.close')}</button>
          )}
        </div>
      </div>
    </div>
  )
}

interface CornerCardProps {
  snapshot: DownloadSnapshot
  onExpand: () => void
  onCancel: () => void
}

/** The minimized corner card. */
function CornerCard({ snapshot, onExpand, onCancel }: CornerCardProps) {
  const { t } = useTranslation()
  const state = snapshot.state
  const active = isDownloading(state)

  return (
    <div className={s.corner}>
      <button
        className={s.cornerButton}
        onClick={onExpand}
        aria-label={t('settings.updates.downloadExpand')}
      >
        <span className={s.cornerIcon} aria-hidden="true">
          <Icon name={state.phase === 'done' ? 'check' : 'download'} size={14} />
        </span>
        <span className={s.cornerText}>
          <span className={s.cornerTitle}>
            {state.phase === 'done'
              ? t('settings.updates.downloadDoneShort')
              : t('settings.updates.downloadRunningShort')}
          </span>
          {active && (
            <span className={s.cornerMeta}>
              {Math.round(percent(state))}% · {formatBytes(state.downloaded)} / {formatBytes(state.total)}
            </span>
          )}
          {/* A finished download names its version and its file: the two
              things the user would otherwise have to go and check. */}
          {state.phase === 'done' && (
            <span className={s.cornerMeta}>
              {[state.version, state.file_name].filter(Boolean).join(' · ')}
            </span>
          )}
          {state.phase === 'failed' && (
            <span className={s.cornerMeta}>{state.error || t('settings.updates.downloadFailed')}</span>
          )}
        </span>
      </button>
      <div className={s.cornerTrack}>
        <div className={s.cornerFill} style={{ width: `${percent(state)}%` }} />
      </div>
      {active && (
        <button className={s.cornerCancel} onClick={onCancel} aria-label={t('settings.updates.downloadCancel')}>
          <Icon name="close" size={12} />
        </button>
      )}
    </div>
  )
}

/** The modal's headline, which changes with the phase. */
function titleFor(t: (k: string, o?: Record<string, unknown>) => string, state: DownloadSnapshot['state']): string {
  switch (state.phase) {
    case 'running': return t('settings.updates.downloadRunning')
    case 'verifying': return t('settings.updates.downloadVerifyingTitle')
    case 'done': return t('settings.updates.downloadDoneTitle')
    case 'failed': return t('settings.updates.downloadFailedTitle')
    case 'canceled': return t('settings.updates.downloadCanceledTitle')
    default: return t('settings.updates.downloadTitle')
  }
}

/** The completion percentage, 0 when the total size is unknown. */
function percent(state: DownloadSnapshot['state']): number {
  if (state.total <= 0) return 0
  const p = (state.downloaded / state.total) * 100
  return p > 100 ? 100 : p
}

/** A byte count in the largest unit that keeps it readable. */
export function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  const rounded = i === 0 ? Math.round(value) : Math.round(value * 10) / 10
  return `${rounded} ${units[i]}`
}

/**
 * A duration in seconds as "1分23秒" / "45秒" / "2小时5分".
 *
 *  The unit wording is locale data, not a hardcoded literal: the same string is
 *  interpolated into `settings.updates.downloadRemaining`, so a hardcoded "秒"
 *  would put Chinese into the English UI. `t` comes first, matching the
 *  `titleFor(t, state)` shape in this file. */
export function formatDuration(t: (k: string, o?: Record<string, unknown>) => string, seconds: number): string {
  if (seconds < 0) return '—'
  if (seconds < 60) {
    const n = Math.ceil(seconds)
    return t('common.durationSeconds', { n, defaultValue: `${n}秒` })
  }
  const minutes = Math.floor(seconds / 60)
  const rest = Math.ceil(seconds % 60)
  if (minutes < 60) {
    return t('common.durationMinutes', { m: minutes, s: rest, defaultValue: `${minutes}分${rest}秒` })
  }
  const hours = Math.floor(minutes / 60)
  return t('common.durationHours', { h: hours, m: minutes % 60, defaultValue: `${hours}小时${minutes % 60}分` })
}
