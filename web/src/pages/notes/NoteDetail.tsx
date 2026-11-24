import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  deleteNote, getNote, getProjects, moveNote, pinNote, setNoteTopic, updateNoteFull,
  type Note, type Project,
} from '../../api/client'
import NoteEditor, { type NoteDraft } from '../../components/notes/NoteEditor'
import VersionHistoryPanel from '../../components/notes/VersionHistoryPanel'
import SourceTypeBadge from '../../components/SourceTypeBadge'
import ErrorBanner from '../../components/ErrorBanner'
import Icon from '../../components/Icon'
import { useConfirmClick } from '../../hooks/useConfirmClick'
import { useNoteMutations } from '../../hooks/useNoteMutations'
import { useNoteVersionHistory } from '../../hooks/useNoteVersionHistory'
import { useTopics } from '../../hooks/useTopics'
import { renderMarkdown } from '../../utils/markdown'
import s from './NoteDetail.module.css'

// /note/:id — one note, in full, on a surface of its own (ADR-0078).
//
// A note is project-owned in the schema (project_notes.project_id is NOT
// NULL), which is why every link to one used to be a PAIR:
// /project/<pid>?tab=notes&noteId=<n>. That pair carried the project the note
// happened to live in at link time, so the address broke the moment MoveNote
// filed the note elsewhere, and reading one note meant landing inside a
// project page with its header, its tab strip and every other note around it.
//
// The wiki layer never had this problem: a page has a slug and /wiki/:id. A
// note does not need a slug to deserve the same treatment — its id is already
// stable and unique — so the detail route is addressed by id alone and reads
// the note's project_id off the record for the scope label and the
// "open in project" link. The project page stays what it is good at: managing
// notes in bulk.

