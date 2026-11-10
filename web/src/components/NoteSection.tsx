import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  listNotes, createNoteWithMeta, updateNoteFull, deleteNote, pinNote, moveNote,
  organizeNote, setNoteTopic,
  getProjects,
  type Note, type NoteTopic, type Project,
} from '../api/client'
import { renderMarkdown } from '../utils/markdown'
import { useApiData } from '../hooks/useApiData'
import { useConfirmClick } from '../hooks/useConfirmClick'
import { useNoteMutations } from '../hooks/useNoteMutations'
import { useNoteDraft } from '../hooks/useNoteDraft'
import { useNoteVersionHistory } from '../hooks/useNoteVersionHistory'
import { useFilteredNotes } from '../hooks/useFilteredNotes'
import { useTopics } from '../hooks/useTopics'
import type { KindFilter } from '../types/kind'
import NoteEditor, { type NoteDraft } from './notes/NoteEditor'
import NoteFilterBar from './notes/NoteFilterBar'
import TopicFilterBar from './notes/TopicFilterBar'
import SourceTypeBadge from './SourceTypeBadge'
import VersionHistoryPanel from './notes/VersionHistoryPanel'
import ErrorBanner from './ErrorBanner'
import Icon from './Icon'
import s from './NoteSection.module.css'
import { describeApiError } from '../utils/apiError'

interface Props {
  projectId: number
  autoNew?: boolean
  // Note id to scroll to and briefly flash once notes have loaded — set by the
  // ?noteId= deep link from the Knowledge page (ADR-0023).
  focusNoteId?: number
}

// An in-progress edit is keyed by note id so a reload or a tab switch can
// restore the typed changes instead of silently dropping them.
const editDraftKey = (noteId: number) => `repotory-edit-draft-${noteId}`

function loadEditDraft(noteId: number): NoteDraft | null {
  try {
    const raw = localStorage.getItem(editDraftKey(noteId))
    if (!raw) return null
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' && typeof parsed.content === 'string'
      ? { ...parsed } as NoteDraft
      : null
  } catch {
    return null
  }
}

