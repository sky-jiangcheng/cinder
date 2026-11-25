import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../../components/Icon'
import s from './ProjectCommitLog.module.css'
import { useToast } from '../../hooks/useToast'
import {
  getCommitDiff,
  searchCommits,
  type CommitDiff,
  type CommitDiffFile,
  type CommitPage,
  type RepoCommit,
  type RepoInfo,
} from '../../api/client'
import CommitFileCompare from './CommitFileCompare'

// 预留钩子类：`commit-log` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

interface Props {
  projectId: number
  repos: RepoInfo[]
}

const PAGE_SIZES = [20, 50, 100]
const shortPath = (p: string) => p.split('/').slice(-2).join('/')

interface Selection {
  scope: string
  repoId: number
  commit: RepoCommit
}

/**
 * The commit log is now the useful half of the detail page: an open book with
 * the (searchable, paged) timeline on the left and the selected commit's patch
 * on the right. Repository choice is still a chip row so a merged, multi-repo
 * timeline can be narrowed to one history without losing the search term.
 *
 * Git is read on demand: daily_stats carries no message or SHA, so none of
 * this can be assembled from the database.
 */
export default function ProjectCommitLog({ projectId, repos }: Props) {
  const { t } = useTranslation()
  const toast = useToast()
  // Tagged with the project it was picked under: /project/:id reuses this
  // instance across projects, and a repo id selected in one project must not
  // filter another's log.
  const [picked, setPicked] = useState<{ projectId: number; repo: number | 'all' }>({ projectId: 0, repo: 'all' })
  const selected: number | 'all' = picked.projectId === projectId ? picked.repo : 'all'
  const choose = (repo: number | 'all') => {
    setPicked({ projectId, repo })
    setPage(0)
    setSelection(null)
  }
  const [copiedHash, setCopiedHash] = useState<string | null>(null)
  const [copiedPath, setCopiedPath] = useState<string | null>(null)

  const isSingle = repos.length === 1
  const fetchRepoId = isSingle
    ? repos[0].id
    : typeof selected === 'number' ? selected : null

  const [query, setQuery] = useState('')
  const [term, setTerm] = useState('')
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(PAGE_SIZES[0])
  const debounce = useRef<number | null>(null)
  useEffect(() => () => { if (debounce.current) window.clearTimeout(debounce.current) }, [])
  const changeQuery = (v: string) => {
    setQuery(v)
    if (debounce.current) window.clearTimeout(debounce.current)
    debounce.current = window.setTimeout(() => {
      setTerm(v)
      setPage(0)
      setSelection(null)
    }, 250)
  }

  const requestKey = `${projectId}|${fetchRepoId ?? 'all'}|${term}|${page}|${pageSize}`
  const [result, setResult] = useState<{ key: string; page: CommitPage | null; error: string } | null>(null)
  const shown = result && result.key === requestKey ? result : null
  // A page turn re-reads the SAME query, so the outgoing page is still true
  // context: keep it on screen (dimmed) until the incoming one lands instead of
  // blanking the reader and bouncing the scroll. The carried page is scoped to
  // project+repo+search term, so switching any of those still shows a skeleton
  // — a previous project's history must never appear under this one.
  const [lastGood, setLastGood] = useState<{ query: string; page: CommitPage } | null>(null)
  const queryKey = `${projectId}|${fetchRepoId ?? 'all'}|${term}`
  const carried = !shown && lastGood && lastGood.query === queryKey ? lastGood.page : null
  const listed = shown?.page ?? carried
  const commits = listed?.items ?? null
  const total = listed?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / pageSize))
  const refreshing = !shown && !!carried

  useEffect(() => {
    let cancelled = false
    const key = requestKey
    searchCommits(projectId, fetchRepoId ?? 0, term, page * pageSize, pageSize)
      .then(data => {
        if (cancelled) return
        setResult({ key, page: data, error: '' })
        setLastGood({ query: `${projectId}|${fetchRepoId ?? 'all'}|${term}`, page: data })
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setResult({ key, page: null, error: e instanceof Error ? e.message : t('common.failed') })
      })
    return () => { cancelled = true }
  }, [requestKey, projectId, fetchRepoId, term, page, pageSize, t])

  // --- right page: selected commit's patch ---------------------------------
  const scopeKey = `${projectId}|${fetchRepoId ?? 'all'}`
  const [selection, setSelection] = useState<Selection | null>(null)
  const liveSelection = selection && selection.scope === scopeKey ? selection : null
  const selectionKey = liveSelection ? `${liveSelection.repoId}|${liveSelection.commit.hash}` : ''
  const [patch, setPatch] = useState<{ key: string; diff: CommitDiff | null; error: string } | null>(null)
  const livePatch = patch && patch.key === selectionKey ? patch : null
  // Loading is derived from the request key, not stored in an effect: the
  // selected commit changes identity before the fetch resolves, and a stale
  // "loaded" flag would otherwise flash the previous commit's patch.
  const patchLoading = !!liveSelection && !livePatch
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const [compare, setCompare] = useState<{ repoId: number; sha: string; file: CommitDiffFile } | null>(null)

  useEffect(() => {
    if (!liveSelection) return
    let cancelled = false
    const key = selectionKey
    getCommitDiff(liveSelection.repoId, liveSelection.commit.hash)
      .then(diff => { if (!cancelled) setPatch({ key, diff, error: '' }) })
      .catch((e: unknown) => {
        if (cancelled) return
        setPatch({ key, diff: null, error: e instanceof Error ? e.message : t('common.failed') })
      })
    return () => { cancelled = true }
  }, [selectionKey, liveSelection, t])

  const repoIdFor = (commit: RepoCommit): number => {
    const repo = repos.find(r => r.path === commit.repo)
    return repo ? repo.id : (typeof fetchRepoId === 'number' ? fetchRepoId : 0)
  }

  const activeRepo = fetchRepoId === null ? null : repos.find(r => r.id === fetchRepoId) ?? null
  const repoTotals = activeRepo
    ? (activeRepo.stats || []).reduce(
        (acc, stat) => ({ added: acc.added + stat.lines_added, deleted: acc.deleted + stat.lines_deleted }),
        { added: 0, deleted: 0 },
      )
    : null
  const showRepo = fetchRepoId === null

  const copyHash = async (hash: string) => {
    try {
      await navigator.clipboard.writeText(hash)
      setCopiedHash(hash)
      setTimeout(() => setCopiedHash(null), 1500)
    } catch {
      /* clipboard unavailable; the hash is visible in the row regardless */
    }
  }

  const copyPath = async (path: string) => {
    try {
      await navigator.clipboard.writeText(path)
      setCopiedPath(path)
      toast({ kind: 'success', title: t('project.copied'), message: path })
      setTimeout(() => setCopiedPath(null), 1500)
    } catch {
      toast({ kind: 'error', title: t('project.copyFailed') })
    }
  }

  const selectCommit = (commit: RepoCommit) => {
    const repoId = repoIdFor(commit)
    if (!repoId) return
    setSelection({ scope: scopeKey, repoId, commit })
    setCompare(null)
  }

  return (
    <div className="commit-log">
      {repos.length > 1 && (
        <div className={s.filter} role="group" aria-label={t('project.subRepos')}>
          <button
            type="button"
            className={`${s.chip} ${selected === 'all' ? s.chipActive : ''}`}
            aria-pressed={selected === 'all'}
            onClick={() => choose('all')}
          >
            {t('project.filterAll')}
          </button>
          {repos.map((repo) => (
            <button
              key={repo.id}
              type="button"
              className={`${s.chip} ${selected === repo.id ? s.chipActive : ''}`}
              aria-pressed={selected === repo.id}
              title={repo.path}
              onClick={() => choose(repo.id)}
            >
              {shortPath(repo.path)}
            </button>
          ))}
        </div>
      )}

      {activeRepo && repoTotals && (
        <div className={s.repoBar}>
          <span className={s.repoPath} title={activeRepo.path}>{activeRepo.path}</span>
          <div className={s.tools}>
            <a
              className={s.tool}
              href={`vscode://file/${activeRepo.path}`}
              title={t('project.openEditor')}
              aria-label={t('project.openEditor')}
            >
              <Icon name="zap" size={13} />
            </a>
            <button
              className={`${s.tool} ${copiedPath === activeRepo.path ? s.toolDone : ''}`}
              onClick={() => copyPath(activeRepo.path)}
              title={t('project.copyPath')}
              aria-label={t('project.copyPath')}
            >
              <Icon name={copiedPath === activeRepo.path ? 'check' : 'file-text'} size={13} />
            </button>
            {activeRepo.web_url && (
              <a
                className={s.tool}
                href={activeRepo.web_url}
                target="_blank"
                rel="noreferrer noopener"
                title={t('project.openForge')}
                aria-label={t('project.openForge')}
              >
                <Icon name="globe" size={13} />
              </a>
            )}
            <div className={s.totals}>
              <span className={repoTotals.added > 0 ? 'green' : 'muted-num'}>
                {repoTotals.added > 0 ? `+${repoTotals.added}` : '0'}
              </span>
              <span className={repoTotals.deleted > 0 ? 'red' : 'muted-num'}>
                {repoTotals.deleted > 0 ? `-${repoTotals.deleted}` : '0'}
              </span>
            </div>
          </div>
        </div>
      )}

      <div className={s.searchRow}>
        <div className={s.searchBox}>
          <Icon name="search" size={14} />
          <input
            type="search"
            value={query}
            placeholder={t('project.searchCommits')}
            aria-label={t('project.searchCommits')}
            onChange={e => changeQuery(e.target.value)}
          />
          {query && (
            <button type="button" className={s.clearSearch} onClick={() => changeQuery('')} aria-label={t('project.clearSearch')}>
              <Icon name="close" size={12} />
            </button>
          )}
        </div>
        <label className={s.pageSize}>
          {t('project.pageSize')}
          <select
            value={pageSize}
            onChange={e => { setPageSize(Number(e.target.value)); setPage(0) }}
          >
            {PAGE_SIZES.map(n => <option key={n} value={n}>{n}</option>)}
          </select>
        </label>
        <span className={s.resultCount}>{t('project.commitCount', { count: total })}</span>
      </div>

      {commits === null && !shown?.error && (
        <div className={s.feedSkeleton} aria-hidden>
          {Array.from({ length: 7 }).map((_, i) => (
            <div key={i} className={s.skeletonRow}>
              <span className={`skeleton ${s.skeletonDot}`} />
              <span className={s.skeletonBody}>
                <span className="skeleton skeleton-text" style={{ width: `${72 - i * 5}%` }} />
                <span className="skeleton skeleton-text" style={{ width: '38%', height: 9 }} />
              </span>
            </div>
          ))}
        </div>
      )}
      {shown?.error && <div className={`${s.status} ${s.statusError}`}>{shown.error}</div>}
      {commits !== null && !shown?.error && commits.length === 0 && (
        <div className={s.status}>{t('project.noCommits')}</div>
      )}

      {commits !== null && commits.length > 0 && (
        <div className={`${s.book} ${refreshing ? s.bookStale : ''}`} aria-busy={refreshing}>
          <section className={`${s.page} ${s.leftPage}`} aria-label={t('project.logPageTitle')}>
            <div className={s.pageHead}>{t('project.logPageTitle')}</div>
            <ul className={s.feed}>
              {commits.map((c, i) => {
                const repo = showRepo ? repos.find(r => r.path === c.repo) : undefined
                const active = liveSelection?.commit.hash === c.hash && liveSelection.commit.repo === c.repo
                return (
                  <li
                    key={c.hash || `${c.time}-${i}`}
                    className={`${s.feedItem} ${active ? s.feedItemActive : ''}`}
                    role="button"
                    tabIndex={0}
                    aria-pressed={active}
                    onClick={() => selectCommit(c)}
                    onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); selectCommit(c) } }}
                  >
                    <span className={s.dot} />
                    <div className={s.feedBody}>
                      <div className={s.feedMessage} title={c.message}>{c.message}</div>
                      <div className={s.feedMeta}>
                        <span>{c.time}</span>
                        {showRepo && (repo ? (
                          <button
                            className={`${s.repo} ${s.repoButton}`}
                            title={`${c.repo} · ${t('project.filterRepoHint')}`}
                            onClick={e => { e.stopPropagation(); choose(repo.id) }}
                          >
                            {shortPath(c.repo)}
                          </button>
                        ) : (
                          <span className={s.repo} title={c.repo}>{shortPath(c.repo)}</span>
                        ))}
                        {c.branch && <span className={s.branch}>{c.branch}</span>}
                        <span className={s.author}>{c.author}</span>
                        <button
                          className={`${s.hash} ${copiedHash === c.hash ? s.hashCopied : ''}`}
                          onClick={e => { e.stopPropagation(); copyHash(c.hash) }}
                          title={t('project.copyHash')}
                        >
                          <Icon name={copiedHash === c.hash ? 'check' : 'file-text'} size={10} />
                          {c.hash.slice(0, 7)}
                        </button>
                      </div>
                    </div>
                  </li>
                )
              })}
            </ul>
            <div className={s.pager}>
              <button className="btn btn-secondary btn-sm" disabled={page === 0} onClick={() => setPage(p => p - 1)}>
                {t('project.prevPage')}
              </button>
              <span>{t('project.pageIndicator', { page: page + 1, pages: pageCount })}</span>
              <button className="btn btn-secondary btn-sm" disabled={page + 1 >= pageCount} onClick={() => setPage(p => p + 1)}>
                {t('project.nextPage')}
              </button>
            </div>
          </section>

          <section className={`${s.page} ${s.rightPage}`} aria-label={t('project.diffPageTitle')}>
            <div className={s.pageHead}>
              {liveSelection
                ? `${t('project.diffPageTitle')} · ${liveSelection.commit.hash.slice(0, 7)}`
                : t('project.diffPageTitle')}
            </div>
            {!liveSelection && <div className={s.diffPlaceholder}>{t('project.diffEmpty')}</div>}
            {patchLoading && <div className={s.status}>{t('common.loading')}</div>}
            {liveSelection && livePatch?.error && <div className={`${s.status} ${s.statusError}`}>{livePatch.error}</div>}
            {liveSelection && livePatch?.diff && (
              <div className={s.diffBody}>
                {livePatch.diff.files.length === 0 && <div className={s.diffPlaceholder}>{t('project.noDiff')}</div>}
                {livePatch.diff.files.map(file => (
                  <article key={file.path} className={s.diffFile}>
                    <div className={s.fileHead}>
                      <button
                        type="button"
                        className={s.fileToggle}
                        aria-label={collapsed[file.path] ? t('project.expandFile') : t('project.collapseFile')}
                        onClick={() => setCollapsed(prev => ({ ...prev, [file.path]: !prev[file.path] }))}
                      >
                        {collapsed[file.path] ? '▸' : '▾'}
                      </button>
                      <button
                        type="button"
                        className={s.fileName}
                        title={t('project.compareFile')}
                        onClick={() => setCompare({ repoId: liveSelection.repoId, sha: liveSelection.commit.hash, file })}
                      >
                        {file.path}
                      </button>
                      <span className={`${s.fileStatus} ${s[`status_${file.status}`] ?? ''}`}>
                        {t(`project.diffStatus_${file.status}`, { defaultValue: file.status })}
                      </span>
                      <span className={s.fileCounts}>
                        <span className="green">+{file.added}</span>
                        <span className="red">-{file.deleted}</span>
                      </span>
                    </div>
                    {!collapsed[file.path] && (
                      file.binary ? (
                        <div className={s.diffPlaceholder}>{t('project.binaryFile')}</div>
                      ) : (
                        <div className={s.hunks}>
                          {file.hunks.map((hunk, hi) => (
                            <div key={hi} className={s.hunk}>
                              {hunk.header && <div className={s.hunkHeader}>@@ -{hunk.old_start} +{hunk.new_start} @@ {hunk.header}</div>}
                              {hunk.lines.map((line, li) => (
                                <div key={li} className={`${s.diffLine} ${line.kind === 'add' ? s.lineAdd : line.kind === 'del' ? s.lineDel : ''}`}>
                                  <span className={s.lineNo}>{line.old_no || ''}</span>
                                  <span className={s.lineNo}>{line.new_no || ''}</span>
                                  <code>{line.text || ' '}</code>
                                </div>
                              ))}
                            </div>
                          ))}
                        </div>
                      )
                    )}
                  </article>
                ))}
              </div>
            )}
          </section>
        </div>
      )}

      {compare && (
        <CommitFileCompare
          repoId={compare.repoId}
          sha={compare.sha}
          file={compare.file}
          onClose={() => setCompare(null)}
        />
      )}
    </div>
  )
}
