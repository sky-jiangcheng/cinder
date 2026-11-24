import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { getNote, listHandoffTimeline, type HandoffTimelineEntry, type Note, type Project } from '../api/client'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import { getToday, getYesterday } from '../utils/dates'
import s from './HandoffTimeline.module.css'

// The knowledge page's handoff timeline: every handoff note across projects,
// newest first, grouped by day. Handoffs live HERE, not in the notes grid —
// one capture per session drowned the notes list, and the user asked for the
// timeline to be the handoff's home (2026-11-24). A card opens the handoff's
// OWN detail panel below it (read-only markdown); the notes detail page stays
// reachable only as a secondary "open in notes" link.

// The window the first render asks for and each "load more" extends. Big
// enough that a heavy user rarely clicks twice; small enough that the first
// paint stays quick.
const PAGE = 50

interface Props {
  /** 0 = all projects (the scope selector's "全部项目"). */
  scope: number
  /** The knowledge page's shared search field, already trimmed. */
  query: string
  projects: Project[]
  onCount: (count: number, countKey: string) => void
  onError: (message: string, retry: () => void) => void
  onLoading: (loading: boolean) => void
}

// updatedAtDay returns the YYYY-MM-DD part of a note timestamp. The DB writes
// SQLite format ("2026-11-24 05:40:19.123"); captures carrying ISO times
// ("2026-11-24T05:40:19Z") exist too — both start with the day.
function updatedAtDay(entry: HandoffTimelineEntry): string {
  return entry.updated_at.slice(0, 10)
}

function updatedAtClock(entry: HandoffTimelineEntry): string {
  const t = entry.updated_at.slice(11, 16)
  return t || ''
}