function NoteSection({ projectId, autoNew = false, focusNoteId }: Props) {
  const { t } = useTranslation()
  const [notes, setNotes] = useState<Note[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editingId, setEditingId] = useState<number | null>(null)
  const [filter, setFilter] = useState<KindFilter>('all')
  const [saving, setSaving] = useState(false)
  // The note id whose AI tidying call is in flight (null = none).
  const [organizingId, setOrganizingId] = useState<number | null>(null)
  // Non-fatal caveat from the last AI proposal (truncated reply, protocol
  // miss). Shown as a banner inside the editor and cleared when editing ends.
  const [organizeWarning, setOrganizeWarning] = useState('')
  // The note id the open editor was seeded from by an AI proposal (null = a
  // manual edit). Drives the "review, then save" banner.
  const [proposalNoteId, setProposalNoteId] = useState<number | null>(null)
  // The note id currently flashing due to a ?noteId= deep link.
  const [flashId, setFlashId] = useState<number | null>(null)
  const focusedRef = useRef<number | null>(null)

  const [draft, setDraft, clearDraft] = useNoteDraft(projectId)
  const { data: projectsData } = useApiData(() => getProjects(undefined, false), [], { cacheKey: 'projects:all' })
  const projects = projectsData ?? []

  // The topic layer (schema v27): a project holds topics, a note is filed
  // under one of them. The hook owns the list so the filter bar, the editor
  // and the note list always read the same topics.
  const topicState = useTopics(projectId)

  const { run, retryLast } = useNoteMutations(setError)

  // Sequence guard: when the project switches (or a refetch races a refetch),
  // a late response for an older query must not overwrite the current list —
  // or flip the loading/error state of a request that is no longer current.
  const fetchSeqRef = useRef(0)
  const fetchNotes = useCallback(() => {
    const seq = ++fetchSeqRef.current
    listNotes(projectId)
      .then(notes => { if (fetchSeqRef.current === seq) setNotes(notes) })
      .catch(e => {
        // A failed load must not masquerade as the "no notes yet" empty state.
        if (fetchSeqRef.current === seq) setError(`${t('project.loadFailed')}: ${describeApiError(t, e)}`)
      })
      .finally(() => { if (fetchSeqRef.current === seq) setLoading(false) })
  }, [projectId, t])

  // The version panel restores the note's content behind the list's back, so
  // the list has to be refetched afterwards — otherwise the card keeps showing
  // the content that was just replaced and the restore reads as a no-op.
  const vh = useNoteVersionHistory(run, fetchNotes)

  useEffect(() => { fetchNotes() }, [fetchNotes])

  const [isNew, setIsNew] = useState(false)
  const [editDraft, setEditDraft] = useState<NoteDraft>({ ...draft })

  useEffect(() => {
    if (editingId === null) return
    try {
      localStorage.setItem(editDraftKey(editingId), JSON.stringify(editDraft))
    } catch {
      // A disabled localStorage simply means the draft cannot survive a reload.
    }
  }, [editingId, editDraft])

  useEffect(() => {
    const dirty = editingId !== null || isNew
    window.dispatchEvent(new CustomEvent('cinder:dirty', { detail: { source: 'note-editor', dirty } }))
    return () => { window.dispatchEvent(new CustomEvent('cinder:dirty', { detail: { source: 'note-editor', dirty: false } })) }
  }, [editingId, isNew])

  useEffect(() => {
    if (autoNew) {
      setIsNew(true) // eslint-disable-line react-hooks/set-state-in-effect
      setEditingId(null)
    }
  }, [autoNew])

  const filteredByKind = useFilteredNotes(notes, filter)

  // The topic filter narrows the kind-filtered list. -1 means "no topic"
  // (the notes that were never filed), 0 means "all topics".
  const filteredNotes = useMemo(() => {
    if (topicState.activeTopicId === 0) return filteredByKind
    if (topicState.activeTopicId === -1) return filteredByKind.filter(n => !n.topic_id)
    return filteredByKind.filter(n => n.topic_id === topicState.activeTopicId)
  }, [filteredByKind, topicState.activeTopicId])

  const unfiledCount = useMemo(() => notes.filter(n => !n.topic_id).length, [notes])

  // Deep-link focus: once the list has loaded, scroll the targeted note into view
  // and flash it briefly so a Knowledge-page click lands the user on the exact
  // note, not just the project's notes tab (ADR-0023). Guarded so a filter
  // toggle or refetch does not re-trigger the animation.
  useEffect(() => {
    if (loading || focusNoteId == null || focusedRef.current === focusNoteId) return
    focusedRef.current = focusNoteId
    const el = document.querySelector(`[data-note-id="${focusNoteId}"]`)
    if (!el) return
    el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    // Defer the state change out of the effect body (react-hooks/set-state-in-effect).
    const raf = requestAnimationFrame(() => setFlashId(focusNoteId))
    const timer = window.setTimeout(() => setFlashId(null), 2400)
    return () => {
      cancelAnimationFrame(raf)
      window.clearTimeout(timer)
    }
  }, [loading, focusNoteId])

  const startNew = () => {
    setIsNew(true)
    setEditingId(null)
  }

  const handleCreate = async () => {
    if (!draft.content.trim()) return
    setSaving(true)
    await run(async () => {
      // The draft lives in localStorage, so its topic can outlive the topic
      // itself: the topic may have been deleted since the draft was written.
      // Filing under a topic that no longer exists would fail AFTER the note
      // was created, leaving a note that the retry button would then create a
      // second copy of. A stale filing is dropped instead.
      const topicId = draft.topicId && topicState.topics.some(tp => tp.id === draft.topicId)
        ? draft.topicId
        : undefined
      const created = await createNoteWithMeta(projectId, draft.content.trim(), {
        title: draft.title.trim(),
        tags: draft.tags.trim(),
        kind: draft.kind,
      })
      // The note has to exist before it can be filed under a topic — the
      // filing is a second write against the new note's id. A failure here
      // must not fail the whole save: the note exists and the user would
      // otherwise be shown an error for a note that is actually there.
      if (topicId) {
        try {
          await setNoteTopic(created.id, topicId)
        } catch (e) {
          setError(t('project.topicSetFailed') + ': ' + describeApiError(t, e))
        }
      }
      clearDraft()
      setIsNew(false)
      fetchNotes()
      topicState.refresh()
    }, t('project.saveFailed') + ': ')
    setSaving(false)
  }

  const startEdit = (note: Note) => {
    const saved = loadEditDraft(note.id)
    setEditingId(note.id)
    setEditDraft(saved ?? {
      content: note.content,
      title: note.title,
      tags: note.tags,
      kind: note.kind || 'other',
      pinned: note.pinned,
      topicId: note.topic_id,
    })
    setIsNew(false)
    setProposalNoteId(null)
    setOrganizeWarning('')
  }

  const handleSaveEdit = async () => {
    if (editingId === null || !editDraft.content.trim()) return
    setSaving(true)
    await run(async () => {
      await updateNoteFull(
        editingId,
        editDraft.content.trim(),
        editDraft.title.trim(),
        editDraft.tags.trim(),
        editDraft.kind,
        !!editDraft.pinned
      )
      try { localStorage.removeItem(editDraftKey(editingId)) } catch { /* ignore */ }
      setEditingId(null)
      setOrganizeWarning('')
      setProposalNoteId(null)
      fetchNotes()
    }, t('project.saveFailed') + ': ')
    setSaving(false)
  }

  /**
   * AI tidying: the service returns a PROPOSAL and writes nothing. The
   * proposal lands straight in the editor (same draft state a manual edit
   * uses), so reviewing it is not a extra step bolted on the side — it IS
   * the edit flow, and only the user's save persists anything. Kind and
   * pinned are carried over from the stored note; title/tags/content come
   * from the model.
   */
  const handleOrganize = async (note: Note) => {
    setOrganizingId(note.id)
    setError('')
    try {
      const res = await organizeNote(note.id)
      setEditingId(note.id)
      setEditDraft({
        content: res.content,
        title: res.title || note.title,
        tags: res.tags || note.tags,
        kind: note.kind || 'other',
        pinned: note.pinned,
        topicId: note.topic_id,
      })
      setOrganizeWarning(res.warning ?? '')
      setProposalNoteId(note.id)
    } catch (e) {
      setError(t('project.organizeFailed') + ': ' + describeApiError(t, e))
    } finally {
      setOrganizingId(null)
    }
  }

  /**
   * Filing an existing note under a topic takes effect immediately, the same
   * way moving it to another project does: both are organizing acts rather
   * than edits to the note's text, and making the user press "save" a second
   * time to file a note is a step with no meaning behind it.
   *
   * The edit draft is synced in the same step because the editor's filing
   * selector is a controlled select bound to it. Without the sync the
   * selector snaps back to the previous value on the next render, so the
   * write lands on the server but the UI claims it never happened.
   */
  const handleSetNoteTopic = async (noteId: number, topicId: number) => {
    await run(async () => {
      await setNoteTopic(noteId, topicId)
      setNotes(prev => prev.map(n => n.id === noteId
        ? { ...n, topic_id: topicId, topic_name: topicState.topics.find(tp => tp.id === topicId)?.name ?? '' }
        : n))
      if (editingId === noteId) setEditDraft(d => ({ ...d, topicId }))
      topicState.refresh()
    }, t('project.topicSetFailed') + ': ')
  }

  const handleMoveProject = async (noteId: number, targetProjectId: number) => {
    if (targetProjectId === projectId) return
    await run(async () => {
      await moveNote(noteId, targetProjectId)
      // The note left its project, so its topic went with it: a topic belongs
      // to exactly one project. The local copy must show that too, or the
      // card would still claim a filing the server already dropped.
      setNotes(prev => prev.filter(n => n.id !== noteId))
      if (editingId === noteId) setEditingId(null)
      topicState.refresh()
    }, t('project.saveFailed') + ': ')
  }

  const handleDelete = async (noteId: number) => {
    await run(async () => {
      await deleteNote(noteId)
      setNotes(prev => prev.filter(n => n.id !== noteId))
    }, t('project.saveFailed') + ': ')
  }

  /**
   * Renaming a topic has to rename it on the note cards too. The cards carry
   * a denormalized `topic_name` from the server, so a rename that only
   * touched the topic list would leave the chips showing the old name next
   * to a filter bar that shows the new one.
   */
  const handleRenameTopic = async (topicId: number, name: string) => {
    const ok = await topicState.rename(topicId, name)
    if (ok) setNotes(prev => prev.map(n => n.topic_id === topicId ? { ...n, topic_name: name } : n))
    return ok
  }

  /**
   * Deleting a topic keeps the notes but drops their filing, so the cards
   * must lose the badge too — the note is unfiled now, not filed under a
   * topic that no longer exists.
   */
  const handleDeleteTopic = async (topicId: number) => {
    const ok = await topicState.remove(topicId)
    if (ok) setNotes(prev => prev.map(n => n.topic_id === topicId ? { ...n, topic_id: 0, topic_name: '' } : n))
    return ok
  }

  const handlePin = async (note: Note) => {
    const nextPinned = !note.pinned
    setNotes(prev => prev.map(n => n.id === note.id ? { ...n, pinned: nextPinned } : n))
    // Roll back the optimistic toggle only when THIS pin actually failed —
    // run() resolves to success/failure (the old lastOpRef check fired on any
    // earlier failed operation and rolled back successful pins too).
    const ok = await run(async () => {
      await pinNote(note.id, nextPinned)
    }, t('project.saveFailed') + ': ')
    if (!ok) setNotes(prev => prev.map(n => n.id === note.id ? { ...n, pinned: note.pinned } : n))
  }

  if (loading) {
    return (
      <div className="panel-section">
        <h3>{t('knowledge.notesTitle', { defaultValue: '知识笔记' })}</h3>
        <div className="skeleton skeleton-text" style={{ height: 60, marginBottom: 8 }} />
        <div className="skeleton skeleton-text" style={{ height: 60 }} />
      </div>
    )
  }

  return (
    <div className="panel-section">
      <div className={s.header}>
        <h3>{t('knowledge.notesTitle', { defaultValue: '知识笔记' })} ({notes.length})</h3>
        {!isNew && editingId === null && (
          <button className="btn btn-sm btn-primary" onClick={startNew}>{t('project.createNote')}</button>
        )}
      </div>

      {error && (
        <ErrorBanner message={error} onRetry={retryLast} />
      )}

      <NoteFilterBar filter={filter} setFilter={setFilter} notesCount={notes.length} t={t} />

      {/* The topic layer sits between the project and the notes: the strip
          filters by subject and manages the subjects themselves. It always
          shows — it carries the "new topic" button, and a project with no
          topics yet is exactly the case that needs one. */}
      <TopicFilterBar
        topics={topicState.topics}
        activeTopicId={topicState.activeTopicId}
        onSelect={topicState.setActiveTopicId}
        unfiledCount={unfiledCount}
        onCreate={topicState.create}
        onRename={handleRenameTopic}
        onDelete={handleDeleteTopic}
      />

      {isNew && (
        <NoteEditor
          value={draft}
          onChange={setDraft}
          onSave={handleCreate}
          onCancel={() => { setIsNew(false); clearDraft() }}
          saving={saving}
          topics={topicState.topics}
          topicId={draft.topicId ?? 0}
          onTopicChange={id => setDraft(d => ({ ...d, topicId: id }))}
        />
      )}

      {filteredNotes.length === 0 && !isNew ? (
        <p className="empty-hint">{filter !== 'all' ? t('knowledge.noMatch') : t('project.noNotesHint')}</p>
      ) : (
        <div className={s.list}>
          {filteredNotes.map(note => (
            <NoteCard
              key={note.id}
              note={note}
              highlighted={flashId === note.id}
              editing={editingId === note.id}
              editDraft={editDraft}
              onEditDraftChange={setEditDraft}
              onSaveEdit={handleSaveEdit}
              onCancelEdit={() => {
                if (editingId !== null) {
                  try { localStorage.removeItem(editDraftKey(editingId)) } catch { /* ignore */ }
                }
                setEditingId(null)
                setOrganizeWarning('')
              }}
              saving={saving}
              projects={projects}
              currentProjectId={projectId}
              onMoveProject={handleMoveProject}
              onPin={handlePin}
              onEdit={startEdit}
              onOrganize={handleOrganize}
              organizing={organizingId === note.id}
              isProposal={editingId === note.id && proposalNoteId === note.id}
              organizeWarning={editingId === note.id ? organizeWarning : ''}
              onOpenHistory={vh.openVersionHistory}
              onDelete={handleDelete}
              topics={topicState.topics}
              onSetTopic={handleSetNoteTopic}
            />
          ))}
        </div>
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

/** NoteCard renders one note, switching to the inline editor when editing. */
function NoteCard({
  note, highlighted, editing, editDraft, onEditDraftChange, onSaveEdit, onCancelEdit, saving,
  projects, currentProjectId, onMoveProject, onPin, onEdit, onOrganize, organizing, isProposal,
  organizeWarning, onOpenHistory, onDelete, topics, onSetTopic,
}: {
  note: Note
  highlighted: boolean
  editing: boolean
  editDraft: NoteDraft
  onEditDraftChange: (d: NoteDraft) => void
  onSaveEdit: () => void
  onCancelEdit: () => void
  saving: boolean
  projects: Project[]
  currentProjectId: number
  onMoveProject: (noteId: number, projectId: number) => void
  onPin: (note: Note) => void
  onEdit: (note: Note) => void
  onOrganize: (note: Note) => void
  organizing: boolean
  /** True while the open editor holds an unconfirmed AI proposal. */
  isProposal: boolean
  /** Non-empty when the open proposal came with a caveat (truncation etc.). */
  organizeWarning: string
  onOpenHistory: (noteId: number) => void
  onDelete: (noteId: number) => void
  /** The project's topics, offered in the editor's filing selector. */
  topics: NoteTopic[]
  /** Files the note under a topic (or clears it with 0). */
  onSetTopic: (noteId: number, topicId: number) => void
}) {
  const { t } = useTranslation()
  const { armed, click } = useConfirmClick(() => { void onDelete(note.id) })

  // The source badge is the derived TYPE (ADR-0022 决策 5), not the raw
  // source name: the server sends `source_type` and the shared badge
  // component renders it, so this card and the settings list speak the same
  // language. 'manual' notes carry no provenance and stay unbadged.

  if (editing) {
    return (
      <div className={`${s.card} ${highlighted ? s.cardFlash : ''}`} data-note-id={note.id}>
        {isProposal && (
          <div className={s.proposalBanner}>
            <span>{t('project.organizeProposal')}</span>
            {organizeWarning && <em>{organizeWarning}</em>}
          </div>
        )}
        <NoteEditor
          value={editDraft}
          onChange={onEditDraftChange}
          onSave={onSaveEdit}
          onCancel={onCancelEdit}
          saving={saving}
          showPinned
          projects={projects}
          currentProjectId={currentProjectId}
          onMoveProject={(pid) => onMoveProject(note.id, pid)}
          topics={topics}
          topicId={editDraft.topicId ?? note.topic_id}
          onTopicChange={(tid) => onSetTopic(note.id, tid)}
        />
      </div>
    )
  }

  return (
    <div className={`${s.card} ${note.pinned ? s.cardPinned : ''} ${highlighted ? s.cardFlash : ''}`} data-note-id={note.id}>
      <div className={s.titleRow}>
        <span className={s.titleText}>{note.title || note.content.split('\n')[0] || t('project.noteWord')}</span>
        <div className={s.titleBadges}>
          {note.kind === 'knowledge' && <span className={s.badgeNoteSm}>{t('project.kinds.knowledge')}</span>}
          {/* The topic badge names the subject the note is filed under — the
              layer between the project and the note. It is a label, not a
              link: the filter strip above is where subjects are switched. */}
          {note.topic_name && <span className={s.badgeTopicSm}>{note.topic_name}</span>}
          {note.source && note.source !== 'manual' && <SourceTypeBadge type={note.source_type} source={note.source} />}
          <button
            className={`pin-btn ${note.pinned ? 'pinned' : ''}`}
            onClick={() => onPin(note)}
            title={note.pinned ? t('project.unpinned') : t('project.pinned')}
          ><Icon name="pin" size={15} /></button>
        </div>
      </div>
      <div className={`${s.body} markdown-body`} dangerouslySetInnerHTML={{ __html: renderMarkdown(note.content) }} />
      {note.tags && (
        <div className={s.tagsRow}>
          {note.tags.split(',').map(tag => tag.trim()).filter(Boolean).map(tag => <span key={tag} className={s.tagChip}>#{tag}</span>)}
        </div>
      )}
      <div className={s.meta}>
        <span className={s.time}>
          {note.updated_at !== note.created_at
            ? `${t('project.updatedAtPrefix')} ${note.updated_at}`
            : `${t('project.createdAtPrefix')} ${note.created_at}`}
        </span>
        <div className={s.actions}>
          <button className="btn btn-sm" onClick={() => onEdit(note)}>{t('project.edit')}</button>
          <button
            className="btn btn-sm"
            onClick={() => onOrganize(note)}
            disabled={organizing}
            title={t('project.organizeHint')}
          >
            {organizing ? t('project.organizing') : t('project.organize')}
          </button>
          <button
            className="btn btn-sm"
            onClick={() => onOpenHistory(note.id)}
            title={t('project.versionHistory')}
          >
            {t('project.history')}
          </button>
          <button
            className={`btn btn-sm ${armed ? 'btn-delete-confirm' : 'btn-danger'}`}
            onClick={click}
          >
            {armed ? t('project.confirmDelete') : t('common.delete')}
          </button>
        </div>
      </div>
    </div>
  )
}

export default NoteSection
