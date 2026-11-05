// api/endpoints.ts — Every backend call, one endpoint per function, routed
// through the shared transport. Names are the stable public surface imported
// across the app.

import { call, isDesktopRuntime } from './transport'
import type {
  AICallLogEntry,
  AIAuditStatus,
  AgentSetupReport,
  AppConfig,
  BackupCopyTargetReport,
  CompileJob,
  DailyStat,
  Evidence,
  EvidenceAnswer,
  HeatmapResponse,
  ImportResult,
  ImportRun,
  HandoffResult,
  KnowledgeSyncStatus,
  MergeCandidateReport,
  WikiLinkSuggestion,
  MergePreview,
  MergeResult,
  Note,
  NoteCount,
  NoteOrganizeResult,
  NoteTopic,
  NoteVersion,
  NoteWithProject,
  OrganizeGroup,
  Project,
  ProjectDetail,
  ProjectOverview,
  RepoCommit,
  ScanStatus,
  SearchHit,
  SourceStatus,
  StatusBarData,
  Summary,
  Todo,
  TodoCount,
  DownloadState,
  UpdateInfo,
  WikiCommunityReport,
  WikiPageVersion,
  WikiPendingPage,
  WikiPageSummary,
  WikiPageDetail,
  InstallInfo,
} from './types'

const JSON_HEADERS = { 'Content-Type': 'application/json' }
const jsonInit = (method: string, body: unknown): RequestInit => ({
  method,
  headers: JSON_HEADERS,
  body: JSON.stringify(body),
})

// --- Projects -----------------------------------------------------------------

export function getProjects(date?: string, starredOnly = false): Promise<Project[]> {
  const params = new URLSearchParams()
  if (date) params.set('date', date)
  if (starredOnly) params.set('starred', '1')
  return call<Project[]>({
    method: 'GetProjects',
    args: [date ?? '', starredOnly],
    path: `/projects?${params.toString()}`,
  }).then(d => d ?? [])
}

export function getProjectDetail(id: number): Promise<ProjectDetail> {
  return call<ProjectDetail>({ method: 'GetProjectDetail', args: [id], path: `/projects/${id}` })
}

// --- Organize (ADR-0023) ------------------------------------------------------
// Landing buckets (import placeholders / the default "Uncategorized") that still
// hold unfiled notes, and the human actions to re-file or remove them. All
// desktop-only: no MCP twin; they travel over /api/rpc, so no REST path is needed.

/** The landing projects awaiting organization, with note counts + repo binding. */
export function listOrganizeGroups(): Promise<OrganizeGroup[]> {
  return call<OrganizeGroup[]>({ method: 'ListOrganizeGroups', args: [] }).then(d => d ?? [])
}

/** Move every note out of a landing project into a target; returns the count moved. */
export function reassignLandingNotes(sourceId: number, targetId: number): Promise<number> {
  return call<number>({ method: 'ReassignLandingNotes', args: [sourceId, targetId] }).then(r => r ?? 0)
}

/** Delete a landing placeholder; deleteNotes confirms removing the notes in it. */
export function deleteLandingProject(id: number, deleteNotes: boolean): Promise<void> {
  return call<void>({ method: 'DeleteLandingProject', args: [id, deleteNotes] })
}

/**
 * One repository's commit log, newest first. Read from git on demand: the
 * stored daily_stats rows carry no message or SHA, so a commit list cannot be
 * assembled from the database.
 */
export function getRepoCommits(repoId: number, limit = 50): Promise<RepoCommit[]> {
  return call<RepoCommit[]>({ method: 'GetRepoCommits', args: [repoId, limit], path: `/repos/${repoId}/commits` })
}

/**
 * A project's merged commit log across all its repositories, newest first.
 * Same on-demand git read as getRepoCommits, all authors, so the merged and
 * per-repo views agree.
 */
export function getProjectCommits(projectId: number, limit = 50): Promise<RepoCommit[]> {
  return call<RepoCommit[]>({ method: 'GetProjectCommits', args: [projectId, limit], path: `/projects/${projectId}/commits` })
}

export function getProjectStats(id: number, date?: string): Promise<DailyStat[]> {
  const params = date ? `?date=${date}` : ''
  return call<DailyStat[]>({
    method: 'GetProjectStats',
    args: [id, date ?? ''],
    path: `/projects/${id}/stats${params}`,
  }).then(d => d ?? [])
}

export function toggleStar(id: number): Promise<boolean> {
  // The Wails binding returns a bare bool (not {starred}), so unwrap it
  // directly. `!!r` normalizes undefined from a failed call to false.
  return call<boolean>({
    method: 'ToggleStar',
    args: [id],
    path: `/projects/${id}/star`,
    init: { method: 'POST' },
  }).then(r => !!r)
}

