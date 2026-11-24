import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import type { NoteWithProject } from '../../api/client'
import { renderMarkdown, stripMarkdown, parseTags } from '../../utils/markdown'
import Icon from '../../components/Icon'
import s from './KnowledgeCard.module.css'

interface Props {
  note: NoteWithProject
  exporting: boolean
  onPin: (id: number, pinned: boolean) => void
  onExport: (id: number) => void
  onSelectTag: (tag: string) => void
}

// note.kind → scoped badge modifier. Unknown kinds fall back to the base
// .badge style (mirrors the old kind-knowledge/idea/log variants).
const badgeByKind: Record<string, string> = {
  knowledge: s.badgeKnowledge,
  idea: s.badgeIdea,
  log: s.badgeLog,
}

/** One card in the knowledge-hub note grid. */
export default function KnowledgeCard({ note, exporting, onPin, onExport, onSelectTag }: Props) {
  const { t } = useTranslation()
  const tags = parseTags(note.tags)
  const kindLabel = note.kind === 'knowledge'
    ? t('project.kinds.knowledge')
    : note.kind === 'idea'
      ? t('project.kinds.idea')
      : note.kind === 'log'
        ? t('project.kinds.log')
        : t('project.noteWord')

  return (
    <div className={`${s.card} ${note.pinned ? s.pinned : ''}`}>
      <div className={s.head}>
        <span className={`${s.badge} ${badgeByKind[note.kind] ?? ''}`}>{kindLabel}</span>
        <button
          className={`pin-btn ${note.pinned ? 'pinned' : ''}`}
          onClick={() => onPin(note.id, note.pinned)}
          title={note.pinned ? t('project.unpinned') : t('project.pinned')}
        >
          <Icon name="pin" size={15} />
        </button>
      </div>
      {/* The card opens the note itself (ADR-0078). It used to hop through
          /project/<pid>?noteId=, which both buried the note inside a project
          page and made the link depend on where the note lived at click time. */}
      <Link to={`/note/${note.id}`} className={s.body}>
        <div className={s.title}>{note.title || stripMarkdown(note.content, 40)}</div>
        {/* The subject the note is filed under (schema v27). On this page the
            project is shown next to it, so the pair reads as
            "project / subject" — the same nesting the note list has. */}
        {note.topic_name && (
          <div className={s.topicRow}>
            <span className={s.topic}>{note.topic_name}</span>
          </div>
        )}
        <div className={`${s.snippet} markdown-body`} dangerouslySetInnerHTML={{ __html: renderMarkdown(stripMarkdown(note.content, 120)) }} />
        <div className={s.foot}>
          <span className={s.projectName}>{note.project_name}</span>
          <span className={s.time}>{note.updated_at.slice(0, 10)}</span>
        </div>
      </Link>
      {tags.length > 0 && (
        <div className={s.tags}>
          {tags.map(tag => <span key={tag} className={s.tag} onClick={() => onSelectTag(tag)}>#{tag}</span>)}
        </div>
      )}
      <button
        className="btn btn-secondary btn-sm"
        style={{ marginTop: 8, alignSelf: 'flex-start' }}
        onClick={() => onExport(note.id)}
        disabled={exporting}
        title={t('knowledge.exportMd')}
      >
        {exporting ? t('knowledge.copying') : t('knowledge.exportMd')}
      </button>
    </div>
  )
}
