import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigationType, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  approveWikiPage, assignWikiPageProject, cancelCompileJob, deleteCompiledPage, diffWikiPageVersion,
  findMergeCandidates, getProjects, listApprovedWikiPages, listCompileJobs, listPendingWikiPages,
  listRejectedWikiPages, listWikiPageVersions, mergeWikiPages, promoteWikiPageToGlobal,
  rejectWikiPage, runCommunitySummary, startCompileJob, startLintJob,
} from '../api/client'
import type {
  CompileJob, MergeCandidate, MergeCandidateReport, Project, WikiCommunityReport,
  WikiPageVersion, WikiPendingPage,
} from '../api/client'
import DiffViewer from '../components/DiffViewer'
import ErrorBanner from '../components/ErrorBanner'
import Icon from '../components/Icon'
import PageShell from '../components/PageShell'
import TabStrip from '../components/TabStrip'
import type { TabItem } from '../components/TabStrip'
import { useConfirmClick } from '../hooks/useConfirmClick'
import { useToast } from '../hooks/useToast'
import { renderMarkdown } from '../utils/markdown'
import s from './Review.module.css'

// Poll cadence for a running compile job. Deliberately slow: a note takes minutes,
// so a fast poll would hammer the backend for no user-visible gain. This is the
// only scheduling the page does — the work itself is a backend job.
const jobPollMs = 5000
const maxNotesPerRun = 10
// How many merge pairs the review surface asks for. The backend's own default
// (mergeDefaultTopK) is the same number; passing it explicitly keeps the note
// ("仅显示前 N 对") true no matter what the binding's default becomes.
const mergeTopK = 20
// The scope surface lists every approved page, and a long-lived library has
// hundreds of them. The rows are one line each, but the list still has to end
// somewhere — and it says so rather than pretending the count is the whole
// truth.
const scopeMaxRows = 100

// Job rows render {done} / {total} with total = notes_total || requested_notes.
// A queued job legitimately has both at 0 only when the worker has not claimed
// it yet AND nothing was requested — which cannot happen (StartCompileJob
// always requests ≥1 note). The other 0/0 shape is real data: the worker was
// killed before it could snapshot the note list. Rendering "0 / 0" there reads
// like a bug report, so those rows show only the status badge until real
// numbers exist.
function jobProgressText(j: CompileJob, t: (k: string, v?: Record<string, unknown>) => string): string {
  const total = j.notes_total || j.requested_notes
  if (total <= 0) return ''
  return j.kind === 'lint'
    ? t('review.lintProgress', { done: j.notes_done, total })
    : t('review.progress', { done: j.notes_done, total })
}

function statusKey(status: string): string {
  // status_* keys exist for every job state; unknown states render verbatim so a
  // new backend status is visible rather than silently labelled "queued".
  return `review.status_${status}`
}

// kindLabel names which queue a row came from. A missing kind means the backend
// predates the column, and such a row is a compile job — so the fallback is the
// compile label rather than a blank badge.
function kindLabel(job: CompileJob, t: (k: string) => string): string {
  const kind = job.kind || 'compile'
  return kind === 'lint' ? t('review.kindLint') : t('review.kindCompile')
}

// jobErrorText localizes the backend's fixed failure reasons, which it stores as
// stable codes (db.CompileJobErr*) precisely so this page can translate them.
// Anything else is a wrapped Go error: diagnostic text, deliberately left as-is
// — rewriting it would hide what actually failed. Exported for the unit test
// because the component path is unobservable under the suite's key-returning
// `t` mock (it discards the interpolated {{msg}}).
const JOB_ERROR_KEYS: Record<string, string> = {
  interrupted_by_restart: 'review.errInterrupted',
  worker_shutdown: 'review.errShutdown',
  canceled_before_start: 'review.errCanceledEarly',
}

export function jobErrorText(msg: string, t: (k: string) => string): string {
  const key = JOB_ERROR_KEYS[msg]
  return key ? t(key) : msg
}

// A dismissed pair is keyed by project too: "ignore" means "not in this view",
// and switching project is a different view. Nothing is persisted — the moment
// the page unmounts the pair is suggested again, which is the point (a local
// dismissal must never become a silent global "these are not duplicates").
const mergePairKey = (projectId: number, c: MergeCandidate) =>
  `${projectId}:${c.loser_id}>${c.winner_id}`

// ADR-0039 decision 2: the six stacked sections become five tabs.
//
// These are NOT filters over one list: they are five different shapes of work
// (an approval queue / a candidate-pair list / a scope table / a job list / a
// record of replaced pages), and each one carries a count badge — a tab that
// hides its count forces the reviewer to click through every tab to learn
// which one has anything in it.
//
// The order is fixed so the badges stay comparable between visits. Three of
// the tabs only exist when they have something to show: "no scope to move" is
// not a section a reviewer needs to scroll past. Pending and merge are always
// there — an empty queue is an answer, and "the structural pass found
// nothing" is the answer ADR-0019's scan exists to give.
type ReviewView = 'pending' | 'merge' | 'scope' | 'jobs' | 'rejected'

const REVIEW_VIEWS: ReviewView[] = ['pending', 'merge', 'scope', 'jobs', 'rejected']

