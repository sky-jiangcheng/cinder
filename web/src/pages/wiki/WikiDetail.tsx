import { useEffect, useMemo, useState, type MouseEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { getProjects, getWikiPage } from '../../api/client'
import type { PageEdge, Project, WikiPageDetail } from '../../api/client'
import ErrorBanner from '../../components/ErrorBanner'
import Icon from '../../components/Icon'
import { renderMarkdown } from '../../utils/markdown'
import s from './WikiDetail.module.css'

// One approved page, in full (ADR-0032 decision 1: the detail route renders
// the body, the metadata and the sources).
//
// The body is the compiler's own markdown, through the same render +
// sanitize chain the review page uses (marked → DOMPurify). The one addition
// is [[wikilinks]]: they are the library's connective tissue, and a page
// whose body says "见 [[gateway-design]]" and shows dead brackets reads as
// broken. They become real links here — but the href is a FRAGMENT, not a
// path: the desktop shell runs on the hash router, so a path-shaped href
// would leave the app entirely. The click is intercepted and turned into a
// router navigation (see handleBodyClick).

// The marker scheme used in the href. A path-shaped href would break under
// the hash router, and a real URL would have to know which router is active.
const WIKILINK_PREFIX = '#wikilink:'

export default function WikiDetail() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { id } = useParams()
  const pageId = Number(id)

  const [loaded, setLoaded] = useState<{ id: number; page: WikiPageDetail } | null>(null)
  const [failure, setFailure] = useState<{ id: number; message: string } | null>(null)
  const [projects, setProjects] = useState<Project[]>([])

  useEffect(() => {
    let cancelled = false
    getProjects()
      .then(ps => { if (!cancelled) setProjects(ps) })
      .catch(() => { /* the scope falls back to the project id */ })
    return () => { cancelled = true }
  }, [])

  // A non-numeric id is a bad URL, not a backend failure: it is decided at
  // render time, so the effect never has to report it by setting state.
  const invalidId = !Number.isFinite(pageId)

  useEffect(() => {
    if (invalidId) return
    let cancelled = false
    getWikiPage(pageId)
      .then(p => { if (!cancelled) setLoaded({ id: pageId, page: p }) })
      .catch((e: unknown) => {
        if (cancelled) return
        const detail = e instanceof Error ? e.message : ''
        setFailure({
          id: pageId,
          message: detail ? `${t('wiki.pageNotFound')} — ${detail}` : t('wiki.pageNotFound'),
        })
      })
    return () => { cancelled = true }
  }, [pageId, invalidId, t])

  // Both the loaded page and the failure are tagged with the id they belong
  // to: navigating from one page to another reuses this component, and a
  // stale page or a stale error must not survive the navigation.
  const page = loaded && loaded.id === pageId ? loaded.page : null
  const error = invalidId
    ? t('wiki.pageNotFound')
    : failure && failure.id === pageId ? failure.message : ''
  const loading = !page && !error

  const projectName = useMemo(() => {
    const m = new Map<number, string>()
    for (const p of projects) m.set(p.id, p.name)
    return (pid: number) => m.get(pid) ?? `#${pid}`
  }, [projects])

  // The [[slug]] targets of THIS page — the edges it points at. A slug that
  // is not among them stays literal text: an unresolved wikilink is a fact
  // about the page, and hiding it would misrepresent the library.
  const linkMap = useMemo(() => {
    const m = new Map<string, PageEdge>()
    for (const l of page?.out_links ?? []) m.set(l.slug, l)
    return m
  }, [page])

  const bodyHtml = useMemo(() => {
    if (!page) return ''
    const withLinks = page.content.replace(/\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g, (whole, rawSlug, rawLabel) => {
      const slug = String(rawSlug).trim()
      const edge = linkMap.get(slug)
      if (!edge) return whole
      // Escape the brackets so a title carrying them cannot break the link
      // markup; the href carries the slug, encoded.
      const text = String(rawLabel ?? edge.title ?? slug).replace(/[[\]]/g, '\\$&')
      return `[${text}](${WIKILINK_PREFIX}${encodeURIComponent(edge.slug)})`
    })
    return renderMarkdown(withLinks)
  }, [page, linkMap])

  const handleBodyClick = (e: MouseEvent<HTMLDivElement>) => {
    const anchor = (e.target as HTMLElement).closest('a')
    if (!anchor) return
    const href = anchor.getAttribute('href') ?? ''
    if (!href.startsWith(WIKILINK_PREFIX)) return
    e.preventDefault()
    const edge = linkMap.get(decodeURIComponent(href.slice(WIKILINK_PREFIX.length)))
    if (edge) navigate(`/wiki/${edge.page_id}`)
  }

  if (loading) {
    return (
      <div className={s.page}>
        <div className="skeleton skeleton-text" style={{ height: 28, width: '40%', marginBottom: 12 }} />
        <div className="skeleton skeleton-text" style={{ height: 18, width: '70%', marginBottom: 24 }} />
        <div className="skeleton skeleton-text" style={{ height: 120 }} />
      </div>
    )
  }

  if (error || !page) {
    return (
      <div className={s.page}>
        <Link to="/knowledge?view=pages" className={s.back}>
          <Icon name="arrow-up" size={14} className={s.backIcon} />
          {t('wiki.backToList')}
        </Link>
        <ErrorBanner message={error || t('wiki.pageNotFound')} />
      </div>
    )
  }

  return (
    <div className={s.page}>
      <Link to="/knowledge?view=pages" className={s.back}>
        <Icon name="arrow-up" size={14} className={s.backIcon} />
        {t('wiki.backToList')}
      </Link>

      <header className={s.head}>
        <div className={s.headMeta}>
          <span className={`${s.kindBadge} ${s['kindBadge_' + page.kind] ?? ''}`}>
            {t(`wiki.kind_${page.kind}`, page.kind)}
          </span>
          <span className={`${s.scopeBadge} ${page.project_id === 0 ? s.scopeBadgeGlobal : ''}`}>
            {page.project_id === 0 ? t('wiki.scopeGlobal') : projectName(page.project_id)}
          </span>
          <span className={s.slug}>{page.slug}</span>
        </div>
        <h1 className={s.title}>{page.title || page.slug}</h1>
        <div className={s.footMeta}>
          <span>{t('wiki.updatedAt')} {page.updated_at.slice(0, 10)}</span>
          <span>{t('wiki.sourceLabel')} {page.source}</span>
        </div>
      </header>

      <article
        className={`${s.body} markdown-body`}
        onClick={handleBodyClick}
        dangerouslySetInnerHTML={{ __html: bodyHtml }}
      />

      {page.source_note_ids.length > 0 && (
        <section className={s.section}>
          <h2 className={s.sectionTitle}>{t('wiki.sourceNotes')}</h2>
          <div className={s.chips}>
            {page.source_note_ids.map(nid => (
              page.project_id === 0 ? (
                <span key={nid} className={s.chip}>{t('wiki.noteRef', { n: nid })}</span>
              ) : (
                <Link
                  key={nid}
                  to={`/project/${page.project_id}?tab=notes&noteId=${nid}`}
                  className={s.chipLink}
                >
                  {t('wiki.noteRef', { n: nid })}
                </Link>
              )
            ))}
          </div>
        </section>
      )}

      {page.out_links.length > 0 && (
        <section className={s.section}>
          <h2 className={s.sectionTitle}>{t('wiki.outLinks')}</h2>
          <div className={s.chips}>
            {page.out_links.map(l => (
              <Link key={`${l.page_id}-${l.relation}`} to={`/wiki/${l.page_id}`} className={s.chipLink}>
                {l.title || l.slug}
              </Link>
            ))}
          </div>
        </section>
      )}

      {page.in_links.length > 0 && (
        <section className={s.section}>
          <h2 className={s.sectionTitle}>{t('wiki.inLinks')}</h2>
          <div className={s.chips}>
            {page.in_links.map(l => (
              <Link key={`${l.page_id}-${l.relation}`} to={`/wiki/${l.page_id}`} className={s.chipLink}>
                {l.title || l.slug}
              </Link>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}
