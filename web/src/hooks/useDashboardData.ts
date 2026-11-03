import { useState, useEffect, useMemo, useCallback, useRef, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getProjects, getSummary, triggerScan, getTodoCounts, getNoteCounts, toggleStar,
  refreshProjectHistory, getConfig, type Summary, type TodoCount, type NoteCount,
} from '../api/client'
import { useScanPolling } from './useScanPolling'
import { useApiData, invalidateCache } from './useApiData'
import {
  getDashboardScope, setDashboardScope, subscribeDashboardScope,
} from '../state/dashboardScope'

export type SortKey = 'name' | 'my_added' | 'my_files' | 'repo_count'

/**
 * Encapsulates all data-fetching, sorting, and mutation logic for the
 * Dashboard page. The page component only handles rendering.
 */
export function useDashboardData() {
  const { t } = useTranslation()
  const [summary, setSummary] = useState<Summary | null>(null)
  const [dailyGoal, setDailyGoal] = useState(500)
  const [sortKey, setSortKey] = useState<SortKey>('my_added')
  const [confirmScan, setConfirmScan] = useState(false)
  const [todoCounts, setTodoCounts] = useState<TodoCount[]>([])
  const [noteCounts, setNoteCounts] = useState<NoteCount[]>([])
  const [starOverride, setStarOverride] = useState<Record<number, boolean>>({})
  const [error, setError] = useState('')
  const [summaryError, setSummaryError] = useState('')
  const fetchSeq = useRef(0)

  // The day and the starred filter live in a shared store, not in this hook:
  // the agent's cards answer "today's code" too, and the only way for the two
  // to never contradict each other is for them to read the same two choices.
  const scope = useSyncExternalStore(subscribeDashboardScope, getDashboardScope, getDashboardScope)
  const { date, starredOnly: showStarredOnly } = scope

  const { data: projectsData, loading: projectsLoading, error: projectsError, refetch: refetchProjects } =
    useApiData(() => getProjects(date, showStarredOnly), [date, showStarredOnly], { cacheKey: 'dashProjects' })

  const projects = useMemo(
    () => (projectsData ?? []).map(p =>
      starOverride[p.id] !== undefined ? { ...p, is_starred: starOverride[p.id] } : p
    ),
    [projectsData, starOverride]
  )

  const [summaryLoading, setSummaryLoading] = useState(true)
  const loading = projectsLoading || summaryLoading
  const displayedError = error || projectsError || summaryError

  // Reset optimistic star overrides whenever the project list is re-scoped
  // (date or starred-only filter changes). Done in the setters instead of an
  // effect to avoid a synchronous setState-in-effect.
  const setDateScoped = useCallback((d: string) => { setStarOverride({}); setDashboardScope({ date: d }) }, [])
  const setShowStarredOnlyScoped = useCallback(
    (v: boolean) => { setStarOverride({}); setDashboardScope({ starredOnly: v }) },
    []
  )

  // The summary strip must aggregate the SAME scope the list filters by:
  // passing the starred filter through to GetSummary keeps header and rows in
  // lockstep (they read as two different products otherwise). The scope is
  // read from the store at call time, so it is never stale.
  const fetchSummary = useCallback(async (selectedDate: string, silent = false) => {
    const seq = ++fetchSeq.current
    if (!silent) setSummaryLoading(true)
    setSummaryError('')
    try {
      const [sumData, counts, noteCountsData] = await Promise.all([
        getSummary(selectedDate, getDashboardScope().starredOnly),
        getTodoCounts(),
        getNoteCounts(),
      ])
      if (seq !== fetchSeq.current) return
      setSummary(sumData)
      setTodoCounts(counts)
      setNoteCounts(noteCountsData)
    } catch (e: unknown) {
      if (seq !== fetchSeq.current) return
      setSummaryError(e instanceof Error ? e.message : t('common.failed'))
    } finally {
      if (seq === fetchSeq.current && !silent) setSummaryLoading(false)
    }
  }, [t])

  const { scanning, message: scanMsg, doneMessage: scanDoneMsg, start: startScanPolling } = useScanPolling(
    () => { void fetchSummary(date); void refetchProjects() }
  )

  useEffect(() => {
    getConfig()
      .then(c => {
        const v = parseInt(c.config.daily_code_standard || '500', 10)
        if (!isNaN(v) && v > 0) setDailyGoal(v)
      })
      .catch(() => {})
    // First load: non-silent so loading state is properly managed.
    void fetchSummary(date) // eslint-disable-line react-hooks/set-state-in-effect
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [date, showStarredOnly])

  const handleScan = useCallback(async () => {
    setConfirmScan(false)
    setError('')
    try {
      await triggerScan()
      startScanPolling(t('dashboard.scanning', { defaultValue: 'Scanning repos…' }))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : t('settings.rescanFailed', { defaultValue: 'Scan failed' }))
    }
  }, [startScanPolling, t])

  const handleToggleStar = useCallback(async (projectId: number): Promise<boolean> => {
    const current = projects.find(p => p.id === projectId)?.is_starred ?? false
    const optimistic = !current
    setStarOverride(prev => ({ ...prev, [projectId]: optimistic }))
    try {
      const newStarred = await toggleStar(projectId)
      setStarOverride(prev => ({ ...prev, [projectId]: newStarred }))
      // Refresh the shared base list so NoteSection/CommandPalette reflect it.
      invalidateCache('projects:all')
      return newStarred
    } catch (e: unknown) {
      setStarOverride(prev => { const n = { ...prev }; delete n[projectId]; return n })
      setError(e instanceof Error ? e.message : t('common.failed'))
      throw e
    }
  }, [projects, t])

  const handleRefreshHistory = useCallback(async (projectId: number) => {
    try {
      await refreshProjectHistory(projectId)
      await fetchSummary(date, true)
      await refetchProjects()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : t('common.failed'))
    }
  }, [date, fetchSummary, refetchProjects, t])

  const sortOptions = useMemo(() => [
    { key: 'name' as const, label: t('dashboard.sortName', { defaultValue: 'Name' }) },
    { key: 'my_added' as const, label: t('dashboard.sortMyAdded', { defaultValue: 'Lines Added' }) },
    { key: 'my_files' as const, label: t('dashboard.sortMyFiles', { defaultValue: 'Files Changed' }) },
    { key: 'repo_count' as const, label: t('dashboard.sortRepos', { defaultValue: 'Repo Count' }) },
  ], [t])

  const sorted = useMemo(() => {
    return projects
      .filter(p => {
        if (showStarredOnly) return !!p.is_starred
        const myAdded = p.my_added || 0
        const myDeleted = p.my_deleted || 0
        const myFiles = p.my_files || 0
        const totalAdded = p.total_added || 0
        const totalDeleted = p.total_deleted || 0
        const hasActivity = myAdded > 0 || myDeleted > 0 || myFiles > 0
        const hasTeamActivity = totalAdded > 0 || totalDeleted > 0
        const hasRepos = (p.repo_count || 0) > 0
        return hasActivity || hasTeamActivity || hasRepos || !!p.is_starred
      })
      .sort((a, b) => {
        switch (sortKey) {
          case 'name': return a.name.localeCompare(b.name)
          case 'my_added': return (b.my_added || 0) - (a.my_added || 0)
          case 'my_files': return (b.my_files || 0) - (a.my_files || 0)
          case 'repo_count': return (b.repo_count || 0) - (a.repo_count || 0)
          default: return 0
        }
      })
  }, [projects, sortKey, showStarredOnly])

  const starredProjects = useMemo(() => sorted.filter(p => p.is_starred), [sorted])
  const unstarredProjects = useMemo(() => sorted.filter(p => !p.is_starred), [sorted])

  const todoMap = useMemo(() => {
    const map = new Map<number, number>()
    todoCounts.forEach(c => map.set(c.project_id, c.count))
    return map
  }, [todoCounts])

  const noteMap = useMemo(() => {
    const map = new Map<number, number>()
    noteCounts.forEach(c => map.set(c.project_id, c.count))
    return map
  }, [noteCounts])

  // The todo counter follows the same scope as the strip: under the starred
  // filter it counts only starred projects' open todos.
  const globalTodoCount = useMemo(() => {
    if (!showStarredOnly) return todoCounts.reduce((sum, c) => sum + c.count, 0)
    const starredIds = new Set(projects.filter(p => p.is_starred).map(p => p.id))
    return todoCounts.filter(c => starredIds.has(c.project_id)).reduce((sum, c) => sum + c.count, 0)
  }, [todoCounts, projects, showStarredOnly])

  const myAdded = summary?.my_added || 0
  const isWorkday = summary?.is_workday ?? false

  return {
    // State
    summary, dailyGoal, date, sortKey, confirmScan, showStarredOnly,
    error, displayedError, scanning, scanMsg, scanDoneMsg, loading,
    // Derived
    projects, sorted, starredProjects, unstarredProjects,
    todoMap, noteMap, globalTodoCount, myAdded, isWorkday, sortOptions,
    // Setters
    setDate: setDateScoped, setSortKey, setConfirmScan, setShowStarredOnly: setShowStarredOnlyScoped,
    // Actions
    handleScan, handleToggleStar, handleRefreshHistory, fetchSummary,
    refetchProjects, setError, setSummaryError,
  }
}
