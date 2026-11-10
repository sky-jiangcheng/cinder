import { useState, useEffect, useRef, useMemo } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { getProjects, searchAll, captureClaudeHandoff, SearchHit } from '../api/client'
import { useTranslation } from 'react-i18next'
import DOMPurify from 'dompurify'
import { useApiData } from '../hooks/useApiData'
import HandoffComposer from './HandoffComposer'
import { hitHref } from '../pages/home/HomeKnowledgeAssistant'
import s from './CommandPalette.module.css'

interface Props {
  open: boolean
  onClose: () => void
}

// CommandPalette is a Cmd/Ctrl+K quick-switcher: search notes & todos across all
// projects and jump straight to a project. It surfaces the knowledge-search
// capability as a first-class keyboard action.
//
// Accessibility: implemented as a combobox-in-dialog (ARIA APG). The dialog traps
// focus, restores it to the trigger on close, and the input drives selection via
// aria-activedescendant over a role="listbox" of role="option" items.
export default function CommandPalette({ open, onClose }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [hits, setHits] = useState<SearchHit[]>([])
  const [searchError, setSearchError] = useState(false)
  // Handoff mode (D25, ADR-0017 附录 4): the palette's first action switches
  // into project-pick for a one-click Claude-session capture, so creating a
  // handoff is reachable from the keyboard without leaving the palette.
  const [handoffMode, setHandoffMode] = useState(false)
  const [handoffMsg, setHandoffMsg] = useState<{ ok: boolean; text: string } | null>(null)
  // The manual composer lives beside the palette, not inside it: the palette's
  // overlay carries a backdrop-filter, which makes it the containing block for
  // fixed-position descendants, and a dialog anchored to the overlay would
  // scroll with it instead of the viewport.
  const [composerOpen, setComposerOpen] = useState(false)
  // All-projects list for the quick-jump suggestions, shared with NoteSection
  // via the same cache key so only one request is ever made.
  const { data: projectsData } = useApiData(() => getProjects(undefined, false), [], { cacheKey: 'projects:all' })
  // Stabilize the fallback so the useMemo below doesn't re-run every render
  // (the `?? []` would otherwise allocate a fresh array on each pass).
  const projects = useMemo(() => projectsData ?? [], [projectsData])
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const previouslyFocused = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (open) {
      // Record the element that had focus (the trigger button) so we can restore it.
      previouslyFocused.current = document.activeElement as HTMLElement
      setQuery('') // eslint-disable-line react-hooks/set-state-in-effect
      setHits([])
      setSearchError(false)
      setActiveIndex(0)
      setHandoffMode(false)
      setHandoffMsg(null)
      setTimeout(() => inputRef.current?.focus(), 30)
    } else {
      // Restore focus to the triggering element when the palette closes.
      const el = previouslyFocused.current
      if (el && typeof el.focus === 'function') {
        el.focus()
        previouslyFocused.current = null
      }
    }
  }, [open])

  useEffect(() => {
    if (!open || handoffMode) return
    const trimmed = query.trim()
    if (!trimmed) { setHits([]); setActiveIndex(0); setSearchError(false); return } // eslint-disable-line react-hooks/set-state-in-effect
    let cancelled = false
    const timer = setTimeout(() => {
      searchAll(trimmed).then(r => {
        if (!cancelled) { setHits(r ?? []); setActiveIndex(0); setSearchError(false) }
      }).catch((e: unknown) => {
        if (cancelled) return
        console.error('[CommandPalette] search failed:', e)
        setSearchError(true)
      })
    }, 150)
    return () => { cancelled = true; clearTimeout(timer) }
  }, [query, open, handoffMode])

  const filteredProjects = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return projects.slice(0, 5)
    return projects.filter(p => p.name.toLowerCase().includes(q)).slice(0, 5)
  }, [projects, query])

  // The handoff actions sit at the top of the idle (empty-query) list:
  // ADR-0017 附录 4 「Command Palette 首位动作改为新建 handoff」. Two of them
  // now — writing one by hand needs nothing configured, while the Claude
  // capture needs the CLI AND the `claude_session_capture` gate (default off),
  // so the manual one comes first and the capture second.
  const showHandoffActions = !handoffMode && !query.trim()
  const actionCount = showHandoffActions ? 2 : 0

  const totalItems = actionCount + hits.length + filteredProjects.length
  const activeId = totalItems > 0 ? `cmdk-opt-${activeIndex}` : undefined

  // Keep the active option in view as the user navigates with the arrow keys,
  // so the visible highlight and aria-activedescendant stay in sync.
  useEffect(() => {
    if (!open || !activeId) return
    document.getElementById(activeId)?.scrollIntoView({ block: 'nearest' })
  }, [activeId, open])

  const runCapture = (projectID: number) => {
    captureClaudeHandoff(projectID)
      .then(r => {
        setHandoffMsg({ ok: true, text: t('commandPalette.handoffOk', { defaultValue: '已创建：{{title}}', title: r?.title ?? '' }) })
        setTimeout(onClose, 1600)
      })
      .catch((e: unknown) => {
        setHandoffMsg({
          ok: false,
          text: t('commandPalette.handoffFail', {
            defaultValue: '捕捉失败：{{msg}}',
            msg: e instanceof Error ? e.message : String(e),
          }),
        })
      })
  }

  const goto = (index: number) => {
    if (handoffMode) {
      const p = filteredProjects[index]
      if (p) runCapture(p.id)
      return
    }
    if (showHandoffActions) {
      if (index === 0) { onClose(); setComposerOpen(true); return }
      if (index === 1) { setHandoffMode(true); setActiveIndex(0); return }
      index -= 2
    }
    if (index < hits.length) {
      const h = hits[index]
      if (h) { navigate(hitHref(h)); onClose() }
    } else {
      const p = filteredProjects[index - hits.length]
      if (p) { navigate(`/project/${p.id}`); onClose() }
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setActiveIndex(i => Math.min(i + 1, totalItems - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActiveIndex(i => Math.max(i - 1, 0)) }
    else if (e.key === 'Enter') { e.preventDefault(); goto(activeIndex) }
    else if (e.key === 'Escape') { e.preventDefault(); onClose() }
    else if (e.key === 'Tab') {
      // Focus trap: the input is the only tabbable element inside the dialog,
      // so Tab / Shift+Tab cycles back to it instead of escaping to the page.
      e.preventDefault()
      inputRef.current?.focus()
    }
  }

  return (
    <>
    {open && (
    <div className={s.cmdkOverlay} onClick={onClose} tabIndex={-1}>
      <div
        className={s.cmdk}
        role="dialog"
        aria-modal="true"
        aria-label={t('commandPalette.title')}
        onClick={e => e.stopPropagation()}
      >
        <input
          ref={inputRef}
          type="text"
          role="combobox"
          aria-label={t('commandPalette.searchLabel')}
          aria-expanded={totalItems > 0}
          aria-controls="cmdk-results"
          aria-autocomplete="list"
          aria-activedescendant={activeId}
          value={query}
          onChange={e => setQuery(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t('commandPalette.placeholder')}
          className={`cmdk-input ${s.cmdkInput}`}
        />
        <div className={s.cmdkResults} id="cmdk-results" role="listbox" aria-label={handoffMode ? t('commandPalette.pickProject', { defaultValue: '选择要捕捉的项目' }) : t('commandPalette.groupNotes')}>
          {handoffMode && filteredProjects.length > 0 && (
            <div className={s.cmdkGroup}>{t('commandPalette.pickProject', { defaultValue: '选择要捕捉的项目' })}</div>
          )}
          {totalItems === 0 && (
            <div className={s.cmdkEmpty}>
              {handoffMode
                ? t('commandPalette.noProjectsToCapture', { defaultValue: '没有可选的项目' })
                : query.trim()
                  ? t('commandPalette.noResults', { defaultValue: 'No results' })
                  : t('commandPalette.startSearch', { defaultValue: 'Start searching' })}
            </div>
          )}
          {searchError && <div className={s.cmdkEmpty}>{t('common.loadFailed')}</div>}
          {showHandoffActions && (
            <>
              <button
                type="button"
                id="cmdk-opt-0"
                role="option"
                aria-selected={activeIndex === 0}
                tabIndex={-1}
                className={`${s.cmdkItem} ${activeIndex === 0 ? s.cmdkItemActive : ''}`}
                onMouseEnter={() => setActiveIndex(0)}
                onClick={() => { onClose(); setComposerOpen(true) }}
              >
                <span className={`${s.cmdkType} ${s.cmdkTypeNote}`}>{t('commandPalette.newHandoffMark', { defaultValue: '新建' })}</span>
                <div className={s.cmdkItemBody}>
                  <div className={s.cmdkItemTitle}>{t('commandPalette.newHandoff', { defaultValue: '写一条交接记录' })}</div>
                  <div className={s.cmdkItemSub}>{t('commandPalette.newHandoffHint', { defaultValue: '手动写下这次会话的交接记录' })}</div>
                </div>
              </button>
              <button
                type="button"
                id="cmdk-opt-1"
                role="option"
                aria-selected={activeIndex === 1}
                tabIndex={-1}
                className={`${s.cmdkItem} ${activeIndex === 1 ? s.cmdkItemActive : ''}`}
                onMouseEnter={() => setActiveIndex(1)}
                onClick={() => { setHandoffMode(true); setHits([]); setActiveIndex(0) }}
              >
                <span className={`${s.cmdkType} ${s.cmdkTypeTodo}`}>{t('commandPalette.captureHandoffMark', { defaultValue: '捕捉' })}</span>
                <div className={s.cmdkItemBody}>
                  <div className={s.cmdkItemTitle}>{t('commandPalette.captureHandoff', { defaultValue: '捕捉 Claude 会话' })}</div>
                  <div className={s.cmdkItemSub}>{t('commandPalette.captureHandoffHint', { defaultValue: '把最近的 Claude 会话转成交接记录' })}</div>
                </div>
              </button>
            </>
          )}
          {hits.length > 0 && <div className={s.cmdkGroup}>{t('commandPalette.groupNotes')}</div>}
          {hits.map((h, i) => {
            const idx = i + actionCount
            return (
            <Link
              key={`${h.type}-${h.id}`}
              id={`cmdk-opt-${idx}`}
              to={hitHref(h)}
              className={`${s.cmdkItem} ${activeIndex === idx ? s.cmdkItemActive : ''}`}
              role="option"
              aria-selected={activeIndex === idx}
              tabIndex={-1}
              onMouseEnter={() => setActiveIndex(idx)}
              onClick={onClose}
            >
              <span className={`${s.cmdkType} ${h.type === 'note' ? s.cmdkTypeNote : s.cmdkTypeTodo}`}>{h.type === 'note' ? t('commandPalette.noteMark') : t('commandPalette.todoMark')}</span>
              <div className={s.cmdkItemBody}>
                <div className={s.cmdkItemTitle}>{h.title}</div>
                <div className={s.cmdkItemSub}>{h.project_name} · <span dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(h.snippet.slice(0, 60)) }} /></div>
              </div>
            </Link>
            )
          })}
          {filteredProjects.map((p, i) => {
            const idx = hits.length + actionCount + i
            if (handoffMode) {
              return (
                <button
                  type="button"
                  key={p.id}
                  id={`cmdk-opt-${i}`}
                  className={`${s.cmdkItem} ${activeIndex === i ? s.cmdkItemActive : ''}`}
                  role="option"
                  aria-selected={activeIndex === i}
                  tabIndex={-1}
                  onMouseEnter={() => setActiveIndex(i)}
                  onClick={() => runCapture(p.id)}
                >
                  <span className={`${s.cmdkType} ${s.cmdkTypeProject}`}>{t('commandPalette.projectMark')}</span>
                  <div className={s.cmdkItemBody}>
                    <div className={s.cmdkItemTitle}>{p.name}</div>
                    <div className={s.cmdkItemSub}>{t('commandPalette.reposCount', { count: p.repo_count })}</div>
                  </div>
                </button>
              )
            }
            return (
              <Link
                key={p.id}
                id={`cmdk-opt-${idx}`}
                to={`/project/${p.id}`}
                className={`${s.cmdkItem} ${activeIndex === idx ? s.cmdkItemActive : ''}`}
                role="option"
                aria-selected={activeIndex === idx}
                tabIndex={-1}
                onMouseEnter={() => setActiveIndex(idx)}
                onClick={onClose}
              >
                <span className={`${s.cmdkType} ${s.cmdkTypeProject}`}>{t('commandPalette.projectMark')}</span>
                <div className={s.cmdkItemBody}>
                  <div className={s.cmdkItemTitle}>{p.name}</div>
                  <div className={s.cmdkItemSub}>{t('commandPalette.reposCount', { count: p.repo_count })}</div>
                </div>
              </Link>
            )
          })}
        </div>
        {handoffMsg && (
          <div className={`${s.cmdkEmpty} ${handoffMsg.ok ? s.cmdkMsgOk : s.cmdkMsgError}`} role="status">{handoffMsg.text}</div>
        )}
        <div className={s.cmdkFoot}>
          <span>{t('commandPalette.footSelect')}</span><span>{t('commandPalette.footOpen')}</span><span>{t('commandPalette.footClose')}</span>
        </div>
        <div className="visually-hidden" role="status" aria-live="polite">
          {totalItems > 0 ? t('commandPalette.resultsCount', { count: totalItems }) : (query.trim() ? t('commandPalette.noResults', { defaultValue: 'No results' }) : '')}
        </div>
      </div>
    </div>
    )}
    {/* The composer outlives the palette: picking "write a handoff" closes the
        palette, and a composer mounted inside it would unmount with it. */}
    <HandoffComposer open={composerOpen} onClose={() => setComposerOpen(false)} />
    </>
  )
}
