import { useTranslation } from 'react-i18next'
import TrendChart, { type TrendDataset } from '../../components/TrendChart'
import ScopeToggle from '../../components/ScopeToggle'
import type { ProjectOverview } from '../../api/client'
import { renderMarkdown } from '../../utils/markdown'
import s from './ProjectOverviewSection.module.css'

// 预留钩子类：`overview-empty` —— 目前全站没有对应样式定义（P35 复核确认），
// 保留在 markup 里作为将来挂样式的锚点，删留都不影响行为。

// tech.category 是自由字符串，只有 framework / tool 有专属配色，其余落回 techChip 基态。
const TECH_CLASS: Record<string, string | undefined> = {
  framework: s.techFramework,
  tool: s.techTool,
}

export type TrendScope = 'week' | 'month' | 'all'

export interface OverviewTotals {
  added: number
  deleted: number
  files: number
  active: number
}

interface Props {
  overview: ProjectOverview
  trend?: { labels: string[]; datasets: TrendDataset[] }
  scope?: TrendScope
  onScopeChange?: (scope: TrendScope) => void
  totals?: OverviewTotals
}

/**
 * The project's single summary page. The trend chart used to live in the
 * Commits tab with four more metrics and almost no context; it is folded into
 * the overview here so "how active is this project" is answered in one place,
 * next to the tech stack, languages and contributors that explain the shape.
 */
export default function ProjectOverviewSection({ overview, trend, scope, onScopeChange, totals }: Props) {
  const { t } = useTranslation()

  const activity = overview.activity
  const hasTrend = !!trend && trend.labels.length > 0
  const hasContent =
    overview.readme_excerpt ||
    (overview.tech_stack?.length ?? 0) > 0 ||
    (overview.dependencies?.length ?? 0) > 0 ||
    (overview.top_contributors?.length ?? 0) > 0 ||
    hasTrend

  if (!hasContent) {
    return (
      <div className="detail-section overview-empty">
        <div className="section-header">
          <h2>{t('project.overview')}</h2>
          <span className={s.cacheHint}>{overview.cached ? t('project.fromCache') : t('project.realtimeMining')}</span>
        </div>
        <p className="empty-hint">{t('project.overviewEmptyFull', { main: t('project.overviewEmpty'), hint: t('project.overviewEmptyHint') })}</p>
      </div>
    )
  }

  return (
    <div className={`detail-section ${s.section}`}>
      <div className="section-header">
        <h2>{t('project.overview')}</h2>
        <span className={s.cacheHint}>{overview.cached ? t('project.fromCache') : t('project.realtimeMining')}</span>
      </div>

      {(activity || hasTrend) && (
        <div className={s.activity}>
          <div className={s.activityHead}>
            <h3 className={s.subTitle}>{t('project.activityTitle')}</h3>
            {scope && onScopeChange && <ScopeToggle scope={scope} onChange={onScopeChange} />}
          </div>
          {activity && (
            <div className={s.metrics}>
              <span className={s.metric}><span className={s.metricLabel}>{t('project.totalCommits')}</span><span className={s.metricValue}>{activity.total_commits}</span></span>
              <span className={s.metric}><span className={s.metricLabel}>{t('project.last30d')}</span><span className={s.metricValue}>{activity.commit_rate_30d}</span></span>
              <span className={s.metric}><span className={s.metricLabel}>{t('project.activeDays90')}</span><span className={s.metricValue}>{activity.active_days}</span></span>
              <span className={s.metric}><span className={s.metricLabel}>{t('project.activeMonths')}</span><span className={s.metricValue}>{activity.active_months}</span></span>
              {totals && (
                <>
                  <span className={s.metric}><span className={s.metricLabel}>{t('project.fileChanges')}</span><span className={s.metricValue}>{totals.files}</span></span>
                  <span className={s.metric}><span className={s.metricLabel}>{t('project.added')}</span><span className={`${s.metricValue} green`}>+{totals.added}</span></span>
                  <span className={s.metric}><span className={s.metricLabel}>{t('project.deleted')}</span><span className={`${s.metricValue} red`}>-{totals.deleted}</span></span>
                </>
              )}
              {activity.last_commit_date && (
                <span className={s.metric}><span className={s.metricLabel}>{t('project.lastCommit')}</span><span className={s.metricValue}>{activity.last_commit_date}</span></span>
              )}
            </div>
          )}
          {hasTrend ? (
            <TrendChart labels={trend!.labels} datasets={trend!.datasets} height={170} />
          ) : (
            <p className="empty-hint">{t('project.noDataInRange')}</p>
          )}
        </div>
      )}

      {(overview.tech_stack?.length ?? 0) > 0 && (
        <div className={s.tech}>
          {overview.tech_stack!.map(tech => (
            <span key={tech.name} className={`${s.techChip} ${TECH_CLASS[tech.category] ?? ''}`}>{tech.name}</span>
          ))}
        </div>
      )}

      {/* Languages and contributors are both short ranked lists, so they share
          one row: stacked, each one left a 600px hole at the right edge of a
          1200px card. `auto-fit` keeps a lone list from looking half-finished
          when the other one has no data. */}
      {((overview.languages?.length ?? 0) > 0 || (overview.top_contributors?.length ?? 0) > 0) && (
        <div className={s.split}>
          {(overview.languages?.length ?? 0) > 0 && (
            <div className={s.langs}>
              {overview.languages!.map(l => {
                const max = overview.languages![0]?.count || 1
                return (
                  <div key={l.language} className={s.langRow}>
                    <span className={s.langName}>{l.language}</span>
                    <div className={s.langBar}><div className={s.langFill} style={{ width: `${(l.count / max) * 100}%` }} /></div>
                    <span className={s.langCount}>{l.count}</span>
                  </div>
                )
              })}
            </div>
          )}

          {(overview.top_contributors?.length ?? 0) > 0 && (
            <div className={s.contributors}>
              <h4 className={s.subTitle}>{t('project.topContributors')}</h4>
              <div className={s.contribList}>
                {overview.top_contributors!.map((c, i) => (
                  <div key={i} className={s.contribItem}>
                    <span className={s.contribRank}>#{i + 1}</span>
                    <span className={s.contribName}>{c.author}</span>
                    <span className={s.contribCount}>{c.count} {t('project.commitsUnit')}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {overview.readme_excerpt && (
        <div className={`${s.readme} markdown-body`} dangerouslySetInnerHTML={{ __html: renderMarkdown(overview.readme_excerpt) }} />
      )}

      {(overview.dependencies?.length ?? 0) > 0 && (
        <div className={s.dependencies}>
          <h4 className={s.subTitle}>{t('project.dependencies')}</h4>
          <div className={s.depsList}>
            {overview.dependencies!.slice(0, 20).map(d => (
              <span key={d.name} className={s.depChip}>
                <span className={s.depName}>{d.name}</span>
                <span className={s.depVersion}>{d.version}</span>
                <span className={s.depSource}>{d.source}</span>
              </span>
            ))}
          </div>
        </div>
      )}

    </div>
  )
}
