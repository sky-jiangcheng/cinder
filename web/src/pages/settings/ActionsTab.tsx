import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import {
  triggerScan,
  importClaudeMemory,
  runBackupNow,
  updateConfig,
  checkBackupCopyTarget,
  listAgentClients,
  setupAgentClients,
  getProjects,
} from '../../api/client'
import type {
  AgentFileResult,
  AgentSetupCode,
  AgentSetupReport,
  BackupCopyTargetCode,
  BackupCopyTargetReport,
  Project,
} from '../../api/types'
import { useApiData } from '../../hooks/useApiData'
import { useScanPolling } from '../../hooks/useScanPolling'
import CodeText from '../../components/CodeText'
import s from './ActionsTab.module.css'

interface Props {
  // Full config map from the settings page: the remote-backup (ADR-0025 L2)
  // form seeds its inputs from the backup_remote_* keys, and the off-disk
  // copy (ADR-0025 L3) seeds its input from backup_copy_dir.
  initialConfig: Record<string, string>
  showMessage: (msg: string) => void
}

// The placeholder the backend returns for a configured secret. Writing it
// back would replace the real credential with eight asterisks, so an
// untouched masked field is skipped when saving — the same rule the AI tab
// already follows for the API keys.
const SECRET_MASK = '********'

// The backend reports WHY a candidate directory is (not) usable as a machine
// -readable code, because it does not know the user's language. The sentence
// is the frontend's job.
const COPY_CODE_KEYS: Record<BackupCopyTargetCode, string> = {
  '': 'settings.backupCopyOk',
  not_configured: 'settings.backupCopyNotConfigured',
  relative_path: 'settings.backupCopyRelativePath',
  not_a_directory: 'settings.backupCopyNotADirectory',
  unwritable: 'settings.backupCopyUnwritable',
  no_ancestor: 'settings.backupCopyNoAncestor',
  ancestor_unwritable: 'settings.backupCopyAncestorUnwritable',
  stat_failed: 'settings.backupCopyStatFailed',
  same_device: 'settings.backupCopySameDevice',
}

// The same rule for the client registration: the backend says WHY in a
// machine-readable code, because it does not know the user's language.
const AGENT_CODE_KEYS: Record<AgentSetupCode, string> = {
  '': 'settings.agentSetupOk',
  no_client: 'settings.agentSetupNoClient',
  bad_client: 'settings.agentSetupBadClient',
  project_not_found: 'settings.agentSetupProjectNotFound',
  virtual_project: 'settings.agentSetupVirtualProject',
  auto_grouped_project: 'settings.agentSetupAutoGroupedProject',
  missing_directory: 'settings.agentSetupMissingDirectory',
  binary_missing: 'settings.agentSetupBinaryMissing',
}

const AGENT_STATUS_KEYS: Record<AgentFileResult['status'], string> = {
  created: 'settings.agentStatusCreated',
  updated: 'settings.agentStatusUpdated',
  unchanged: 'settings.agentStatusUnchanged',
  failed: 'settings.agentStatusFailed',
}