export default function HandoffTimelineBody({ scope, query, projects, onCount, onError, onLoading }: Props) {
  const { t } = useTranslation()
  const [entries, setEntries] = useState<HandoffTimelineEntry[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [limit, setLimit] = useState(PAGE)
  const [loadingMore, setLoadingMore] = useState(false)
  // The expanded card's handoff detail, fetched in full on open.
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [detail, setDetail] = useState<Note | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState('')

  useEffect(() => {
    let cancelled = false
    setLoading(true) // eslint-disable-line react-hooks/set-state-in-effect
    setError('')
    listHandoffTimeline(limit)
      .then(list => {
        if (cancelled) return
        setEntries(list)
        setError('')
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setEntries([])
        setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
    // `limit` is the only fetch knob: the page refetches on load-more only.
  }, [limit])

  // The report layer is SEPARATE from the fetch effect, on purpose: the body
  // stays mounted across view switches, and switching re-binds these
  // callbacks (noop ↔ the shell's handlers). Reporting the CURRENT state
  // whenever the binding changes is what clears the shell's skeleton after
  // switchView() set bodyLoading=true — without this, the skeleton hung
  // forever over an already-rendered timeline (bug report 2026-11-24).
  // Same pattern as WikiPagesView's report effects.
  useEffect(() => { onLoading(loading) }, [loading, onLoading])
  const retryFetch = useCallback(() => setLimit(l => l + 1), [])
  useEffect(() => { onError(error, retryFetch) }, [error, onError, retryFetch])

  // Filters mirror the knowledge page's shared controls. Scope filters by
  // project; the query narrows to title + excerpt + project name (client-side:
  // the window is ≤200 rows, so a round trip buys nothing).
  const filtered = useMemo(() => {
    if (!entries) return []
    const q = query.trim().toLowerCase()
    return entries.filter(e => {
      if (scope !== 0 && e.project_id !== scope) return false
      if (!q) return true
      return (
        e.title.toLowerCase().includes(q) ||
        e.excerpt.toLowerCase().includes(q) ||
        e.project_name.toLowerCase().includes(q)
      )
    })
  }, [entries, scope, query])

  useEffect(() => { onCount(filtered.length, 'knowledge.timelineCount') }, [filtered.length, onCount])

  // Group the filtered window into day buckets, newest first. The server
  // already ordered them; grouping preserves that order bucket by bucket.
  const groups = useMemo(() => {
    const byDay = new Map<string, HandoffTimelineEntry[]>()
    for (const e of filtered) {
      const day = updatedAtDay(e)
      if (!byDay.has(day)) byDay.set(day, [])
      byDay.get(day)!.push(e)
    }
    return [...byDay.entries()]
  }, [filtered])

  // The opened card's full body. Fetched when the selection changes, kept
  // while the card stays open.
  useEffect(() => {
    if (selectedId === null) return
    let cancelled = false
    setDetailLoading(true) // eslint-disable-line react-hooks/set-state-in-effect
    setDetailError('')
    getNote(selectedId)
      .then(n => { if (!cancelled) setDetail(n) })
      .catch((err: unknown) => {
        if (cancelled) return
        setDetail(null)
        setDetailError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => { if (!cancelled) setDetailLoading(false) })
    return () => { cancelled = true }
  }, [selectedId])

  const openCard = (id: number) => {
    setSelectedId(cur => (cur === id ? null : id))
    setDetail(null)
    setDetailError('')
  }

  const dayLabel = (day: string): string => {
    if (day === getToday()) return t('knowledge.timelineToday', { defaultValue: '今天' })
    if (day === getYesterday()) return t('knowledge.timelineYesterday', { defaultValue: '昨天' })
    return day
  }

  const projectName = (id: number): string =>
    projects.find(p => p.id === id)?.name ?? ''

  const loadMore = () => {
    setLoadingMore(true)
    setLimit(l => l + PAGE)
    // The fetch effect clears loadingMore via onLoading; this local flag only
    // decorates the button while the round trip runs.
    setTimeout(() => setLoadingMore(false), 800)
  }

  if (entries === null) return null

  if (entries.length === 0) {
    return (
      <div className={s.empty}>
        <p>{t('knowledge.timelineEmpty', { defaultValue: '还没有 Handoff 笔记。会话结束时捕获的交接笔记会出现在这条时间轴上。' })}</p>
      </div>
    )
  }

  return (
    <div className={s.timeline} role="list" aria-label={t('knowledge.timelineAria', { defaultValue: 'Handoff 时间轴' })}>
      {groups.map(([day, dayEntries]) => (
        <section key={day} className={s.dayGroup} aria-label={dayLabel(day)}>
          <h3 className={s.dayHeader}>
            <span className={s.dayLabel}>{dayLabel(day)}</span>
            <span className={s.dayCount}>{t('knowledge.timelineDayCount', { count: dayEntries.length, defaultValue: '{{count}} 条' })}</span>
          </h3>
          <ol className={s.dayList}>
            {dayEntries.map(e => (
              <li key={e.note_id} className={s.item} role="listitem">
                <span className={s.dot} aria-hidden="true" />
                <button
                  type="button"
                  className={s.card}
                  onClick={() => openCard(e.note_id)}
                  aria-expanded={selectedId === e.note_id}
                  title={e.title}
                >
                  <span className={s.cardTop}>
                    {updatedAtClock(e) && <time className={s.clock}>{updatedAtClock(e)}</time>}
                    <span className={s.project}>{e.project_name || projectName(e.project_id)}</span>
                    {e.auto_captured && (
                      <span className={s.autoBadge}>{t('knowledge.timelineAuto', { defaultValue: '自动捕获' })}</span>
                    )}
                  </span>
                  <span className={s.cardTitle}>{e.title}</span>
                  {e.excerpt && (
                    <span className={s.cardExcerpt}>
                      {/* The capture body opens with markdown scaffolding
                          ("# Session Handoff ## Summary …"); the preview
                          strips it so the card reads content, not syntax. */}
                      {stripMarkdown(e.excerpt, 180)}
                    </span>
                  )}
                </button>
                {selectedId === e.note_id && (
                  <div className={s.detail} role="region" aria-label={t('knowledge.timelineDetailAria', { defaultValue: 'Handoff 详情' })}>
                    <div className={s.detailHead}>
                      <span className={s.detailMeta}>
                        {e.project_name || projectName(e.project_id)}
                        {updatedAtClock(e) ? ` · ${updatedAtClock(e)}` : ''}
                        {e.auto_captured ? ` · ${t('knowledge.timelineAuto', { defaultValue: '自动捕获' })}` : ''}
                      </span>
                      <span className={s.detailActions}>
                        <Link className={s.detailLink} to={`/note/${e.note_id}`}>
                          {t('knowledge.timelineOpenInNotes', { defaultValue: '在笔记中打开' })}
                        </Link>
                        <button
                          type="button"
                          className={s.detailClose}
                          onClick={() => openCard(e.note_id)}
                          aria-label={t('knowledge.timelineClose', { defaultValue: '收起详情' })}
                        >
                          ×
                        </button>
                      </span>
                    </div>
                    {detailLoading ? (
                      <div className={s.detailLoading} aria-hidden>
                        <div className="skeleton skeleton-text" style={{ height: 16 }} />
                        <div className="skeleton skeleton-text" style={{ height: 16, width: '70%' }} />
                      </div>
                    ) : detailError ? (
                      <p className={s.detailError}>{detailError}</p>
                    ) : detail ? (
                      <div
                        className={`${s.detailBody} markdown-body`}
                        // Same render path as NoteDetail's body: one markdown
                        // renderer, one sanitization story.
                        dangerouslySetInnerHTML={{ __html: renderMarkdown(detail.content) }}
                      />
                    ) : null}
                  </div>
                )}
              </li>
            ))}
          </ol>
        </section>
      ))}
      {filtered.length >= limit && (
        <div className={s.moreRow}>
          <button type="button" className={s.moreBtn} onClick={loadMore} disabled={loadingMore}>
            {loadingMore
              ? t('knowledge.timelineLoadingMore', { defaultValue: '加载中…' })
              : t('knowledge.timelineLoadMore', { defaultValue: '加载更早的 Handoff' })}
          </button>
        </div>
      )}
    </div>
  )
}
