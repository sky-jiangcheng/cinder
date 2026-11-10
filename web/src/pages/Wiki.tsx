import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { browseWikiPages, searchWikiPages } from '../api/client'
import type { Project, WikiPageSummary } from '../api/client'
import Icon from '../components/Icon'
import s from './Wiki.module.css'

// The approved knowledge layer, read by a person (ADR-0032).
//
// This is the PAGES BODY of the knowledge tab (ADR-0038 decision 4): the
// skeleton — search field, project scope, result count, empty/error
// handling — belongs to the tab, and this file is only what the approved
// pages look like. The scope, the query and the count are handed in; the
// URL is the tab's business.
//
// Three things this page is NOT:
//
//   - it is not a second review surface. Only approved pages are visible
//     here; the gate lives on the review page, and copying it would blur
//     "to be judged" and "published";
//   - it is not an editor. Changing an approved page belongs to ADR-0021's
//     revision flow (ADR-0032 decision 2);
//   - it is not the evidence path. It calls the SAME backend retrieval, so a
//     reader and an answer can never disagree about which pages exist — but
//     opening a page here does NOT count as an access (ADR-0032 decision 4:
//     access_count means "surfaced into an answer", and one column must
//     carry one meaning).
//
// The scope selector carries the evidence path's scope, not the review
// queue's: a project means "that project's pages PLUS the global ones"
// (ADR-0031). Global pages therefore show up inside a project scope, and
// every row says so — without that label the extra page reads like a filter
// that leaked.

export interface WikiPagesViewProps {
  // The shared query and project scope (0 = every project). The skeleton
  // owns both, because the notes body searches with the same two.
  query?: string
  scope?: number
  // The project list, for naming the scope of a row.
  projects?: Project[]
  // The skeleton renders the result count and the error banner, so the body
  // reports what it has instead of drawing its own.
  onCount?: (count: number, countKey: string) => void
  onError?: (message: string, retry: () => void) => void
  onLoading?: (loading: boolean) => void
}

export default function WikiPagesView({
  query = '',
  scope = 0,
  projects = [],
  onCount,
  onError,
  onLoading,
}: WikiPagesViewProps) {
  const { t } = useTranslation()
  const [pages, setPages] = useState<WikiPageSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [token, setToken] = useState(0)

  useEffect(() => {
    const q = query.trim()
    let cancelled = false
    // A new query or scope invalidates the old list and any stale error.
    setLoading(true) // eslint-disable-line react-hooks/set-state-in-effect
    setError('')
    // The search is debounced; the browse feed is not (there is nothing to
    // type into it).
    const timer = window.setTimeout(() => {
      const req = q ? searchWikiPages(scope, q, 50) : browseWikiPages(scope, 100)
      req
        .then(list => {
          if (cancelled) return
          setPages(list)
          setError('')
          setLoading(false)
        })
        .catch((e: unknown) => {
          if (cancelled) return
          setError(e instanceof Error ? e.message : t('wiki.loadFailed'))
          setLoading(false)
        })
    }, q ? 200 : 0)
    return () => { cancelled = true; window.clearTimeout(timer) }
  }, [query, scope, token, t])

  const projectName = useMemo(() => {
    const m = new Map<number, string>()
    for (const p of projects) m.set(p.id, p.name)
    return (pid: number) => m.get(pid) ?? `#${pid}`
  }, [projects])

  const searching = query.trim().length > 0

  // The count and the failure are the skeleton's to draw: one count bar and
  // one error banner for the whole tab, whatever the body underneath is.
  useEffect(() => { onCount?.(pages.length, 'wiki.resultCount') }, [pages.length, onCount])
  const retry = useCallback(() => { setToken(n => n + 1) }, [])
  useEffect(() => { onError?.(error, retry) }, [error, retry, onError])
  useEffect(() => { onLoading?.(loading) }, [loading, onLoading])

  // While the pages are loading the skeleton shows the shared skeleton
  // block, and a failure is the skeleton's error banner — this body draws
  // neither, so the two views fail and load the same way.
  if (loading || error) return null

  if (pages.length === 0) {
    return searching ? (
      <div className="empty-state small">
        <div className="empty-icon"><Icon name="search" size={32} /></div>
        <h3>{t('wiki.noMatchTitle')}</h3>
        <p>{t('wiki.noMatchMsg')}</p>
      </div>
    ) : (
      <div className="empty-state large">
        <div className="empty-icon"><Icon name="file-text" size={32} /></div>
        <h3>{t('wiki.emptyTitle')}</h3>
        <p>{t('wiki.emptyMsg')}</p>
        <div className="empty-actions">
          <Link to="/review" className="btn btn-secondary">{t('wiki.emptyLink')}</Link>
        </div>
      </div>
    )
  }

  return (
    <ul className={s.list}>
      {pages.map(p => (
        <li key={p.id}>
          <Link to={`/wiki/${p.id}`} state={{ q: query, project: scope }} className={s.row}>
            <span className={s.rowHead}>
              <span className={`${s.kindBadge} ${s['kindBadge_' + p.kind] ?? ''}`}>
                {t(`wiki.kind_${p.kind}`, p.kind)}
              </span>
              <span className={s.rowTitle}>{p.title || p.slug}</span>
              <span className={`${s.scopeBadge} ${p.project_id === 0 ? s.scopeBadgeGlobal : ''}`}>
                {p.project_id === 0 ? t('wiki.scopeGlobal') : projectName(p.project_id)}
              </span>
            </span>
            <span className={s.rowSlug}>{p.slug}</span>
            <span className={s.rowSnippet}>{p.snippet}</span>
            <span className={s.rowDate}>{p.updated_at.slice(0, 10)}</span>
          </Link>
        </li>
      ))}
    </ul>
  )
}
