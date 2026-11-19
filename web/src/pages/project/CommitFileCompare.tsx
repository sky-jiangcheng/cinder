import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../../components/Icon'
import { getCommitFile, type CommitDiffFile, type FileSnapshot } from '../../api/client'
import s from './CommitFileCompare.module.css'

interface Props {
  repoId: number
  sha: string
  file: CommitDiffFile
  onClose: () => void
}

/** One column of the side-by-side comparison; line numbers stay selectable. */
function Snippet({ label, snapshot }: { label: string; snapshot: FileSnapshot | null }) {
  const { t } = useTranslation()
  const lines = snapshot?.content.replace(/\n$/, '').split('\n') ?? []
  return (
    <section className={s.side}>
      <h4 className={s.sideTitle}>{label}</h4>
      {!snapshot?.exists ? (
        <div className={s.missing}>{t('project.fileMissing')}</div>
      ) : (
        <pre className={s.pre}>
          {lines.map((line, i) => (
            <div key={i} className={s.line}>
              <span className={s.lineNo}>{i + 1}</span>
              <code>{line || ' '}</code>
            </div>
          ))}
        </pre>
      )}
    </section>
  )
}

/**
 * Full-file before/after popup for one file of a commit. The patch pane only
 * shows hunks; this reads the two blobs at the commit and its parent so a
 * rename or a move can be compared in full without losing context lines.
 */
export default function CommitFileCompare({ repoId, sha, file, onClose }: Props) {
  const { t } = useTranslation()
  const [before, setBefore] = useState<FileSnapshot | null>(null)
  const [after, setAfter] = useState<FileSnapshot | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    const beforePath = file.old_path || file.path
    Promise.all([
      getCommitFile(repoId, sha, beforePath, true).catch(() => ({ path: beforePath, content: '', exists: false })),
      getCommitFile(repoId, sha, file.path, false).catch(() => ({ path: file.path, content: '', exists: false })),
    ]).then(([oldFile, newFile]) => {
      if (cancelled) return
      setBefore(oldFile)
      setAfter(newFile)
    }).catch((e: unknown) => {
      if (!cancelled) setError(e instanceof Error ? e.message : t('common.failed'))
    })
    return () => { cancelled = true }
  }, [repoId, sha, file.old_path, file.path, t])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className={s.overlay} onClick={onClose} role="presentation">
      <div
        className={s.dialog}
        role="dialog"
        aria-modal="true"
        aria-label={t('project.compareTitle')}
        onClick={e => e.stopPropagation()}
      >
        <header className={s.head}>
          <div className={s.title}>
            <span>{t('project.compareTitle')}</span>
            <code>{file.old_path && file.old_path !== file.path ? `${file.old_path} → ` : ''}{file.path}</code>
          </div>
          <button className="btn btn-secondary btn-sm btn-icon" onClick={onClose} aria-label={t('common.close')}>
            <Icon name="close" size={14} />
          </button>
        </header>
        {error && <div className={s.error}>{error}</div>}
        {!before && !error && <div className={s.missing}>{t('common.loading')}</div>}
        {before && (
          <div className={s.columns}>
            <Snippet label={t('project.before')} snapshot={before} />
            <Snippet label={t('project.after')} snapshot={after} />
          </div>
        )}
      </div>
    </div>
  )
}