export function refreshProjectHistory(id: number): Promise<{ success: boolean }> {
  return call<{ success: boolean }>({
    method: 'RefreshProjectHistory',
    args: [id],
    path: `/projects/${id}/refresh-history`,
    init: { method: 'POST' },
  })
}

// `confirmed` must be true for "up": a merge absorbs and DELETES sibling projects
// along with their notes and todos, and the service refuses an unconfirmed call.
// "down" splits by repository — it is not an undo of a merge — and ignores the flag.
export function updateProjectLevel(
  id: number,
  direction: 'up' | 'down',
  confirmed: boolean
): Promise<{ success: boolean; new_level: number }> {
  return call({
    method: 'UpdateProjectLevel',
    args: [id, direction, confirmed],
    path: `/projects/${id}/level`,
    init: jsonInit('POST', { direction, confirmed }),
  })
}

// Read-only preview of what "up" would absorb and delete; backs the confirmation
// dialog. Desktop-only binding like the other organize/grouping actions: it travels
// over /api/rpc, so it declares no REST path.
export function previewProjectMergeUp(id: number): Promise<MergePreview> {
  return call<MergePreview>({ method: 'PreviewProjectMergeUp', args: [id] })
}

export function searchProjects(query: string): Promise<Project[]> {
  return call<Project[]>({
    method: 'SearchProjects',
    args: [query],
    path: `/projects/search?q=${encodeURIComponent(query)}`,
  }).then(d => d ?? [])
}

export function getProjectOverview(projectId: number): Promise<ProjectOverview> {
  const empty: ProjectOverview = {
    readme_excerpt: '', tech_stack: [], languages: [], recent_commits: [], cached: false,
    dependencies: [], top_contributors: [], activity: null,
  }
  // Backend may serialize nil slices as null; normalize so callers can safely
  // treat every array field as an array.
  return call<ProjectOverview>({
    method: 'GetProjectOverview',
    args: [projectId],
    path: `/projects/${projectId}/overview`,
  }).then(d => {
    if (!d) return empty
    return {
      ...d,
      readme_excerpt: d.readme_excerpt ?? empty.readme_excerpt,
      tech_stack: d.tech_stack ?? [],
      languages: d.languages ?? [],
      dependencies: d.dependencies ?? [],
      top_contributors: d.top_contributors ?? [],
      recent_commits: d.recent_commits ?? [],
      activity: d.activity ?? null,
      cached: d.cached ?? false,
    }
  })
}

// --- Scan -----------------------------------------------------------------------

export function triggerScan(): Promise<{ success: boolean }> {
  return call({ method: 'TriggerScan', path: '/scan', init: { method: 'POST' } })
}

export function getScanStatus(): Promise<ScanStatus> {
  const empty: ScanStatus = { running: false, backfilling: false, message: '', progress: 0, total: 0 }
  return call<ScanStatus>({ method: 'GetScanStatus', path: '/scan/status' }).then(d => d ?? empty)
}

// --- Config ---------------------------------------------------------------------

export function getConfig(): Promise<AppConfig> {
  return call<AppConfig>({ method: 'GetConfig', path: '/config' })
}

export function updateConfig(key: string, value: string): Promise<{ success: boolean }> {
  return call<{ success: boolean }>({
    method: 'UpdateConfig',
    args: [key, value],
    path: '/config',
    init: jsonInit('PUT', { key, value }),
  })
}

// --- AI audit (ADR-0030) ---------------------------------------------------------

/**
 * Current state of the AI call audit plugin: whether auditing and the
 * input/output reviews are on, how many rules are configured, and the
 * counters from the recent-call window.
 */
export function getAIAuditStatus(): Promise<AIAuditStatus> {
  return call<AIAuditStatus>({ method: 'GetAIAuditStatus', path: '/ai/audit/status' })
}

/**
 * The most recent audited model calls, newest first. Prompts and replies are
 * NOT returned — the trail stores a hash and a size, never the text.
 */
export function listAICallAudit(limit: number): Promise<AICallLogEntry[]> {
  return call<AICallLogEntry[]>({
    method: 'ListAICallAudit',
    args: [limit],
    path: '/ai/audit/calls',
  })
}

/**
 * Why one submitted scan root was refused. `reason` is a stable code the UI maps
 * to localised text; `detail` is the backend's English fallback.
 */
export interface ScanRootRejection {
  path: string
  reason: 'empty' | 'relative' | 'not_found' | 'not_a_directory' | 'duplicate' | 'inaccessible' | string
  detail: string
}

/** What the backend actually stored, plus anything it refused and why. */
export interface ScanRootsResult {
  scan_roots: string[]
  rejected: ScanRootRejection[]
}

export function updateScanRoots(scan_roots: string[]): Promise<ScanRootsResult> {
  return call<ScanRootsResult>({
    method: 'UpdateScanRoots',
    args: [scan_roots],
    path: '/config',
    init: jsonInit('PUT', { scan_roots }),
  })
}

