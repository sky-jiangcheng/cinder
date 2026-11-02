import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useNavigationType, useSearchParams } from 'react-router-dom'
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
import TabStrip from '../components/TabStrip'
import { listRecentHandoffs, type HandoffEntry } from '../api/client'
import { useToast } from '../hooks/useToast'
import { useDashboardData, type SortKey } from '../hooks/useDashboardData'
import s from './Dashboard.module.css'

type DashboardView = 'projects' | 'sessions'

/**
 * The deep-link surface: ?view=projects|sessions is shareable. An unknown or
 * missing value falls back to the projects view — the one that carries the
 * dashboard's payload.
 */
function readView(raw: string | null): DashboardView {
  return raw === 'sessions' ? 'sessions' : 'projects'
}

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

  // ADR-0039 决策 4：滚动区收成「项目 / 会话」两个 tab。
  //
  // 「记忆环」讲的是上次的会话，「项目列表」讲的是今天的产出——两种不相干
  // 的对象平级堆在同一个滚动区里，正是 ADR-0038 要消灭的形状换了个位置
  // 复现。tab 之后一次只看一个，徽标负责回答「该不该点进去」。
  //
  // 「全部 / 关注」那个范围开关**不动**：它切的是取数范围（连 summary 一起
  // 变），不是「看哪一块」——那是 chip 语义，不是 tab 语义。
  const [searchParams, setSearchParams] = useSearchParams()
  const urlView = readView(searchParams.get('view'))
  const [view, setView] = useState<DashboardView>(urlView)
  // 仪表盘的滚动容器是 .scroll（overflow-y:auto 的 div），不是 window。
  const scrollRef = useRef<HTMLDivElement | null>(null)

  // What WE know about the URL: the view we last wrote or adopted. Our own
  // write goes through this ref — react-router applies setSearchParams as a
  // transition, and there is a frame in between where the URL still holds the
  // old value; treating that frame as somebody else's change would throw the
  // user back to the tab they just left.
  const written = useRef<DashboardView>(urlView)
  // Only a history navigation (back / forward) may overwrite the local state;
  // our own PUSH writes must not.
  const navType = useNavigationType()

  useEffect(() => {
    if (navType !== 'POP') return
    if (urlView !== written.current) {
      written.current = urlView
      setView(urlView)
      // Coming back through history lands on the top of that panel too, not on
      // the offset the previous panel was left at.
      scrollRef.current?.scrollTo?.({ top: 0 })
    }
  }, [navType, urlView])

  const switchView = useCallback((next: DashboardView) => {
    if (next === view) return
    setView(next)
    written.current = next
    // PUSH, not REPLACE: the back button has to be able to walk back through a
    // tab switch — with REPLACE it would step over it and land on the page
    // before the tab, which reads as "the tab ate my history".
    // The default view stays out of the URL (the same convention as the
    // knowledge page's ?view=): "/dashboard" says "projects", "?view=sessions"
    // says which one.
    setSearchParams(next === 'projects' ? {} : { view: next }, { replace: false })
    // The new panel starts at the top; staying at the old offset would drop
    // the user into the middle of a panel they never scrolled to.
    scrollRef.current?.scrollTo?.({ top: 0 })
  }, [view, setSearchParams])

  const onTabChange = useCallback((key: string) => {
    if (key === 'projects' || key === 'sessions') switchView(key)
  }, [switchView])

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

      {/* tab 条在滚动区的最上面，固定头部（hero / summary / heatmap / controls）
          不动：它讲的是「今天的状态」，与「看哪一块」无关。 */}
      <div className={s.scroll} ref={scrollRef}>
        <TabStrip
          items={[
            { key: 'projects', label: t('dashboard.tabProjects', { defaultValue: '项目' }), count: sorted.length },
            // 徽标是「有 3 条没看」一眼可见的唯一手段（ADR-0038 决策 5 要的是
            // 首屏可达）：埋进 tab 而不加徽标，就把那个落点改弱了。
            { key: 'sessions', label: t('dashboard.tabSessions', { defaultValue: '会话' }), count: entries?.length ?? 0 },
          ]}
          active={view}
          onChange={onTabChange}
          ariaLabel={t('dashboard.tablistLabel', { defaultValue: '仪表盘视图' })}
          idPrefix="dashboard"
          className={s.tabBar}
        />

        {view === 'projects' ? (
          <div role="tabpanel" id="dashboard-panel-projects" aria-labelledby="dashboard-tab-projects">
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
        ) : (
          <div role="tabpanel" id="dashboard-panel-sessions" aria-labelledby="dashboard-tab-sessions">
            {/* 记忆环搬进「会话」tab（ADR-0039 决策 4）：它讲的是「上次的会话」，
                「项目列表」讲的是「今天的产出」，两种不相干的对象不该平级堆在
                一个滚动区里。放在滚动区顶部，记忆环仍然是仪表盘的第一屏第一块
                ——只要先点一下 tab。首页已是整页对话台，这里是记忆环的唯一一处。 */}
            <section className={s.ring} aria-label={t('handoff.ringTitle', { defaultValue: '记忆环' })}>
              <div className={s.ringHead}>
                <div>
                  <h2>{t('handoff.ringTitle', { defaultValue: '记忆环' })}</h2>
                  <p className={s.ringSub}>{t('handoff.ringSub', { defaultValue: '最近的会话交接记录' })}</p>
                </div>
                {/* 零配置进入会话循环的入口：手写一条交接记录。agent 那一半需要
                    MCP 客户端、模型和「记得调用工具」的客户端——第一次用的人一样
                    都没有，所以手写这条路是唯一能立刻试起来的。
                    按钮只长在记忆环这一侧：它属于记忆环，就跟记忆环待在一起。 */}
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
          </div>
        )}
      </div>

      {/* 弹窗是全局的：它不属于任何一个 tab，所以挂在页面这一层，切 tab 不会
          把它卸载掉。 */}
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
