import { Link, useNavigate, useNavigationType, useSearchParams } from 'react-router-dom'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { stripMarkdown } from '../utils/markdown'
import DOMPurify from 'dompurify'
import KnowledgeCard from './knowledge/KnowledgeCard'
import KnowledgeOrganize from './knowledge/KnowledgeOrganize'
import ErrorBanner from '../components/ErrorBanner'
import Icon from '../components/Icon'
import { useKnowledgePage } from '../hooks/useKnowledgePage'
import WikiPagesView from './Wiki'
import { getProjects, type Project } from '../api/client'
import s from './Knowledge.module.css'

// The knowledge tab: ONE object, two states (ADR-0038 decision 4).
//
// The two views are the raw material (notes — writable, filterable) and the
// finished product (approved pages — read only, ADR-0032). They used to be
// two top-level tabs with near-identical skeletons, which made the user
// decide "which one do I want?" before knowing what either one held, and
// left the two surfaces uneven (the pages had a project scope, the notes
// did not — even though the notes search was cross-project all along).
//
// So the tab owns the SKELETON — one search field, one project scope, one
// result count, one loading/error/empty treatment — and the two bodies own
// only what actually differs: the notes body is a grid with filters and
// write actions, the pages body is a read-only list.
//
// The skeleton holds the state (view / query / scope), keeps it in the URL
// (so a view is shareable and the back button walks it) and hands it down.
// The bodies report back what they have: how many rows, whether they failed,
// and how to retry — the skeleton draws those, so both views count and fail
// the same way.

// 预留钩子类：`knowledge`、`knowledge-section`、`large` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

type KnowledgeView = 'notes' | 'pages'

/** The deep-link surface: ?view= / ?q= / ?project= are shareable. */
function readView(raw: string | null): KnowledgeView {
  return raw === 'pages' ? 'pages' : 'notes'
}

/**
 * The search params for a given state. The view is left out when it is the
 * default, so the common URL stays short ("/knowledge") while a shared
 * pages view says what it is ("/knowledge?view=pages").
 */
function buildParams(view: KnowledgeView, query: string, scope: number): Record<string, string> {
  const next: Record<string, string> = {}
  if (view !== 'notes') next.view = view
  const q = query.trim()
  if (q) next.q = q
  if (scope) next.project = String(scope)
  return next
}

