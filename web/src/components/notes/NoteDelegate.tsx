import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { generateWeeklyReport, findDuplicateNotes, generateNoteInsights, type NoteDuplicate, type NoteOrganizeResult } from '../../api/client'
import { describeApiError } from '../../utils/apiError'
import Icon from '../Icon'
import s from './NoteDelegate.module.css'

interface Props {
  /** Scope for the two actions: a project id, or 0 for the whole library. */
  projectId: number
  /** How many notes are in view. */
  total: number
  /** How many of them already carry a summary. */
  summarized: number
  /** Called after a batch produced summaries, so the grid re-reads the cache. */
  onGenerated: () => void
  /**
   * Called with a weekly-report PROPOSAL. Omit it and the action is not
   * rendered at all: a page with nowhere to review the draft (the global
   * knowledge grid has no composer) must not offer a button whose only
   * possible outcome is a lost draft.
   */
  onWeekly?: (proposal: NoteOrganizeResult) => void
}

/**
 * The delegation strip above a note grid: the actions that turn a list into
 * work the AI does.
 *
 * This is the difference between an AI-looking list and an AI-native one. A
 * richer card layout still leaves every task — digest this batch, find the
 * copies — to the reader; two buttons here hand those over, and both report
 * what they actually did (including "nothing left to do" and "12 of 30, press
 * again"), because a delegation that silently no-ops is worse than no button.
 *
 * The duplicate panel lives here rather than on the page because it is the
 * result of one action in this strip: a list of pairs to inspect, not a page.
 */
export default function NoteDelegate({ projectId, total, summarized, onGenerated, onWeekly }: Props) {
  const { t } = useTranslation()
  const [generating, setGenerating] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const [duplicates, setDuplicates] = useState<NoteDuplicate[] | null>(null)
  const [scanning, setScanning] = useState(false)
  const [weeklyOpen, setWeeklyOpen] = useState(false)
  const [weeklyBusy, setWeeklyBusy] = useState(false)

  const summarize = async () => {
    setGenerating(true)
    setStatus('')
    setError('')
    try {
      const res = await generateNoteInsights(projectId, [])
      setStatus(
        res.warning
          || (res.insights.length === 0
            ? t('project.aiSummaryUpToDate')
            : t('project.aiSummaryDone', { count: res.insights.length })),
      )
      if (res.insights.length > 0) onGenerated()
    } catch (e) {
      setError(`${t('project.aiSummaryFailed')}: ${describeApiError(t, e)}`)
    } finally {
      setGenerating(false)
    }
  }

  const findDuplicates = async () => {
    if (duplicates) {
      setDuplicates(null)
      return
    }
    setScanning(true)
    setError('')
    try {
      setDuplicates(await findDuplicateNotes(projectId))
    } catch (e) {
      setError(`${t('project.aiDuplicateFailed')}: ${describeApiError(t, e)}`)
    } finally {
      setScanning(false)
    }
  }

  /**
   * The weekly report is the one action here that produces a NEW note rather
   * than acting on existing ones, so the week has to be chosen: "this week"
   * is still open and will read differently on Friday than on Monday.
   *
   * The proposal goes to the parent, which owns the composer. Refusing when a
   * draft is in progress would live here if this component owned the draft;
   * it does not, and a button that silently eats someone's half-written note
   * is worse than one that says "finish that first".
   */
  const writeWeekly = async (weekOffset: number) => {
    setWeeklyBusy(true)
    setStatus('')
    setError('')
    try {
      const proposal = await generateWeeklyReport(projectId, weekOffset)
      onWeekly?.(proposal)
      setWeeklyOpen(false)
      setStatus(proposal.warning || t('project.weeklyDone'))
    } catch (e) {
      setError(`${t('project.weeklyFailed')}: ${describeApiError(t, e)}`)
    } finally {
      setWeeklyBusy(false)
    }
  }

  return (
    <>
      <div className={s.bar}>
        <span className={s.label}>
          <span className={s.spark} aria-hidden="true">✦</span>
          {t('project.aiDelegate')}
        </span>
        <button
          type="button"
          className={s.btn}
          onClick={summarize}
          disabled={generating}
          title={t('project.aiSummaryHint')}
        >
          {generating
            ? t('project.aiGenerating')
            : t('project.aiSummaryAction', { done: summarized, total })}
        </button>
        <button
          type="button"
          className={s.btn}
          onClick={findDuplicates}
          disabled={scanning}
          title={t('project.aiDuplicateHint')}
        >
          {scanning ? t('project.aiScanning') : t('project.aiDuplicateAction')}
        </button>
        {onWeekly && (
          <button
            type="button"
            className={s.btn}
            onClick={() => setWeeklyOpen(open => !open)}
            disabled={weeklyBusy}
            aria-expanded={weeklyOpen}
            title={t('project.weeklyHint')}
          >
            {weeklyBusy ? t('project.weeklyDrafting') : t('project.weeklyAction')}
          </button>
        )}
        {onWeekly && weeklyOpen && !weeklyBusy && (
          <span className={s.choices}>
            <button type="button" className={s.choice} onClick={() => writeWeekly(0)}>
              {t('project.weeklyThisWeek')}
            </button>
            <button type="button" className={s.choice} onClick={() => writeWeekly(-1)}>
              {t('project.weeklyLastWeek')}
            </button>
          </span>
        )}
        {status && <span className={s.note}>{status}</span>}
        {error && <span className={s.error}>{error}</span>}
      </div>

      {duplicates && (
        <div className={s.panel}>
          <div className={s.panelHead}>
            <strong>{t('project.aiDuplicateTitle')}</strong>
            <span className={s.panelNote}>
              {duplicates.length === 0
                ? t('project.aiDuplicateEmpty')
                : t('project.aiDuplicateCount', { count: duplicates.length })}
            </span>
            <button type="button" className={s.close} onClick={() => setDuplicates(null)} aria-label={t('common.close')}>
              <Icon name="close" size={13} />
            </button>
          </div>
          {duplicates.map(d => (
            <div key={`${d.a.id}-${d.b.id}`} className={s.row}>
              <Link to={`/note/${d.a.id}`} className={s.title}>{d.a.title}</Link>
              <span className={s.vs} aria-hidden="true">≈</span>
              <Link to={`/note/${d.b.id}`} className={s.title}>{d.b.title}</Link>
              <span className={s.score}>{Math.round(d.score * 100)}%</span>
              {d.shared && d.shared.length > 0 && (
                <span className={s.shared}>{d.shared.join(t('common.listSeparator'))}</span>
              )}
            </div>
          ))}
        </div>
      )}
    </>
  )
}
