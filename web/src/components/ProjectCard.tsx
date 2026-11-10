import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import Icon from './Icon'
import type { Project } from '../api/client'
// Scoped styles for everything ProjectCard owns. Only `.card-star` (shared with
// ProjectSearchDropdown) and the `.badge` base class (shared with NoteSection)
// stay global — the badge variants, the refresh button and the spin animation
// live in ProjectCard.module.css; see its header.
import s from './ProjectCard.module.css'

interface Props {
  project: Project
  date?: string
  todoCount?: number
  noteCount?: number
  dailyGoal?: number
  isWorkday?: boolean
  onToggleStar?: (id: number) => void
  onRefreshHistory?: (id: number) => Promise<void>
}

function ProjectCard({ project, date, todoCount, noteCount, dailyGoal = 0, isWorkday = true, onToggleStar, onRefreshHistory }: Props) {
  const { t } = useTranslation()
  const [refreshing, setRefreshing] = useState(false)
  const myAdded = project.my_added || 0
  const myDeleted = project.my_deleted || 0
  const to = date ? `/project/${project.id}?date=${date}` : `/project/${project.id}`

  const reachedGoal = isWorkday && myAdded > 0 && myAdded >= dailyGoal

  // Landing buckets (ADR-0023) are flagged so unfiled knowledge is obvious on the
  // dashboard: an import placeholder (a repo an import landed notes on) or the
  // default "Uncategorized" bucket. Scanner projects get no badge. root_path is
  // surfaced as the tooltip since the flat row is too dense to show the full path.
  const kindBadge =
    project.project_kind === 'import_placeholder'
      ? { label: t('project.kindPlaceholder', { defaultValue: '未归类仓库' }), cls: s.badgePlaceholder }
      : project.project_kind === 'default'
        ? { label: t('project.kindDefault', { defaultValue: '未分类' }), cls: s.badgeDefault }
        : null

  const handleStarClick = (e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    onToggleStar?.(project.id)
  }

  const handleRefreshClick = async (e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    if (refreshing || !onRefreshHistory) return
    setRefreshing(true)
    try {
      await onRefreshHistory(project.id)
    } finally {
      setRefreshing(false)
    }
  }

  if (!project.is_starred) {
    return (
      <div className={`${s.card} ${s.minimal}`}>
        <button
          className="card-star"
          onClick={handleStarClick}
          title={t('project.star')}
          aria-label={t('project.star')}
        >
          <Icon name="star" size={16} />
        </button>
        {/* The name is the link; the star stays a sibling of the anchor. */}
        <Link to={to} className={s.name} title={project.root_path}>{project.name}</Link>
        {kindBadge && <span className={`badge ${kindBadge.cls}`}>{kindBadge.label}</span>}
      </div>
    )
  }

  // The star and refresh buttons are siblings of the card Link (never nested
  // inside the anchor) so the markup stays valid and keyboard-friendly.
  // Starred rows are deliberately FLAT: a single line — name, today's number,
  // my +/-, repo count, badges — instead of the old tall hero card. One
  // starred repo used to render a full-width slab that read like a dropdown.
  return (
    <div className={s.shell}>
      <button
        className="card-star starred"
        onClick={handleStarClick}
        title={t('project.unstar')}
        aria-label={t('project.unstar')}
      >
        <Icon name="star" size={16} filled />
      </button>
      <Link to={to} className={`${s.card} ${s.flat} ${reachedGoal ? s.goalReached : ''}`}>
        <span className={s.flatName} title={project.root_path}>{project.name}</span>
        <span className={s.flatStats}>
          <span className={s.pair}>
            <span className={s.label}>{t('project.todayAdded')}</span>
            <span className={`${s.num} ${myAdded > 0 ? s.numSuccess : s.numMuted}`}>{myAdded > 0 ? `+${myAdded}` : '0'}</span>
          </span>
          <span className={s.pair}>
            <span className={s.label}>{t('project.added')}</span>
            <span className={`${s.num} ${myAdded > 0 ? s.numSuccess : s.numMuted}`}>{myAdded > 0 ? `+${myAdded}` : '0'}</span>
          </span>
          <span className={s.pair}>
            <span className={s.label}>{t('project.deleted')}</span>
            <span className={`${s.num} ${myDeleted > 0 ? s.numDanger : s.numMuted}`}>{myDeleted > 0 ? `-${myDeleted}` : '0'}</span>
          </span>
          <span className={s.pair}>
            <span className={s.label}>{t('project.repo')}</span>
            <span className={s.num}>{project.repo_count || 0}</span>
          </span>
        </span>
        <span className={s.badges}>
          {kindBadge && <span className={`badge ${kindBadge.cls}`} title={project.root_path}>{kindBadge.label}</span>}
          {reachedGoal && <span className={`badge ${s.badgeGoal}`} title={t('project.goalReached', { defaultValue: '已达成今日目标' })}>{t('project.goalBadge')}</span>}
          {noteCount !== undefined && noteCount > 0 && (
            <span className={`badge ${s.badgeNote}`} title={t('project.noteBadgeTitle')}>{noteCount}</span>
          )}
          {todoCount !== undefined && todoCount > 0 && (
            <span className={`badge ${s.badgeTodo}`}>{todoCount}</span>
          )}
          {project.below_standard && <span className={`badge ${s.badgeWarning}`}>{t('project.belowBadge')}</span>}
        </span>
      </Link>
      <button
        className={s.cardRefreshBtn}
        onClick={handleRefreshClick}
        disabled={refreshing}
        title={refreshing ? t('project.refreshingHistory', { defaultValue: 'Refreshing…' }) : t('project.refreshHistory', { defaultValue: 'Refresh history' })}
        aria-label={refreshing ? t('project.refreshingHistory', { defaultValue: 'Refreshing…' }) : t('project.refreshHistory', { defaultValue: 'Refresh history' })}
      >
        {refreshing ? (
          <Icon name="refresh-partial" size={14} className={s.spin} />
        ) : (
          <Icon name="refresh" size={14} />
        )}
      </button>
    </div>
  )
}

export default ProjectCard