// --- Summary / heatmap / status bar -----------------------------------------------

export function getSummary(date?: string, starredOnly = false): Promise<Summary> {
  const empty: Summary = {
    date: '', repo_count: 0, total_files: 0, total_added: 0,
    total_deleted: 0, my_added: 0, my_deleted: 0, my_files: 0, is_workday: false,
  }
  const params = date ? `?date=${date}` : ''
  return call<Summary>({
    method: 'GetSummary',
    args: [date ?? '', starredOnly],
    path: `/summary${params}`,
  }).then(d => d ?? empty)
}

export function getHeatmapData(projectId = 0): Promise<HeatmapResponse> {
  return call<HeatmapResponse>({
    method: 'GetHeatmapData',
    args: [projectId],
    path: `/heatmap?project_id=${projectId}`,
  }).then(d => d ?? { days: [] })
}

export function getStatusBar(): Promise<StatusBarData> {
  const empty: StatusBarData = {
    current_time: '', last_commit_time: '', last_commit_repo: '',
    last_commit_branch: '', last_commit_msg: '',
  }
  return call<StatusBarData>({ method: 'GetStatusBar', path: '/status-bar' }).then(d => d ?? empty)
}

export function getTodoCounts(): Promise<TodoCount[]> {
  return call<TodoCount[]>({ method: 'GetTodoCounts', path: '/todo-counts' }).then(d => d ?? [])
}

export function getNoteCounts(): Promise<NoteCount[]> {
  return call<NoteCount[]>({ method: 'GetNoteCounts', path: '/note-counts' }).then(d => d ?? [])
}

// --- Search ---------------------------------------------------------------------

export function searchNotes(query: string): Promise<SearchHit[]> {
  return call<SearchHit[]>({
    method: 'SearchNotes',
    args: [query],
    path: `/notes/search?q=${encodeURIComponent(query)}`,
  }).then(d => d ?? [])
}

export function searchAll(query: string): Promise<SearchHit[]> {
  return call<SearchHit[]>({
    method: 'SearchAll',
    args: [query],
    path: `/search?q=${encodeURIComponent(query)}`,
  }).then(d => d ?? [])
}

// --- Todos ------------------------------------------------------------------------

export function listTodos(projectId: number): Promise<Todo[]> {
  return call<Todo[]>({
    method: 'ListTodos',
    args: [projectId],
    path: `/todos?project_id=${projectId}`,
  }).then(d => d ?? [])
}

export interface AIModelListResult {
  models: string[]
  resolved_base_url: string
}

export interface AITestResult {
  reply: string
  resolved_base_url: string
}

export function listAIModels(baseURL: string, apiKey: string): Promise<AIModelListResult> {
  return call<AIModelListResult>({
    method: 'ListAIModels',
    args: [baseURL, apiKey],
  }).then(d => d ?? { models: [], resolved_base_url: '' })
}

export function testAIChat(baseURL: string, model: string, apiKey: string): Promise<AITestResult> {
  return call<AITestResult>({
    method: 'TestAIChat',
    args: [baseURL, model, apiKey],
  }).then(d => d ?? { reply: '', resolved_base_url: '' })
}

export function rebuildEmbeddings(): Promise<number> {
  return call<number>({ method: 'RebuildEmbeddings' }).then(d => d ?? 0)
}

export function askAI(projectId: number, question: string): Promise<string> {
  return call<string>({
    method: 'AskAI',
    args: [projectId, question],
  })
}

// askAIWithEvidence answers through the W2 retrieval path and returns the
// evidence alongside the reply, so the panel can show what was cited and file the
// answer back with those same refs. AskAI stays as the plain call.
export function askAIWithEvidence(projectId: number, question: string): Promise<EvidenceAnswer> {
  return call<EvidenceAnswer>({
    method: 'AskAIWithEvidence',
    args: [projectId, question],
  }).then(d => d ?? { reply: '', evidence: { items: [], dropped: 0, elapsed_ms: 0, truncated: false } })
}

// fileAnswerAsPage closes the query loop: the answer becomes a `query` wiki page
// linked to the pages it cited. evidence must be the exact set the reply was
// generated from — refs are positional labels of that one retrieval, and the
// backend resolves them only against it (an invented ref links to nothing).
export function fileAnswerAsPage(
  projectId: number,
  question: string,
  answer: string,
  evidence: Evidence,
  citedRefs: string[],
): Promise<number> {
  return call<number>({
    method: 'FileAnswerAsPage',
    args: [projectId, question, answer, evidence, citedRefs],
  })
}

export function createTodo(projectId: number, title: string): Promise<Todo> {
  return call<Todo>({
    method: 'CreateTodo',
    args: [projectId, title],
    path: '/todos',
    init: jsonInit('POST', { project_id: projectId, title }),
  })
}

