// components/QuickCapturePanel.tsx — write something down about a project.
//
// It used to live inside the floating capture button, and the button is now
// the floating AGENT (a draggable robot). The capture form itself did not
// change: same project selector, same kind chips, same two saves. Only its
// home moved — the agent panel's "记一笔" mode — so the capture path stays
// one click from anywhere without costing the agent panel its own space.

import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createNoteWithMeta,
  createTodo,
  getProjects,
  type NoteKind,
  type Project,
} from '../api/client'
import type { PushToast } from '../hooks/useToast'
import s from './AgentBot.module.css'

interface Props {
  onToast: PushToast
  /** Called after a successful save, so the host can go back to asking. */
  onDone?: () => void
  /** The project to preselect (the one whose page the user is on). */
  defaultProjectId?: number | null
}

export default function QuickCapturePanel({ onToast, onDone, defaultProjectId }: Props) {
  const { t } = useTranslation()
  const [projects, setProjects] = useState<Project[]>([])
  const [projectId, setProjectId] = useState<number | null>(defaultProjectId ?? null)
  const [content, setContent] = useState('')
  const [kind, setKind] = useState<NoteKind>('knowledge')
  const [saving, setSaving] = useState<'note' | 'todo' | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    let cancelled = false
    getProjects().then(ps => {
      if (cancelled) return
      setProjects(ps)
      setProjectId(prev => prev ?? defaultProjectId ?? ps[0]?.id ?? null)
    }).catch(() => { /* the save buttons stay disabled without a project */ })
    return () => { cancelled = true }
  }, [defaultProjectId])

  useEffect(() => {
    textareaRef.current?.focus()
  }, [])

  const firstLine = content.split('\n').map(l => l.trim()).find(l => l !== '') || ''

  const saveNote = async () => {
    if (!projectId || !content.trim() || saving) return
    setSaving('note')
    try {
      await createNoteWithMeta(projectId, content, { title: firstLine, kind, source: 'quick' })
      onToast({ kind: 'success', title: t('fab.noteSaved') })
      setContent('')
      onDone?.()
    } catch (e) {
      onToast({ kind: 'error', title: t('fab.saveFailed'), message: e instanceof Error ? e.message : undefined })
      setSaving(null)
    }
  }

  const saveTodo = async () => {
    if (!projectId || !firstLine || saving) return
    setSaving('todo')
    try {
      await createTodo(projectId, firstLine)
      onToast({ kind: 'success', title: t('fab.todoSaved') })
      setContent('')
      onDone?.()
    } catch (e) {
      onToast({ kind: 'error', title: t('fab.saveFailed'), message: e instanceof Error ? e.message : undefined })
      setSaving(null)
    }
  }

  if (projects.length === 0) {
    return <p className={s.empty}>{t('fab.noProjects')}</p>
  }

  const canNote = projectId !== null && content.trim() !== '' && saving === null
  const canTodo = projectId !== null && firstLine !== '' && saving === null

  return (
    <>
      <select
        className={`${s.projectSelect} form-input`}
        value={projectId ?? ''}
        onChange={e => setProjectId(Number(e.target.value))}
        aria-label={t('fab.project')}
      >
        {projects.map(p => (
          <option key={p.id} value={p.id}>{p.name}</option>
        ))}
      </select>

      <textarea
        ref={textareaRef}
        className="fab-content form-input"
        rows={4}
        value={content}
        onChange={e => setContent(e.target.value)}
        placeholder={t('fab.placeholder')}
        aria-label={t('fab.placeholder')}
      />

      <div className={s.kinds} role="radiogroup" aria-label={t('fab.kind')}>
        {(['knowledge', 'log', 'idea', 'other'] as NoteKind[]).map(k => (
          <button
            key={k}
            className={`filter-btn ${kind === k ? 'active' : ''}`}
            onClick={() => setKind(k)}
            role="radio"
            aria-checked={kind === k}
          >
            {t(`fab.kind_${k}`)}
          </button>
        ))}
      </div>

      <div className={s.captureActions}>
        <button className="btn btn-secondary btn-sm" onClick={saveTodo} disabled={!canTodo}>
          {saving === 'todo' ? t('fab.saving') : t('fab.saveTodo')}
        </button>
        <button className="btn btn-primary btn-sm" onClick={saveNote} disabled={!canNote}>
          {saving === 'note' ? t('fab.saving') : t('fab.saveNote')}
        </button>
      </div>
    </>
  )
}
