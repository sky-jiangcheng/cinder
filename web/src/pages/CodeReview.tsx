import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { latestReviewReport, runReviewNow } from '../api/client'
import type { ReviewFinding, ReviewSeverity, ReviewSnapshot } from '../api/client'
import ErrorBanner from '../components/ErrorBanner'
import PageShell from '../components/PageShell'
import { useToast } from '../hooks/useToast'
import s from './CodeReview.module.css'

// The code review page (ADR-0053 D7).
//
// It is a READER, not a workflow: the report is produced by a daily job (or
// the button), stored as data, and this page renders it grouped by file
// because that is how a person checks a finding — open the file, look at the
// line, decide. Nothing here writes to the knowledge base: a machine report
// mixed into notes would destroy the meaning of "my notes" (D6).

/** Severity filters, worst first. `all` is the default because the count of
 * high findings is already visible in the header — hiding the rest by default
 * would answer a question the user did not ask. */
const SEVERITIES: (ReviewSeverity | 'all')[] = ['all', 'high', 'medium', 'low']

const severityRank: Record<string, number> = { high: 0, medium: 1, low: 2 }

interface FileGroup {
  key: string
  repo: string
  file: string
  findings: ReviewFinding[]
}

function groupByFile(findings: ReviewFinding[]): FileGroup[] {
  const map = new Map<string, FileGroup>()
  for (const f of findings) {
    const key = f.repo_path + '\u0000' + f.file_path
    let g = map.get(key)
    if (!g) {
      g = { key, repo: f.repo_path, file: f.file_path, findings: [] }
      map.set(key, g)
    }
    g.findings.push(f)
  }
  const groups = [...map.values()]
  for (const g of groups) {
    g.findings.sort((a, b) => a.line - b.line)
  }
  // Worst file first: a group inherits its most severe finding's rank.
  groups.sort((a, b) => {
    const ra = Math.min(...a.findings.map(f => severityRank[f.severity] ?? 9))
    const rb = Math.min(...b.findings.map(f => severityRank[f.severity] ?? 9))
    return ra - rb || a.file.localeCompare(b.file)
  })
  return groups
}