export default function Knowledge() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()

  const urlView = readView(searchParams.get('view'))
  const urlQuery = searchParams.get('q') ?? ''
  const urlScope = Number(searchParams.get('project') ?? '0') || 0

  const [view, setView] = useState<KnowledgeView>(urlView)
  const [query, setQuery] = useState(urlQuery)
  const [scope, setScope] = useState(urlScope)
  const [projects, setProjects] = useState<Project[]>([])

  // What WE put into the URL. react-router may render an intermediate
  // location while our own write is in flight, and adopting that as an
  // external change would wipe the tail of what the user just typed.
  const written = useRef({ v: urlView, q: urlQuery, p: urlScope })
  // Only a history navigation (back / forward) may overwrite the local state;
  // our own PUSH/REPLACE writes must not.
  const navType = useNavigationType()

  useEffect(() => {
    if (navType !== 'POP') return
    if (urlView !== written.current.v) setView(urlView)
    if (urlQuery !== written.current.q) setQuery(urlQuery)
    if (urlScope !== written.current.p) setScope(urlScope)
  }, [navType, urlView, urlQuery, urlScope])

  // The project list is fetched ONCE, here: the scope dropdown belongs to
  // the skeleton, and the notes body reuses this list instead of asking for
  // a second copy (the hook takes it as an option).
  useEffect(() => {
    let cancelled = false
    getProjects()
      .then(ps => { if (!cancelled) setProjects(ps) })
      .catch(() => { /* the scope degrades to "all projects" */ })
    return () => { cancelled = true }
  }, [])

  // The URL follows the local state. REPLACE, not PUSH: typing a query must
  // not push a history entry per keystroke, and the scope dropdown is one
  // step, not a trail. (The view switch is the exception — see switchView.)
  useEffect(() => {
    const q = query.trim()
    written.current = { v: view, q, p: scope }
    setSearchParams(buildParams(view, q, scope), { replace: true })
  }, [view, query, scope, setSearchParams])

  // What the body underneath reports: the count the shared count bar shows,
  // the failure the shared error banner shows, and whether it is still
  // loading. The skeleton draws all three, so both views look the same.
  const [count, setCount] = useState(0)
  const [countKey, setCountKey] = useState('knowledge.resultCountGeneric')
  const [bodyError, setBodyError] = useState('')
  const [bodyLoading, setBodyLoading] = useState(false)
  const retryRef = useRef<(() => void) | null>(null)

  const handleCount = useCallback((n: number, key: string) => {
    setCount(n)
    setCountKey(key)
  }, [])
  const handleError = useCallback((message: string, retry: () => void) => {
    retryRef.current = retry
    setBodyError(message)
  }, [])
  const handleLoading = useCallback((loading: boolean) => { setBodyLoading(loading) }, [])

  const switchView = useCallback((next: KnowledgeView) => {
    if (next === view) return
    setView(next)
    // PUSH, not REPLACE: the back button has to be able to walk back
    // through a view switch — with REPLACE it would step over it and land
    // on the page before the tab, which reads as "the tab ate my history".
    setSearchParams(buildParams(next, query, scope), { replace: false })
    // The old body's count / failure belong to the old body.
    setCount(0)
    setBodyError('')
    setBodyLoading(true)
  }, [view, query, scope, setSearchParams])

  return (
    <div className="knowledge">
      {/* The nav bar already names this section ("知识"), so repeating it as a
          page title is pure duplication. The h1 stays for screen readers and
          for the document outline, but visually the page now opens on its
          actual content: the view switch and the search field. */}
      <h1 className="visually-hidden">{t('knowledge.hubTitle', { defaultValue: '知识' })}</h1>

      <div className={s.toolbar}>
        {/* One object, two states: a segmented control says "same thing,
            two faces", where two tabs would say "two places". */}
        <div className={s.viewSwitch} role="group" aria-label={t('knowledge.viewLabel', { defaultValue: '视图' })}>
          <button
            type="button"
            className={`${s.viewTab} ${view === 'notes' ? s.viewTabActive : ''}`}
            aria-pressed={view === 'notes'}
            data-view="notes"
            onClick={() => switchView('notes')}
          >
            {t('knowledge.viewNotes', { defaultValue: '笔记' })}
          </button>
          <button
            type="button"
            className={`${s.viewTab} ${view === 'pages' ? s.viewTabActive : ''}`}
            aria-pressed={view === 'pages'}
            data-view="pages"
            onClick={() => switchView('pages')}
          >
            {t('knowledge.viewPages', { defaultValue: '知识页' })}
          </button>
        </div>

        {/* One field, two views — but the placeholder says what is actually
            being searched. "搜索笔记与待办…" would be a lie in the pages
            view, which retrieves approved pages. */}
        <div className={s.search} role="search" aria-label={view === 'pages' ? t('wiki.searchAria') : t('knowledge.searchAria')}>
          <Icon name="search" size={15} className={s.searchIcon} />
          <input
            type="search"
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder={view === 'pages' ? t('wiki.searchPlaceholder') : t('knowledge.searchPlaceholder')}
            aria-label={view === 'pages' ? t('wiki.searchAria') : t('knowledge.searchAria')}
            className={`form-input ${s.searchInput}`}
            autoFocus
          />
        </div>

        {/* The scope selector used to live only on the pages view, even
            though the notes search was cross-project all along — the uneven
            pair was the clearest sign that these were one thing split in
            two. Now both views narrow by the same scope. */}
        <label className={s.scope}>
          <span className={s.scopeLabel}>{t('knowledge.scopeLabel', { defaultValue: '作用域' })}</span>
          <select
            className={s.select}
            value={scope}
            onChange={e => setScope(Number(e.target.value))}
          >
            <option value={0}>{t('knowledge.scopeAll', { defaultValue: '全部项目' })}</option>
            {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </label>
      </div>

      {bodyError ? (
        <ErrorBanner message={bodyError} onRetry={() => retryRef.current?.()} />
      ) : bodyLoading ? (
        <div className={s.loading} aria-hidden>
          <div className="skeleton skeleton-text" style={{ height: 56 }} />
          <div className="skeleton skeleton-text" style={{ height: 56 }} />
          <div className="skeleton skeleton-text" style={{ height: 56 }} />
        </div>
      ) : null}

      {/* One count bar for the tab. It sits right under the toolbar so both
          views read the same way: what you are looking at, and how much of
          it there is. */}
      {count > 0 && (
        <div className={s.resultCount} aria-live="polite">
          {t(countKey, { count })}
        </div>
      )}

      {view === 'notes' ? (
        <NotesBody
          query={query}
          scope={scope}
          projects={projects}
          onCount={handleCount}
          onError={handleError}
          onLoading={handleLoading}
        />
      ) : (
        <WikiPagesView
          query={query}
          scope={scope}
          projects={projects}
          onCount={handleCount}
          onError={handleError}
          onLoading={handleLoading}
        />
      )}
    </div>
  )
}

