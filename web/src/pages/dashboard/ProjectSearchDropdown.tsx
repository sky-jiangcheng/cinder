import { useTranslation } from 'react-i18next'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { searchAll, searchProjects, type Project, type SearchHit } from '../../api/client'
import { useDebouncedCallback } from '../../hooks/useDebouncedCallback'
import DOMPurify from 'dompurify'
import Icon from '../../components/Icon'
import { hitHref } from '../home/HomeKnowledgeAssistant'
import s from './ProjectSearchDropdown.module.css'

interface Props {
  /** Toggles the star server-side and resolves to the new starred state. */
  onToggleStar: (projectId: number) => Promise<boolean>
}

/**
 * The dashboard omnibox: debounced federated search across repositories and
 * notes/todos with a click-outside dropdown.
 */
export default function ProjectSearchDropdown({ onToggleStar }: Props) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [noteHits, setNoteHits] = useState<SearchHit[] | null>(null)
  const [projectHits, setProjectHits] = useState<Project[] | null>(null)
  const [searching, setSearching] = useState(false)
  const [searchError, setSearchError] = useState(false)
  const boxRef = useRef<HTMLDivElement>(null)
  // Sequence guard: the debounce serialises the requests, but responses can
  // still arrive out of order — without the guard a slow older response
  // overwrote the newer results and prematurely cleared the "searching" flag.
  const searchSeqRef = useRef(0)

  const runSearch = useDebouncedCallback((q: string) => {
    if (!q.trim()) {
      setNoteHits(null)
      setProjectHits(null)
      setSearching(false)
      setSearchError(false)
      return
    }
    const seq = ++searchSeqRef.current
    let failed = false
    Promise.all([
      searchAll(q).catch((e: unknown) => { console.error('[ProjectSearchDropdown] searchAll failed:', e); failed = true; return [] as SearchHit[] }),
      searchProjects(q).catch((e: unknown) => { console.error('[ProjectSearchDropdown] searchProjects failed:', e); failed = true; return [] as Project[] }),
    ]).then(([hits, projects]) => {
      if (searchSeqRef.current !== seq) return
      setNoteHits(hits)
      setProjectHits(projects)
      setSearching(false)
      setSearchError(failed)
    })
  }, 300)

  const onChange = useCallback((q: string) => {
    setQuery(q)
    if (q.trim()) setSearching(true)
    runSearch(q)
  }, [runSearch])

  const reset = useCallback(() => {
    setQuery('')
    setNoteHits(null)
    setProjectHits(null)
    setSearching(false)
  }, [])

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) reset()
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [reset])

  const handleToggleStar = async (projectId: number) => {
    try {
      const starred = await onToggleStar(projectId)
      setProjectHits(prev => prev?.map(p => p.id === projectId ? { ...p, is_starred: starred } : p) ?? null)
    } catch { /* parent surfaces errors */ }
  }

  return (
    <div className={s.searchBox} ref={boxRef} role="search" aria-label={t('dashboard.searchAria')}>
      <input
        type="text"
        value={query}
        onChange={e => onChange(e.target.value)}
        placeholder={t('dashboard.searchPlaceholder')}
        aria-label={t('dashboard.searchAria')}
        className={`form-input search-input ${s.searchInput}`}
      />
      {noteHits !== null && (
        <div className={s.searchDropdown}>
          {searchError && <div className={s.searchEmpty}>{t('common.loadFailed')}</div>}
          {searching ? (
            <div className={s.searchLoading}>{t('dashboard.searching')}</div>
          ) : noteHits.length === 0 && (!projectHits || projectHits.length === 0) ? (
            <div className={s.searchEmpty}>{t('dashboard.noMatches')}</div>
          ) : (
            <>
              {projectHits && projectHits.length > 0 && (
                <div className={s.searchGroup}>
                  <div className={s.searchGroupHeader}>{t('dashboard.groupRepos')}</div>
                  {projectHits.map(p => (
                    <div key={`project-${p.id}`} className={`${s.searchResultItem} ${s.searchResultProjectItem}`}>
                      <button
                        className={`card-star ${p.is_starred ? 'starred' : ''}`}
                        onClick={(e) => { e.preventDefault(); e.stopPropagation(); void handleToggleStar(p.id) }}
                        title={p.is_starred ? t('project.unstar') : t('project.star')}
                      >
                        <Icon name="star" size={14} filled={p.is_starred} />
                      </button>
                      <Link to={`/project/${p.id}`} className={s.searchProjectName}>
                        {p.name}
                      </Link>
                    </div>
                  ))}
                </div>
              )}
              {noteHits.length > 0 && (
                <div className={s.searchGroup}>
                  <div className={s.searchGroupHeader}>{t('dashboard.groupNotesTodos')}</div>
                  {noteHits.map(h => (
                    <Link key={`${h.type}-${h.id}`} to={hitHref(h)} className={s.searchResultItem}>
                      <div className={s.searchResultHeader}>
                        <span className={`${s.hitTypeMini} hit-type-${h.type}`}>{h.type === 'note' ? t('dashboard.noteType') : t('summaryBar.todos')}</span>
                        <span className={s.searchResultProject}>{h.project_name}</span>
                      </div>
                      <div className={s.searchResultTitle}>{h.title}</div>
                      <div className={s.searchResultPreview} dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(h.snippet) }} />
                    </Link>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}