export function toggleTodo(todoId: number): Promise<void> {
  return call<void>({ method: 'ToggleTodo', args: [todoId], path: `/todos/${todoId}/toggle`, init: { method: 'POST' } })
}

export function deleteTodo(todoId: number): Promise<void> {
  return call<void>({ method: 'DeleteTodo', args: [todoId], path: `/todos/${todoId}`, init: { method: 'DELETE' } })
}

export function reorderTodos(todoIds: number[]): Promise<void> {
  return call<void>({
    method: 'ReorderTodos',
    args: [todoIds],
    path: '/todos/reorder',
    init: jsonInit('POST', { todo_ids: todoIds }),
  })
}

// --- Notes ------------------------------------------------------------------------

export function listNotes(projectId: number): Promise<Note[]> {
  return call<Note[]>({
    method: 'ListNotes',
    args: [projectId],
    path: `/notes?project_id=${projectId}`,
  }).then(d => d ?? [])
}

export function createNote(projectId: number, content: string): Promise<Note> {
  return call<Note>({
    method: 'CreateNote',
    args: [projectId, content],
    path: '/notes',
    init: jsonInit('POST', { project_id: projectId, content }),
  })
}

export interface NoteCreateInput {
  title?: string
  tags?: string
  kind?: string
  source?: string
}

export function createNoteWithMeta(
  projectId: number,
  content: string,
  meta: NoteCreateInput = {}
): Promise<Note> {
  const title = meta.title ?? ''
  const tags = meta.tags ?? ''
  const kind = meta.kind ?? ''
  const source = meta.source ?? ''
  return call<Note>({
    method: 'CreateNoteWithMeta',
    args: [projectId, title, content, tags, kind, source],
    path: '/notes',
    init: jsonInit('POST', { project_id: projectId, content, title, tags, kind, source }),
  })
}

export function updateNote(noteId: number, content: string): Promise<void> {
  return call<void>({
    method: 'UpdateNote',
    args: [noteId, content],
    path: `/notes/${noteId}`,
    init: jsonInit('PUT', { content }),
  })
}

export function updateNoteFull(
  noteId: number,
  content: string,
  title: string,
  tags: string,
  kind: string,
  pinned: boolean
): Promise<void> {
  return call<void>({
    method: 'UpdateNoteFull',
    args: [noteId, content, title, tags, kind, pinned],
    path: `/notes/${noteId}`,
    init: jsonInit('PUT', { content, title, tags, kind, pinned }),
  })
}

export function updateNoteMeta(
  noteId: number,
  title: string,
  tags: string,
  kind: string,
  pinned: boolean
): Promise<void> {
  return call<void>({
    method: 'UpdateNoteMeta',
    args: [noteId, title, tags, kind, pinned],
    path: `/notes/${noteId}/meta`,
    init: jsonInit('PUT', { title, tags, kind, pinned }),
  })
}

/**
 * Ask the AI to restructure one note. Returns a PROPOSAL — nothing is saved;
 * the caller opens the note editor with it and the user's save lands it.
 * A slow model call is expected here (the service allows minutes), so callers
 * show progress rather than a spinner that dies.
 */
export function organizeNote(noteId: number): Promise<NoteOrganizeResult> {
  return call<NoteOrganizeResult>({
    method: 'OrganizeNote',
    args: [noteId],
    path: `/notes/${noteId}/organize`,
  })
}

export function pinNote(noteId: number, pinned: boolean): Promise<void> {
  return call<void>({
    method: 'PinNote',
    args: [noteId, pinned],
    path: `/notes/${noteId}/pin`,
    init: jsonInit('POST', { pinned }),
  })
}

export function deleteNote(noteId: number): Promise<void> {
  return call<void>({ method: 'DeleteNote', args: [noteId], path: `/notes/${noteId}`, init: { method: 'DELETE' } })
}

export function moveNote(noteId: number, projectId: number): Promise<void> {
  return call<void>({
    method: 'MoveNote',
    args: [noteId, projectId],
    path: `/notes/${noteId}/move`,
    init: jsonInit('POST', { project_id: projectId }),
  })
}

// --- Topics (schema v27: project -> topic -> note) --------------------------------
// A topic is a real entity inside one project: renaming it is one UPDATE, and
// "how many notes are in this subject" is answerable at all. The endpoints
// travel over /api/rpc only — the desktop UI is the only surface that files
// notes by subject, so there is no MCP twin.

/** A project's topics with their note counts. */
export function listTopics(projectId: number): Promise<NoteTopic[]> {
  return call<NoteTopic[]>({
    method: 'ListTopics',
    args: [projectId],
    path: `/topics?project_id=${projectId}`,
  }).then(d => d ?? [])
}

/** Create a topic in a project. */
export function createTopic(projectId: number, name: string, description = ''): Promise<NoteTopic> {
  return call<NoteTopic>({
    method: 'CreateTopic',
    args: [projectId, name, description],
    path: '/topics',
    init: jsonInit('POST', { project_id: projectId, name, description }),
  })
}

