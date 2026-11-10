import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import {
  listAllNotes, listAllTags, searchAll, pinNote, importClaudeMemory, exportNoteAsMarkdown,
  getProjects,
  type NoteWithProject, type Project, type SearchHit,
} from '../api/client'
import { parseTags } from '../utils/markdown'
import { copyText } from '../utils/clipboard'
import { useDebouncedCallback } from './useDebouncedCallback'

// A 30s cache (same TTL as useApiData) keeps the knowledge tab from
// refetching the whole library every time its body remounts on a view switch.
const FETCH_TTL = 30_000
let fetchAllCache: {
  notes: NoteWithProject[]
  tags: string[]
  projects: Project[]
  at: number
} | null = null

/** Drop the cached library so the next mount fetches fresh data. */
export function invalidateKnowledgeCache() {
  fetchAllCache = null
}

export type KindFilter = 'all' | 'knowledge' | 'other'

export interface UseKnowledgePageOptions {
  // The knowledge tab's skeleton owns the project list (the scope dropdown
  // lives there), so the hook must NOT fetch a second copy when the caller
  // hands one in: the same list fetched twice is one fetch too many, and the
  // two copies could disagree.
  projects?: Project[]
  // The shared project scope (0 = every project). It is the outermost
  // narrowing of the note list — the notes view is cross-project by nature,
  // so the scope is what turns it into "this project's notes".
  scopeProjectId?: number
}

/**
 * Encapsulates all data-fetching, search, and mutation logic for the
 * Knowledge page. The page component only handles rendering.
 */
