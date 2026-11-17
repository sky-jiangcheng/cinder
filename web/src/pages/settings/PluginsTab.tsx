import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getKnowledgeSources, getKnowledgeSyncStatus, triggerKnowledgeImport, previewKnowledgeImport, captureClaudeHandoff,
  updateConfig, type SourceStatus, type ImportPreview,
} from '../../api/client'
import CodeText from '../../components/CodeText'

// Imported as `css`, not the usual `s`: this file maps sources.map((s) => ...), which
// would shadow the module object inside the callback.
import css from './PluginsTab.module.css'

// B-end / global-memory sources that need an explicit target project (their
// memory is agent-global, not per-project). Value = project name or numeric id.
const TARGET_PROJECT_KEYS = ['openclaw_project', 'hermes_project', 'codebuddy_project'] as const

interface Props {
  initialAutoImport: boolean
  initialClaudeCapture: boolean
  initialTargets: Record<string, string>
  // Full config map from the settings page: per-source periodic-sync switches
  // live under `sync_source_<name>` keys, so the whole map is the least-brittle
  // way to seed the toggles without hardcoding the source list twice.
  initialConfig: Record<string, string>
  showMessage: (msg: string) => void
}

export default function PluginsTab({ initialAutoImport, initialClaudeCapture, initialTargets, initialConfig, showMessage }: Props) {
  const { t } = useTranslation()
  const [sources, setSources] = useState<SourceStatus[]>([])
  const [importingSource, setImportingSource] = useState('')
  const [previewingSource, setPreviewingSource] = useState('')
  const [preview, setPreview] = useState<{ name: string; data: ImportPreview } | null>(null)
  const [autoImport, setAutoImport] = useState(initialAutoImport)
  const [claudeCapture, setClaudeCapture] = useState(initialClaudeCapture)
  // Default is ON, unlike every other switch here, so the seed is "not
  // explicitly off" rather than "= 1". Reading it as `=== '1'` would flip the
  // switch off for every user who never touched the key while the backend
  // keeps writing the block — the UI would be lying about the state.
  const [agentInstructions, setAgentInstructions] = useState(initialConfig.agent_instructions_auto !== '0')
  const [targets, setTargets] = useState<Record<string, string>>(
    Object.fromEntries(TARGET_PROJECT_KEYS.map((k) => [k, initialTargets[k] ?? ''])),
  )
  const [captureProjectId, setCaptureProjectId] = useState('')
  const [saving, setSaving] = useState(false)
  const [capturing, setCapturing] = useState(false)
  // Per-source sync switch: persisted value lives in config
  // (`sync_source_<name>`), and this map only records user flips since mount.
  // Derived default = config, so no seeding effect is needed.
  const [syncOverrides, setSyncOverrides] = useState<Record<string, boolean>>({})
  const [lastSyncRun, setLastSyncRun] = useState('')
  const [lastSyncError, setLastSyncError] = useState('')

  useEffect(() => {
    getKnowledgeSources().then(setSources).catch(() => {})
    // Periodic sync is a global ticker; its last round is one status row for
    // the whole source list, shown under the list.
    getKnowledgeSyncStatus()
      .then((st) => { setLastSyncRun(st.last_run); setLastSyncError(st.last_error) })
      .catch(() => {})
  }, [])

  const syncEnabledFor = (name: string) => syncOverrides[name] ?? initialConfig[`sync_source_${name}`] === '1'

  const handleImportSource = async (name: string) => {
    setImportingSource(name)
    try {
      const r = await triggerKnowledgeImport(name)
      // A non-zero failed count changes what the message says: "0 imported"
      // alone reads as "nothing new", and that is exactly how the 2026-10-18
      // opencode breakage stayed invisible for days.
      if (r.failed && r.failed > 0) {
        showMessage(t('settings.importDoneWithFailures', {
          created: r.created,
          updated: r.updated,
          skipped: r.skipped,
          failed: r.failed,
          msg: (r.errors && r.errors[0]) || '',
        }))
      } else {
        showMessage(t('settings.importDone', { created: r.created, updated: r.updated, skipped: r.skipped }))
      }
    } catch (e: unknown) {
      showMessage(t('settings.importFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setImportingSource('')
    }
  }

  const handlePreviewSource = async (name: string) => {
    setPreviewingSource(name)
    try {
      const r = await previewKnowledgeImport(name)
      setPreview({ name, data: r })
    } catch (e: unknown) {
      showMessage(t('settings.previewFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setPreviewingSource('')
    }
  }

  const persist = async (key: string, value: string, ok: string) => {
    setSaving(true)
    try {
      await updateConfig(key, value)
      showMessage(ok)
    } catch (e: unknown) {
      showMessage(t('settings.saveFailedMsg', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setSaving(false)
    }
  }

  const handleAutoImportToggle = (on: boolean) => {
    setAutoImport(on)
    void persist('auto_import', on ? '1' : '0', on ? t('settings.autoImportOn') : t('settings.autoImportOff'))
  }

  const handleClaudeCaptureToggle = (on: boolean) => {
    setClaudeCapture(on)
    void persist('claude_session_capture', on ? '1' : '0', on ? t('settings.claudeCapOn') : t('settings.claudeCapOff'))
  }

  const handleAgentInstructionsToggle = (on: boolean) => {
    setAgentInstructions(on)
    void persist('agent_instructions_auto', on ? '1' : '0', on ? t('settings.agentInstOn') : t('settings.agentInstOff'))
  }

  const handleSyncToggle = (name: string, on: boolean) => {
    setSyncOverrides((prev) => ({ ...prev, [name]: on }))
    void persist(
      `sync_source_${name}`,
      on ? '1' : '0',
      on ? t('settings.syncOn', { name }) : t('settings.syncOff', { name }),
    )
  }

  const handleTargetSave = (key: string) => {
    void persist(key, (targets[key] ?? '').trim(), t('settings.targetSaved', { source: key.replace('_project', '') }))
  }

  const handleCapture = async () => {
    const pid = Number(captureProjectId)
    if (!Number.isInteger(pid) || pid <= 0) {
      showMessage(t('settings.captureNeedProject'))
      return
    }
    setCapturing(true)
    try {
      const r = await captureClaudeHandoff(pid)
      showMessage(t('settings.captureDone', { title: r.title }))
    } catch (e: unknown) {
      showMessage(t('settings.captureFailed', { msg: e instanceof Error ? e.message : t('common.unknownError') }))
    } finally {
      setCapturing(false)
    }
  }

  return (
    <div className="settings-section">
      <p className="section-desc"><CodeText text={t('settings.pluginDesc')} /></p>

      {/* --- Knowledge import --- */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.groupImport')}</h2>
        <div className="form-group">
          <label id="settings-auto-import-label">{t('settings.autoImportLabel')}</label>
          <div className="toggle-row">
            <button
              className={`${css.toggle} ${autoImport ? css.toggleOn : ''}`}
              onClick={() => handleAutoImportToggle(!autoImport)}
              disabled={saving}
              aria-pressed={autoImport}
              aria-labelledby="settings-auto-import-label"
            >
              <span className={css.toggleKnob} />
            </button>
            <span className="form-hint" style={{ marginTop: 0 }}>
              {autoImport ? t('settings.autoImportOnHint') : t('settings.autoImportOffHint')}
            </span>
          </div>
        </div>
        {sources.length === 0 ? (
          <div className="empty-hint">{t('settings.noSources')}</div>
        ) : (
          <>
            <ul className={css.pluginList}>
              {sources.map((s) => (
                <li key={s.name} className={css.pluginItem}>
                  <div className={css.pluginInfo}>
                    <div className={css.pluginTop}>
                      <span className={css.pluginName}>{s.name}</span>
                      <span className={css.typeChip}>
                        {t(`settings.sourceTypes.${s.type}`, { defaultValue: s.type })}
                      </span>
                      <span
                        className={`${css.statusDot} ${s.detected ? css.statusDotOn : ''}`}
                        title={s.detail || undefined}
                        aria-hidden="true"
                      />
                      <span className={css.statusText}>
                        {s.detected ? t('settings.sourceDetected') : t('settings.sourceNotDetected')}
                      </span>
                    </div>
                    <span className={css.pluginPath}>{t('settings.fromPlugin', { name: s.plugin || 'builtin' })}</span>
                    {s.detail && <span className={css.pluginDetail}>{s.detail}</span>}
                    {/* Per-source sync trail (ADR-0022 决策 3): the round-level
                        line below says when the last round ran; this says when
                        THIS source last ran, and what went wrong for it. */}
                    {s.last_run && (
                      <span className={css.pluginDetail}>
                        {t('settings.sourceLastSyncAt', { time: s.last_run })}
                      </span>
                    )}
                    {s.last_error && (
                      <span className={css.syncError}>{t('settings.syncLastError', { msg: s.last_error })}</span>
                    )}
                    {/* Documents the importer REFUSED to produce — files it could
                        not read or parse. Without this line the only signal was
                        "0 imported", which is indistinguishable from "there was
                        nothing new"; on 2026-10-18 an upstream format change
                        made every opencode session unreadable and the import
                        reported a clean zero with no error anywhere. */}
                    {!!s.failed && s.failed > 0 && (
                      <span className={css.syncError}>
                        {t('settings.sourceRejectedFiles', {
                          count: s.failed,
                          msg: (s.errors && s.errors[0]) || '',
                        })}
                      </span>
                    )}
                  </div>
                  <div className={css.pluginActions}>
                    <div className={css.syncToggleWrap}>
                      <button
                        className={`${css.toggle} ${syncEnabledFor(s.name) ? css.toggleOn : ''}`}
                        onClick={() => handleSyncToggle(s.name, !syncEnabledFor(s.name))}
                        disabled={saving || !s.detected}
                        aria-pressed={syncEnabledFor(s.name)}
                        aria-label={t('settings.syncToggleLabel', { name: s.name })}
                        title={s.detected ? undefined : t('settings.syncHiddenHint')}
                      >
                        <span className={css.toggleKnob} />
                      </button>
                      <span className={css.syncToggleText}>
                        {syncEnabledFor(s.name) ? t('settings.syncOnShort') : t('settings.syncOffShort')}
                      </span>
                    </div>
                    {/* Secondary, not primary: five identical filled-blue CTAs in a
                        row read as one giant primary action and none of them wins.
                        The row's hover state carries the emphasis instead. */}
                    <button
                      className={`btn btn-sm ${css.pluginImportBtn}`}
                      onClick={() => handlePreviewSource(s.name)}
                      disabled={previewingSource !== '' || !s.detected}
                      title={t('settings.previewHint')}
                    >
                      {previewingSource === s.name ? t('settings.previewing') : t('settings.preview')}
                    </button>
                    <button
                      className={`btn btn-sm ${css.pluginImportBtn}`}
                      onClick={() => handleImportSource(s.name)}
                      disabled={importingSource !== '' || !s.enabled}
                    >
                      {importingSource === s.name ? t('settings.importing') : t('settings.importNow')}
                    </button>
                  </div>
                  {preview?.name === s.name && (
                    <details className={css.pluginPreview}>
                      <summary>
                        {t('settings.previewResult', {
                          created: preview.data.created,
                          updated: preview.data.updated,
                          skipped: preview.data.skipped,
                        })}
                      </summary>
                      <ul>
                        {preview.data.items.map((it) => (
                          <li key={`${it.action}-${it.title}`}>
                            <span className={css.pluginDetail}>
                              {it.action === 'create' ? '+ ' : it.action === 'update' ? '~ ' : '- '}
                              {it.title}
                            </span>
                          </li>
                        ))}
                      </ul>
                    </details>
                  )}
                </li>
              ))}
            </ul>
            <p className={css.syncStatusLine}>
              {lastSyncRun
                ? t('settings.lastSyncAt', { time: lastSyncRun })
                : t('settings.neverSynced')}
              {lastSyncRun && lastSyncError && (
                <span className={css.syncError}> · {t('settings.syncLastError', { msg: lastSyncError })}</span>
              )}
            </p>
          </>
        )}
      </section>

      {/* --- Periodic sync --- */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.groupSync')}</h2>
        <p className="form-hint" style={{ marginTop: 0 }}>{t('settings.syncHint')}</p>
      </section>

      {/* --- Global-memory sources: target project --- */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.groupTargets')}</h2>
        <p className="form-hint" style={{ marginTop: 0, marginBottom: 12 }}>{t('settings.targetProjectHint')}</p>
        {TARGET_PROJECT_KEYS.map((key) => (
          <div className="form-group" key={key}>
            <label htmlFor={`target-${key}`}>{t('settings.targetProjectLabel', { source: key.replace('_project', '') })}</label>
            <div className="input-row">
              <input
                id={`target-${key}`}
                className="form-input"
                type="text"
                placeholder={t('settings.targetProjectPlaceholder')}
                value={targets[key] ?? ''}
                onChange={(e) => setTargets((prev) => ({ ...prev, [key]: e.target.value }))}
                disabled={saving}
              />
              <button className="btn btn-secondary btn-sm" onClick={() => handleTargetSave(key)} disabled={saving}>
                {t('settings.save')}
              </button>
            </div>
          </div>
        ))}
      </section>

      {/* --- Claude session capture --- */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.groupCapture')}</h2>
        <div className="form-group">
          <label id="settings-claude-capture-label">{t('settings.claudeCapLabel')}</label>
          <div className="toggle-row">
            <button
              className={`${css.toggle} ${claudeCapture ? css.toggleOn : ''}`}
              onClick={() => handleClaudeCaptureToggle(!claudeCapture)}
              disabled={saving}
              aria-pressed={claudeCapture}
              aria-labelledby="settings-claude-capture-label"
            >
              <span className={css.toggleKnob} />
            </button>
            <span className="form-hint" style={{ marginTop: 0 }}>
              {claudeCapture ? t('settings.claudeCapOnHint') : t('settings.claudeCapOffHint')}
            </span>
          </div>
        </div>
        <div className="form-group">
          <label htmlFor="capture-project-id">{t('settings.captureProjectLabel')}</label>
          <div className="input-row">
            <input
              id="capture-project-id"
              className="form-input"
              type="number"
              min={1}
              inputMode="numeric"
              placeholder={t('settings.captureProjectPlaceholder')}
              value={captureProjectId}
              onChange={(e) => setCaptureProjectId(e.target.value)}
              disabled={!claudeCapture || capturing}
            />
            <button className="btn btn-secondary btn-sm" onClick={handleCapture} disabled={!claudeCapture || capturing}>
              {capturing ? t('settings.capturing') : t('settings.captureBtn')}
            </button>
          </div>
        </div>
      </section>

      {/* --- Agent instruction block: written on every scan, no per-client setup --- */}
      <section className="settings-group">
        <h2 className="settings-group-title">{t('settings.groupAgentInstructions')}</h2>
        <div className="form-group">
          <label id="settings-agent-instructions-label">{t('settings.agentInstLabel')}</label>
          <div className="toggle-row">
            <button
              className={`${css.toggle} ${agentInstructions ? css.toggleOn : ''}`}
              onClick={() => handleAgentInstructionsToggle(!agentInstructions)}
              disabled={saving}
              aria-pressed={agentInstructions}
              aria-labelledby="settings-agent-instructions-label"
            >
              <span className={css.toggleKnob} />
            </button>
            <span className="form-hint" style={{ marginTop: 0 }}>
              {agentInstructions ? t('settings.agentInstOnHint') : t('settings.agentInstOffHint')}
            </span>
          </div>
        </div>
      </section>
    </div>
  )
}