export default function NoteDetail() {
  const { t } = useTranslation()
  const { id } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const noteId = Number(id)

  // A non-numeric id is a bad URL, not a backend failure: it is decided at
  // render time so the effect never reports it by setting state.
  const invalidId = !Number.isFinite(noteId) || noteId <= 0

  // The list that led here passes its own path as `state.from`, so "back"
  // returns to the search results / scope the note was found in rather than to
  // a fixed destination. A direct deep link has no `from` and falls back to
  // the notes view of the knowledge tab.
  const from = (location.state as { from?: string } | null)?.from
  const backTo = from || '/knowledge?view=notes'

  const [loaded, setLoaded] = useState<{ id: number; note: Note } | null>(null)
  const [failure, setFailure] = useState<{ id: number; message: string } | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [error, setError] = useState('')
  const [reloadKey, setReloadKey] = useState(0)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<NoteDraft | null>(null)
  const [saving, setSaving] = useState(false)

  const reload = useCallback(() => setReloadKey(k => k + 1), [])
  const { run, retryLast } = useNoteMutations(setError)

  useEffect(() => {
    if (invalidId) return
    let cancelled = false
    getNote(noteId)
      .then(n => { if (!cancelled) setLoaded({ id: noteId, note: n }) })
      .catch((e: unknown) => {
        if (cancelled) return
        const detail = e instanceof Error ? e.message : ''
        setFailure({
          id: noteId,
          message: detail ? `${t('errors.noteNotFound')} — ${detail}` : t('errors.noteNotFound'),
        })
      })
    return () => { cancelled = true }
  }, [noteId, invalidId, reloadKey, t])

  useEffect(() => {
    getProjects()
      .then(ps => setProjects(ps))
      .catch(() => { /* the scope label falls back to #id */ })
  }, [])

  // Both the loaded note and the failure carry the id they belong to:
  // navigating from one note to another reuses this component, and a stale
  // note (or a stale error) must not survive the navigation.
  const note = loaded && loaded.id === noteId ? loaded.note : null
  const loadError = invalidId
    ? t('errors.noteNotFound')
    : failure && failure.id === noteId ? failure.message : ''
  const loading = !note && !loadError

  // Topics are the project's filing layer, so the editor's selector needs the
  // note's own project — which is only known once the note has loaded (0 makes
  // the hook ask for nothing until then).
  const topicState = useTopics(note?.project_id ?? 0)
  const vh = useNoteVersionHistory(run, reload)

  // Same unsaved-changes guard the project's note list uses: an editor holding
  // a draft must make the shell warn before the tab closes.
  useEffect(() => {
    window.dispatchEvent(new CustomEvent('cinder:dirty', { detail: { source: 'note-detail-editor', dirty: editing } }))
    return () => { window.dispatchEvent(new CustomEvent('cinder:dirty', { detail: { source: 'note-detail-editor', dirty: false } })) }
  }, [editing])

  const projectName = useMemo(() => {
    const m = new Map<number, string>()
    for (const p of projects) m.set(p.id, p.name)
    return (pid: number) => m.get(pid) ?? `#${pid}`
  }, [projects])

  const startEdit = () => {
    if (!note) return
    setDraft({
      content: note.content,
      title: note.title,
      tags: note.tags,
      kind: note.kind || 'other',
      pinned: note.pinned,
      topicId: note.topic_id,
    })
    setEditing(true)
  }

  const saveEdit = async () => {
    if (!note || !draft || !draft.content.trim()) return
    setSaving(true)
    await run(async () => {
      // The version this page was loaded with goes back with the save: a
      // concurrent writer (an agent over MCP, another window) is reported as a
      // conflict instead of being silently overwritten (ADR-0057 D2).
      await updateNoteFull(
        note.id,
        draft.content.trim(),
        draft.title.trim(),
        draft.tags.trim(),
        draft.kind,
        !!draft.pinned,
        note.version ?? 0,
      )
      setEditing(false)
      setDraft(null)
      reload()
    }, t('project.saveFailed') + ': ')
    setSaving(false)
  }

  const handleMoveProject = async (pid: number) => {
    if (!note || pid === note.project_id) return
    await run(async () => {
      await moveNote(note.id, pid)
      // A topic belongs to exactly one project, so the filing went with the
      // move; both the note and the topic list have to be re-read.
      topicState.refresh()
      reload()
    }, t('project.saveFailed') + ': ')
  }

  const handleSetTopic = async (tid: number) => {
    if (!note) return
    await run(async () => {
      await setNoteTopic(note.id, tid)
      setDraft(d => (d ? { ...d, topicId: tid } : d))
      reload()
    }, t('project.topicSetFailed') + ': ')
  }

  const togglePin = async () => {
    if (!note) return
    const next = !note.pinned
    setLoaded({ id: noteId, note: { ...note, pinned: next } })
    const ok = await run(async () => {
      await pinNote(note.id, next)
    }, t('project.saveFailed') + ': ')
    if (!ok) setLoaded({ id: noteId, note: { ...note, pinned: !next } })
  }

  const removeNote = async () => {
    if (!note) return
    const pid = note.project_id
    // Deleting the record this page IS leaves nowhere to stand, so the page
    // hands the user back to the list the note lived in.
    await run(async () => {
      await deleteNote(note.id)
      navigate(`/project/${pid}?tab=notes`, { replace: true })
    }, t('project.saveFailed') + ': ')
  }
  const { armed, click } = useConfirmClick(() => { void removeNote() })

  if (loading) {
    return (
      <div className={s.page}>
        <div className="skeleton skeleton-text" style={{ height: 26, width: '45%', marginBottom: 12 }} />
        <div className="skeleton skeleton-text" style={{ height: 14, width: '30%', marginBottom: 24 }} />
        <div className="skeleton skeleton-text" style={{ height: 160 }} />
      </div>
    )
  }

  if (loadError || !note) {
    return (
      <div className={s.page}>
        <Link to={backTo} className={s.back}>
          <Icon name="arrow-up" size={14} className={s.backIcon} />
          {t('noteDetail.backToNotes')}
        </Link>
        <ErrorBanner message={loadError || t('errors.noteNotFound')} />
      </div>
    )
  }

  return (
    <div className={s.page}>
      <Link to={backTo} className={s.back}>
        <Icon name="arrow-up" size={14} className={s.backIcon} />
        {t('noteDetail.backToNotes')}
      </Link>

      <header className={s.head}>
        <div className={s.badges}>
          <span className={s.kindBadge}>{t(`project.kinds.${note.kind}`, note.kind)}</span>
          <Link to={`/project/${note.project_id}?tab=notes`} className={s.projectBadge}>
            {projectName(note.project_id)}
          </Link>
          {note.topic_name && <span className={s.topicBadge}>{note.topic_name}</span>}
          {note.source && note.source !== 'manual' && (
            <SourceTypeBadge type={note.source_type} source={note.source} />
          )}
        </div>
        <div className={s.titleRow}>
          <h1 className={s.title}>{note.title || note.content.split('\n')[0] || t('project.noteWord')}</h1>
          <button
            className={`pin-btn ${note.pinned ? 'pinned' : ''}`}
            onClick={togglePin}
            title={note.pinned ? t('project.unpinned') : t('project.pinned')}
          >
            <Icon name="pin" size={15} />
          </button>
        </div>
        <div className={s.footMeta}>
          <span>{t('project.createdAtPrefix')} {note.created_at.slice(0, 16)}</span>
          {note.updated_at !== note.created_at && (
            <span>{t('project.updatedAtPrefix')} {note.updated_at.slice(0, 16)}</span>
          )}
        </div>
      </header>

      {error && <ErrorBanner message={error} onRetry={retryLast} />}

      {editing && draft ? (
        <NoteEditor
          value={draft}
          onChange={setDraft}
          onSave={saveEdit}
          onCancel={() => { setEditing(false); setDraft(null) }}
          saving={saving}
          showPinned
          projects={projects}
          currentProjectId={note.project_id}
          onMoveProject={handleMoveProject}
          topics={topicState.topics}
          topicId={draft.topicId ?? note.topic_id}
          onTopicChange={handleSetTopic}
        />
      ) : (
        <>
          <article
            className={`${s.body} markdown-body`}
            dangerouslySetInnerHTML={{ __html: renderMarkdown(note.content) }}
          />
          {note.tags && (
            <div className={s.tags}>
              {note.tags.split(',').map(tag => tag.trim()).filter(Boolean)
                .map(tag => <span key={tag} className={s.tag}>#{tag}</span>)}
            </div>
          )}
          <div className={s.actions}>
            <button className="btn btn-sm btn-primary" onClick={startEdit}>{t('project.edit')}</button>
            <button
              className="btn btn-sm"
              onClick={() => { void vh.openVersionHistory(note.id) }}
              title={t('project.versionHistory')}
            >
              {t('project.history')}
            </button>
            <Link to={`/project/${note.project_id}?tab=notes&noteId=${note.id}`} className={s.secondaryLink}>
              {t('noteDetail.openInProject')}
            </Link>
            <button
              className={`btn btn-sm ${armed ? 'btn-delete-confirm' : 'btn-danger'}`}
              onClick={click}
            >
              {armed ? t('project.confirmDelete') : t('common.delete')}
            </button>
          </div>
        </>
      )}

      {vh.versionHistory !== null && (
        <VersionHistoryPanel
          versions={vh.versionHistory}
          restoringId={vh.restoringId}
          diffText={vh.diffText}
          onRestore={vh.handleRestoreVersion}
          onShowDiff={vh.handleShowDiff}
          onClose={vh.closeVersionHistory}
        />
      )}
    </div>
  )
}