/** Rename a topic and/or change its description. */
export function updateTopic(topicId: number, name: string, description = ''): Promise<void> {
  return call<void>({
    method: 'UpdateTopic',
    args: [topicId, name, description],
    path: `/topics/${topicId}`,
    init: jsonInit('PUT', { name, description }),
  })
}

/**
 * Delete a topic. The notes filed under it are KEPT and fall back to "no
 * topic" — deleting a filing label must never destroy knowledge.
 */
export function deleteTopic(topicId: number): Promise<void> {
  return call<void>({ method: 'DeleteTopic', args: [topicId], path: `/topics/${topicId}`, init: { method: 'DELETE' } })
}

/** File a note under a topic; topicId 0 clears the note's topic. */
export function setNoteTopic(noteId: number, topicId: number): Promise<void> {
  return call<void>({
    method: 'SetNoteTopic',
    args: [noteId, topicId],
    path: `/notes/${noteId}/topic`,
    init: jsonInit('POST', { topic_id: topicId }),
  })
}

// --- Note version history -----------------------------------------------------------

export function listNoteVersions(noteId: number): Promise<NoteVersion[]> {
  return call<NoteVersion[]>({
    method: 'ListNoteVersions',
    args: [noteId],
    path: `/notes/${noteId}/versions`,
  }).then(d => d ?? [])
}

export function restoreNoteVersion(noteId: number, versionId: number): Promise<void> {
  return call<void>({
    method: 'RestoreNoteVersion',
    args: [noteId, versionId],
    path: `/notes/${noteId}/versions/${versionId}/restore`,
    init: { method: 'POST' },
  })
}

export function diffNoteVersions(noteId: number, versionId: number): Promise<string> {
  return call<string>({
    method: 'DiffNoteVersions',
    args: [noteId, versionId],
    path: `/notes/${noteId}/versions/${versionId}/diff`,
  }).then(d => d ?? '')
}

// --- Knowledge hub -------------------------------------------------------------------

export function listAllNotes(): Promise<NoteWithProject[]> {
  return call<NoteWithProject[]>({ method: 'ListAllNotes', path: '/notes/all' }).then(d => d ?? [])
}

export function listAllTags(): Promise<string[]> {
  return call<string[]>({ method: 'ListAllTags', path: '/notes/tags' }).then(d => d ?? [])
}

export function importClaudeMemory(): Promise<ImportResult> {
  return call<ImportResult>({
    method: 'ImportClaudeMemory',
    path: '/knowledge/import',
    init: { method: 'POST' },
  }).then(d => d ?? { synced: 0, updated: 0, skipped: 0 })
}

// captureClaudeHandoff runs an on-demand M1 capture for one project: it reads
// that project's latest Claude Code session transcript and persists a handoff
// note. Backend-gated by the `claude_session_capture` config (default off), so
// it rejects unless the user has explicitly enabled capture.
export function captureClaudeHandoff(projectId: number): Promise<HandoffResult> {
  return call<HandoffResult>({
    method: 'CaptureClaudeHandoff',
    args: [projectId],
    path: `/project/${projectId}/capture-claude-handoff`,
    init: { method: 'POST' },
  })
}

// --- AI-facing exports ------------------------------------------------------------------

export function generateLLMsTxt(): Promise<string> {
  return call<string>({ method: 'GenerateLLMsTxt', path: '/llms' })
}

export function exportNoteAsMarkdown(noteId: number): Promise<string> {
  return call<string>({
    method: 'ExportNoteAsMarkdown',
    args: [noteId],
    path: `/notes/${noteId}/markdown`,
  }).then(d => d ?? '')
}

// exportMemoryJSON returns the knowledge base as an OMP-style portable memory
// JSON string (ADR-0013 interop seam). limit<=0 = all notes.
export function exportMemoryJSON(limit: number): Promise<string> {
  return call<string>({
    method: 'ExportMemoryJSON',
    args: [limit],
    path: `/memory/export.json?limit=${limit}`,
  }).then(d => d ?? '[]')
}

/** RunBackupNow writes today's local snapshot immediately (ADR-0025 L1). */
export function runBackupNow(): Promise<string> {
  return call<string>({ method: 'RunBackupNow' })
}

/**
 * CheckBackupCopyTarget inspects a candidate off-disk backup directory
 * (ADR-0025 L3).
 *
 * An empty path means "the target that is configured right now". The report
 * says whether the directory exists, whether a copy could be written there,
 * and — the question a path cannot answer by looking at it — whether the
 * target really is on another disk.
 */
export function checkBackupCopyTarget(path: string): Promise<BackupCopyTargetReport> {
  return call<BackupCopyTargetReport>({
    method: 'CheckBackupCopyTarget',
    args: [path],
  }).then(d => d ?? { path: '', exists: false, writable: false, same_device: false, device_known: false, code: '', blocking: false })
}