export function useKnowledgePage(options: UseKnowledgePageOptions = {}) {
  const { projects: externalProjects, scopeProjectId = 0 } = options
  const { t } = useTranslation()
  const [notes, setNotes] = useState<NoteWithProject[]>([])
  const [tags, setTags] = useState<string[]>([])
  const [ownProjects, setOwnProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [query, setQueryState] = useState('')
  const [hits, setHits] = useState<SearchHit[] | null>(null)
  const [kindFilter, setKindFilter] = useState<KindFilter>('all')
  const [activeTag, setActiveTag] = useState<string | null>(null)
  // The topic filter (schema v27). Topics belong to a project, but this page
  // lists notes across projects, so the filter is by topic NAME: two projects
  // may each have a topic called "网关", and on this page they read as the
  // same subject. Filtering by id would hide one of them for no visible
  // reason.
  const [activeTopic, setActiveTopic] = useState<string | null>(null)
  const [pinnedOnly, setPinnedOnly] = useState(false)
  const [importing, setImporting] = useState(false)
  const [message, setMessage] = useState('')
  const [newNotePicker, setNewNotePicker] = useState(false)
  const [askMode, setAskMode] = useState(false)
  const [exportingId, setExportingId] = useState<number | null>(null)
  const messageTimer = useRef<number | null>(null)
  const searchSeq = useRef(0)

  const flashMessage = useCallback((msg: string, ms = 3000) => {
    setMessage(msg)
    if (messageTimer.current) clearTimeout(messageTimer.current)
    messageTimer.current = window.setTimeout(() => setMessage(''), ms)
  }, [])

  useEffect(() => () => { if (messageTimer.current) clearTimeout(messageTimer.current) }, [])

  // The externally supplied project list is read through a ref so that
  // `fetchAll` keeps a stable identity: the skeleton loads the projects
  // right after mount, and a new list identity must not re-trigger the
  // fetch effect (it would refetch every note for a list that did not
  // change).
  const externalRef = useRef(externalProjects)
  useEffect(() => { externalRef.current = externalProjects }, [externalProjects])

  const fetchAll = useCallback(() => {
    setError('')
    // With an external list the hook does not fetch projects at all — the
    // skeleton already has them, and two copies of the same list can
    // disagree.
    const ext = externalRef.current
    const ps = ext ? Promise.resolve(ext) : getProjects()
    Promise.all([listAllNotes(), listAllTags(), ps])
      .then(([n, tg, p]) => {
        fetchAllCache = { notes: n, tags: tg, projects: p, at: Date.now() }
        setNotes(n)
        setTags(tg)
        if (!ext) setOwnProjects(p)
      })
      .catch((e: unknown) => {
        // Keep whatever is already on screen: a failed refresh must not look
        // like an empty library.
        console.error('[useKnowledgePage] fetchAll failed', e)
        setError(e instanceof Error ? e.message : t('common.failed'))
      })
      .finally(() => setLoading(false))
  }, [t])

  useEffect(() => {
    const ext = externalRef.current
    const cached = fetchAllCache
    if (cached && Date.now() - cached.at < FETCH_TTL) {
      setError('') // eslint-disable-line react-hooks/set-state-in-effect
      setNotes(cached.notes)
      setTags(cached.tags)
      if (!ext) setOwnProjects(cached.projects)
      setLoading(false)
      return
    }
    void fetchAll()
  }, [fetchAll])

  const runSearch = useDebouncedCallback((q: string, ask: boolean) => {
    if (!q.trim()) { setHits(null); return }
    const seq = ++searchSeq.current
    searchAll(q.trim())
      .then(h => { if (seq === searchSeq.current) { setHits(h); if (ask) setAskMode(true) } })
      .catch(() => { if (seq === searchSeq.current) { setHits([]); if (ask) setAskMode(true) } })
  }, 300)

  // setQuery IS the search entry point. The knowledge tab's skeleton owns the
  // query string (one field for both views, ADR-0038), and it drives the
  // hook through this setter — so a search always follows the field the
  // user is actually typing in.
  const setQuery = useCallback((q: string) => {
    setQueryState(q)
    runSearch(q, askMode)
  }, [runSearch, askMode])

  // The old name for the same thing, kept for the callers that still speak
  // it: two entrances to one behaviour would drift apart.
  const handleSearchInput = useCallback((q: string) => {
    setQuery(q)
  }, [setQuery])

  const handlePin = useCallback(async (id: number, pinned: boolean) => {
    setNotes(prev => prev.map(n => n.id === id ? { ...n, pinned: !pinned } : n))
    try { await pinNote(id, !pinned) } catch (e: unknown) {
      setNotes(prev => prev.map(n => n.id === id ? { ...n, pinned } : n))
      flashMessage(t('knowledge.pinFailed', { defaultValue: 'Failed to pin note' }) + (e instanceof Error ? ' ' + e.message : ''))
    }
  }, [flashMessage, t])

  const handleExport = useCallback(async (id: number) => {
    setExportingId(id)
    try {
      const md = await exportNoteAsMarkdown(id)
      if (!md) return
      await copyText(md)
      flashMessage(t('knowledge.copiedMd'))
    } catch (e) {
      flashMessage(t('knowledge.exportFailed') + (e instanceof Error ? e.message : t('common.unknownError')))
    } finally {
      setExportingId(null)
    }
  }, [flashMessage, t])

  const handleImport = useCallback(async () => {
    setImporting(true)
    try {
      const r = await importClaudeMemory()
      flashMessage(t('knowledge.importDone', { created: r.synced, updated: r.updated, skipped: r.skipped }), 4000)
      fetchAll()
    } catch (e) {
      flashMessage(t('knowledge.importFailed') + (e instanceof Error ? e.message : t('common.unknownError')), 4000)
    } finally {
      setImporting(false)
    }
  }, [flashMessage, fetchAll, t])

  // The notes that fall inside the current project scope. Every derived list
  // below is computed from THESE, not from the whole library: under a scope,
  // a chip that advertises a tag or a topic that does not exist in the scope
  // can only lead to an empty state, which reads as "the scope broke the
  // filter" rather than "there is nothing here".
  const scopedNotes = useMemo(
    () => (scopeProjectId ? notes.filter(n => n.project_id === scopeProjectId) : notes),
    [notes, scopeProjectId],
  )

  const filtered = useMemo(() => {
    let list = scopedNotes
    // The project scope is the OUTERMOST narrowing: it answers "whose notes",
    // and the kind / tag / topic filters then narrow within that project.
    // (ADR-0038 decision 4 — the notes view is cross-project by nature, so
    // the scope is what turns it into "this project's notes".)
    if (kindFilter === 'knowledge') list = list.filter(n => n.kind === 'knowledge')
    else if (kindFilter === 'other') list = list.filter(n => n.kind !== 'knowledge')
    if (activeTag) list = list.filter(n => parseTags(n.tags).includes(activeTag))
    if (activeTopic) list = list.filter(n => n.topic_name === activeTopic)
    if (pinnedOnly) list = list.filter(n => n.pinned)
    return list
  }, [scopedNotes, kindFilter, activeTag, activeTopic, pinnedOnly])

  // The tag chips follow the scope too. The backend's tag list spans the
  // whole library, so under a project scope it would advertise tags the
  // scope does not have.
  const scopedTags = useMemo(() => {
    if (!scopeProjectId) return tags
    const set = new Set<string>()
    scopedNotes.forEach(n => parseTags(n.tags).forEach(tg => set.add(tg)))
    return [...set].sort((a, b) => a.localeCompare(b))
  }, [tags, scopedNotes, scopeProjectId])

  // The topics actually present in the loaded notes, with their counts. The
  // list is derived from the notes rather than fetched per project: this
  // page spans projects, and one query per project would be a request storm
  // for a chip row.
  const topicCounts = useMemo(() => {
    const counts = new Map<string, number>()
    scopedNotes.forEach(n => {
      if (!n.topic_name) return
      counts.set(n.topic_name, (counts.get(n.topic_name) ?? 0) + 1)
    })
    return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
  }, [scopedNotes])

  // The project list is the caller's when it hands one in (the knowledge
  // tab's skeleton owns it); otherwise the hook fetches its own.
  const projects = externalProjects ?? ownProjects

  // Quick Note / Create Note / the project jump list must all work on a cold
  // start (zero notes), so this is derived from the scanned project list.
  // Deriving it from notes (the old behavior) deadlocked the first note: no
  // notes -> no projects to attach it to -> "No projects found" guard.
  const projectNames = useMemo(() => {
    const set = new Map<string, number>()
    projects.forEach(p => set.set(p.name, p.id))
    return Array.from(set.entries()).sort((a, b) => a[0].localeCompare(b[0]))
  }, [projects])

  const pinnedCount = useMemo(() => scopedNotes.filter(n => n.pinned).length, [scopedNotes])

  const recentNotes = useMemo(() => {
    return [...scopedNotes]
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
      .slice(0, 5)
  }, [scopedNotes])

  // The search hits follow the scope as well. The pages view's retrieval is
  // scoped server-side (`searchWikiPages(scope, …)`), so the notes view's
  // hits have to be narrowed the same way — otherwise the two views of the
  // same tab answer "what does this project contain?" differently.
  const scopedHits = useMemo(
    () => (hits && scopeProjectId ? hits.filter(h => h.project_id === scopeProjectId) : hits),
    [hits, scopeProjectId],
  )

  return {
    // State
    notes, tags: scopedTags, loading, error, query, hits: scopedHits, kindFilter, activeTag,
    activeTopic, pinnedOnly, importing, message, newNotePicker, askMode, exportingId,
    // Derived
    filtered, projectNames, pinnedCount, recentNotes, topicCounts,
    // Actions
    setQuery, setKindFilter, setActiveTag, setActiveTopic, setPinnedOnly, setNewNotePicker,
    handleSearchInput, handlePin, handleExport, handleImport, fetchAll, flashMessage,
  }
}
