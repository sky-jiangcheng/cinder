import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { getProjectDetail, getProjectOverview, updateProjectLevel, previewProjectMergeUp } from '../api/client'
import type { MergePreview, ProjectDetail, ProjectOverview } from '../api/client'
import type { TrendDataset } from '../components/TrendChart'
import { describeApiError } from '../utils/apiError'

/** Aggregated daily stat for trend chart. */
interface DailyStat {
  added: number
  deleted: number
  files: number
  commits: number
}

/** A ?date= deep link names the day the user came from; pick a default window
 *  that actually contains it instead of always the last 7 days. */
function defaultScopeFromUrl(): 'week' | 'month' | 'all' {
  // BrowserRouter keeps the query in search; HashRouter (the desktop build)
  // keeps it inside the hash after the path, so read both.
  const hash = window.location.hash
  const hashQuery = hash.includes('?') ? hash.slice(hash.indexOf('?')) : ''
  const date = new URLSearchParams(window.location.search).get('date')
    || new URLSearchParams(hashQuery).get('date') || ''
  if (!date) return 'week'
  const ts = new Date(date.slice(0, 10) + 'T00:00:00').getTime()
  if (isNaN(ts)) return 'week'
  const ageDays = Math.floor((Date.now() - ts) / 86_400_000)
  if (ageDays > 30) return 'all'
  if (ageDays > 7) return 'month'
  return 'week'
}