// --- AI client wiring -------------------------------------------------------------------------
// The desktop twin of the CLI's `repotory-init`: it writes the MCP server
// entry into the AI clients the user picks, so "click once and the agent
// sees the memory" is one click instead of a wiki page of JSON.

/**
 * The AI clients this build can register the MCP server with.
 *
 * The UI asks instead of hardcoding the list: a client the backend cannot
 * register must not be offered as a checkbox.
 */
export function listAgentClients(): Promise<string[]> {
  return call<string[]>({ method: 'ListAgentClients' }).then(d => d ?? [])
}

/**
 * Register the MCP server with the picked clients for one project.
 *
 * A refusal comes back as a code in the report (never as a thrown error):
 * "that project is a container" is an answer the UI renders, not a crash.
 */
export function setupAgentClients(
  projectId: number,
  clients: string[],
  withHook: boolean,
  withInstructions: boolean,
): Promise<AgentSetupReport> {
  return call<AgentSetupReport>({
    method: 'SetupAgentClients',
    args: [projectId, clients, withHook, withInstructions],
  }).then(d => d ?? { code: '', blocking: false, binary: '', files: [] })
}

// --- Plugins -------------------------------------------------------------------------------

export function getKnowledgeSources(): Promise<SourceStatus[]> {
  return call<SourceStatus[]>({ method: 'GetKnowledgeSources', path: '/plugins/sources' }).then(d => d ?? [])
}

export function getKnowledgeSyncStatus(): Promise<KnowledgeSyncStatus> {
  return call<KnowledgeSyncStatus>({ method: 'GetKnowledgeSyncStatus', path: '/plugins/sync-status' }).then(d => d ?? { last_run: '', last_error: '' })
}

export function triggerKnowledgeImport(name: string): Promise<ImportRun> {
  const empty: ImportRun = { created: 0, updated: 0, skipped: 0 }
  return call<ImportRun>({
    method: 'TriggerKnowledgeImport',
    args: [name],
    path: '/plugins/import',
    init: jsonInit('POST', { source: name }),
  }).then(d => d ?? empty)
}

// --- Wiki review & compile jobs ------------------------------------------------------------

// listPendingWikiPages feeds the review tab: unreviewed local-model output, each
// with its source notes so a reviewer can compare before approving.
export function listPendingWikiPages(projectId: number): Promise<WikiPendingPage[]> {
  return call<WikiPendingPage[]>({ method: 'ListPendingWikiPages', args: [projectId] }).then(d => d ?? [])
}

// listRejectedWikiPages returns the error-page tombstones (a rejected result is
// replaced by an error page, not deleted, so inbound links keep resolving).
export function listRejectedWikiPages(projectId: number): Promise<WikiPendingPage[]> {
  return call<WikiPendingPage[]>({ method: 'ListRejectedWikiPages', args: [projectId] }).then(d => d ?? [])
}

export function approveWikiPage(pageId: number): Promise<void> {
  return call<void>({ method: 'ApproveWikiPage', args: [pageId] })
}

export function rejectWikiPage(pageId: number): Promise<void> {
  return call<void>({ method: 'RejectWikiPage', args: [pageId] })
}

export function deleteCompiledPage(pageId: number): Promise<void> {
  return call<void>({ method: 'DeleteCompiledPage', args: [pageId] })
}

// --- page scope: the project tier and the global tier (ADR-0031) ----------------

// listApprovedWikiPages is the scope surface's feed: the approved pages whose
// scope a human may widen (into the global tier) or narrow (back into a
// project). A project id narrows to that project; 0 means every page, global
// ones included.
export function listApprovedWikiPages(projectId: number): Promise<WikiPendingPage[]> {
  return call<WikiPendingPage[]>({ method: 'ListApprovedWikiPages', args: [projectId] }).then(d => d ?? [])
}

// promoteWikiPageToGlobal moves an approved page into the global tier, where
// it enters the cross-project profile and becomes visible to every project's
// evidence gathering. Human-only by design: there is no automatic path here.
export function promoteWikiPageToGlobal(pageId: number): Promise<void> {
  return call<void>({ method: 'PromoteWikiPageToGlobal', args: [pageId] })
}

// assignWikiPageProject moves a page back into a project tier — the inverse of
// the promotion, and what makes it reversible.
export function assignWikiPageProject(pageId: number, projectId: number): Promise<void> {
  return call<void>({ method: 'AssignWikiPageProject', args: [pageId, projectId] })
}

// startCompileJob queues a batch compile and returns the job id immediately; the
// caller polls. Submission is not supposed to block on the model.
export function startCompileJob(projectId: number, maxNotes: number): Promise<number> {
  return call<number>({ method: 'StartCompileJob', args: [projectId, maxNotes] }).then(d => d ?? 0)
}

