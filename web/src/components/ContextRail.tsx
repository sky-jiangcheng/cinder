// components/ContextRail.tsx — the right column of the desktop shell.
//
// A persistent context rail, not another navigation surface: it answers
// "what is waiting for me, and what changed" without leaving the page you
// are on. Two blocks, each backed by an existing binding:
//
//   1. the review queue — pages the local model compiled that only a human
//      may approve (ADR-0015)
//   2. the knowledge pages that changed last (ADR-0032)
//
// The global search input that used to sit on top of this rail is gone
// (ADR-0038 决策 6): the home page is now a full-page conversation console
// with its own input, and ⌘K still opens the command palette — a second
// search box in the rail made "search" look like a rail feature when it is
// a top-level action. What is left is exactly the context the rail is for:
// things waiting for a decision, and things that just changed.
//
// It collapses to a handle on the right edge and stays that way until the
// user opens it again; the state is remembered per machine.

import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { browseWikiPages, listPendingWikiPages, type WikiPageSummary } from '../api/client'
import s from './ContextRail.module.css'

/**
 * Run a binding call and report its outcome, without letting a synchronous
 * throw escape.
 *
 * The transport throws synchronously when a binding is missing — an older
 * desktop binary running a newer frontend, or a test mock that does not carry
 * the method. A throw inside an effect would take the whole shell down, and
 * the rail is decoration: it degrades to empty lists instead.
 */
function load<T>(run: () => Promise<T>, onValue: (value: T) => void, onFailure: () => void) {
  try {
    run().then(onValue, onFailure)
  } catch {
    onFailure()
  }
}


interface Props {
  /** True when the rail is folded away to its handle. */
  collapsed: boolean
  onToggle: (collapsed: boolean) => void
}

export default function ContextRail({ collapsed, onToggle }: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [pending, setPending] = useState<{ id: number; title: string }[] | null>(null)
  const [recent, setRecent] = useState<WikiPageSummary[] | null>(null)

  useEffect(() => {
    let cancelled = false
    load(
      () => listPendingWikiPages(0),
      pages => { if (!cancelled) setPending((pages ?? []).map(p => ({ id: p.page.id, title: p.page.title }))) },
      () => { if (!cancelled) setPending([]) },
    )
    load(
      () => browseWikiPages(0, 6),
      pages => { if (!cancelled) setRecent(pages ?? []) },
      () => { if (!cancelled) setRecent([]) },
    )
    return () => { cancelled = true }
  }, [])

  if (collapsed) {
    // The folded handle is the rail's whole presence, so it carries the one
    // number that matters: how many compiled pages are waiting for a human
    // decision. Context reaches the user without the rail eating the page.
    const waiting = pending?.length ?? 0
    return (
      <button
        type="button"
        className={s.handle}
        onClick={() => onToggle(false)}
        aria-label={t('shell.contextShow')}
        title={t('shell.contextShow')}
      >
        <Icon name="panel-right" size={16} />
        {waiting > 0 && <span className={s.handleCount}>{waiting}</span>}
      </button>
    )
  }

  return (
    <aside className={s.rail} aria-label={t('shell.contextTitle')}>
      <header className={s.head}>
        <h2 className={s.headTitle}>{t('shell.contextTitle')}</h2>
        <button
          type="button"
          className={s.collapseBtn}
          onClick={() => onToggle(true)}
          aria-label={t('shell.contextHide')}
          title={t('shell.contextHide')}
        >
          <Icon name="panel-right" size={15} />
        </button>
      </header>

      <section className={s.block}>
        <h3 className={s.blockTitle}>
          <span>{t('shell.pendingTitle')}</span>
          {pending && pending.length > 0 && <span className={s.count}>{pending.length}</span>}
        </h3>
        {pending === null ? null : pending.length === 0 ? (
          <p className={s.empty}>{t('shell.pendingEmpty')}</p>
        ) : (
          <ul className={s.list}>
            {pending.slice(0, 5).map(p => (
              <li key={p.id}>
                <button type="button" className={s.item} onClick={() => navigate('/review')}>
                  <span className={s.itemTitle}>{p.title}</span>
                </button>
              </li>
            ))}
            {pending.length > 5 && (
              <li>
                <button type="button" className={s.more} onClick={() => navigate('/review')}>
                  {t('shell.pendingMore', { count: pending.length - 5 })}
                </button>
              </li>
            )}
          </ul>
        )}
      </section>

      <section className={s.block}>
        <h3 className={s.blockTitle}>
          <span>{t('shell.recentTitle')}</span>
        </h3>
        {recent === null ? null : recent.length === 0 ? (
          <p className={s.empty}>{t('shell.recentEmpty')}</p>
        ) : (
          <ul className={s.list}>
            {recent.map(p => (
              <li key={p.id}>
                <button type="button" className={s.item} onClick={() => navigate(`/wiki/${p.id}`)}>
                  <span className={s.itemTitle}>{p.title}</span>
                  <span className={s.itemMeta}>{p.updated_at.slice(0, 10)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </aside>
  )
}