function CodeReview() {
  const { t } = useTranslation()
  const pushToast = useToast()
  const [snapshot, setSnapshot] = useState<ReviewSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [running, setRunning] = useState(false)
  const [severity, setSeverity] = useState<ReviewSeverity | 'all'>('all')

  // State lands only inside the promise callbacks: a synchronous setState in
  // the effect body would cascade a second render before the first paint.
  const fetchReport = useCallback(() => {
    return latestReviewReport()
      .then(r => {
        setSnapshot(r)
        setError('')
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : t('common.failed')))
      .finally(() => setLoading(false))
  }, [t])

  useEffect(() => {
    void fetchReport()
  }, [fetchReport])

  // Refresh is an event, so it may flip the skeleton on up front.
  const reload = () => {
    setLoading(true)
    void fetchReport()
  }

  const runNow = async () => {
    setRunning(true)
    try {
      const result = await runReviewNow()
      if (result.already_done) {
        pushToast({ kind: 'info', title: t('codeReview.alreadyDone') })
      } else {
        pushToast({
          kind: 'success',
          title: t('codeReview.runFinished'),
          message: t('codeReview.runFinishedDetail', { count: result.run.candidates }),
        })
      }
      void fetchReport()
    } catch (e: unknown) {
      // A failure here is ordinary (no endpoint, no commits, unreadable repo),
      // and the toast is where the reason belongs — a page-level banner would
      // hide the report that is already on screen.
      pushToast({ kind: 'error', title: t('codeReview.runFailed'), message: e instanceof Error ? e.message : t('common.failed') })
    } finally {
      setRunning(false)
    }
  }

  const run = snapshot?.run
  const visible = useMemo(() => {
    const all = snapshot?.findings ?? []
    return severity === 'all' ? all : all.filter(f => f.severity === severity)
  }, [snapshot, severity])
  const groups = useMemo(() => groupByFile(visible), [visible])
  const counts = useMemo(() => {
    const c: Record<string, number> = { high: 0, medium: 0, low: 0 }
    for (const f of snapshot?.findings ?? []) c[f.severity] = (c[f.severity] ?? 0) + 1
    return c
  }, [snapshot])

  if (loading && !snapshot) {
    return (
      <div className={s.page}>
        <div className="skeleton skeleton-text" style={{ width: 200, height: 26, marginBottom: 12 }} />
        <div className="skeleton skeleton-text" style={{ width: '100%', height: 64 }} />
      </div>
    )
  }

  if (error) {
    return (
      <div className={s.page}>
        <ErrorBanner message={error} onRetry={reload} />
      </div>
    )
  }

  return (
    <PageShell
      header={
        <>
          <h1 className="visually-hidden">{t('nav.codeReview')}</h1>
          <div className={s.toolbar}>
            <button className="btn btn-primary" onClick={runNow} disabled={running}>
              {running ? t('codeReview.running') : t('codeReview.runNow')}
            </button>
            <button className="btn" onClick={reload} disabled={loading}>
              {t('common.refresh')}
            </button>
            <div className={s.filters} role="group" aria-label={t('codeReview.filterLabel')}>
              {SEVERITIES.map(sev => (
                <button
                  key={sev}
                  className={`tab-btn ${severity === sev ? 'tab-active' : ''}`}
                  aria-pressed={severity === sev}
                  onClick={() => setSeverity(sev)}
                >
                  {sev === 'all'
                    ? t('codeReview.severityAll', { count: snapshot?.findings.length ?? 0 })
                    : t(`codeReview.severity.${sev}`, { count: counts[sev] ?? 0 })}
                </button>
              ))}
            </div>
            {run && (
              <span className={s.meta}>
                {t('codeReview.meta', {
                  date: run.run_date,
                  repos: run.repos,
                  commits: run.commits,
                  files: run.files,
                  ai: run.ai_calls,
                  skipped: run.skipped,
                })}
              </span>
            )}
          </div>
        </>
      }
    >
      {!run && (
        <div className={s.empty}>
          <p>{t('codeReview.neverRan')}</p>
          <p className={s.emptyHint}>{t('codeReview.neverRanHint')}</p>
        </div>
      )}

      {run && run.findings === 0 && (
        <div className={s.empty}>
          <p>{t('codeReview.nothingFound', { date: run.run_date })}</p>
          <p className={s.emptyHint}>{t('codeReview.nothingFoundHint')}</p>
        </div>
      )}

      {groups.map(g => (
        <section key={g.key} className={s.fileGroup}>
          <h2 className={s.fileHead} title={g.repo}>
            <span className={s.repo}>{g.repo.split('/').pop()}</span>
            <span className={s.file}>{g.file}</span>
            <span className={s.badge}>{t('codeReview.findingCount', { count: g.findings.length })}</span>
          </h2>
          {g.findings.map(f => (
            <article key={f.id} className={`${s.finding} ${s[f.severity] ?? ''}`}>
              <div className={s.findingHead}>
                <span className={`${s.sev} ${s[f.severity] ?? ''}`}>{t(`codeReview.severity.${f.severity}`)}</span>
                <code className={s.rule}>{f.rule}</code>
                {f.line > 0 && <span className={s.line}>:{f.line}</span>}
                <span className={`${s.verdict} ${s[f.verdict] ?? ''}`}>{t(`codeReview.verdict.${f.verdict}`)}</span>
              </div>
              <p className={s.message}>{f.message}</p>
              {f.suggestion && <p className={s.suggestion}>→ {f.suggestion}</p>}
              {f.evidence && (
                <details className={s.evidence}>
                  <summary>{t('codeReview.showEvidence')}</summary>
                  <pre>{f.evidence}</pre>
                </details>
              )}
            </article>
          ))}
        </section>
      ))}
    </PageShell>
  )
}

export default CodeReview
