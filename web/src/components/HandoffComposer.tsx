import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { createHandoff, getProjects, type Project } from '../api/client'
import s from './HandoffComposer.module.css'

interface Props {
  open: boolean
  onClose: () => void
  /** Pre-selected project (e.g. the project page the user is on). */
  projectId?: number
  /** Called after the handoff lands, with the note id. */
  onCreated?: (noteId: number) => void
}

// The four optional sections of the handoff template, in the order the
// template renders them. The order is the contract the next session reads,
// so it is not a UI choice.
const SECTIONS = [
  { key: 'changes', labelKey: 'handoff.changes' },
  { key: 'decisions', labelKey: 'handoff.decisions' },
  { key: 'gotchas', labelKey: 'handoff.gotchas' },
  { key: 'nextSteps', labelKey: 'handoff.nextSteps' },
] as const

type SectionKey = (typeof SECTIONS)[number]['key']

/** One bullet per line; blank lines are dropped by the backend too. */
function toBullets(text: string): string[] {
  return text
    .split('\n')
    .map(l => l.trim())
    .filter(l => l !== '')
}

/**
 * HandoffComposer writes one session handoff by hand.
 *
 * It exists because the agent half of the session protocol needs three things
 * a first-time user does not have: an MCP client, a model, and a client that
 * remembers to call repotory_handoff at the end of the session. Without this
 * the loop cannot be tried at all — and a loop that cannot be tried is a loop
 * nobody can judge.
 *
 * The record it writes is the same one the agent writes: same template, same
 * `handoff` tag, same rendering at the top of the next session's context.
 */
export default function HandoffComposer({ open, onClose, projectId, onCreated }: Props) {
  const { t } = useTranslation()
  const [projects, setProjects] = useState<Project[]>([])
  const [target, setTarget] = useState<number | null>(null)
  const [summary, setSummary] = useState('')
  const [sections, setSections] = useState<Record<SectionKey, string>>({
    changes: '',
    decisions: '',
    gotchas: '',
    nextSteps: '',
  })
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const summaryRef = useRef<HTMLTextAreaElement>(null)

  // The draft is cleared on the way OUT, not on the way in: resetting it in an
  // effect on `open` would be a second source of truth for "what is in the
  // form", and React rightly flags a synchronous setState there.
  const reset = useCallback(() => {
    setSummary('')
    setSections({ changes: '', decisions: '', gotchas: '', nextSteps: '' })
    setError('')
    setSaving(false)
  }, [])

  const finish = useCallback(() => {
    reset()
    onClose()
  }, [reset, onClose])

  const close = useCallback(() => {
    const hasContent = summary.trim() !== '' || SECTIONS.some(({ key }) => sections[key].trim() !== '')
    if (hasContent && !window.confirm(t('common.confirmDiscard'))) return
    finish()
  }, [finish, summary, sections, t])

  useEffect(() => {
    if (!open) return
    let cancelled = false
    getProjects()
      .then(ps => {
        if (cancelled) return
        setProjects(ps)
        setTarget(projectId ?? ps[0]?.id ?? null)
      })
      .catch(() => { /* the select stays empty; the save button stays disabled */ })
    return () => { cancelled = true }
  }, [open, projectId])

  useEffect(() => {
    if (!open) return
    setTimeout(() => summaryRef.current?.focus(), 30)
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, close])

  useEffect(() => {
    if (!open) return
    const dirty = summary.trim() !== '' || SECTIONS.some(({ key }) => sections[key].trim() !== '')
    window.dispatchEvent(new CustomEvent('cinder:dirty', { detail: { source: 'handoff-composer', dirty } }))
    return () => { window.dispatchEvent(new CustomEvent('cinder:dirty', { detail: { source: 'handoff-composer', dirty: false } })) }
  }, [open, summary, sections])

  const bullets = useMemo(
    () => Object.fromEntries(SECTIONS.map(({ key }) => [key, toBullets(sections[key])])) as Record<SectionKey, string[]>,
    [sections],
  )

  const hasSection = SECTIONS.some(({ key }) => bullets[key].length > 0)
  const canSave = target !== null && summary.trim() !== '' && hasSection && !saving

  const save = async () => {
    if (!canSave || target === null) return
    setSaving(true)
    setError('')
    try {
      const res = await createHandoff(target, {
        summary: summary.trim(),
        changes: bullets.changes,
        decisions: bullets.decisions,
        gotchas: bullets.gotchas,
        nextSteps: bullets.nextSteps,
      })
      onCreated?.(res.note_id)
      finish()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setSaving(false)
    }
  }

  if (!open) return null

  return (
    <>
      <div className={s.overlay} onClick={close} />
      <div className={s.dialog} role="dialog" aria-modal="true" aria-label={t('handoff.title')}>
        <div className={s.head}>
          <div className={s.headText}>
            <span className={s.title}>{t('handoff.title')}</span>
            <span className={s.hint}>{t('handoff.hint')}</span>
          </div>
          <button className={s.close} onClick={close} aria-label={t('common.close', { defaultValue: 'Close' })}>
            <Icon name="close" size={14} />
          </button>
        </div>

        <div className={s.body}>
          {projects.length === 0 ? (
            <p className={s.empty}>{t('handoff.noProjects')}</p>
          ) : (
            <>
              <label className={s.field}>
                <span className={s.label}>{t('handoff.project')}</span>
                <select
                  className={`${s.project} form-select`}
                  value={target ?? ''}
                  onChange={e => setTarget(Number(e.target.value))}
                >
                  {projects.map(p => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </select>
              </label>

              <label className={s.field}>
                <span className={s.label}>
                  {t('handoff.summary')}
                  <span className={s.required}>{t('handoff.required')}</span>
                </span>
                <textarea
                  ref={summaryRef}
                  className={`${s.summary} form-input`}
                  rows={3}
                  value={summary}
                  onChange={e => setSummary(e.target.value)}
                  placeholder={t('handoff.summaryPlaceholder')}
                />
              </label>

              <div className={s.sections}>
                {SECTIONS.map(({ key, labelKey }) => (
                  <label key={key} className={s.field}>
                    <span className={s.label}>
                      {t(labelKey)}
                      <span className={s.optional}>{t('handoff.optional')}</span>
                    </span>
                    <textarea
                      className={`${s.sectionInput} form-input`}
                      rows={2}
                      value={sections[key]}
                      onChange={e => setSections(prev => ({ ...prev, [key]: e.target.value }))}
                      placeholder={t('handoff.sectionPlaceholder')}
                    />
                  </label>
                ))}
              </div>

              <p className={s.note}>{t('handoff.nextSessionNote')}</p>
            </>
          )}
          {error && <p className={s.error} role="alert">{error}</p>}
        </div>

        <div className={s.foot}>
          <span className={s.footHint}>{t('handoff.requiredHint')}</span>
          <div className={s.actions}>
            <button className="btn btn-secondary btn-sm" onClick={close}>
              {t('common.cancel', { defaultValue: 'Cancel' })}
            </button>
            <button className="btn btn-primary btn-sm" onClick={save} disabled={!canSave}>
              {saving ? t('handoff.saving') : t('handoff.save')}
            </button>
          </div>
        </div>
      </div>
    </>
  )
}
