import { useTranslation } from 'react-i18next'
import Icon from '../../components/Icon'
import TrendChart, { type TrendDataset } from '../../components/TrendChart'
import ScopeToggle from '../../components/ScopeToggle'
import type { ProjectOverview } from '../../api/client'
import { renderMarkdown } from '../../utils/markdown'
import s from './ProjectOverviewSection.module.css'

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

function formatCount(value: number): string {
  return value.toLocaleString()
}

export default function ProjectOverviewSection({ overview, trend, scope, onScopeChange, totals }: Props) {
  const { t } = useTranslation()
  const activity = overview.activity
  const languages = overview.languages ?? []
  const contributors = overview.top_contributors ?? []
  const techStack = overview.tech_stack ?? []
  const dependencies = overview.dependencies ?? []
  const hasTrend = !!trend && trend.labels.length > 0
  const hasContent = !!activity || hasTrend || !!totals || !!overview.readme_excerpt ||
    techStack.length > 0 || languages.length > 0 || dependencies.length > 0 || contributors.length > 0
  const languageTotal = languages.reduce((sum, language) => sum + language.count, 0)

  if (!hasContent) {
    return (
      <div className="empty-state">
        <div className="empty-icon"><Icon name="grid" size={34} /></div>
        <h3>{t('project.overviewEmpty')}</h3>
        <p>{t('project.overviewEmptyHint')}</p>
      </div>
    )
  }

  return (
    <div className={s.section}>
      {(activity || hasTrend || totals) && (
        <section className={s.activityCard}>
          <div className={s.activityHead}>
            <div>
              <div className={s.titleRow}>
                <h2>{t('project.activityTitle')}</h2>
                <span className={`${s.cacheHint} ${overview.cached ? s.cached : s.live}`}>
                  <span className={s.statusDot} />
                  {overview.cached ? t('project.fromCache') : t('project.realtimeMining')}
                </span>
              </div>
              {activity?.last_commit_date && (
                <p className={s.lastCommit}>{t('project.lastCommit')} · {activity.last_commit_date}</p>
              )}
            </div>
            {scope && onScopeChange && <ScopeToggle scope={scope} onChange={onScopeChange} />}
          </div>

          {activity && (
            <div className={s.metricGrid}>
              <div className={s.metric}>
                <span className={s.metricLabel}>{t('project.totalCommits')}</span>
                <strong className={s.metricValue}>{formatCount(activity.total_commits)}</strong>
              </div>
              <div className={s.metric}>
                <span className={s.metricLabel}>{t('project.last30d')}</span>
                <strong className={s.metricValue}>{formatCount(activity.commit_rate_30d)}</strong>
              </div>
              <div className={s.metric}>
                <span className={s.metricLabel}>{t('project.activeDays90')}</span>
                <strong className={s.metricValue}>{formatCount(activity.active_days)}</strong>
              </div>
              <div className={s.metric}>
                <span className={s.metricLabel}>{t('project.activeMonths')}</span>
                <strong className={s.metricValue}>{formatCount(activity.active_months)}</strong>
              </div>
            </div>
          )}

          {hasTrend ? (
            <div className={s.trend}>
              <div className={s.trendHead}>
                <h3>{t('project.trendTitle')}</h3>
                {totals && (
                  <div className={s.rangeSummary} aria-label={t('project.selectedRange')}>
                    <span>{t('project.fileChanges')} <strong>{formatCount(totals.files)}</strong></span>
                    <span className={s.added}>{t('project.added')} <strong>+{formatCount(totals.added)}</strong></span>
                    <span className={s.deleted}>{t('project.deleted')} <strong>−{formatCount(totals.deleted)}</strong></span>
                  </div>
                )}
              </div>
              <TrendChart labels={trend!.labels} datasets={trend!.datasets} height={190} />
            </div>
          ) : (
            <p className={s.noData}>{t('project.noDataInRange')}</p>
          )}
        </section>
      )}

      <div className={s.contentGrid}>
        {(techStack.length > 0 || languages.length > 0) && (
          <section className={`${s.panel} ${s.stackPanel}`}>
            {techStack.length > 0 && (
              <div className={s.panelBlock}>
                <h3 className={s.panelTitle}>{t('project.techStack')}</h3>
                <div className={s.techList}>
                  {techStack.map(tech => (
                    <span key={tech.name} className={`${s.techChip} ${TECH_CLASS[tech.category] ?? ''}`}>
                      {tech.name}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {languages.length > 0 && (
              <div className={s.panelBlock}>
                <div className={s.panelTitleRow}>
                  <h3 className={s.panelTitle}>{t('project.languages')}</h3>
                  <span className={s.panelMeta}>{languages.length} {t('project.languagesUnit')}</span>
                </div>
                <div className={s.langList}>
                  {languages.slice(0, 6).map(language => {
                    const percent = languageTotal > 0 ? Math.round((language.count / languageTotal) * 100) : 0
                    return (
                      <div key={language.language} className={s.langRow}>
                        <span className={s.langName}>{language.language}</span>
                        <div className={s.langBar} aria-hidden="true">
                          <div className={s.langFill} style={{ width: `${percent}%` }} />
                        </div>
                        <span className={s.langCount}>{formatCount(language.count)}</span>
                        <span className={s.langPercent}>{percent}%</span>
                      </div>
                    )
                  })}
                </div>
                {languages.length > 6 && (
                  <p className={s.moreHint}>{t('project.moreCount', { count: languages.length - 6 })}</p>
                )}
              </div>
            )}
          </section>
        )}

        {contributors.length > 0 && (
          <section className={`${s.panel} ${s.contributorsPanel}`}>
            <div className={s.panelTitleRow}>
              <h3 className={s.panelTitle}>{t('project.topContributors')}</h3>
              <span className={s.panelMeta}>{t('project.commitsUnit')}</span>
            </div>
            <div className={s.contribList}>
              {contributors.slice(0, 5).map((contributor, index) => (
                <div key={`${contributor.author}-${index}`} className={s.contribItem}>
                  <span className={s.contribRank}>{String(index + 1).padStart(2, '0')}</span>
                  <span className={s.contribName} title={contributor.author}>{contributor.author}</span>
                  <strong className={s.contribCount}>{formatCount(contributor.count)}</strong>
                </div>
              ))}
            </div>
            {contributors.length > 5 && (
              <p className={s.moreHint}>{t('project.moreCount', { count: contributors.length - 5 })}</p>
            )}
          </section>
        )}
      </div>

      <div className={s.lowerGrid}>
        {overview.readme_excerpt && (
          <details className={`${s.panel} ${s.disclosure}`}>
            <summary className={s.disclosureSummary}>
              <span className={s.disclosureTitle}><Icon name="file-text" size={15} />{t('project.readme')}</span>
              <Icon name="arrow-down" size={15} className={s.disclosureChevron} />
            </summary>
            <div className={`${s.readme} markdown-body`} dangerouslySetInnerHTML={{ __html: renderMarkdown(overview.readme_excerpt) }} />
          </details>
        )}

        {dependencies.length > 0 && (
          <details className={`${s.panel} ${s.disclosure}`}>
            <summary className={s.disclosureSummary}>
              <span className={s.disclosureTitle}><Icon name="layers" size={15} />{t('project.dependencies')}</span>
              <span className={s.depCount}>{dependencies.length}</span>
              <Icon name="arrow-down" size={15} className={s.disclosureChevron} />
            </summary>
            <div className={s.depsList}>
              {dependencies.slice(0, 20).map(dependency => (
                <span key={`${dependency.source}-${dependency.name}`} className={s.depChip}>
                  <span className={s.depName}>{dependency.name}</span>
                  {dependency.version && <span className={s.depVersion}>{dependency.version}</span>}
                  <span className={s.depSource}>{dependency.source}</span>
                </span>
              ))}
            </div>
            {dependencies.length > 20 && (
              <p className={s.moreHint}>{t('project.moreCount', { count: dependencies.length - 20 })}</p>
            )}
          </details>
        )}
      </div>
    </div>
  )
}
