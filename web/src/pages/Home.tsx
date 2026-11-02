import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import HandoffComposer from '../components/HandoffComposer'
import {
  listRecentHandoffs,
  getProjects,
  getSummary,
  type HandoffEntry,
  type Project,
  type Summary,
} from '../api/client'
import { useToast } from '../hooks/useToast'
import HomeKnowledgeAssistant from './home/HomeKnowledgeAssistant'
import s from './Home.module.css'

export default function Home() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const pushToast = useToast()

  const [entries, setEntries] = useState<HandoffEntry[] | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [summary, setSummary] = useState<Summary | null>(null)
  const [composerOpen, setComposerOpen] = useState(false)

  const loadHandoffs = useCallback(() => {
    listRecentHandoffs(10)
      .then(r => setEntries(r ?? []))
      .catch(() => setEntries([]))
  }, [])

  useEffect(() => {
    document.title = 'Home - Repotory'
  }, [])

  useEffect(() => {
    let cancelled = false
    loadHandoffs()

    getProjects(undefined, false)
      .then(p => {
        if (!cancelled) setProjects(p ?? [])
      })
      .catch(() => {
        if (!cancelled) setProjects([])
      })

    getSummary('')
      .then(s => {
        if (!cancelled) setSummary(s)
      })
      .catch(() => {
        if (!cancelled) setSummary(null)
      })

    return () => {
      cancelled = true
    }
  }, [loadHandoffs])

  const starredOrTopProjects = projects
    .filter(p => p.is_starred)
    .concat(projects.filter(p => !p.is_starred))
    .slice(0, 3)

  // Honest scale hint: `projects` counts every row in the table — knowledge-
  // import landing projects and scan remnants included — while the dashboard's
  // repo list only shows projects that actually carry git repositories. State
  // both numbers so the two pages cannot read as contradictory.
  const withRepos = projects.filter(p => (p.repo_count || 0) > 0).length

  return (
    <div className={s.page}>
      <h1 className="visually-hidden">{t('home.title', { defaultValue: '首页' })}</h1>

      {/* Memory-ring strip (ADR-0024): recent handoffs + the global-search entry */}
      <section className={s.ring} aria-label={t('home.ringTitle', { defaultValue: '记忆环' })}>
        <div className={s.ringHead}>
          <h2>{t('home.ringTitle', { defaultValue: '记忆环' })}</h2>
          <p className={s.ringSub}>{t('home.ringSub', { defaultValue: '最近的会话交接记录' })}</p>
        </div>

        {/* The zero-config entry into the session loop: write one handoff by
            hand. The agent half of the protocol needs an MCP client, a model
            and a client that remembers to call the tool — none of which a
            first-time user has. */}
        <button
          type="button"
          className={`${s.ringWrite} btn btn-secondary btn-sm`}
          onClick={() => setComposerOpen(true)}
        >
          <Icon name="plus" size={12} />
          {t('home.writeHandoff', { defaultValue: '写一条交接记录' })}
        </button>

        {entries === null ? null : entries.length === 0 ? (
          <p className={s.ringEmpty}>
            {t('home.noHandoffs', { defaultValue: '还没有交接记录——会话结束或手动捕捉后会出现在这里' })}
          </p>
        ) : (
          <ul className={s.handoffList}>
            {entries.map(e => (
              <li key={e.note_id}>
                <button
                  type="button"
                  className={s.handoffItem}
                  onClick={() => navigate(`/project/${e.project_id}`)}
                  title={t('home.openProject', { defaultValue: '打开项目 {{name}}', name: e.project_name })}
                >
                  <span className={s.handoffTitle}>{e.title}</span>
                  <span className={s.handoffMeta}>
                    <span className={s.handoffProject}>{e.project_name}</span>
                    {e.auto_captured && (
                      <span className={s.autoBadge}>{t('home.autoCaptured', { defaultValue: '自动' })}</span>
                    )}
                    <span>{e.updated_at.slice(0, 16)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}

        <Link to="/search" className={s.searchEntry} title={t("search:homeEntryHint")}>
          <Icon name="search" size={14} />
          <span className={s.searchEntryLabel}>{t('search:homeEntry', { defaultValue: '全局搜索' })}</span>
          <span className={s.searchEntryHint}>{t('search:homeEntryHint', { defaultValue: '笔记 · 待办 · 项目' })}</span>
        </Link>
      </section>

      <HandoffComposer
        open={composerOpen}
        onClose={() => setComposerOpen(false)}
        onCreated={() => {
          loadHandoffs()
          pushToast({ kind: 'success', title: t('handoff.saved', { defaultValue: '交接记录已写入' }) })
        }}
      />

      {/* Interactive AI Knowledge Dialogue Workbench */}
      <section className={s.chatArea} aria-label={t('home.assistantTitle', { defaultValue: '知识库智能对话' })}>
        <HomeKnowledgeAssistant
          projects={projects}
          onToast={pushToast}
          onGoToSettings={() => navigate('/settings')}
        />
      </section>

      {/* Quick Project Focus & Developer Dashboard Portal (ADR-0017 project status) */}
      <section className={s.statusSection} aria-label={t('dashboard.title', { defaultValue: '仪表盘' })}>
        <div className="visually-hidden">{t('dashboard.title')}</div>
        <div className={s.statusSectionHeader}>
          <div className={s.statusTitleArea}>
            <h2 className={s.statusSectionTitle}>
              <Icon name="grid" size={15} />
              <span>{t('home.quickProjectsTitle', { defaultValue: '重点项目速览' })}</span>
            </h2>
            <span className={s.statusSectionHint}>
              {projects.length > 0
                ? withRepos === projects.length
                  ? t('home.projectCount', { count: projects.length })
                  : t('home.projectCountWithRepos', { count: projects.length, withRepos })
                : ''}
              {/* The grid below shows a fixed few cards; without this the header
                  count and the visible cards read as contradictory (33 projects
                  announced, 3 rendered, no hint that 30 more exist). */}
              {starredOrTopProjects.length < projects.length && (
                <span className={s.statusSectionMore}>
                  {t('home.projectCardsTruncated', {
                    shown: starredOrTopProjects.length,
                    total: projects.length,
                  })}
                </span>
              )}
            </span>
          </div>

          <Link to="/dashboard" className={s.openDashboardBtn}>
            <span>{t('home.viewAllDashboard', { defaultValue: '进入完整仪表盘' })}</span>
            <span className={s.arrowIcon}>→</span>
          </Link>
        </div>

        <div className={s.statusGrid}>
          {starredOrTopProjects.map(p => (
            <div
              key={p.id}
              className={s.projectQuickCard}
              onClick={() => navigate(`/project/${p.id}`)}
              role="button"
              tabIndex={0}
              onKeyDown={e => {
                if (e.key === 'Enter') navigate(`/project/${p.id}`)
              }}
            >
              <div className={s.quickCardHead}>
                <span className={s.quickCardName}>
                  <Icon name="file-text" size={14} />
                  {p.name}
                </span>
                {p.is_starred && <Icon name="star" size={13} filled={true} className={s.starIcon} />}
              </div>
              <div className={s.quickCardPath}>{p.root_path}</div>
              <div className={s.quickCardFoot}>
                <span>{p.repo_count} repos</span>
                <span className={s.quickCardLink}>{t('home.openProject', { name: '', defaultValue: '详情' })} →</span>
              </div>
            </div>
          ))}

          {/* Today's Dev Activity Summary Card */}
          <div className={s.todaySummaryCard} title={t("home.todayCardTooltip")}>
            <div className={s.todayHead}>
              <Icon name="zap" size={14} />
              <span>{t('home.todayActivity', { defaultValue: '今日开发动态' })}</span>
            </div>
            <div className={s.todayStats}>
              <div className={s.todayStatItem}>
                <span className={s.todayStatValue}>
                  {summary ? `+${summary.my_added}` : '0'}
                </span>
                <span className={s.todayStatLabel}>{t('dashboard.personalAdded', { defaultValue: '新增行数' })}</span>
              </div>
              <div className={s.todayStatItem}>
                <span className={s.todayStatValue}>{summary?.repo_count ?? projects.length}</span>
                <span className={s.todayStatLabel}>{t('dashboard.reposShort', { defaultValue: '仓库数' })}</span>
              </div>
            </div>
            <Link to="/dashboard" className={s.todayLink}>
              {t('home.viewAllDashboard', { defaultValue: '查看热力图与统计' })} →
            </Link>
          </div>
        </div>
      </section>
    </div>
  )
}