export function getCompileJob(jobId: number): Promise<CompileJob | null> {
  return call<CompileJob | null>({ method: 'GetCompileJob', args: [jobId] })
}

// kind: '' lists both queues, 'compile' / 'lint' filter to one. The review panel
// asks for '' and renders each row against its own counters.
export function listCompileJobs(projectId: number, kind: string, limit: number): Promise<CompileJob[]> {
  return call<CompileJob[]>({ method: 'ListCompileJobs', args: [projectId, kind, limit] }).then(d => d ?? [])
}

export function startLintJob(projectId: number): Promise<number> {
  return call<number>({ method: 'StartLintJob', args: [projectId] })
}

export function cancelCompileJob(jobId: number): Promise<CompileJob | null> {
  return call<CompileJob | null>({ method: 'CancelCompileJob', args: [jobId] })
}

// --- Approved wiki browsing (ADR-0032) ----------------------------------------
// The read side a person uses: the pages that already passed the approval gate.
//
// projectId 0 means "no scope" — every approved page, whatever its project.
// A project id means "that project's pages PLUS the global ones" (ADR-0031),
// the same visibility rule the AI evidence path applies, so the reader and the
// answer can never disagree about which pages exist.

// browseWikiPages lists the approved pages, newest first. It is a dedicated
// list query rather than a search with an empty query: db.SearchWikiPages
// treats an empty query as "no results" on purpose.
export function browseWikiPages(projectId: number, limit = 100): Promise<WikiPageSummary[]> {
  return call<WikiPageSummary[]>({ method: 'BrowseWikiPages', args: [projectId, limit] }).then(d => d ?? [])
}

// searchWikiPages searches the same visible set through the backend's own
// retrieval (approved-only, bm25, CJK relaxation) — the one the evidence path
// uses, so the two surfaces cannot drift apart.
export function searchWikiPages(projectId: number, query: string, limit = 20): Promise<WikiPageSummary[]> {
  return call<WikiPageSummary[]>({ method: 'SearchWikiPages', args: [projectId, query, limit] }).then(d => d ?? [])
}

// getWikiPage returns one approved page in full. The backend refuses a page
// that is not approved, so this never renders a pending one.
export function getWikiPage(pageId: number): Promise<WikiPageDetail> {
  return call<WikiPageDetail>({ method: 'GetWikiPage', args: [pageId] })
}

// --- Concept merge, community synthesis & page versions -------------------------
// (ADR-0019 / ADR-0020 / ADR-0021). These five bindings are desktop-only by
// design: no MCP twin, because a merge destroys knowledge and a synthesis run
// writes model output into the library — both are human decisions.

// suggestWikiLinks suggests [[wikilink]] candidates for given text based on library vocabulary.
export function suggestWikiLinks(projectId: number, content: string): Promise<WikiLinkSuggestion[]> {
  return call<WikiLinkSuggestion[] | null>({ method: 'SuggestWikiLinks', args: [projectId, content] })
    .then(list => list ?? [])
}

// findMergeCandidates runs the free structural pass over a project's pages.
// The report is normalised rather than passed through because the review page
// maps over `candidates` directly: a null here would take the whole page down,
// and "no report" and "no candidates" render the same empty state anyway.
export function findMergeCandidates(projectId: number, topK: number): Promise<MergeCandidateReport> {
  return call<MergeCandidateReport | null>({ method: 'FindMergeCandidates', args: [projectId, topK] })
    .then(d => ({
      project_id: d?.project_id ?? projectId,
      pages: d?.pages ?? 0,
      pairs_scored: d?.pairs_scored ?? 0,
      pairs_possible: d?.pairs_possible ?? 0,
      candidates: d?.candidates ?? [],
      truncated: !!d?.truncated,
    }))
}

// mergeWikiPages absorbs the loser into the winner. Null is passed through
// (getCompileJob's shape): the call either reported what it moved, or the
// promise rejected. Zeros for a missing report would read as "the merge did
// nothing", which is a claim the UI is not entitled to make.
export function mergeWikiPages(loserId: number, winnerId: number): Promise<MergeResult | null> {
  return call<MergeResult | null>({ method: 'MergeWikiPages', args: [loserId, winnerId] })
}

// runCommunitySummary is the slow, token-spending half of ADR-0020. Same
// pass-through rationale as mergeWikiPages: a null report must not render as a
// run that created zero pages.
export function runCommunitySummary(projectId: number): Promise<WikiCommunityReport | null> {
  return call<WikiCommunityReport | null>({ method: 'RunCommunitySummary', args: [projectId] })
}

// listWikiPageVersions returns a page's approved snapshots, newest first
// (the backend orders them; the panel must not re-sort).
export function listWikiPageVersions(pageId: number): Promise<WikiPageVersion[]> {
  return call<WikiPageVersion[]>({ method: 'ListWikiPageVersions', args: [pageId] }).then(d => d ?? [])
}