/** The deep-link surface: ?view= is shareable; an unknown value falls back to pending. */
function readView(raw: string | null): ReviewView {
  return raw !== null && (REVIEW_VIEWS as string[]).includes(raw) ? (raw as ReviewView) : 'pending'
}

function ReviewPage() {
  const { t } = useTranslation()
  const toast = useToast()

  // ---- Which tab is open (ADR-0039 decision 6: the tabs are addressable) ----
  const [searchParams, setSearchParams] = useSearchParams()
  const urlView = readView(searchParams.get('view'))
  const [view, setView] = useState<ReviewView>(urlView)
  // What WE put into the URL. react-router may render an intermediate
  // location while our own write is in flight, and adopting that as an
  // external change would move the reviewer out of the panel they just picked.
  const written = useRef<ReviewView>(urlView)
  // Only a history navigation (back / forward) may overwrite the local state.
  const navType = useNavigationType()
  const scrollRef = useRef<HTMLDivElement>(null)

  // Switching tabs returns the reader to the top of the new panel: the rows
  // of the previous tab are gone, and staying at the old scroll offset makes
  // the switch look like it did nothing. The scroller is the page's own
  // scrolling region (PageShell), not the window — the tab strip is chrome
  // pinned above that region and never scrolls away, so the offset that
  // matters is the region's own.
  const resetScroll = useCallback(() => {
    scrollRef.current?.scrollTo?.({ top: 0 })
  }, [])

  const switchView = useCallback((next: string) => {
    const key = next as ReviewView
    if (key === view) return
    setView(key)
    written.current = key
    // PUSH, not REPLACE: the back button has to be able to walk back through
    // the tab switches — with REPLACE it would step over them and land on the
    // page before the tab, which reads as "the tab ate my history" (the same
    // call as the knowledge page's view switch).
    // The default view stays out of the URL (same convention as the knowledge
    // page's ?view=): "/review" says "the queue", "?view=merge" says which.
    setSearchParams(key === 'pending' ? {} : { view: key }, { replace: false })
    resetScroll()
  }, [view, setSearchParams, resetScroll])

  useEffect(() => {
    if (navType !== 'POP') return
    if (urlView === written.current) return
    written.current = urlView
    setView(urlView)
    resetScroll()
  }, [navType, urlView, resetScroll])

  const [projects, setProjects] = useState<Project[]>([])
  const [projectFilter, setProjectFilter] = useState<number>(0) // 0 = all
  const [pending, setPending] = useState<WikiPendingPage[]>([])
  const [rejected, setRejected] = useState<WikiPendingPage[]>([])
  const [approved, setApproved] = useState<WikiPendingPage[]>([])
  const [jobs, setJobs] = useState<CompileJob[]>([])
  const [merge, setMerge] = useState<MergeCandidateReport | null>(null)
  const [dismissed, setDismissed] = useState<string[]>([])
  const [community, setCommunity] = useState<WikiCommunityReport | null>(null)
  const [communityBusy, setCommunityBusy] = useState(false)
  const [queueErrors, setQueueErrors] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [reloadToken, setReloadToken] = useState(0)
  const reload = useCallback(() => setReloadToken(n => n + 1), [])

  useEffect(() => {
    // Each queue is independent: one failure must not blank the others.
    let cancelled = false
    setLoading(true) // eslint-disable-line react-hooks/set-state-in-effect
    Promise.allSettled([
      getProjects(),
      listPendingWikiPages(projectFilter),
      listRejectedWikiPages(projectFilter),
      listApprovedWikiPages(projectFilter),
      listCompileJobs(projectFilter, '', 10),
      findMergeCandidates(projectFilter, mergeTopK),
    ]).then(([projs, pend, rej, appr, jb, cand]) => {
      if (cancelled) return
      const errors: Record<string, string> = {}
      const detail = (r: PromiseRejectedResult) => r.reason instanceof Error ? r.reason.message : String(r.reason)
      if (projs.status === 'fulfilled') setProjects(projs.value); else errors.projects = detail(projs)
      if (pend.status === 'fulfilled') setPending(pend.value); else errors.pending = detail(pend)
      if (rej.status === 'fulfilled') setRejected(rej.value); else errors.rejected = detail(rej)
      if (appr.status === 'fulfilled') setApproved(appr.value); else errors.approved = detail(appr)
      if (jb.status === 'fulfilled') setJobs(jb.value); else errors.jobs = detail(jb)
      if (cand.status === 'fulfilled') setMerge(cand.value); else errors.merge = detail(cand)
      setQueueErrors(errors)
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [projectFilter, reloadToken])

  // A running job only changes the job list, so the poll refreshes just that
  // queue instead of re-issuing all six requests.
  const fetchJobs = useCallback(() => {
    listCompileJobs(projectFilter, '', 10)
      .then(jb => setJobs(jb))
      .catch(() => { /* keep the last job list on a failed poll */ })
  }, [projectFilter])
  const anyActive = jobs.some(j => j.status === 'queued' || j.status === 'running')
  useEffect(() => {
    if (!anyActive) return
    const id = setInterval(fetchJobs, jobPollMs)
    return () => clearInterval(id)
  }, [anyActive, fetchJobs])

  const runGuarded = useCallback(async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
      reload()
    } catch (e) {
      toast({ kind: 'error', title: t('review.actionError'), message: e instanceof Error ? e.message : undefined })
    } finally {
      setBusy(false)
    }
  }, [reload, toast, t])

  const onApprove = (id: number) => void runGuarded(() => approveWikiPage(id))
  const onReject = (id: number) => void runGuarded(() => rejectWikiPage(id))
  const onDelete = (id: number) => {
    if (!window.confirm(t('review.confirmDelete'))) return
    void runGuarded(() => deleteCompiledPage(id))
  }
  const onStart = () => void runGuarded(() => startCompileJob(projectFilter, maxNotesPerRun))
  // A queued lint job is per-project (db.CreateLintJob), so the button is
  // disabled on "all projects" rather than failing after the click.
  const onStartLint = () => void runGuarded(() => startLintJob(projectFilter))
  const onCancel = (id: number) => void runGuarded(() => cancelCompileJob(id))

  // ADR-0019 决策 1: this click is the only path to the merge transaction in the
  // whole product — no job, no ticker reaches it. The row arms the button first
  // (useConfirmClick), so nothing here fires without a deliberate second click.
  const onMerge = (c: MergeCandidate) => void runGuarded(async () => {
    const r = await mergeWikiPages(c.loser_id, c.winner_id)
    toast({
      kind: 'success',
      title: t('review.mergeDone', { loser: c.loser_slug, winner: c.winner_slug }),
      message: r
        ? t('review.mergeResult', {
          edges: r.edges_redirected,
          dropped: r.edges_dropped,
          notes: r.notes_reattached,
        })
        : t('review.mergeResultUnknown'),
    })
  })

  const onIgnoreMerge = (c: MergeCandidate) =>
    setDismissed(d => (d.includes(mergePairKey(projectFilter, c)) ? d : [...d, mergePairKey(projectFilter, c)]))

  // ADR-0031: the scope moves. Promotion WIDENS a page's reach — it enters the
  // cross-project profile (L3) and every project's evidence gathering — and
  // the reverse move puts it back. Both are human acts: no compiler, no job
  // and no agent reaches them, because a page that speaks for the user across
  // projects is a curated claim, not an inferred one.
  const onPromote = (id: number) => void runGuarded(async () => {
    await promoteWikiPageToGlobal(id)
    toast({ kind: 'success', title: t('review.scopePromoted') })
  })
  const onAssign = (id: number, projectId: number) => void runGuarded(async () => {
    await assignWikiPageProject(id, projectId)
    toast({ kind: 'success', title: t('review.scopeAssigned') })
  })

  // ADR-0020: slow and paid for, so it is its own in-flight flag rather than
  // riding on `busy` — the page's other buttons should not grey out for it, and
  // its own label has to change. runGuarded still owns the reload and the error
  // toast once the call settles.
  const onRunCommunity = () => {
    setCommunityBusy(true)
    void runGuarded(async () => {
      const r = await runCommunitySummary(projectFilter)
      setCommunity(r)
      if (!r) {
        toast({ kind: 'error', title: t('review.communityNoReport') })
        return
      }
      // The report lives in the jobs tab ("what the machine produced"). The run
      // was paid for and it just finished — the reader has to see the counters
      // without having to know which tab they are in and go looking.
      switchView('jobs')
      // Gate off = detection only. Saying "0 pages created" would read as "there
      // is nothing to synthesise", when the real answer is "you never turned this
      // on" — so the backend's own note goes up verbatim instead.
      if (r.community_gate_off) {
        toast({ kind: 'info', title: t('review.communityGateOff'), message: r.llm_note || undefined })
        return
      }
      toast({ kind: 'success', title: t('review.communityDone') })
    }).finally(() => setCommunityBusy(false))
  }

  const mergeCandidates = useMemo(() => {
    const list = merge?.candidates ?? []
    if (dismissed.length === 0) return list
    const hidden = new Set(dismissed)
    return list.filter(c => !hidden.has(mergePairKey(projectFilter, c)))
  }, [merge, dismissed, projectFilter])

  const projectLabel = useMemo(() => {
    const m = new Map<number, string>()
    for (const p of projects) m.set(p.id, p.name)
    return (pid: number) => (pid === 0 ? t('review.globalProject') : (m.get(pid) ?? t('review.globalProject')))
  }, [projects, t])

  // The strip's contents. The badge is the number that says whether a tab is
  // worth clicking, so every tab that exists carries one — including the
  // empty pending queue ("0" is information, a missing badge is not).
  //
  // A queue that has something takes a slot. The SELECTED one keeps its slot
  // even when it empties under the reader (the last replaced page is deleted
  // while the rejected tab is open): a tab disappearing mid-review would move
  // the panel without being asked, and — worse — the panel would jump back
  // the moment another replaced page showed up. An empty tab that you are
  // standing on says "0"; a tab that vanishes says nothing.
  const tabs = useMemo<TabItem[]>(() => {
    const items: TabItem[] = [
      { key: 'pending', label: t('review.tabPending'), count: pending.length },
      { key: 'merge', label: t('review.tabMerge'), count: mergeCandidates.length },
    ]
    if (approved.length > 0 || view === 'scope') {
      items.push({ key: 'scope', label: t('review.tabScope'), count: approved.length })
    }
    // The community synthesis report is "what the machine produced", not a
    // queue — so it shares the jobs tab rather than holding a tab of its own.
    if (jobs.length > 0 || community !== null || view === 'jobs') {
      items.push({ key: 'jobs', label: t('review.tabJobs'), count: jobs.length })
    }
    if (rejected.length > 0 || view === 'rejected') {
      items.push({ key: 'rejected', label: t('review.tabRejected'), count: rejected.length })
    }
    return items
  }, [t, view, pending.length, mergeCandidates.length, approved.length, jobs.length, rejected.length, community])

  // The panel follows the selection. The fallback is a safety net for the
  // frame before the data lands: a deep link to a queue that has not loaded
  // yet must not render a panel whose tab is not on the strip.
  const activeView: ReviewView = tabs.some(tab => tab.key === view) ? view : 'pending'

  return (
    <PageShell
      bodyRef={scrollRef}
      header={
        <>
          {/* The nav bar already names this tab ("审核"), so a visible title would
              be duplication — it stays for screen readers and the document outline
              (same call as the knowledge page). */}
          <h1 className="visually-hidden">{t('review.title')}</h1>

          <div className={s.toolbar}>
            <select
              className={s.select}
              value={projectFilter}
              onChange={e => setProjectFilter(Number(e.target.value))}
              aria-label={t('review.projectFilter')}
            >
              <option value={0}>{t('review.allProjects')}</option>
              {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <div className={s.toolbarActions}>
              <button className="btn btn-sm" onClick={reload}>{t('review.reload')}</button>
              <button
                className="btn btn-sm btn-primary"
                onClick={onStart}
                disabled={busy || projectFilter === 0}
                title={projectFilter === 0 ? t('review.projectRequired') : undefined}
              >
                {t('review.startCompile')}
              </button>
              {/* Not a primary button: two primaries on one toolbar compete, and
                  this one is off by default anyway (wiki_lint_llm). */}
              <button
                className="btn btn-sm"
                onClick={onStartLint}
                disabled={busy || projectFilter === 0}
                title={projectFilter === 0 ? t('review.lintNeedsProject') : t('review.startLintHint')}
              >
                {t('review.startLint')}
              </button>
              {/* Community synthesis (ADR-0020). Same project rule as lint, same
                  reason: project_id 0 is the GLOBAL tier on the backend, not
                  "every project" — running it there would synthesise a topic out of
                  cross-project pages and say nothing about the project the reviewer
                  picked, so the button is disabled with that stated, not silently
                  aimed at tier 0. */}
              <button
                className="btn btn-sm"
                onClick={onRunCommunity}
                disabled={busy || communityBusy || projectFilter === 0}
                title={projectFilter === 0 ? t('review.communityNeedsProject') : t('review.communityHint')}
              >
                {communityBusy ? t('review.communityRunning') : t('review.startCommunity')}
              </button>
            </div>
          </div>

          {/* One switcher for the five queues (ADR-0039 decision 2). The tab
              label IS the heading, so no panel repeats it. The strip is chrome,
              not content: it lives in the pinned header, so the scrolling
              region starts UNDER it — the strip never scrolls away and the
              scrollbar's top edge is the strip's bottom edge. */}
          <div className={s.tabRow}>
            <div className={s.tabStrip}>
              <TabStrip
                items={tabs}
                active={activeView}
                onChange={switchView}
                ariaLabel={t('review.tablistLabel')}
                idPrefix="review"
              />
            </div>
          </div>
        </>
      }
    >
      <div className={s.content}>
        <p className={s.lead}>{t('review.subtitle')}</p>

        {loading && <p className="empty-hint">{t('common.loading')}</p>}
        {!loading && Object.entries(queueErrors).map(([queue, msg]) => (
          <ErrorBanner key={queue} message={`${t('review.loadError')}: ${msg}`} onRetry={reload} />
        ))}

        {!loading && (<>
        {activeView === 'pending' && (
          <div
            role="tabpanel"
            id="review-panel-pending"
            aria-labelledby="review-tab-pending"
            className={s.panel}
          >
            {pending.length === 0
              ? <div className="empty-state small">
                  <div className="empty-icon"><Icon name="file-text" size={32} /></div>
                  <h3>{t('review.emptyPendingTitle')}</h3>
                  <p>{t('review.emptyPendingMsg')}</p>
                </div>
              : <ul className={s.cardList}>
                  {pending.map(p => (
                    <ReviewCard key={p.page.id} item={p} projectLabel={projectLabel}
                      busy={busy} onApprove={onApprove} onReject={onReject} />
                  ))}
                </ul>}
          </div>
        )}

        {/* Merge suggestions (ADR-0019): always mounted, because "the structural
            pass found nothing" is itself the answer a reviewer wants — unlike the
            job and error tabs, which are absence of a thing nobody asked for. */}
        {activeView === 'merge' && (
          <div
            role="tabpanel"
            id="review-panel-merge"
            aria-labelledby="review-tab-merge"
            className={s.panel}
          >
            <p className={s.lead}>{t('review.mergeHint')}</p>
            {mergeCandidates.length === 0
              ? <div className="empty-state small">
                  <div className="empty-icon"><Icon name="check" size={32} /></div>
                  <h3>{t('review.emptyMergeTitle')}</h3>
                  <p>{t('review.emptyMergeMsg')}</p>
                </div>
              : <ul className={s.mergeList}>
                  {mergeCandidates.map(c => (
                    <MergeRow key={`${c.loser_id}-${c.winner_id}`} item={c} busy={busy}
                      onMerge={onMerge} onIgnore={onIgnoreMerge} />
                  ))}
                </ul>}
            {merge?.truncated && (
              <p className={s.note}>{t('review.mergeTruncated', { n: merge.candidates?.length ?? 0 })}</p>
            )}
          </div>
        )}

        {/* Page scope (ADR-0031). The tab only exists when there is something to
            scope: a surface about a move nobody can make is noise. */}
        {activeView === 'scope' && (
          <div
            role="tabpanel"
            id="review-panel-scope"
            aria-labelledby="review-tab-scope"
            className={s.panel}
          >
            <p className={s.lead}>{t('review.scopeHint')}</p>
            {approved.length === 0 ? (
              <p className="empty-hint">{t('review.emptyTab')}</p>
            ) : (
              <ul className={s.scopeList}>
                {approved.slice(0, scopeMaxRows).map(p => (
                  <ScopeRow key={p.page.id} item={p} projects={projects} projectLabel={projectLabel}
                    busy={busy} onPromote={onPromote} onAssign={onAssign} />
                ))}
              </ul>
            )}
            {approved.length > scopeMaxRows && (
              <p className={s.note}>{t('review.scopeTruncated', { n: approved.length })}</p>
            )}
          </div>
        )}

        {activeView === 'jobs' && (
          <div
            role="tabpanel"
            id="review-panel-jobs"
            aria-labelledby="review-tab-jobs"
            className={s.panel}
          >
            {jobs.length > 0 ? (
              <ul className={s.jobList}>
                {jobs.map(j => (
                  <JobRow key={j.id} job={j} projectLabel={projectLabel} busy={busy} onCancel={onCancel} />
                ))}
              </ul>
            ) : !community ? (
              <p className="empty-hint">{t('review.emptyTab')}</p>
            ) : null}
            {/* The last synthesis run stays on screen once it exists: its
                counters are the evidence that a paid-for call did something,
                and a toast expires long before a reviewer has read six
                numbers. */}
            {community && <CommunityReport report={community} />}
          </div>
        )}

        {activeView === 'rejected' && (
          <div
            role="tabpanel"
            id="review-panel-rejected"
            aria-labelledby="review-tab-rejected"
            className={s.panel}
          >
            {/* The tab label had to shrink to "error pages" to fit the strip;
                what it lost — that these pages were REPLACED, not deleted — is
                what this line carries. */}
            <p className={s.lead}>{t('review.rejectedHint')}</p>
            {rejected.length === 0 ? (
              <p className="empty-hint">{t('review.emptyTab')}</p>
            ) : (
              <ul className={s.cardList}>
                {rejected.map(p => (
                  <ReviewCard key={p.page.id} item={p} projectLabel={projectLabel}
                    busy={busy} rejected onDelete={onDelete} />
                ))}
              </ul>
            )}
          </div>
        )}
        </>)}
      </div>
    </PageShell>
  )
}

function JobRow({
  job, projectLabel, busy, onCancel,
}: {
  job: CompileJob
  projectLabel: (pid: number) => string
  busy: boolean
  onCancel: (id: number) => void
}) {
  const { t } = useTranslation()
  const active = job.status === 'queued' || job.status === 'running'
  return (
    <li className={s.job}>
      <div className={s.jobTop}>
        <span className={`${s.status} ${s['status_' + job.status] ?? ''}`}>
          {job.status === 'running' && <span className={s.dot} />}
          {t(statusKey(job.status), job.status)}
        </span>
        <span className={s.jobKind}>{kindLabel(job, t)}</span>
        <span className={s.jobProject}>{projectLabel(job.project_id)}</span>
        <span className={s.jobProgress}>{jobProgressText(job, t)}</span>
        {active && (
          <button className={`btn btn-sm ${s.cancel}`} onClick={() => onCancel(job.id)} disabled={busy}>
            {t('review.cancelJob')}
          </button>
        )}
      </div>
      {active && job.notes_total > 0 && (
        <div className={s.progressTrack}>
          <div className={s.progressFill}
            style={{ width: `${Math.min(100, (job.notes_done / job.notes_total) * 100)}%` }} />
        </div>
      )}
      {/* One queue, two kernels (ADR-0016 待决 ①), so the counters are
          labelled per kind: "pages created" is always 0 on a lint row and
          "notes" is not what a lint row counts. Chips are only emitted for
          non-zero stats — a wall of "新建 0 页" chips is noise, and the
          done>0 disjunct keeps an in-flight row's first chip visible from
          note one. */}
      <div className={s.jobStats}>
        {job.kind === 'lint'
          ? (job.findings > 0 || job.notes_done > 0) && (
              <span className={s.stat}>{t('review.findingsCount', { n: job.findings })}</span>
            )
          : <>
              {(job.pages_created > 0 || job.notes_done > 0) && (
                <span className={s.stat}>{t('review.pagesCreated', { n: job.pages_created })}</span>
              )}
              {job.pages_updated > 0 && <span className={s.stat}>{t('review.pagesUpdated', { n: job.pages_updated })}</span>}
              {job.links_created > 0 && <span className={s.stat}>{t('review.linksCreated', { n: job.links_created })}</span>}
              {job.rejected_ops > 0 && <span className={s.stat}>{t('review.rejectedOps', { n: job.rejected_ops })}</span>}
              {job.revision_todos > 0 && <span className={s.stat}>{t('review.revisionTodos', { n: job.revision_todos })}</span>}
            </>}
      </div>
      {job.stopped && <p className={s.jobNote}>{t('review.stopped', { reason: job.stopped })}</p>}
      {job.error && <p className={s.jobError}>{t('review.error', { msg: jobErrorText(job.error, t) })}</p>}
    </li>
  )
}

// One suggested pair. The direction is the whole content of the row: [[loser]] →
// [[winner]] is what the merge would do, and it is provisional (ADR-0019 待决 1 —
// structural evidence picked it, not a judgement). The score and the signal names
// are shown rather than a bar because the point of a structural pass is that a
// human can read WHY the pair was suggested; an opaque 0.87 could not be argued
// with. The signals are the backend's stable vocabulary (shared-notes,
// slug-shape, title-overlap, linked) and appear verbatim, like job statuses do.
function MergeRow({
  item, busy, onMerge, onIgnore,
}: {
  item: MergeCandidate
  busy: boolean
  onMerge: (c: MergeCandidate) => void
  onIgnore: (c: MergeCandidate) => void
}) {
  const { t } = useTranslation()
  // Two-click arm: the first click only swaps the label to 确认？, the second one
  // merges. The warning lives on the row itself because that is where the two
  // slugs being destroyed are visible — a modal (or window.confirm, which the
  // delete path above still uses) would take the reviewer away from the very
  // evidence they need in order to decide.
  const { armed, click } = useConfirmClick(() => onMerge(item))
  return (
    <li className={s.mergeRow}>
      <div className={s.mergeTop}>
        <span className={s.mergeDirection}>
          <span className={s.mergeLoser}>{item.loser_slug}</span>
          <span className={s.mergeArrow} aria-hidden="true">→</span>
          <span className={s.mergeWinner}>{item.winner_slug}</span>
          <span className="visually-hidden">
            {t('review.mergeDirectionAria', { loser: item.loser_slug, winner: item.winner_slug })}
          </span>
        </span>
        <span className={s.stat}>{t('review.mergeScore', { n: item.score })}</span>
        {(item.signals ?? []).map(sig => <span key={sig} className={s.signal}>{sig}</span>)}
        {item.shared_notes > 0 && (
          <span className={s.signal}>{t('review.mergeSharedNotes', { n: item.shared_notes })}</span>
        )}
      </div>
      <div className={s.mergeActions}>
        {/* Ignore is local and ephemeral by design: hiding a pair the reviewer has
            already looked at is a view convenience, not a stored decision, and a
            stored one would quietly teach the detector that these are different. */}
        <button className="btn btn-sm" disabled={busy} onClick={() => onIgnore(item)}>
          {t('review.mergeIgnore')}
        </button>
        <button
          className={`btn btn-sm ${armed ? 'btn-danger' : 'btn-primary'} ${s.mergeAdopt}`}
          onClick={click}
          disabled={busy}
          title={t('review.mergeConfirmHint')}
        >
          {armed ? t('review.mergeConfirm') : t('review.mergeAdopt')}
        </button>
      </div>
    </li>
  )
}

// The synthesis report. Every counter is shown even when zero: "0 pages created"
// is a fact about this run, whereas hiding zeros would make a run that found
// nothing indistinguishable from a button that was never pressed.
function CommunityReport({ report }: { report: WikiCommunityReport }) {
  const { t } = useTranslation()
  const gates = [
    { key: 'review.communityPagesCreated', n: report.pages_created },
    { key: 'review.communityPagesUpdated', n: report.pages_updated },
    { key: 'review.communityDocsLinked', n: report.documents_linked },
    { key: 'review.communityRevisionTodos', n: report.revision_todos },
    { key: 'review.communityEmptyBodies', n: report.empty_bodies },
  ]
  return (
    <section aria-label={t('review.communityHeading')}>
      <div className={s.sectionHead}>
        <h2>{t('review.communityHeading')}</h2>
      </div>
      {report.community_gate_off ? (
        // Detection only: the model half never ran, so the generation counters
        // would be zeros nobody earned. The backend's own sentence says why.
        <p className={s.note}>{t('review.communityGateOffNote')}</p>
      ) : (
        <div className={s.jobStats}>
          {gates.map(g => <span key={g.key} className={s.stat}>{t(g.key, { n: g.n })}</span>)}
          <span className={s.stat}>{t('review.communityOutbound', { n: report.outbound_bytes })}</span>
        </div>
      )}
      {/* llm_note is backend copy (why nothing was found, why nothing was sent),
          so it is rendered verbatim rather than paraphrased into a counter. */}
      {report.llm_note && <p className={s.jobNote}>{report.llm_note}</p>}
    </section>
  )
}

// One approved page and its scope (ADR-0031). The row is deliberately NOT a
// review card: a scope move is not a verdict, so it gets no body, no source
// notes and no version history — only the page's identity, where it currently
// speaks, and the one move available from there.
function ScopeRow({
  item, projects, projectLabel, busy, onPromote, onAssign,
}: {
  item: WikiPendingPage
  projects: Project[]
  projectLabel: (pid: number) => string
  busy: boolean
  onPromote: (id: number) => void
  onAssign: (id: number, projectId: number) => void
}) {
  const { t } = useTranslation()
  const p = item.page
  const isGlobal = p.project_id === 0
  // The target project is picked per row: once a page is global, "put it back
  // where it was" is not knowable from the row, so the human names it.
  const [target, setTarget] = useState(0)
  // Two-click arm, same as the merge row: the promotion changes what steers
  // every future answer in every project, so it must not be one stray click.
  const { armed, click } = useConfirmClick(() => onPromote(p.id))

  return (
    <li className={s.scopeRow}>
      <div className={s.scopeTop}>
        <span className={`${s.kindBadge} ${s['kindBadge_' + p.kind] ?? ''}`}>
          {t(`review.kind_${p.kind}`, p.kind)}
        </span>
        <span className={s.cardTitle}>{p.title || p.slug}</span>
        <span className={s.scopeBadge}>
          {isGlobal ? t('review.scopeGlobal') : projectLabel(p.project_id)}
        </span>
      </div>
      <p className={s.scopeSlug}>{p.slug}</p>
      <div className={s.scopeActions}>
        {isGlobal ? (
          <>
            <select
              className={s.select}
              value={target}
              onChange={e => setTarget(Number(e.target.value))}
              aria-label={t('review.scopeTarget')}
            >
              <option value={0}>{t('review.scopeChooseProject')}</option>
              {projects.map(pr => <option key={pr.id} value={pr.id}>{pr.name}</option>)}
            </select>
            <button
              className="btn btn-sm"
              disabled={busy || target === 0}
              onClick={() => onAssign(p.id, target)}
              title={t('review.scopeDemoteHint')}
            >
              {t('review.scopeDemote')}
            </button>
          </>
        ) : (
          <button
            className={`btn btn-sm ${armed ? 'btn-primary' : ''}`}
            disabled={busy}
            onClick={click}
            title={t('review.scopePromoteHint')}
          >
            {armed ? t('review.scopePromoteConfirm') : t('review.scopePromote')}
          </button>
        )}
      </div>
    </li>
  )
}

function ReviewCard({
  item, projectLabel, busy, rejected = false, onApprove, onReject, onDelete,
}: {
  item: WikiPendingPage
  projectLabel: (pid: number) => string
  busy: boolean
  rejected?: boolean
  onApprove?: (id: number) => void
  onReject?: (id: number) => void
  onDelete?: (id: number) => void
}) {
  const { t } = useTranslation()
  const p = item.page
  // Page version history (ADR-0021) is lazy and per-card: the list is a second
  // round trip nobody asked for until they opened the row, and one reviewer
  // comparing three pages should not pay for all three histories.
  const [versions, setVersions] = useState<WikiPageVersion[] | null>(null)
  const [versionsBusy, setVersionsBusy] = useState(false)
  const [versionsError, setVersionsError] = useState('')
  const [diff, setDiff] = useState<{ versionId: number; text: string } | null>(null)
  const [diffBusy, setDiffBusy] = useState(false)

  const failure = (e: unknown) => {
    const detail = e instanceof Error ? e.message : String(e)
    setVersionsError(detail ? `${t('review.versionLoadError')}: ${detail}` : t('review.versionLoadError'))
  }

  const toggleVersions = () => {
    if (versions !== null) {
      setVersions(null)
      setDiff(null)
      setVersionsError('')
      return
    }
    setVersionsError('')
    setVersionsBusy(true)
    // Newest first is the backend's contract (ORDER BY id DESC on the page_id
    // index), so it is rendered as returned — re-sorting here would let the two
    // version screens (notes and pages) disagree about what "newest" means.
    listWikiPageVersions(p.id)
      .then(v => setVersions(v))
      .catch(failure)
      .finally(() => setVersionsBusy(false))
  }

  const showDiff = (versionId: number) => {
    if (diff?.versionId === versionId) {
      setDiff(null)
      return
    }
    setVersionsError('')
    setDiffBusy(true)
    diffWikiPageVersion(p.id, versionId)
      .then(text => setDiff({ versionId, text }))
      .catch(failure)
      .finally(() => setDiffBusy(false))
  }

  return (
    <li className={`${s.card} ${rejected ? s.cardRejected : ''}`}>
      <div className={s.cardHead}>
        <span className={`${s.kindBadge} ${s['kindBadge_' + p.kind] ?? ''}`}>
          {t(`review.kind_${p.kind}`, p.kind)}
        </span>
        {/* Title first — the slug is the machine key, the title is what a
            reviewer compares against the sources. A missing title renders the
            slug instead so a row never opens nameless. */}
        <span className={s.cardTitle}>{p.title || p.slug}</span>
        <span className={s.cardProject}>{projectLabel(p.project_id)}</span>
      </div>
      <p className={s.slug}>{p.slug}</p>
      {/* Render the body as markdown (the same pipeline the knowledge base
          uses): compiler output IS markdown — headings, lists, [[wikilinks]] —
          and a raw <pre> made every pending page read like a dump. The
          rejected branch stays plain text on purpose: its content is an error
          placeholder, not a document. renderMarkdown strips frontmatter and
          sanitizes (DOMPurify), matching NoteSection. */}
      {rejected
        ? <p className={s.errLine}><Icon name="warning" size={14} /> {p.content}</p>
        : <div className={`${s.body} markdown-body`} dangerouslySetInnerHTML={{ __html: renderMarkdown(p.content) }} />}
      {item.source_note_ids?.length > 0 && (
        <div className={s.metaRow}>
          <span className={s.metaLabel}>{t('review.sourceNotes')}</span>
          {item.source_note_ids.map(id => (
            p.project_id === 0 ? (
              <span key={id} className={s.chip}>{t('review.noteRef', { n: id })}</span>
            ) : (
              <Link key={id} to={`/project/${p.project_id}?tab=notes&noteId=${id}`} className={s.chip}>
                {t('review.noteRef', { n: id })}
              </Link>
            )
          ))}
        </div>
      )}
      {item.in_links?.length > 0 && (
        <div className={s.metaRow}>
          <span className={s.metaLabel}>{t('review.inLinks')}</span>
          {item.in_links.map(l => (
            <Link key={`${l.page_id}-${l.relation}`} to={`/wiki/${l.page_id}`} className={s.chip}>{l.title || l.slug}</Link>
          ))}
        </div>
      )}
      {item.out_links?.length > 0 && (
        <div className={s.metaRow}>
          <span className={s.metaLabel}>{t('review.outLinks')}</span>
          {item.out_links.map(l => (
            <Link key={`${l.page_id}-${l.relation}`} to={`/wiki/${l.page_id}`} className={s.chip}>{l.title || l.slug}</Link>
          ))}
        </div>
      )}
      {/* Secondary action left, primary right — the page's whole job is the
          approve/decline decision, so the pair reads as one dialog. The version
          history toggle sits left of them: it is a reading aid, not a verdict,
          and putting it on the right would make it compete with 批准. */}
      <div className={s.actions}>
        <button
          className={`btn btn-sm ${s.versionToggle}`}
          onClick={toggleVersions}
          disabled={versionsBusy}
          aria-expanded={versions !== null}
          title={t('review.versionHistoryHint')}
        >
          {versionsBusy ? t('review.versionLoading') : t('review.versionHistory')}
        </button>
        {!rejected && (
          <>
            <button className="btn btn-sm" disabled={busy} onClick={() => onReject?.(p.id)}>
              {t('review.reject')}
            </button>
            <button className="btn btn-sm btn-primary" disabled={busy} onClick={() => onApprove?.(p.id)}>
              <Icon name="check" size={13} /> {t('review.approve')}
            </button>
          </>
        )}
        {rejected && (
          <button className="btn btn-sm btn-danger" disabled={busy} onClick={() => onDelete?.(p.id)}>
            {t('review.deletePermanently')}
          </button>
        )}
      </div>

      {/* ADR-0021: what a reviewer actually signed off on. The trigger keeps one
          snapshot per approved state, so this is the audit answer to "was it this
          page before?" — and on a rejected card it is the last thing the page said
          while it was still a conclusion. */}
      {versions !== null && (
        <div className={s.versions}>
          <div className={s.versionsHead}>
            <h3>{t('review.versionListTitle')}</h3>
            <button className="btn btn-sm" onClick={toggleVersions}>{t('common.close')}</button>
          </div>
          {versionsError && <p className={s.jobError}>{versionsError}</p>}
          {versions.length === 0 && <p className="empty-hint">{t('review.versionEmpty')}</p>}
          <ul className={s.versionList}>
            {versions.map(v => (
              <li key={v.id}>
                <button
                  className={`${s.versionItem} ${diff?.versionId === v.id ? s.versionItemActive : ''}`}
                  onClick={() => showDiff(v.id)}
                  disabled={diffBusy}
                  title={t('review.versionDiffHint')}
                >
                  <span className={s.versionTime}>{v.created_at}</span>
                  <span className={s.versionKind}>{t(`review.kind_${v.kind}`, v.kind)}</span>
                  <span className={s.versionSource}>{v.source}</span>
                </button>
              </li>
            ))}
          </ul>
          {diff && (
            diff.text
              ? <DiffViewer text={diff.text} />
              : <p className="empty-hint">{t('review.versionNoDiff')}</p>
          )}
        </div>
      )}
    </li>
  )
}

export default ReviewPage