// The notes body: a grid of notes with filters, plus the write actions
// (create / import / file). It is the only writable half of the tab.
function NotesBody({
  query,
  scope,
  projects,
  onCount,
  onError,
  onLoading,
}: {
  query: string
  scope: number
  projects: Project[]
  onCount: (count: number, countKey: string) => void
  onError: (message: string, retry: () => void) => void
  onLoading: (loading: boolean) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  // Notes waiting to be filed, reported up by KnowledgeOrganize so the toolbar
  // can offer a jump link (see the button below).
  const [pendingOrganize, setPendingOrganize] = useState(0)
  const {
    notes, tags, loading, error, hits, kindFilter, activeTag,
    activeTopic, pinnedOnly, importing, message, newNotePicker, exportingId,
    filtered, projectNames, recentNotes, topicCounts,
    setKindFilter, setActiveTag, setActiveTopic, setPinnedOnly, setNewNotePicker,
    setQuery, handlePin, handleExport, handleImport, fetchAll, flashMessage,
  } = useKnowledgePage({ projects, scopeProjectId: scope })

  // The query is the skeleton's (one field for both views); the hook's
  // search follows it. The hook keeps its own copy so that it still owns
  // "what is being searched for" — but the field the user types in is the
  // one that decides.
  useEffect(() => { setQuery(query) }, [query, setQuery])

  // The count is the skeleton's count bar's business: the body reports what
  // it has, the skeleton draws it in one place for both views. In search
  // mode the hits list carries its own count in its heading, so the shared
  // bar stays out of the way.
  const count = hits !== null ? 0 : filtered.length
  useEffect(() => { onCount(count, 'knowledge.resultCount') }, [count, onCount])

  const retry = useCallback(() => { void fetchAll() }, [fetchAll])
  useEffect(() => { onError(error, retry) }, [error, retry, onError])
  useEffect(() => { onLoading(loading) }, [loading, onLoading])

  const handleQuickCreate = () => {
    if (projectNames.length === 0) {
      flashMessage(t('knowledge.noProjectsMsg', { defaultValue: 'No projects configured. Add one in Settings.' }), 4000)
      return
    }
    if (projectNames.length === 1) {
      navigate(`/project/${projectNames[0][1]}?newNote=1`)
      return
    }
    setNewNotePicker(v => !v)
  }

  const pickProject = (id: number) => {
    setNewNotePicker(false)
    navigate(`/project/${id}?newNote=1`)
  }

  // While the notes load the skeleton shows the shared loading block, and a
  // failure is the shared error banner — the body draws neither.
  if (loading || error) return null

  return (
    <>
      {/* The write actions belong to the notes half of the tab (the pages
          half is read only), so they live with the notes — but they are
          still the tab's primary verbs, hence the row of their own right
          under the shared toolbar. */}
      <div className={`page-head-actions ${s.actions}`}>
        {/* The organize section renders at the very bottom of a page that is
            ~10,000px tall (the note grid sits above it), so with nothing at
            the top pointing at it, 14 notes across 6 landing buckets read as
            "nowhere to be seen" — which is exactly how it looked in the
            2026-10-21 smoke test. A count-bearing jump link puts it one
            click away, and disappears when there is nothing to file. */}
        {pendingOrganize > 0 && (
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => document.getElementById('knowledge-organize')?.scrollIntoView({ block: 'start' })}
          >
            {t('knowledge.organizeJump', { count: pendingOrganize, defaultValue: `待整理 ${pendingOrganize}` })}
          </button>
        )}
        <button className="btn btn-primary btn-sm" onClick={handleQuickCreate}>
          {t('knowledge.quickCreate')}
        </button>
        <button className="btn btn-secondary btn-sm" onClick={handleImport} disabled={importing}>
          {importing ? t('knowledge.importing') : t('knowledge.importClaude')}
        </button>
      </div>

      {newNotePicker && (
        <div className={s.newNotePicker}>
          <div className={s.newNotePickerTitle}>{t('knowledge.selectProject')}</div>
          <div className={s.newNotePickerList}>
            {projectNames.map(([name, id]) => (
              <button key={id} className={s.newNotePickerItem} onClick={() => pickProject(id)}>
                <span className={s.hitProject}>{name}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {message && <div className="message-banner">{message}</div>}

      {hits !== null ? (
        <div className="knowledge-section">
          <div className="section-header">
            <h2>{t('knowledge.searchResults')} ({hits.length})</h2>
          </div>
          {hits.length === 0 ? (
            <p className="empty-hint">{t('knowledge.noResults')}</p>
          ) : (
            <div className={s.hitList}>
              {hits.map(h => (
                <Link
                  key={`${h.type}-${h.id}`}
                  to={
                    h.type === 'note'
                      ? `/project/${h.project_id}?tab=notes&noteId=${h.id}`
                      : `/project/${h.project_id}?tab=todos`
                  }
                  className={s.hitItem}
                >
                  <div className={s.hitHead}>
                    <span className={`${s.hitType} hit-type-${h.type}`}>{h.type === 'note' ? t('dashboard.noteType') : t('summaryBar.todos')}</span>
                    <span className={s.hitProject}>{h.project_name}</span>
                  </div>
                  <div className={s.hitTitle}>{h.title}</div>
                  <div className={s.hitSnippet} dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(h.snippet) }} />
                </Link>
              ))}
            </div>
          )}
        </div>
      ) : (
        <>
          {recentNotes.length > 0 && (
            <div className={`knowledge-section ${s.recentSection}`}>
              <div className="section-header">
                <h2>{t('knowledge.recent')}</h2>
              </div>
              <div className={s.recentList}>
                {recentNotes.map(n => (
                  <Link key={n.id} to={`/project/${n.project_id}?tab=notes&noteId=${n.id}`} className={s.recentItem}>
                    <span className={s.recentTitle}>{n.title || stripMarkdown(n.content, 40)}</span>
                    <span className={s.recentProject}>{n.project_name}</span>
                    <span className={s.recentTime}>{n.updated_at.slice(0, 10)}</span>
                  </Link>
                ))}
              </div>
            </div>
          )}

          <div className={s.filters}>
            <div className="filter-toggle">
              <button className={`filter-btn ${kindFilter === 'all' ? 'active' : ''}`} onClick={() => setKindFilter('all')}>{t('knowledge.all')}</button>
              <button className={`filter-btn ${kindFilter === 'knowledge' ? 'active' : ''}`} onClick={() => setKindFilter('knowledge')}>{t('knowledge.knowledge')}</button>
              <button className={`filter-btn ${kindFilter === 'other' ? 'active' : ''}`} onClick={() => setKindFilter('other')}>{t('knowledge.other')}</button>
            </div>
            <button
              className={`filter-btn ${pinnedOnly ? 'active pinned-active' : ''}`}
              onClick={() => setPinnedOnly(v => !v)}
              title={t('knowledge.pinnedOnly')}
            >
              <Icon name="pin" size={14} /> {t('knowledge.pinnedOnly')}
              {/* No count here: it sits one line below in the result count, and
                  two live counters on screen disagree the moment a filter is
                  on. The chip says which filter; the count says how much. */}
            </button>
          </div>

          {tags.length > 0 && (
            <div className={s.tagChips}>
              <button
                className={`${s.tagChip} ${activeTag === null ? s.tagChipActive : ''}`}
                onClick={() => setActiveTag(null)}
              >
                {t('knowledge.allTags')}
              </button>
              {tags.map(tg => (
                <button
                  key={tg}
                  className={`${s.tagChip} ${activeTag === tg ? s.tagChipActive : ''}`}
                  onClick={() => setActiveTag(activeTag === tg ? null : tg)}
                >
                  #{tg}
                </button>
              ))}
            </div>
          )}

          {/* The topic layer seen from the hub: one chip per subject that
              actually has notes here, with its count. Clicking narrows the
              list to that subject. */}
          {topicCounts.length > 0 && (
            <div className={s.tagChips}>
              <button
                className={`${s.tagChip} ${activeTopic === null ? s.tagChipActive : ''}`}
                onClick={() => setActiveTopic(null)}
              >
                {t('knowledge.allTopics')}
              </button>
              {topicCounts.map(([name, count]) => (
                <button
                  key={name}
                  className={`${s.tagChip} ${activeTopic === name ? s.tagChipActive : ''}`}
                  onClick={() => setActiveTopic(activeTopic === name ? null : name)}
                  title={t('knowledge.topicFilterHint', { name })}
                >
                  {name} ({count})
                </button>
              ))}
            </div>
          )}

          <div className="knowledge-section">
            {projectNames.length > 0 && (
              <div className={s.projectJump}>
                <span className={s.projectJumpLabel}>{t('knowledge.jumpProject')}</span>
                {projectNames.slice(0, 8).map(([name, id]) => (
                  <Link key={id} to={`/project/${id}`} className={s.projectJumpItem} title={name}>{name}</Link>
                ))}
              </div>
            )}

            {filtered.length === 0 ? (
              notes.length === 0 ? (
                <div className="empty-state large">
                  <div className="empty-icon"><Icon name="file-text" size={32} /></div>
                  <h3>{t('knowledge.startBrain')}</h3>
                  <p>{t('knowledge.startBrainMsg')}</p>
                  <div className="empty-actions">
                    <button className="btn btn-primary" onClick={handleQuickCreate}>
                      {t('knowledge.createNote')}
                    </button>
                    <button className="btn btn-secondary" onClick={handleImport} disabled={importing}>
                      {importing ? t('knowledge.importing') : t('knowledge.importClaude')}
                    </button>
                  </div>
                </div>
              ) : (
                <div className="empty-state small">
                  <div className="empty-icon"><Icon name="search" size={32} /></div>
                  <h3>{t('knowledge.noMatch')}</h3>
                  <p>{t('knowledge.adjustMsg')}</p>
                </div>
              )
            ) : (
              <div className={s.noteGrid}>
                {filtered.map(n => (
                  <KnowledgeCard
                    key={n.id}
                    note={n}
                    exporting={exportingId === n.id}
                    onPin={handlePin}
                    onExport={handleExport}
                    onSelectTag={setActiveTag}
                  />
                ))}
              </div>
            )}
          </div>
        </>
      )}

      {/* Always mounted (search mode too); renders nothing when no landing bucket
          awaits filing. The organize surface is where imported-but-unmatched notes
          are assigned to a real project — the ADR-0023 landing counterpart. */}
      <KnowledgeOrganize onPendingChange={setPendingOrganize} />
    </>
  )
}