export function useProjectDetail(id: string | undefined) {
  const { t } = useTranslation()

  const [project, setProject] = useState<ProjectDetail | null>(null)
  const [overview, setOverview] = useState<ProjectOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [scope, setScope] = useState<'week' | 'month' | 'all'>('week')
  // Widening the window is a one-shot correction, not a rule: without this
  // guard a user who deliberately parks on an empty week gets yanked to
  // "all" on every reload.
  const autoWidened = useRef(false)

  const load = useCallback(() => {
    if (!id) return
    // Sequence guard: /project/:id reuses this component instance across
    // navigation, so a slow response for the previous project id would
    // otherwise overwrite the new project's page with the old project's data.
    const seq = ++loadSeqRef.current
    const current = () => loadSeqRef.current === seq
    setLoading(true)
    setError('')
    // A new project gets a fresh widening budget and the default window;
    // carrying either over would show project B in project A's scope.
    autoWidened.current = false
    setScope(defaultScopeFromUrl())
    getProjectDetail(Number(id))
      .then(p => {
        if (!current()) return
        setProject(p)
        // A project whose last commit predates the default 7-day window
        // opened onto two empty charts, which reads as "broken" rather than
        // "no recent activity". Step the window out until the data is in view
        // — once per page load, so an intentional empty-week choice sticks.
        if (!autoWidened.current) {
          const days = (p.repos || []).flatMap(r => (r.stats || []).map(s => s.stat_date))
          const newest = days.reduce<string>((acc, d) => (d > acc ? d : acc), '')
          if (newest) {
            const ageDays = Math.floor(
              (Date.now() - new Date(newest.slice(0, 10) + 'T00:00:00').getTime()) / 86_400_000,
            )
            if (ageDays > 7) {
              autoWidened.current = true
              setScope(ageDays > 30 ? 'all' : 'month')
            }
          }
        }
        // The overview is nested inside detail's chain: it needs the same
        // guard, or a late overview could still land after the next load.
        getProjectOverview(Number(id))
          .then(ov => { if (current()) setOverview(ov) })
          .catch((e: unknown) => {
            // Overview is optional decoration: keep the page state intact,
            // but never swallow the failure silently.
            console.error('[useProjectDetail] getProjectOverview failed', e)
          })
      })
      .catch((e) => { if (current()) setError(e instanceof Error ? e.message : t('common.failed')) })
      .finally(() => { if (current()) setLoading(false) })
  }, [id, t])

  // Keeps the token for the most recent load; anything older is discarded.
  const loadSeqRef = useRef(0)

  // Initial + on-`id`-change data load. Synchronous setState (loading/error
  // reset) is intentional here; matches the established set-state-in-effect
  // disable pattern used across the codebase.
  useEffect(() => { load() }, [load]) // eslint-disable-line react-hooks/set-state-in-effect

  // Returns the outcome so the page can flash visible feedback — the old
  // version only setError()ed, and the error banner lives outside the normal
  // render path, so a failed merge/split looked exactly like a dead button.
  // `confirmed` is required by the backend for "up" (a merge deletes the absorbed
  // siblings); "down" ignores it.
  const handleLevelChange = async (
    direction: 'up' | 'down',
    confirmed: boolean,
  ): Promise<{ ok: boolean; error?: string; raw?: string }> => {
    if (!id) return { ok: false }
    try {
      await updateProjectLevel(Number(id), direction, confirmed)
      const updated = await getProjectDetail(Number(id))
      setProject(updated)
      return { ok: true }
    } catch (e: unknown) {
      // `raw` is kept because the page still has one message-specific branch
      // ("no sibling here") that must key off the backend's own sentence, not off
      // the localized text the user is shown.
      const raw = e instanceof Error ? e.message : typeof e === 'string' ? e : ''
      const msg = describeApiError(t, e) || t('common.failed')
      setError(msg)
      return { ok: false, error: msg, raw }
    }
  }

  // What "up" would absorb and delete, so the page can put real counts in front of
  // the user before asking. A failed preview returns null and the page must then NOT
  // proceed with the merge — guessing at the damage is exactly what this guards.
  const previewMergeUp = async (): Promise<MergePreview | null> => {
    if (!id) return null
    try {
      return await previewProjectMergeUp(Number(id))
    } catch (e: unknown) {
      setError(describeApiError(t, e) || t('common.failed'))
      return null
    }
  }

  const stats = useMemo(() => {
    const map = new Map<string, DailyStat>()
    if (project?.repos) {
      project.repos.forEach((repo) => {
        repo.stats?.forEach((stat) => {
          // Key on the bare calendar day: stat_date carries the DATETIME
          // storage format ("2026-10-06T00:00:00Z") in some rows. trendData
          // labels are sliced days — a datetime key made stats.get(day)
          // return undefined and the whole detail page crashed (the "?!date=
          // 一点就蹦" report).
          const key = stat.stat_date.slice(0, 10)
          const cur = map.get(key) || { added: 0, deleted: 0, files: 0, commits: 0 }
          cur.added += stat.lines_added
          cur.deleted += stat.lines_deleted
          cur.files += stat.files_changed
          cur.commits++
          map.set(key, cur)
        })
      })
    }
    return map
  }, [project])

  // The chart and the stat strip must answer the same question: both use the
  // SAME date window (the page labels the totals "selected range").
  const scopedDates = useMemo(() => {
    // stat_date carries the DATETIME storage format in some rows; the axis
    // must show bare calendar days, not "2025-10-09T00:00:00Z".
    let dates = Array.from(stats.keys()).map(d => d.slice(0, 10)).sort()
    if (scope === 'week') {
      const weekDates = new Set(getLastDays(7))
      dates = dates.filter((d) => weekDates.has(d))
    } else if (scope === 'month') {
      const monthDates = new Set(getLastDays(30))
      dates = dates.filter((d) => monthDates.has(d))
    }
    return dates
  }, [stats, scope])

  const trendData = useMemo(() => {
    return {
      labels: scopedDates,
      datasets: [
        { label: t('dashboard.sortMyAdded', { defaultValue: 'Lines Added' }), data: scopedDates.map((d) => stats.get(d)!.added), color: '--success' },
        { label: t('project.deletedLines'), data: scopedDates.map((d) => stats.get(d)!.deleted), color: '--danger' },
        { label: t('dashboard.sortMyFiles', { defaultValue: 'Files Changed' }), data: scopedDates.map((d) => stats.get(d)!.files), color: '--info' },
      ] as TrendDataset[],
    }
  }, [stats, scopedDates, t])

  const totals = useMemo(() => {
    let added = 0, deleted = 0, files = 0, active = 0
    scopedDates.forEach((d) => {
      const v = stats.get(d)
      if (!v) return
      added += v.added
      deleted += v.deleted
      files += v.files
      if (v.added + v.deleted > 0) active++
    })
    return { added, deleted, files, active, repos: project?.repos?.length || 0 }
  }, [stats, scopedDates, project])

  return {
    project,
    overview,
    loading,
    error,
    setError,
    scope,
    setScope,
    trendData,
    totals,
    retry: load,
    handleLevelChange,
    previewMergeUp,
  }
}

function getLastDays(n: number): string[] {
  const result: string[] = []
  const now = new Date()
  for (let i = n - 1; i >= 0; i--) {
    const d = new Date(now)
    d.setDate(d.getDate() - i)
    result.push(`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`)
  }
  return result
}