export default function ActionsTab({ initialConfig, showMessage }: Props) {
  const { t } = useTranslation()
  // TriggerScan only STARTS the scan — it returns a task id immediately while
  // the backend keeps walking every repo for minutes. Showing "rescan done"
  // right after that call was a lie that also vanished after 3s (the banner
  // timeout), leaving the button idle while the scan was still running. Poll
  // the real status instead, exactly like the dashboard does.
  const { scanning, message: scanMsg, doneMessage: scanDoneMsg, start: startScanPolling } = useScanPolling(() => {})
  const [importing, setImporting] = useState(false)
  const [backingUp, setBackingUp] = useState(false)
  // Remote backup (ADR-0025 L2): an S3-compatible push of the daily snapshot.
  // Defaults all empty = zero egress; nothing here dials anything, the form
  // only persists config keys for the backend's upload flow to consult.
  const [remoteOn, setRemoteOn] = useState(initialConfig.backup_remote_enabled === '1')
  const [remote, setRemote] = useState({
    endpoint: initialConfig.backup_remote_endpoint ?? '',
    bucket: initialConfig.backup_remote_bucket ?? '',
    prefix: initialConfig.backup_remote_prefix ?? '',
    accessKey: initialConfig.backup_remote_access_key ?? '',
    secretKey: initialConfig.backup_remote_secret_key ?? '',
  })
  const [savingRemote, setSavingRemote] = useState(false)
  // Off-disk copy (ADR-0025 L3): a second copy of the daily snapshot on a
  // directory the user picks — an external drive, a NAS mount, a synced
  // folder. Empty = off, and nothing is probed or written until they fill it
  // in.
  const [copyDir, setCopyDir] = useState(initialConfig.backup_copy_dir ?? '')
  const [copyReport, setCopyReport] = useState<BackupCopyTargetReport | null>(null)
  const [checkingCopy, setCheckingCopy] = useState(false)
  const [savingCopy, setSavingCopy] = useState(false)

  // --- AI client wiring ------------------------------------------------------
  // The clients come from the backend, so one it cannot register is never
  // offered as a checkbox. The projects come from the shared list every page
  // already loads, minus the rows that cannot receive client configuration (a
  // scan-time container, and the synthetic "未分类" bucket — the backend
  // refuses both, so offering them would be offering a dead end).
  const [agentClients, setAgentClients] = useState<string[]>([])
  const [pickedClients, setPickedClients] = useState<string[]>([])
  const [targetProject, setTargetProject] = useState('')
  const [withHook, setWithHook] = useState(true)
  // ADR-0043: the managed repotory block in AGENTS.md / CLAUDE.md. On by
  // default alongside the hook — the two are independent halves of the same
  // problem (the hook only exists for Claude Code; the instruction file is what
  // every other agent reads). Both write into files the user tracks in git, so
  // both are reported per file below and neither is silent.
  const [withInstructions, setWithInstructions] = useState(true)
  const [scanAfter, setScanAfter] = useState(false)
  const [connecting, setConnecting] = useState(false)
  const [agentReport, setAgentReport] = useState<AgentSetupReport | null>(null)
  const { data: projectList } = useApiData<Project[]>(
    () => getProjects(undefined, false),
    [],
    { cacheKey: 'projects:all' },
  )

  useEffect(() => {
    let cancelled = false
    listAgentClients()
      .then((cs) => {
        if (!cancelled) setAgentClients(cs)
      })
      .catch(() => {
        // The backend is not reachable (headless mode not started yet); the
        // section stays empty and the button stays disabled.
      })
    return () => { cancelled = true }
  }, [])

  const connectableProjects = useMemo(
    () => (projectList ?? []).filter(p => !p.is_auto_grouped && p.project_kind !== 'default'),
    [projectList],
  )

  const toggleClient = (name: string) => {
    setPickedClients(prev =>
      prev.includes(name) ? prev.filter(c => c !== name) : [...prev, name],
    )
  }

  const clientLabel = (name: string): string => {
    switch (name) {
      case 'claude': return t('settings.agentClientClaude')
      case 'cursor': return t('settings.agentClientCursor')
      case 'vscode': return t('settings.agentClientVSCode')
      case 'windsurf': return t('settings.agentClientWindsurf')
      default: return name
    }
  }

  const handleRescan = async () => {
    try {
      await triggerScan()
      startScanPolling(t('common.scanning', { defaultValue: 'Scanning…' }))
    } catch (e: unknown) {
      showMessage(t('settings.rescanFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    }
  }

  const handleBackup = async () => {
    setBackingUp(true)
    try {
      const path = await runBackupNow()
      showMessage(t('settings.backupDone', { path }))
    } catch (e: unknown) {
      showMessage(t('settings.backupFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setBackingUp(false)
    }
  }

  const handleRemoteToggle = (on: boolean) => {
    setRemoteOn(on)
    void persistRemote(
      { backup_remote_enabled: on ? '1' : '0' },
      on ? t('settings.backupRemoteOn') : t('settings.backupRemoteOff'),
    )
  }

  const persistRemote = async (kv: Record<string, string>, ok: string) => {
    setSavingRemote(true)
    try {
      for (const [k, v] of Object.entries(kv)) {
        await updateConfig(k, v)
      }
      showMessage(ok)
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setSavingRemote(false)
    }
  }

  const handleRemoteSave = () => {
    const kv: Record<string, string> = {
      backup_remote_endpoint: remote.endpoint.trim(),
      backup_remote_bucket: remote.bucket.trim(),
      backup_remote_prefix: remote.prefix.trim(),
      backup_remote_access_key: remote.accessKey.trim(),
    }
    // A configured secret comes back masked. Writing the mask back would
    // destroy the credential the user never touched, so an untouched masked
    // field is simply not written.
    const secret = remote.secretKey.trim()
    if (secret && secret !== SECRET_MASK) {
      kv.backup_remote_secret_key = secret
    }
    void persistRemote(kv, t('settings.backupRemoteSaved'))
  }

  // Checking the target answers the one question a path cannot: is it really
  // on another disk? A backup directory one folder away looks exactly like
  // one on an external drive, and it is not one.
  const handleCopyCheck = async () => {
    setCheckingCopy(true)
    try {
      setCopyReport(await checkBackupCopyTarget(copyDir.trim()))
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setCheckingCopy(false)
    }
  }

  const handleCopySave = async () => {
    setSavingCopy(true)
    try {
      await updateConfig('backup_copy_dir', copyDir.trim())
      // The verdict belongs to the path that was checked, so it is dropped
      // rather than left standing next to a directory that just changed.
      setCopyReport(null)
      showMessage(t('settings.backupCopySaved'))
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setSavingCopy(false)
    }
  }

  const copyStatus = copyReport ? (
    <p
      className={
        copyReport.blocking
          ? s.copyStatusError
          : copyReport.code === 'same_device'
            ? s.copyStatusWarn
            : s.copyStatusOk
      }
      role="status"
    >
      {t(COPY_CODE_KEYS[copyReport.code])}
      {copyReport.code === '' && !copyReport.exists && ` ${t('settings.backupCopyWillCreate')}`}
    </p>
  ) : null

  const handleImport = async () => {
    setImporting(true)
    try {
      const r = await importClaudeMemory()
      showMessage(t('settings.importDone', { created: r.synced, updated: r.updated, skipped: r.skipped }))
    } catch (e: unknown) {
      showMessage(t('settings.importFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setImporting(false)
    }
  }

  // Registering the MCP server is the whole point of the button: after it,
  // the agent sees the memory. The scan is optional — a project the user
  // never scanned has nothing in the knowledge base yet, and the agent
  // would find an empty one.
  const handleConnect = async () => {
    if (!targetProject || pickedClients.length === 0) return
    setConnecting(true)
    try {
      const report = await setupAgentClients(Number(targetProject), pickedClients, withHook, withInstructions)
      setAgentReport(report)
      if (report.code) return
      if (scanAfter) {
        await triggerScan()
        startScanPolling(t('common.scanning', { defaultValue: 'Scanning…' }))
        showMessage(t('settings.agentSetupScanStarted'))
      }
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setConnecting(false)
    }
  }

  return (
    <div className="settings-section">
      {/* Two independent maintenance actions, split into groups so neither
          reads as the page's single primary action. */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.groupRescan')}</h2>
        <p className="section-desc">{t('settings.actionsDesc')}</p>
        <div className={s.actionRow}>
          <button className="btn btn-primary" onClick={handleRescan} disabled={scanning}>
            {scanning ? (scanMsg || t('common.scanning', { defaultValue: 'Scanning…' })) : t('settings.rescanAll')}
          </button>
          {/* The done message is a live region rather than showMessage(): the
              banner clears after 3s, which is shorter than a typical scan, so
              the completion notice has to outlive it. */}
          <span className="visually-hidden" role="status" aria-live="polite">{scanDoneMsg}</span>
          {scanDoneMsg && <span className="message-banner" role="status">{scanDoneMsg}</span>}
        </div>
      </section>

      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.backupTitle')}</h2>
        <p className="section-desc">{t('settings.backupDesc')}</p>
        <div className={s.actionRow}>
          <button className="btn btn-primary" onClick={handleBackup} disabled={backingUp}>
            {backingUp ? t('settings.backingUp') : t('settings.backupNow')}
          </button>
        </div>
        <p className="section-desc">{t('settings.backupRemoteHint')}</p>
        <div className={s.actionRow}>
          <button
            className="btn btn-secondary"
            onClick={() => handleRemoteToggle(!remoteOn)}
            disabled={savingRemote}
            aria-pressed={remoteOn}
          >
            {remoteOn ? t('settings.backupRemoteOnShort') : t('settings.backupRemoteOffShort')}
          </button>
        </div>
        {remoteOn && (
          <div className={s.remoteGrid}>
            <label>
              <span>{t('settings.backupRemoteEndpoint')}</span>
              <input
                className="form-input"
                value={remote.endpoint}
                onChange={(e) => setRemote({ ...remote, endpoint: e.target.value })}
                placeholder="https://s3.example.com"
              />
            </label>
            <label>
              <span>{t('settings.backupRemoteBucket')}</span>
              <input
                className="form-input"
                value={remote.bucket}
                onChange={(e) => setRemote({ ...remote, bucket: e.target.value })}
              />
            </label>
            <label>
              <span>{t('settings.backupRemotePrefix')}</span>
              <input
                className="form-input"
                value={remote.prefix}
                onChange={(e) => setRemote({ ...remote, prefix: e.target.value })}
                placeholder="repotory"
              />
            </label>
            <label>
              <span>{t('settings.backupRemoteAccessKey')}</span>
              <input
                className="form-input"
                value={remote.accessKey}
                onChange={(e) => setRemote({ ...remote, accessKey: e.target.value })}
              />
            </label>
            <label>
              <span>{t('settings.backupRemoteSecretKey')}</span>
              <input
                className="form-input"
                type="password"
                value={remote.secretKey}
                onChange={(e) => setRemote({ ...remote, secretKey: e.target.value })}
              />
            </label>
            <div className={s.actionRow}>
              <button className="btn btn-primary" onClick={handleRemoteSave} disabled={savingRemote}>
                {savingRemote ? t('settings.saving') : t('settings.backupRemoteSave')}
              </button>
            </div>
          </div>
        )}

        {/* The off-disk exit (ADR-0025 L3). It sits below the remote form on
            purpose: it needs no account and no credentials, but it only
            protects against a disk failing — which is the accident this
            feature was born from. */}
        <div className={s.copyBlock}>
          <h3 className={s.copyTitle}>{t('settings.backupCopyTitle')}</h3>
          <p className="section-desc">{t('settings.backupCopyHint')}</p>
          <label className={s.copyLabel}>
            <span>{t('settings.backupCopyPath')}</span>
            <input
              className="form-input"
              value={copyDir}
              onChange={(e) => {
                setCopyDir(e.target.value)
                setCopyReport(null)
              }}
              placeholder={t('settings.backupCopyPlaceholder')}
            />
          </label>
          <div className={s.actionRow}>
            <button
              className="btn btn-secondary"
              onClick={handleCopyCheck}
              disabled={checkingCopy || savingCopy}
            >
              {checkingCopy ? t('settings.backupCopyChecking') : t('settings.backupCopyCheck')}
            </button>
            <button className="btn btn-primary" onClick={handleCopySave} disabled={savingCopy || checkingCopy}>
              {savingCopy ? t('settings.saving') : t('settings.backupCopySave')}
            </button>
          </div>
          {copyStatus}
        </div>
      </section>

      {/* The one-click AI client wiring. It writes into the user's own
          machine — MCP config files in the project directory they picked, and
          optionally the Claude Code SessionEnd hook — so the project and the
          clients are the user's choice, never defaults. */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.agentClientsTitle')}</h2>
        <p className="section-desc">{t('settings.agentClientsDesc')}</p>
        <div className={s.agentBlock}>
          <label className={s.agentLabel}>
            <span>{t('settings.agentClientsProject')}</span>
            <select
              className="form-input"
              value={targetProject}
              onChange={(e) => {
                setTargetProject(e.target.value)
                setAgentReport(null)
              }}
            >
              <option value="">{t('settings.agentClientsPickProject')}</option>
              {connectableProjects.map(p => (
                <option key={p.id} value={p.id}>{p.name}</option>
              ))}
            </select>
          </label>
          <fieldset className={s.agentClients}>
            <legend>{t('settings.agentClientsPick')}</legend>
            {agentClients.map(c => (
              <label key={c} className={s.agentCheck}>
                <input
                  type="checkbox"
                  checked={pickedClients.includes(c)}
                  onChange={() => toggleClient(c)}
                />
                <span>{clientLabel(c)}</span>
              </label>
            ))}
          </fieldset>
          <label className={s.agentCheck}>
            <input
              type="checkbox"
              checked={withHook}
              onChange={(e) => setWithHook(e.target.checked)}
            />
            <span>{t('settings.agentClientsHook')}</span>
          </label>
          <label className={s.agentCheck}>
            <input
              type="checkbox"
              checked={withInstructions}
              onChange={(e) => setWithInstructions(e.target.checked)}
            />
            <span>{t('settings.agentClientsInstructions')}</span>
          </label>
          <label className={s.agentCheck}>
            <input
              type="checkbox"
              checked={scanAfter}
              onChange={(e) => setScanAfter(e.target.checked)}
            />
            <span>{t('settings.agentClientsScanAfter')}</span>
          </label>
          <div className={s.actionRow}>
            <button
              className="btn btn-primary"
              onClick={handleConnect}
              disabled={connecting || !targetProject || pickedClients.length === 0}
            >
              {connecting ? t('settings.agentClientsConnecting') : t('settings.agentClientsConnect')}
            </button>
          </div>
          {agentReport && (
            <div className={s.agentResult}>
              <p className={agentReport.blocking ? s.agentStatusError : s.agentStatusOk} role="status">
                {t(AGENT_CODE_KEYS[agentReport.code])}
              </p>
              <ul className={s.agentFiles}>
                {agentReport.files.map((f, i) => (
                  <li key={`${f.path}-${i}`} className={f.status === 'failed' ? s.agentStatusError : undefined}>
                    <span className={s.agentFileStatus}>{t(AGENT_STATUS_KEYS[f.status])}</span>
                    <code>{f.path}</code>
                    {f.note && <span className={s.agentFileNote}>{f.note}</span>}
                  </li>
                ))}
                {agentReport.hook && (
                  <li>
                    <span className={s.agentFileStatus}>{t(AGENT_STATUS_KEYS[agentReport.hook.status])}</span>
                    <code>{agentReport.hook.path}</code>
                  </li>
                )}
                {agentReport.hook_settings && (
                  <li>
                    <span className={s.agentFileStatus}>{t(AGENT_STATUS_KEYS[agentReport.hook_settings.status])}</span>
                    <code>{agentReport.hook_settings.path}</code>
                  </li>
                )}
                {(agentReport.instructions ?? []).map((f, i) => (
                  <li key={`instr-${f.path}-${i}`} className={f.status === 'failed' ? s.agentStatusError : undefined}>
                    <span className={s.agentFileStatus}>{t(AGENT_STATUS_KEYS[f.status])}</span>
                    <code>{f.path}</code>
                    {f.note && <span className={s.agentFileNote}>{f.note}</span>}
                  </li>
                ))}
              </ul>
              {(agentReport.warnings ?? []).map((w, i) => (
                <p key={i} className={s.agentStatusError} role="status">{w}</p>
              ))}
            </div>
          )}
        </div>
      </section>

      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.importClaudeTitle')}</h2>
        <p className="section-desc"><CodeText text={t('settings.importClaudeDesc')} /></p>
        <div className={s.actionRow}>
          <button className="btn btn-primary" onClick={handleImport} disabled={importing}>
            {importing ? t('settings.importing') : t('settings.importClaudeAction')}
          </button>
          <Link to="/knowledge" className="btn btn-secondary">{t('settings.gotoKnowledge')}</Link>
        </div>
      </section>
    </div>
  )
}
