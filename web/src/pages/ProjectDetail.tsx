import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import StatusBar from '../components/StatusBar'
import ProjectOverviewSection from './project/ProjectOverviewSection'
import ProjectCommitLog from './project/ProjectCommitLog'
import ErrorBanner from '../components/ErrorBanner'
import Icon from '../components/Icon'
import NoteSection from '../components/NoteSection'
import PageShell from '../components/PageShell'
import TodoSection from '../components/TodoSection'
import { useProjectDetail } from '../hooks/useProjectDetail'
import { copyText } from '../utils/clipboard'
import { getAgentInstructionsStatus, installAgentInstructions } from '../api/endpoints'
import type { AgentInstructionsStatus as AgentInstructionsStatusData } from '../api/types'
import s from './ProjectDetail.module.css'

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
  // Derive the tab from the URL so a back/forward or another deep link switches
  // it without a stale local copy. A ?noteId= link opens on notes.
  const rawTab = searchParams.get('tab')
  const tab: DetailTab =
    rawTab === 'repos' ? 'commits'
    : rawTab && TAB_KEYS.includes(rawTab as DetailTab) ? (rawTab as DetailTab)
    : searchParams.get('newNote') === '1' || searchParams.get('noteId') ? 'notes'
    : 'overview'

  // The note a deep link asked to focus (Knowledge page → this project). Passed
  // to NoteSection, which scrolls to it and flashes it once.
  const focusNoteId = Number(searchParams.get('noteId') || '') || undefined

  const selectTab = (next: DetailTab) => {
    const params = new URLSearchParams(searchParams)
    params.set('tab', next)
    params.delete('newNote')
    setSearchParams(params, { replace: false })
  }

  // A tab is a page of its own. Without this the scroll container keeps the
  // offset of the tab the user just left, so switching from a long notes list
  // to the overview dropped the reader into the middle of the overview —
  // and on a short panel the strip could scroll out of reach entirely.
  // The ref is PageShell's scrolling body (the only scrolling region on the
  // page), so "switch tab" always starts at that tab's own beginning.
  const bodyRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    bodyRef.current?.scrollTo({ top: 0 })
  }, [tab])

  const [copied, setCopied] = useState(false)
  const [actionMsg, setActionMsg] = useState('')
  const actionTimer = useRef<number | null>(null)

  // --- Collected-project instruction checklist (ADR-0054 follow-up) ---------
  // A project whose AGENTS.md/CLAUDE.md carries no managed repotory block is
  // exactly the project whose agent sessions start blind — the condition that
  // let the handoff protocol idle for two weeks. The banner says so once per
  // project visit and offers the one-click top-up (block only, no configs, no
  // hook); "dismiss" is per-visit local state, never persisted, so the banner
  // returns next visit until the block actually exists.
  //
  // Both pieces of state carry the project id they belong to and are compared
  // at render time instead of being cleared synchronously in the effect — an
  // async fetch cannot race a project switch that way, and the effect stays
  // free of render-phase setState.
  const [instrStatus, setInstrStatus] = useState<(AgentInstructionsStatusData & { pid: number }) | null>(null)
  const [instrDismissedFor, setInstrDismissedFor] = useState<number | null>(null)
  const [instrBusy, setInstrBusy] = useState(false)

  useEffect(() => {
    if (!project?.id) return
    const pid = project.id
    let alive = true
    getAgentInstructionsStatus(pid)
      .then(st => { if (alive) setInstrStatus({ ...st, pid }) })
      .catch(() => { if (alive) setInstrStatus(null) })
    return () => { alive = false }
  }, [project?.id])

  const topUpInstructions = async () => {
    if (!project?.id || instrBusy) return
    const pid = project.id
    setInstrBusy(true)
    try {
      const report = await installAgentInstructions(pid)
      if (report.blocking || report.code) {
        flashAction(t('project.agentInstructions.refused', { code: report.code, defaultValue: `未执行：${report.code}` }))
      } else {
        flashAction(t('project.agentInstructions.done'))
        const st = await getAgentInstructionsStatus(pid)
        setInstrStatus({ ...st, pid })
      }
    } catch {
      flashAction(t('common.failed'))
    } finally {
      setInstrBusy(false)
    }
  }

  const instrBannerVisible =
    !!instrStatus && instrStatus.pid === project?.id &&
    (instrStatus.state === 'missing' || instrStatus.state === 'no_block') &&
    instrDismissedFor !== project?.id

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
              <button className="btn btn-primary" onClick={() => navigate(-1)}>
                {t('project.backToDashboard')}
              </button>
            </div>
          </div>
        </PageShell>
      )
    }
    return (
      <PageShell footer={<StatusBar />}>
        <button className={`btn btn-secondary ${s.backButton}`} onClick={() => navigate(-1)}>&larr; {t('project.backToDashboard')}</button>
        <ErrorBanner message={error || t('project.noRepos')} onRetry={retry} />
      </PageShell>
    )
  }

  return (
    <PageShell
      header={
        <>
          {actionMsg && <div className="message-banner" role="status">{actionMsg}</div>}

          {/* Instruction checklist banner: shows while this project's directory
              lacks the managed repotory block, with the one-click top-up. The
              "skipped" and "ok" states render nothing on purpose — a container
              is nobody's working directory, and "ok" is not a message worth the
              user's attention (the same rule the review rail dot follows). */}
          {instrBannerVisible && instrStatus && (
            <div className="message-banner" role="status">
              <span>
                {t(`project.agentInstructions.banner_${instrStatus.state}`, {
                  files: instrStatus.missing.join(', '),
                  defaultValue: '',
                })}
              </span>
              <button
                className="btn btn-secondary btn-sm"
                disabled={instrBusy}
                onClick={topUpInstructions}
              >
                {instrBusy ? t('project.agentInstructions.working') : t('project.agentInstructions.topUp')}
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => setInstrDismissedFor(project!.id)}>
                {t('project.agentInstructions.dismiss')}
              </button>
            </div>
          )}

          <div className={s.head}>
            <div className={s.headTop}>
              <div className={s.identity}>
                <button className={`btn btn-secondary btn-sm ${s.backButton}`} onClick={() => navigate(-1)}>
                  &larr; {t('project.backToDashboard')}
                </button>
                <div className={s.titleBlock}>
                  <div className={s.titleLine}>
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
                  </div>
                  <div className={s.headPath} title={project.root_path}>{project.root_path}</div>
                </div>
              </div>

              <div className={s.headActions}>
                <span className={s.levelControl} title={t('project.groupLevelHint')}>
                  <button className="btn btn-sm btn-icon" onClick={() => onLevelChange('down')} aria-label={t('project.levelDown')}><Icon name="minus" size={14} /></button>
                  <span className={s.levelValue}>{t('project.groupLevel', { n: project.level_override || 0 })}</span>
                  <button className="btn btn-sm btn-icon" onClick={() => onLevelChange('up')} aria-label={t('project.levelUp')}><Icon name="plus" size={14} /></button>
                </span>
                <button className="btn btn-primary btn-sm" onClick={handleCopyContext}>
                  {copied ? t('project.copied') : t('project.copyContext')}
                </button>
              </div>
            </div>

            <div className={s.headMeta}>
              <span>{t('commandPalette.reposCount', { count: project.repos?.length || 0 })}</span>
              {overview?.languages?.[0] && (
                <>
                  <span className={s.metaDivider} />
                  <span>{t('project.mainLanguage')} <strong>{overview.languages[0].language}</strong></span>
                </>
              )}
              <span className={s.metaDivider} />
              <span>{project.is_auto_grouped ? t('project.autoGroup') : t('project.manualGroup')}</span>
              {dateParam && (
                <>
                  <span className={s.metaDivider} />
                  <span>{t('project.datePill')} <strong>{dateParam}</strong></span>
                </>
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
      bodyRef={bodyRef}
    >
      <div className={s.panelBody} role="tabpanel" id={`project-panel-${tab}`} aria-labelledby={`project-tab-${tab}`}>
        {tab === 'overview' && overview && (
          <ProjectOverviewSection
            overview={overview}
            trend={trendData}
            scope={scope}
            onScopeChange={setScope}
            totals={totals}
          />
        )}

        {tab === 'commits' && (
          /* One card, one question: "what exactly happened". The trend chart
             and activity metrics now live in the Overview tab, where they sit
             next to the languages/README that explain the shape.
             No heading above the log: the log's own left pane is titled
             "提交记录", so an outer h2 with the same words repeated the title
             and added a second frame around a card that already has one. */
          <div className="detail-section commit-log-section">
            <ProjectCommitLog projectId={Number(id)} repos={project.repos || []} />
          </div>
        )}

        {tab === 'notes' && (
          /* Notes are a card grid, like the knowledge hub: at 720px the tab
             showed one sheet per screen, which is a reading column doing a
             scanning job. The grid gets the full content width (it improves
             with width), and the editor — the only thing that still needs a
             measure — caps itself at 760px inside NoteSection. `key` remounts
             the section when the project changes: /project/:id reuses this page
             instance, and without the key the previous project's open draft was
             persisted into the new project's localStorage key — and could be
             saved there. */
          <div className={s.gridColumn}>
            <NoteSection key={`notes-${id}`} projectId={Number(id)} autoNew={searchParams.get('newNote') === '1'} focusNoteId={focusNoteId} />
          </div>
        )}

        {tab === 'todos' && (
          /* Same column as notes: a checklist whose checkbox and delete button
             sit 1100px apart is a checklist nobody scans. */
          <div className={s.readingColumn}>
            <TodoSection key={`todos-${id}`} projectId={Number(id)} />
          </div>
        )}
      </div>

    </PageShell>
  )
}

export default ProjectDetailPage
