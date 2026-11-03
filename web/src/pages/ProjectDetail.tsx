import { useRef, useState } from 'react'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import TrendChart from '../components/TrendChart'
import StatusBar from '../components/StatusBar'
import ScopeToggle from '../components/ScopeToggle'
import ProjectOverviewSection from './project/ProjectOverviewSection'
import ProjectCommitLog from './project/ProjectCommitLog'
import ErrorBanner from '../components/ErrorBanner'
import Icon from '../components/Icon'
import NoteSection from '../components/NoteSection'
import PageShell from '../components/PageShell'
import TodoSection from '../components/TodoSection'
import { useProjectDetail } from '../hooks/useProjectDetail'
import { copyText } from '../utils/clipboard'
import s from './ProjectDetail.module.css'
import trendCss from '../components/TrendChart.module.css'

// 预留钩子类：`commits-trend` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

const TAB_KEYS = ['overview', 'commits', 'notes', 'todos'] as const
type DetailTab = typeof TAB_KEYS[number]

function ProjectDetailPage() {
  const { t } = useTranslation()
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const dateParam = searchParams.get('date') || ''

  const {
    project, overview, loading, error, setError,
    scope, setScope, trendData, totals, retry, handleLevelChange, previewMergeUp,
  } = useProjectDetail(id)

  // The page's sections live behind tabs instead of one long scroll: the
  // fixed header used to eat most of the viewport and every dynamic block
  // (heatmap, trends, repos, notes) sat below the fold. The active tab is
  // mirrored into ?tab= so a section is linkable; ?newNote=1 (Knowledge page
  // jump) lands on the notes tab where the create form actually lives.
  const initialTab = (): DetailTab => {
    // A ?noteId= deep link (from the Knowledge page) must open on the notes tab
    // so the target note is visible and can be highlighted.
    if (searchParams.get('newNote') === '1' || searchParams.get('noteId')) return 'notes'
    const raw = searchParams.get('tab')
    // 'repos' merged into 'commits': the per-repo breakdown IS the commit
    // view — heatmap/trends project-wide, breakdown by repository below.
    const normalized: DetailTab | null =
      raw === 'repos' ? 'commits'
      : raw && TAB_KEYS.includes(raw as DetailTab) ? (raw as DetailTab)
      : null
    return normalized ?? 'overview'
  }
  const [tab, setTab] = useState<DetailTab>(initialTab)

  // The note a deep link asked to focus (Knowledge page → this project). Passed
  // to NoteSection, which scrolls to it and flashes it once.
  const focusNoteId = Number(searchParams.get('noteId') || '') || undefined

  const selectTab = (next: DetailTab) => {
    setTab(next)
    const params = new URLSearchParams(searchParams)
    params.set('tab', next)
    params.delete('newNote')
    params.delete('noteId')
    setSearchParams(params, { replace: true })
  }

  const [copied, setCopied] = useState(false)
  const [actionMsg, setActionMsg] = useState('')
  const actionTimer = useRef<number | null>(null)

  const flashAction = (msg: string) => {
    setActionMsg(msg)
    if (actionTimer.current) clearTimeout(actionTimer.current)
    actionTimer.current = window.setTimeout(() => setActionMsg(''), 3500)
  }

  const onLevelChange = async (direction: 'up' | 'down') => {
    let confirmed = false
    if (direction === 'up') {
      // A merge is destructive: it deletes every sibling project it absorbs, along
      // with their notes and todos. The counts come from the server, so what the
      // user approves is what the merge will do. No preview means no merge.
      const pre = await previewMergeUp()
      if (!pre) return
      if (pre.siblings.length > 0) {
        const shown = pre.siblings.slice(0, 8).map(s => `• ${s.name}`).join('\n')
        const more = pre.siblings.length > 8
          ? `\n${t('project.levelUpMore', { n: pre.siblings.length - 8, defaultValue: '' })}`
          : ''
        const ok = window.confirm(t('project.levelUpConfirm', {
          count: pre.siblings.length,
          target: pre.target_name,
          names: shown + more,
          repos: pre.repos,
          notes: pre.notes,
          todos: pre.todos,
          defaultValue: '',
        }))
        if (!ok) return
        confirmed = true
      } else {
        // Nothing is absorbed, so no project disappears: the click only moves the
        // root up one level. No dialog, and the backend's explicit-intent flag is
        // set because a human did ask for this.
        confirmed = true
      }
    }
    const r = await handleLevelChange(direction, confirmed)
    if (r.ok) {
      flashAction(t('project.levelChanged'))
    } else if (r.raw && /not found/i.test(r.raw)) {
      // MergeProjectUp/SplitProjectDown surface "no sibling here" as 404 —
      // translate it or the button reads as dead. Matched on the backend sentence
      // because r.error is already localized for display.
      flashAction(t('project.levelNoSibling'))
    } else {
      flashAction(r.error || t('common.failed'))
    }
  }

  // The shared context pack: one builder feeds both "Copy AI Context" and the
  // AI Q&A dialog's prompt pack.
  const buildContextLines = (): string[] | null => {
    if (!project) return null
    // This markdown feeds an AI model, not the UI: new section headers stay
    // in fixed English so the prompt-like output is stable across locales
    // (existing t() labels kept for continuity).
    const lines: string[] = [
      `# ${project.name}`,
      `path: ${project.root_path}`,
      `grouping: ${project.is_auto_grouped ? t('project.autoGroup') : t('project.manualGroup')}`,
    ]
    if (overview?.readme_excerpt) lines.push('', `## README\n${overview.readme_excerpt}`)
    if (overview?.tech_stack?.length) lines.push('', `## ${t('project.techStack')}\n${overview.tech_stack?.map(x => x.name).join(', ')}`)
    if (overview?.languages?.length) {
      lines.push('', `## Languages\n${overview.languages.slice(0, 8).map(l => `${l.language}: ${l.count}`).join('\n')}`)
    }
    if (overview?.activity && (overview.activity.total_commits > 0 || overview.activity.last_commit_date)) {
      lines.push(
        '',
        `## Activity\n- total commits: ${overview.activity.total_commits}\n- active days (90d): ${overview.activity.active_days}\n- commits (30d): ${overview.activity.commit_rate_30d}\n- last commit: ${overview.activity.last_commit_date}`,
      )
    }
    if (totals) {
      lines.push('', `## Stats (selected range)\n- added: ${totals.added}\n- deleted: ${totals.deleted}\n- files changed: ${totals.files}\n- active days: ${totals.active}`)
    }
    if (project.repos?.length) {
      const capped = project.repos.slice(0, 30)
      const more = project.repos.length - capped.length
      lines.push('', `## Repositories (${project.repos.length})\n${capped.map(r => `- ${r.path}`).join('\n')}${more > 0 ? `\n- ...and ${more} more` : ''}`)
    }
    if (overview?.recent_commits?.length) lines.push('', `## ${t('project.recentCommits')}\n${overview.recent_commits?.slice(0, 5).map(c => `- ${c.time} ${c.message}`).join('\n')}`)
    return lines
  }

  const handleCopyContext = async () => {
    const lines = buildContextLines()
    if (!lines) return
    try {
      await copyText(lines.join('\n'))
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setError(t('common.failed'))
    }
  }

  if (loading) {
    return (
      <PageShell
        header={
          <>
            <div className="skeleton skeleton-text" style={{width: 200, height: 28, marginBottom: 8}} />
            <div className="skeleton skeleton-text" style={{width: '50%', height: 14, marginBottom: 20}} />
            <div className="skeleton skeleton-text" style={{width: '100%', height: 80, marginBottom: 16}} />
          </>
        }
        footer={<StatusBar />}
      >
        <div className="skeleton skeleton-text" style={{width: '100%', height: 280, marginBottom: 16}} />
      </PageShell>
    )
  }

  if (error || !project) {
    // A missing project is not a transient failure: retrying the same request
    // will fail identically, so it gets a dead-end state with a way out rather
    // than a retry button. The backend message ("project not found") is
    // untranslated, so the not-found case is detected from it and re-rendered
    // in the UI language.
    const notFound = !error || /not found|不存在/i.test(error)
    if (notFound) {
      return (
        <PageShell footer={<StatusBar />}>
          <div className="empty-state">
            <div className="empty-icon"><Icon name="search" size={34} /></div>
            <h3>{t('project.notFoundTitle')}</h3>
            <p>{t('project.notFoundDesc')}</p>
            <div className="empty-actions">
              <button className="btn btn-primary" onClick={() => navigate('/dashboard')}>
                {t('project.backToDashboard')}
              </button>
            </div>
          </div>
        </PageShell>
      )
    }
    return (
      <PageShell footer={<StatusBar />}>
        <button className={`btn btn-secondary ${s.backButton}`} onClick={() => navigate('/dashboard')}>&larr; {t('project.backToDashboard')}</button>
        <ErrorBanner message={error || t('project.noRepos')} onRetry={retry} />
      </PageShell>
    )
  }

  return (
    <PageShell
      header={
        <>
          {actionMsg && <div className="message-banner" role="status">{actionMsg}</div>}

          {/* Compact head: identity + copy action on line 1, one stat strip on
              line 2, tab strip on line 3. Everything that used to be a stacked
              card here (title card, 4-column stats grid, meta row) collapsed
              into ~120px so the tab panels own the viewport. */}
          <div className={s.head}>
            <div className={s.headLine}>
              <button className="btn btn-secondary btn-sm" onClick={() => navigate('/dashboard')}>
                &larr; {t('project.backToDashboard')}
              </button>
              <h1>{project.name}</h1>
              {project.project_kind === 'import_placeholder' && (
                <span className="badge" title={t('project.kindPlaceholderTip', { defaultValue: '导入时未匹配到项目，按仓库名自动创建的落点项目' })}>
                  {t('project.kindPlaceholder', { defaultValue: '未归类仓库' })}
                </span>
              )}
              {project.project_kind === 'default' && (
                <span className="badge" title={t('project.kindDefaultTip', { defaultValue: '无对应仓库的全局记忆落点，可在知识库「整理」里归口' })}>
                  {t('project.kindDefault', { defaultValue: '未分类' })}
                </span>
              )}
              <span className={s.headPath} title={project.root_path}>{project.root_path}</span>
              <span className={s.levelControl} title={t('project.groupLevelHint')}>
                <button className="btn btn-sm btn-icon" onClick={() => onLevelChange('down')} aria-label={t('project.levelDown')}><Icon name="minus" size={14} /></button>
                <span className={s.levelValue}>{t('project.groupLevel', { n: project.level_override || 0 })}</span>
                <button className="btn btn-sm btn-icon" onClick={() => onLevelChange('up')} aria-label={t('project.levelUp')}><Icon name="plus" size={14} /></button>
              </span>
              <button className="btn btn-primary btn-sm" onClick={handleCopyContext}>
                {copied ? t('project.copied') : t('project.copyContext')}
              </button>
            </div>

            <div className={s.statsLine}>
              <span className={s.stat}><span className={s.statLabel}>{t('project.activeDays')}</span><span className={s.statValue}>{totals.active}</span></span>
              <span className={s.stat}><span className={s.statLabel}>{t('project.fileChanges')}</span><span className={s.statValue}>{totals.files}</span></span>
              <span className={s.stat}><span className={s.statLabel}>{t('project.added')}</span><span className={`${s.statValue} ${totals.added > 0 ? 'green' : ''}`}>{totals.added > 0 ? `+${totals.added}` : '0'}</span></span>
              <span className={s.stat}><span className={s.statLabel}>{t('project.deleted')}</span><span className={`${s.statValue} ${totals.deleted > 0 ? 'red' : ''}`}>{totals.deleted > 0 ? `-${totals.deleted}` : '0'}</span></span>
              <span className={s.stat}><span className={s.statLabel}>{t('project.subRepos')}</span><span className={s.statValue}>{project.repos?.length || 0}</span></span>
              {overview?.activity?.last_commit_date && (
                <span className={s.stat}><span className={s.statLabel}>{t('project.lastCommit')}</span><span className={s.statValue}>{overview.activity.last_commit_date}</span></span>
              )}
              {overview?.languages?.[0] && (
                <span className={s.stat}><span className={s.statLabel}>{t('project.mainLanguage')}</span><span className={s.statValue}>{overview.languages[0].language}</span></span>
              )}
              <span className={s.stat}><span className={s.statLabel}>{t('project.grouping')}</span><span className={s.statValue}>{project.is_auto_grouped ? t('project.autoGroup') : t('project.manualGroup')}</span></span>
              {dateParam && (
                <span className={s.stat}><span className={s.statLabel}>{t('project.datePill')}</span><span className={s.statValue}>{dateParam}</span></span>
              )}
            </div>

            <div className={s.tabs} role="tablist" aria-label={t('project.sectionTabs', { defaultValue: 'Sections' })}>
              {TAB_KEYS.map((key) => (
                <button
                  key={key}
                  role="tab"
                  id={`project-tab-${key}`}
                  aria-selected={tab === key}
                  aria-controls={`project-panel-${key}`}
                  tabIndex={tab === key ? 0 : -1}
                  className={`tab-btn ${tab === key ? 'tab-active' : ''}`}
                  onClick={() => selectTab(key)}
                  onKeyDown={(e) => {
                    const i = TAB_KEYS.indexOf(key)
                    if (e.key === 'ArrowRight') selectTab(TAB_KEYS[(i + 1) % TAB_KEYS.length])
                    else if (e.key === 'ArrowLeft') selectTab(TAB_KEYS[(i - 1 + TAB_KEYS.length) % TAB_KEYS.length])
                    else if (e.key === 'Home') selectTab(TAB_KEYS[0])
                    else if (e.key === 'End') selectTab(TAB_KEYS[TAB_KEYS.length - 1])
                    else return
                    e.preventDefault()
                  }}
                >
                  {t(`project.tab_${key}`)}
                </button>
              ))}
            </div>
          </div>
        </>
      }
      footer={<StatusBar />}
    >
      <div role="tabpanel" id={`project-panel-${tab}`} aria-labelledby={`project-tab-${tab}`}>
        {tab === 'overview' && overview && <ProjectOverviewSection overview={overview} />}

        {tab === 'commits' && (
          /* Two cards, two questions: the trend card answers "how much,
             when" (chart plus the activity metrics folded into its header),
             the log card answers "what exactly happened" (one timeline with
             a repo filter). The heatmap card that used to hold half the top
             band duplicated the dashboard's heatmap and pushed both
             answers below the fold. */
          <div className={s.commitsTab}>
            <div className="detail-section commits-trend">
              <div className="section-header">
                <h2>{t('project.trendTitle')}</h2>
                {overview?.activity && (
                  <div className={trendCss.metrics}>
                    <span className={trendCss.metric}>
                      <span className={trendCss.metricLabel}>{t('project.totalCommits')}</span>
                      <span className={trendCss.metricValue}>{overview.activity.total_commits}</span>
                    </span>
                    <span className={trendCss.metric}>
                      <span className={trendCss.metricLabel}>{t('project.last30d')}</span>
                      <span className={trendCss.metricValue}>{overview.activity.commit_rate_30d}</span>
                    </span>
                    <span className={trendCss.metric}>
                      <span className={trendCss.metricLabel}>{t('project.activeDays90')}</span>
                      <span className={trendCss.metricValue}>{overview.activity.active_days}</span>
                    </span>
                    <span className={trendCss.metric}>
                      <span className={trendCss.metricLabel}>{t('project.activeMonths')}</span>
                      <span className={trendCss.metricValue}>{overview.activity.active_months}</span>
                    </span>
                  </div>
                )}
                <ScopeToggle scope={scope} onChange={setScope} />
              </div>
              {trendData.labels.length > 0 ? (
                <TrendChart labels={trendData.labels} datasets={trendData.datasets} />
              ) : (
                <div className={s.emptySection}>
                  {t('project.noDataInRange')}
                  {scope !== 'all' && (
                    <div className="empty-actions">
                      {scope === 'week' && (
                        <button className="btn btn-secondary btn-sm" onClick={() => setScope('month')}>
                          {t('heatmap.show30d')}
                        </button>
                      )}
                      <button className="btn btn-secondary btn-sm" onClick={() => setScope('all')}>
                        {t('heatmap.showAll')}
                      </button>
                    </div>
                  )}
                </div>
              )}
            </div>

            <div className="detail-section">
              <div className="section-header">
                <h2>{t('project.commitLog')}</h2>
              </div>
              <ProjectCommitLog projectId={Number(id)} repos={project.repos || []} />
            </div>
          </div>
        )}

        {tab === 'notes' && (
          /* key remounts the section when the project changes: /project/:id
              reuses this page instance, and without the key the previous
              project's open draft was persisted into the new project's
              localStorage key — and could be saved there. */
          <NoteSection key={`notes-${id}`} projectId={Number(id)} autoNew={searchParams.get('newNote') === '1'} focusNoteId={focusNoteId} />
        )}

        {tab === 'todos' && (
          <TodoSection key={`todos-${id}`} projectId={Number(id)} />
        )}
      </div>

    </PageShell>
  )
}

export default ProjectDetailPage
