import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import SummaryBar from '../components/SummaryBar'
import GoalRing from '../components/GoalRing'
import Heatmap from '../components/Heatmap'
import StatusBar from '../components/StatusBar'
import DatePicker from '../components/DatePicker'
import ProjectCard from '../components/ProjectCard'
import ProjectSearchDropdown from './dashboard/ProjectSearchDropdown'
import ErrorBanner from '../components/ErrorBanner'
import Icon from '../components/Icon'
import HandoffComposer from '../components/HandoffComposer'
import { listRecentHandoffs, type HandoffEntry } from '../api/client'
import { useToast } from '../hooks/useToast'
import { useDashboardData, type SortKey } from '../hooks/useDashboardData'
import s from './Dashboard.module.css'

function Dashboard() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const pushToast = useToast()
  const {
    summary, dailyGoal, date, sortKey, confirmScan, showStarredOnly,
    displayedError, scanning, scanMsg, scanDoneMsg, loading,
    sorted, starredProjects, unstarredProjects,
    todoMap, noteMap, globalTodoCount, myAdded, isWorkday, sortOptions,
    setDate, setSortKey, setConfirmScan, setShowStarredOnly,
    handleScan, handleToggleStar, handleRefreshHistory, fetchSummary,
    refetchProjects, setError, setSummaryError,
  } = useDashboardData()

  // 记忆环（ADR-0038 决策 5）：最近交接记录 + 「写一条交接记录」。
  // 它讲的是「我上次干到哪儿了」，属于「我的状态」，不属于首页对话台回答的
  // 「我要答案」；而且「写一条交接记录」是零配置进入会话循环的唯一入口，
  // 仪表盘同样是首屏可达。
  const [entries, setEntries] = useState<HandoffEntry[] | null>(null)
  const [composerOpen, setComposerOpen] = useState(false)

  const loadHandoffs = useCallback(() => {
    listRecentHandoffs(10)
      .then(r => setEntries(r ?? []))
      .catch(() => setEntries([]))
  }, [])

  useEffect(() => {
    loadHandoffs()
  }, [loadHandoffs])

  return (
    <div className={s.page}>
      <h1 className="visually-hidden">{t('dashboard.title')}</h1>
      <div className="visually-hidden" role="status" aria-live="polite">
        {scanning ? (scanMsg || t('dashboard.scanning', { defaultValue: 'Scanning…' })) : scanDoneMsg}
      </div>
      <div className={s.fixed}>
        <div className={s.heroRow}>
          <div className={s.heroCard}>
            <GoalRing
              value={myAdded}
              goal={isWorkday ? dailyGoal : 0}
              label={isWorkday ? t('dashboard.todayGoal', { defaultValue: "Today's Goal" }) : t('dashboard.notWorkday', { defaultValue: 'Not a workday' })}
              sublabel={isWorkday ? `${myAdded} / ${dailyGoal} ${t('dashboard.linesUnit')}` : `${myAdded} ${t('dashboard.linesUnit')}`}
            />
            <div className={s.heroText}>
              <div className={s.heroEyebrow}>{date} · {isWorkday ? t('dashboard.workday', { defaultValue: 'Workday' }) : t('dashboard.weekendShort')}</div>
              <div className={s.heroTitle}>
                {isWorkday
                  ? (myAdded >= dailyGoal ? t('dashboard.goalReached', { defaultValue: "Today's goal reached" }) : t('dashboard.goalRemaining', { count: Math.max(dailyGoal - myAdded, 0) }))
                  : t('dashboard.weekend', { defaultValue: 'Happy weekend' })}
              </div>
              <div className={s.heroSub}>
                {t('dashboard.personalAdded')}{' '}
                <strong className={myAdded > 0 ? 'green' : 'muted-num'}>
                  {myAdded > 0 ? `+${myAdded}` : '0'}
                </strong>{' · '}
                {t('dashboard.filesShort')} <strong>{summary?.my_files || 0}</strong> ·
                {t('dashboard.reposInvolved', { count: summary?.repo_count || 0 })}
              </div>
            </div>
          </div>

          <SummaryBar summary={summary} globalTodoCount={globalTodoCount} />
        </div>

        <Heatmap onDayClick={setDate} />

        <div className={s.controls}>
          <DatePicker value={date} onChange={setDate} />
          <div className={s.actions}>
            <ProjectSearchDropdown onToggleStar={handleToggleStar} />
            <div className="filter-toggle">
              <button className={`filter-btn ${!showStarredOnly ? 'active' : ''}`} onClick={() => setShowStarredOnly(false)}>{t('dashboard.all', { defaultValue: 'All' })}</button>
              <button className={`filter-btn ${showStarredOnly ? 'active' : ''}`} onClick={() => setShowStarredOnly(true)}>{t('dashboard.starred', { defaultValue: 'Starred' })}</button>
            </div>
            <div className={s.sortControl}>
              <label htmlFor="dashboard-sort">{t('dashboard.sortBy')}</label>
              <select id="dashboard-sort" value={sortKey} onChange={(e) => setSortKey(e.target.value as SortKey)} className="form-input sort-select">
                {sortOptions.map(opt => <option key={opt.key} value={opt.key}>{opt.label}</option>)}
              </select>
            </div>
            {confirmScan ? (
              <div className={`${s.confirmGroup}`}>
                <span className={`${s.confirmText}`}>{t('dashboard.confirmRescan')}</span>
                <button className="btn btn-primary btn-sm" onClick={handleScan} disabled={scanning}>{t('common.confirm')}</button>
                <button className="btn btn-sm" onClick={() => setConfirmScan(false)}>{t('common.cancel')}</button>
              </div>
            ) : (
              <button className="btn btn-primary" onClick={() => setConfirmScan(true)} disabled={scanning}>
                {scanning ? (scanMsg || t('dashboard.scanning', { defaultValue: 'Processing...' })) : t('dashboard.rescan', { defaultValue: 'Rescan' })}
              </button>
            )}
          </div>
        </div>

        {displayedError && (
          <ErrorBanner message={displayedError} onRetry={() => { setError(''); setSummaryError(''); void fetchSummary(date); void refetchProjects() }} />
        )}
      </div>

      <div className={s.scroll}>
        {/* 记忆环放在滚动区的最上面，而不是 s.fixed 仪表盘头部里。
            s.fixed 是「永远不滚动」的头部区，把一块会增长的记录列表塞进去，
            窄窗口下会把整个头部顶爆；放在滚动区顶部，记忆环仍然是仪表盘的
            第一屏第一块（「上次的会话」）。首页已是整页对话台，这里是记忆环
            的唯一一处。 */}
        <section className={s.ring} aria-label={t('handoff.ringTitle', { defaultValue: '记忆环' })}>
          <div className={s.ringHead}>
            <div>
              <h2>{t('handoff.ringTitle', { defaultValue: '记忆环' })}</h2>
              <p className={s.ringSub}>{t('handoff.ringSub', { defaultValue: '最近的会话交接记录' })}</p>
            </div>
            {/* 零配置进入会话循环的入口：手写一条交接记录。agent 那一半需要
                MCP 客户端、模型和「记得调用工具」的客户端——第一次用的人一样
                都没有，所以手写这条路是唯一能立刻试起来的。 */}
            <button
              type="button"
              className={`${s.ringWrite} btn btn-secondary btn-sm`}
              onClick={() => setComposerOpen(true)}
            >
              <Icon name="plus" size={12} />
              {t('handoff.write', { defaultValue: '写一条交接记录' })}
            </button>
          </div>

          {entries === null ? null : entries.length === 0 ? (
            <p className={s.ringEmpty}>
              {t('handoff.empty', { defaultValue: '还没有交接记录——写一条，或等会话结束后出现在这里' })}
            </p>
          ) : (
            <ul className={s.handoffList}>
              {entries.map(e => (
                <li key={e.note_id}>
                  <button
                    type="button"
                    className={s.handoffItem}
                    onClick={() => navigate(`/project/${e.project_id}`)}
                    title={t('handoff.openProject', { defaultValue: '打开项目 {{name}}', name: e.project_name })}
                  >
                    <span className={s.handoffTitle}>{e.title}</span>
                    <span className={s.handoffMeta}>
                      <span className={s.handoffProject}>{e.project_name}</span>
                      {e.auto_captured && (
                        <span className={s.autoBadge}>{t('handoff.auto', { defaultValue: '自动' })}</span>
                      )}
                      <span>{e.updated_at.slice(0, 16)}</span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>

        {loading ? (
          <div className={s.projectGrid}>
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className={s.skeletonCard}>
                <div className={s.skeletonHeader}>
                  <div className="skeleton skeleton-text" style={{ width: '60%', height: 20 }} />
                </div>
                <div className={s.skeletonGrid}>
                  {Array.from({ length: 4 }).map((_, j) => (
                    <div key={j} className={s.skeletonStat}>
                      <div className="skeleton skeleton-text" style={{ width: 32, height: 10 }} />
                      <div className="skeleton skeleton-text" style={{ width: 40, height: 16 }} />
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        ) : sorted.length === 0 ? (
          <div className="empty-state">
            <div className="empty-icon">
              <Icon name={showStarredOnly ? 'star' : 'search'} size={32} filled={showStarredOnly} />
            </div>
            <h3>{showStarredOnly ? t('dashboard.starredOnly', { defaultValue: 'No starred projects' }) : t('dashboard.noProjects', { defaultValue: 'No project data' })}</h3>
            <p>
              {showStarredOnly
                ? t('dashboard.starMsg', { defaultValue: 'Star a project to follow it.' })
                : t('dashboard.scanMsg', { defaultValue: 'No repos found. Configure scan roots first.' })}
            </p>
            <div className="empty-actions">
              {showStarredOnly ? (
                // Clearing a filter is a secondary, reversible action — a filled
                // primary button overstates it and competes with the real
                // primary actions elsewhere on the page.
                <button className="btn btn-secondary" onClick={() => setShowStarredOnly(false)}>{t('dashboard.viewAllProjects')}</button>
              ) : (
                <>
                  <button className="btn btn-primary" onClick={() => setConfirmScan(true)}>{t('dashboard.startScan')}</button>
                  <Link to="/settings" className="btn btn-secondary">{t('dashboard.configureDirs')}</Link>
                </>
              )}
            </div>
          </div>
        ) : (
          <>
            {starredProjects.length > 0 && (
              <div className={s.section}>
                <div className={s.sectionHeader}>
                  <h2 className={s.sectionTitle}>{t('dashboard.starredSection')}</h2>
                  <span className={s.sectionCount}>{starredProjects.length}</span>
                </div>
                <div className={s.listFlat}>
                  {starredProjects.map(p => (
                    <ProjectCard
                      key={p.id}
                      project={p}
                      date={date}
                      todoCount={todoMap.get(p.id)}
                      noteCount={noteMap.get(p.id)}
                      dailyGoal={isWorkday ? dailyGoal : 0}
                      isWorkday={isWorkday}
                      onToggleStar={handleToggleStar}
                      onRefreshHistory={handleRefreshHistory}
                    />
                  ))}
                </div>
              </div>
            )}

            {unstarredProjects.length > 0 && (
              <div className={s.section}>
                <div className={s.sectionHeader}>
                  <h2 className={s.sectionTitle}>{t('dashboard.otherSection')}</h2>
                  <span className={s.sectionCount}>{unstarredProjects.length}</span>
                </div>
                <div className={`${s.projectGrid} ${s.projectGridMinimal}`}>
                  {unstarredProjects.map(p => (
                    <ProjectCard
                      key={p.id}
                      project={p}
                      date={date}
                      onToggleStar={handleToggleStar}
                    />
                  ))}
                </div>
              </div>
            )}
          </>
        )}
      </div>

      <HandoffComposer
        open={composerOpen}
        onClose={() => setComposerOpen(false)}
        onCreated={() => {
          loadHandoffs()
          pushToast({ kind: 'success', title: t('handoff.saved', { defaultValue: '交接记录已写入' }) })
        }}
      />

      <StatusBar />
    </div>
  )
}

export default Dashboard
