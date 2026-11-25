import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import type { RelatedNote } from '../../api/client'
import s from './RelatedNoteChips.module.css'

interface Props {
  related: RelatedNote[]
  /** How many chips to show. A card has room for three. */
  max?: number
  /** Also print the reason inline, for the note page's reading column. */
  showReason?: boolean
}

/**
 * The reason a note is considered related, phrased in the reader's language.
 *
 * The server ships the EVIDENCE (overlapping terms, same-topic / same-project
 * flags) and this turns it into a sentence — the alternative, a sentence
 * generated server-side, would be in the wrong language for half the users.
 * The terms come first because they are the part a reader can verify.
 */
export function relatedReason(r: RelatedNote, t: TFunction): string {
  const sep = t('common.listSeparator')
  const parts: string[] = []
  if (r.shared && r.shared.length > 0) {
    parts.push(t('project.relatedShared', { terms: r.shared.join(sep) }))
  }
  if (r.same_topic) parts.push(t('project.relatedSameTopic'))
  else if (r.same_project && !r.shared?.length) parts.push(t('project.relatedSameProject'))
  if (parts.length === 0) parts.push(t('project.relatedSimilar'))
  return parts.join(sep)
}

/**
 * The related-note row: which notes this one connects to, and why.
 *
 * Each chip carries its reason as the native tooltip rather than as visible
 * text — on a grid card a second line of justification per chip would cost the
 * preview more than it tells. The note page, where there is room, sets
 * `showReason` and prints it.
 */
export default function RelatedNoteChips({ related, max = 3, showReason = false }: Props) {
  const { t } = useTranslation()
  if (!related || related.length === 0) return null
  const shown = related.slice(0, max)

  return (
    <div className={`${s.row} ${showReason ? s.rowWrapped : ''}`}>
      <span className={s.label}>{t('project.relatedLabel')}</span>
      {shown.map(r => {
        const reason = relatedReason(r, t)
        return (
          // The reason sits NEXT TO the chip on the note page, not inside it:
          // pressed into the chip it shrank the title to "Se…", which is the one
          // thing the chip exists to show.
          <span key={r.id} className={s.item}>
            <Link to={`/note/${r.id}`} className={s.chip} title={reason}>
              <span className={s.arrow} aria-hidden="true">↳</span>
              <span className={s.title}>{r.title}</span>
            </Link>
            {showReason && <span className={s.reason}>{reason}</span>}
          </span>
        )
      })}
    </div>
  )
}