// diffWikiPageVersion compares one snapshot against the page's CURRENT content as
// unified line-diff text — the same format DiffNoteVersions returns, so the same
// DiffViewer renders both.
export function diffWikiPageVersion(pageId: number, versionId: number): Promise<string> {
  return call<string>({ method: 'DiffWikiPageVersion', args: [pageId, versionId] }).then(d => d ?? '')
}

// --- Update check -----------------------------------------------------------------
// Desktop-only bindings (no MCP twin — see internal/app/surface.go): an update
// check is the app maintaining itself, and a headless/MCP user updates through
// their own deployment channel.
//
// The check is advisory: it never installs anything, it only tells the
// settings card what the newest release is. The DOWNLOAD that follows it
// fetches the bundle into a local file and verifies its digest — the app
// still never replaces its own binary; it hands the verified file to the OS
// installer and lets the user finish.

/**
 * Ask GitHub Releases whether a newer stable version exists. Rejects when the
 * network call fails — "up to date" and "could not check" are different
 * answers, and collapsing them would teach users the button is broken.
 */
export function checkUpdate(): Promise<UpdateInfo> {
  return call<UpdateInfo>({ method: 'CheckUpdate', args: [] })
}

/**
 * Start downloading the release bundle for this platform.
 *
 * url / fileName / sha256 / version come straight from the UpdateInfo the
 * check returned; the backend rejects anything that is not https. The version
 * goes into the file name, so the bundle the user finds in Downloads says
 * which release it is. The download runs in the background — poll
 * updateDownloadState for progress.
 */
export function startUpdateDownload(url: string, fileName: string, sha256: string, version: string): Promise<void> {
  return call<void>({ method: 'StartUpdateDownload', args: [url, fileName, sha256, version] })
}

/** The progress of the current (or last) update download. */
export function updateDownloadState(): Promise<DownloadState> {
  return call<DownloadState>({ method: 'UpdateDownloadState', args: [] })
}

/** Abort an in-flight update download. */
export function cancelUpdateDownload(): Promise<void> {
  return call<void>({ method: 'CancelUpdateDownload', args: [] })
}

/**
 * Hand the downloaded bundle to the OS so the user can install it: macOS
 * mounts the .dmg, Windows reveals the folder holding the .zip, Linux
 * reveals the folder holding the tarball.
 */
export function openDownloadedUpdate(path: string): Promise<void> {
  return call<void>({ method: 'OpenDownloadedUpdate', args: [path] })
}

/**
 * Show the downloaded bundle in the OS file manager, selected.
 *
 * It is the answer to "where did that download go?": the bundle sits in the
 * Downloads folder, and this puts the file manager on it instead of making
 * the user go and look for it.
 */
export function revealDownloadedUpdate(path: string): Promise<void> {
  return call<void>({ method: 'RevealDownloadedUpdate', args: [path] })
}

/**
 * Unmount the installer disk image on macOS.
 *
 * A mounted DMG shows up as a second "Cinder" in the Finder sidebar, and
 * closing its window does not unmount it. The app ejects it automatically once
 * it detects the install; this is the manual way to do the same thing.
 *
 * Resolves to whether a volume was actually ejected — "nothing was mounted"
 * is a normal answer, not an error.
 */
export function ejectInstallerVolume(): Promise<boolean> {
  return call<boolean>({ method: 'EjectInstallerVolume', args: [] })
}

/**
 * Where this build is installed, and where update bundles are written.
 *
 * Both questions used to have no answer inside the app: the bundle landed in
 * a hidden config directory, and the install path appeared nowhere.
 */
export function getInstallInfo(): Promise<InstallInfo> {
  return call<InstallInfo>({ method: 'GetInstallInfo', args: [] })
}

/**
 * Open a URL in the system browser. A Wails webview navigating to an external
 * page would trap the user inside the app window, so the desktop build hands
 * the URL to the OS browser instead.
 *
 * In a plain browser tab (the headless server's own UI) there is no OS
 * browser to hand off to — the tab IS the browser, and window.open is the
 * native way out of it. The binding would fail there (no Wails context), so
 * the two runtimes take different paths.
 */
export function openBrowserURL(url: string): Promise<void> {
  if (!isDesktopRuntime()) {
    window.open(url, '_blank', 'noopener,noreferrer')
    return Promise.resolve()
  }
  return call<void>({ method: 'OpenBrowserURL', args: [url] })
}

/**
 * The running build's version, read from the Health payload (which already
 * carries it). The update card shows "current version" before the user asks
 * for a check — a card that can only tell you your version AFTER a network
 * round trip to GitHub would be blank on a machine that cannot reach it.
 */
export function getAppVersion(): Promise<string> {
  return call<{ version?: string }>({ method: 'Health', args: [] })
    .then(h => h?.version ?? '')
    .catch(() => '')
}
