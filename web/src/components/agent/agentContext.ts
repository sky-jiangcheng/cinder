// components/agent/agentContext.ts — what the agent knows, per page.
//
// Why this file exists. A floating entry point that opens the same input box
// everywhere is a chat window with a logo; it does not know where the user
// is, so every question has to be typed from scratch ("today's commits?",
// "which project was I in?"). The pages already hold the answer — the
// dashboard holds today's counts, a project page holds that project's
// overview — so the agent's job is to KNOW THE PAGE and offer the questions
// that page can answer.
//
// The split is deliberate:
//   - the ACTIONS here are answered from LOCAL data (the same bindings the
//     page itself uses). They are fast, exact, and work with no AI endpoint
//     configured — a card with today's commit count is a fact, not a guess;
//   - free-form questions go to the AI, with the page's scope attached
//     (a project page scopes the question to that project, everything else
//     scopes it to the whole library).
//
// The module is pure data + closures: no React, no DOM. That is what makes
// it testable — the cards can be asserted without a browser.

import { getYesterday } from '../../utils/dates'
import type { DashboardScope } from '../../state/dashboardScope'
import {
  getHeatmapData,
  getInstallInfo,
  getNoteCounts,
  getProjectOverview,
  getProjects,
  getStatusBar,
  getSummary,
  getTodoCounts,
  listPendingWikiPages,
  listRecentHandoffs,
} from '../../api/client'

/** A labelled number in a card. */
export interface AgentMetric {
  label: string
  value: string
  hint?: string
}

/** One row in a card's list. */
export interface AgentCardItem {
  title: string
  meta?: string
  /** An in-app route the row opens when clicked. */
  to?: string
}

/**
 * A card: the answer, in a shape the panel can render without knowing what
 * was asked.
 */
export interface AgentCard {
  title: string
  subtitle?: string
  metrics?: AgentMetric[]
  items?: AgentCardItem[]
  /** Free text (the AI's answer, or a sentence of explanation). */
  body?: string
  footer?: string
  /** A "go there" target — the panel turns it into a link. */
  to?: string
  toLabel?: string
}

/** A quick action the current context offers. */
export interface AgentAction {
  key: string
  label: string
  load: () => Promise<AgentCard>
}

/** What the agent knows about where the user is. */
export interface AgentContext {
  /** Where the user is, in one line ("仪表盘"). */
  label: string
  /** The scope free-form questions are asked in ("全库" / a project name). */
  scopeLabel: string
  /** The scope id for AI questions: a project id, or 0 for the whole library. */
  projectId: number
  /** The quick actions this context offers. */
  actions: AgentAction[]
}

/**
 * The parts of a context the caller can override.
 *
 * The route decides the QUICK ACTIONS (the page's own answers); the scope
 * chip decides what a free-form QUESTION is about. They are two different
 * things, and ADR-0045 decision 4 makes them two different things on
 * purpose: the desktop bot asks about the whole library unless the user
 * says otherwise, even while it sits on a project page.
 */
export interface AgentContextOptions {
  /**
   * The project a free-form question is scoped to; 0 = the whole library.
   *
   * Left undefined, the question follows the route (ADR-0041's behaviour:
   * a project page asks about that project).
   */
  questionProjectId?: number
  /** The scope's label ("全库" / a project name). */
  questionScopeLabel?: string
}

/** The translator the builders use. */
export type TFunc = (key: string, opts?: Record<string, unknown>) => string

// --- helpers ----------------------------------------------------------------

/** Today in the app's date format (local time, YYYY-MM-DD). */
export function todayISO(now: Date = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

/** A number with thousands separators. */
function fmt(n: number | undefined | null): string {
  return (n ?? 0).toLocaleString()
}

/**
 * A signed count.
 *
 * The sign only appears when there is something to sign: a "+0" reads as a
 * (tiny) movement that did not happen. This is the same rule the dashboard's
 * summary strip follows, and the card must look like the strip it sits next
 * to.
 */
function signed(n: number | undefined | null): string {
  const v = n ?? 0
  if (v > 0) return `+${fmt(v)}`
  if (v < 0) return `-${fmt(Math.abs(v))}`
  return '0'
}

/** A deleted-line count: always a magnitude, signed only when non-zero. */
function minus(n: number | undefined | null): string {
  const v = n ?? 0
  return v > 0 ? `-${fmt(v)}` : '0'
}

/** The date N days before the given date, as YYYY-MM-DD. */
function daysBeforeISO(date: string, days: number): string {
  const d = new Date(`${date}T00:00:00`)
  d.setDate(d.getDate() - days)
  return todayISO(d)
}

// --- shared cards ------------------------------------------------------------

/**
 * The day's commit count and code churn.
 *
 * The commit count comes from the heatmap (the only place the per-day commit
 * count exists — the summary is about lines changed, not commits), and the
 * churn from the SAME summary the dashboard's own strip reads, with the SAME
 * scope. So the card and the page can never disagree.
 *
 * WHICH DAY. The card follows the DASHBOARD's day (shared scope, which
 * defaults to the last FINISHED day, `utils/dates.getYesterday`), not the
 * wall-clock today: the daily-code dashboard opens on the last FINISHED day,
 * and the summary strip calls that day's numbers 今日目标. A card anchored on
 * the wall-clock today would read "0 commits" on a morning while the dashboard
 * next to it shows 371 lines — two numbers for the same question, and the one
 * the user can check against the page would be the wrong one.
 *
 * WHICH SCOPE. The same argument, one step further: the summary aggregates
 * either the starred projects or the whole library, and the dashboard's strip
 * follows the toggle. So the card takes the toggle from the same shared
 * scope — otherwise the card says "371 lines" while the strip beside it says
 * "10,327", and the user has no way to tell which one is the app's answer.
 *
 * The one number that is deliberately NOT scoped with the rest: the commit
 * count. The heatmap has no starred filter, so it always counts the user's
 * commits across every repository — which is also what the dashboard's
 * heatmap shows. The footer says so, out loud.
 *
 * A failed read says so. It does NOT fall back to zeros: "0 commits today"
 * is a fact about the user's day, and a card that says it when the read
 * failed is a lie the user cannot detect.
 */
export function todayCard(
  t: TFunc,
  date: string = getYesterday(),
  starredOnly = true,
): Promise<AgentCard> {
  return Promise.all([
    getSummary(date, starredOnly),
    getHeatmapData(0),
  ]).then(([summary, heat]) => {
    const day = heat.days.find(d => d.date === date)
    const isToday = date === getYesterday()
    return {
      title: isToday ? t('agent.card.today.title') : t('agent.card.codeVolume.title'),
      subtitle: t('agent.card.today.subtitle', { date }),
      metrics: [
        { label: t('agent.metric.commits'), value: fmt(day?.commits) },
        { label: t('agent.metric.myAdded'), value: signed(summary.my_added) },
        { label: t('agent.metric.myDeleted'), value: minus(summary.my_deleted) },
        { label: t('agent.metric.myFiles'), value: fmt(summary.my_files) },
        { label: t('agent.metric.repos'), value: fmt(summary.repo_count) },
      ],
      footer: starredOnly
        ? t('agent.card.today.footerStarred')
        : t('agent.card.today.footerAll'),
      to: '/dashboard',
      toLabel: t('agent.action.goDashboard'),
    }
  }).catch(() => ({
    title: t('agent.card.today.title'),
    body: t('agent.error.load'),
  }))
}

/** The last seven days, day by day. */
export function weekCard(t: TFunc, date: string = getYesterday()): Promise<AgentCard> {
  const from = daysBeforeISO(date, 6)
  const today = date
  return getHeatmapData(0).then(heat => {
    const days = heat.days.filter(d => d.date >= from && d.date <= today)
    const commits = days.reduce((sum, d) => sum + (d.commits || 0), 0)
    const active = days.filter(d => (d.commits || 0) > 0).length
    return {
      title: t('agent.card.week.title'),
      subtitle: t('agent.card.week.subtitle', { from, to: today }),
      metrics: [
        { label: t('agent.metric.commits7d'), value: fmt(commits) },
        { label: t('agent.metric.activeDays'), value: fmt(active) },
      ],
      items: days
        .slice()
        .reverse()
        .map(d => ({
          title: t('agent.card.week.day', { date: d.date }),
          meta: t('agent.card.week.commits', { n: d.commits || 0 }),
        })),
      to: '/dashboard',
      toLabel: t('agent.action.goDashboard'),
    }
  }).catch(() => ({
    title: t('agent.card.week.title'),
    body: t('agent.error.load'),
  }))
}

/** The most recent commit in the library. */
export function lastCommitCard(t: TFunc): Promise<AgentCard> {
  return getStatusBar().then(s => ({
    title: t('agent.card.lastCommit.title'),
    body: s.last_commit_msg || t('agent.card.lastCommit.empty'),
    footer: [s.last_commit_repo, s.last_commit_branch, s.last_commit_time].filter(Boolean).join(' · '),
  })).catch(() => ({
    title: t('agent.card.lastCommit.title'),
    body: t('agent.error.load'),
  }))
}

/** Todos and notes, by project. */
export function pendingCard(t: TFunc): Promise<AgentCard> {
  return Promise.all([
    getTodoCounts().catch(() => []),
    getNoteCounts().catch(() => []),
    getProjects().catch(() => []),
  ]).then(([todos, notes, projects]) => {
    const names = new Map(projects.map(p => [p.id, p.name]))
    const todoTotal = todos.reduce((sum, x) => sum + (x.total || 0), 0)
    const noteTotal = notes.reduce((sum, x) => sum + (x.count || 0), 0)
    return {
      title: t('agent.card.pending.title'),
      metrics: [
        { label: t('agent.metric.todos'), value: fmt(todoTotal) },
        { label: t('agent.metric.notes'), value: fmt(noteTotal) },
      ],
      items: todos
        .filter(x => (x.total || 0) > 0)
        .slice(0, 8)
        .map(x => ({
          title: names.get(x.project_id) || t('agent.card.pending.unknownProject'),
          meta: t('agent.card.pending.todoCount', { n: x.total || 0 }),
          to: `/project/${x.project_id}`,
        })),
      to: '/dashboard',
      toLabel: t('agent.action.goDashboard'),
    }
  }).catch(() => ({
    title: t('agent.card.pending.title'),
    body: t('agent.error.load'),
  }))
}

/** One project's own picture: stack, activity, contributors. */
export function projectCard(t: TFunc, projectId: number): Promise<AgentCard> {
  return getProjectOverview(projectId).then(o => ({
    title: t('agent.card.project.title'),
    subtitle: o.activity?.last_commit_date
      ? t('agent.card.project.lastCommit', { date: o.activity.last_commit_date })
      : undefined,
    metrics: [
      { label: t('agent.metric.totalCommits'), value: fmt(o.activity?.total_commits) },
      { label: t('agent.metric.commitRate30d'), value: fmt(o.activity?.commit_rate_30d) },
      { label: t('agent.metric.activeDays'), value: fmt(o.activity?.active_days) },
      { label: t('agent.metric.techStack'), value: fmt(o.tech_stack?.length) },
    ],
    items: (o.tech_stack || []).slice(0, 8).map(x => ({ title: x.name })),
    to: `/project/${projectId}`,
    toLabel: t('agent.action.goProject'),
  })).catch(() => ({
    title: t('agent.card.project.title'),
    body: t('agent.error.load'),
  }))
}

/** A project's recent commits. */
export function projectCommitsCard(t: TFunc, projectId: number): Promise<AgentCard> {
  return getProjectOverview(projectId).then(o => ({
    title: t('agent.card.projectCommits.title'),
    items: (o.recent_commits || []).slice(0, 8).map(c => ({
      title: c.message,
      meta: [c.time, c.author, c.branch].filter(Boolean).join(' · '),
    })),
    to: `/project/${projectId}`,
    toLabel: t('agent.action.goProject'),
  })).catch(() => ({
    title: t('agent.card.projectCommits.title'),
    body: t('agent.error.load'),
  }))
}

/** A project's tech stack and language split. */
export function projectStackCard(t: TFunc, projectId: number): Promise<AgentCard> {
  return getProjectOverview(projectId).then(o => ({
    title: t('agent.card.projectStack.title'),
    items: [
      ...(o.tech_stack || []).map(x => ({ title: x.name, meta: x.category })),
      ...(o.languages || []).map(l => ({ title: l.language, meta: fmt(l.count) })),
    ].slice(0, 12),
    to: `/project/${projectId}`,
    toLabel: t('agent.action.goProject'),
  })).catch(() => ({
    title: t('agent.card.projectStack.title'),
    body: t('agent.error.load'),
  }))
}

/** The project's own commits on the scoped day. */
export function projectTodayCard(
  t: TFunc,
  projectId: number,
  date: string = getYesterday(),
): Promise<AgentCard> {
  return getHeatmapData(projectId).then(heat => {
    const day = heat.days.find(d => d.date === date)
    const isToday = date === getYesterday()
    return {
      title: isToday ? t('agent.card.projectToday.title') : t('agent.card.projectVolume.title'),
      subtitle: t('agent.card.today.subtitle', { date }),
      metrics: [
        { label: t('agent.metric.commits'), value: fmt(day?.commits) },
        { label: t('agent.metric.added'), value: signed(day?.lines_added) },
        { label: t('agent.metric.deleted'), value: minus(day?.lines_deleted) },
      ],
      to: `/project/${projectId}`,
      toLabel: t('agent.action.goProject'),
    }
  }).catch(() => ({
    title: t('agent.card.projectToday.title'),
    body: t('agent.error.load'),
  }))
}

/** The recent handoff notes — "where did I leave off". */
export function handoffCard(t: TFunc): Promise<AgentCard> {
  return listRecentHandoffs(5).then(entries => ({
    title: t('agent.card.handoff.title'),
    items: entries.map(e => ({
      title: e.title,
      meta: [e.project_name, e.updated_at].filter(Boolean).join(' · '),
    })),
    to: '/dashboard?view=sessions',
    toLabel: t('agent.action.goSessions'),
  })).catch(() => ({
    title: t('agent.card.handoff.title'),
    body: t('agent.error.load'),
  }))
}

/** Pages waiting for a human decision. */
export function reviewCard(t: TFunc): Promise<AgentCard> {
  return listPendingWikiPages(0).then(pages => ({
    title: t('agent.card.review.title'),
    metrics: [{ label: t('agent.metric.pendingPages'), value: fmt(pages.length) }],
    items: pages.slice(0, 8).map(p => ({
      title: p.page.title,
      // project_id 0 is the GLOBAL tier, above 0 is a project tier (ADR-0031).
      meta: p.page.project_id
        ? t('agent.card.review.projectScope')
        : t('agent.card.review.globalScope'),
      to: '/review',
    })),
    to: '/review',
    toLabel: t('agent.action.goReview'),
  })).catch(() => ({
    title: t('agent.card.review.title'),
    body: t('agent.error.load'),
  }))
}

/** Where this build lives — the install path and the download folder. */
export function installCard(t: TFunc): Promise<AgentCard> {
  return getInstallInfo().then(info => ({
    title: t('agent.card.install.title'),
    metrics: [
      { label: t('agent.metric.version'), value: info.version },
      { label: t('agent.metric.platform'), value: info.platform },
    ],
    items: [
      { title: t('agent.card.install.path'), meta: info.install_path },
      { title: t('agent.card.install.downloadDir'), meta: info.download_dir },
    ],
  })).catch(() => ({
    title: t('agent.card.install.title'),
    body: t('agent.error.load'),
  }))
}

// --- contexts ----------------------------------------------------------------

/** The scope the dashboard's own controls define. */
export const DEFAULT_SCOPE: DashboardScope = { date: getYesterday(), starredOnly: true }

/**
 * The context for a route.
 *
 * `scope` is the dashboard's shared scope (the day and the starred filter).
 * The cards that answer "what did I do today" are built from it, so the
 * agent and the dashboard are always talking about the same day and the same
 * projects. The chip label follows it too: a chip that says "今日提交量"
 * while the card answers about another day is a chip that lies.
 *
 * `options.questionProjectId` decides what a free-form question is about.
 * Left undefined the question follows the route; set to 0 it is asked about
 * the whole library (ADR-0045 decision 4).
 */
export function buildAgentContext(
  pathname: string,
  t: TFunc,
  projectName?: string,
  scope: DashboardScope = DEFAULT_SCOPE,
  options: AgentContextOptions = {},
): AgentContext {
  const { date, starredOnly } = scope
  const isToday = date === getYesterday()
  const projectMatch = pathname.match(/^\/project\/(\d+)/)
  const routeProjectId = projectMatch ? Number(projectMatch[1]) : 0
  // The question's scope: the caller's choice wins, otherwise the route's.
  const projectId = options.questionProjectId === undefined
    ? routeProjectId
    : options.questionProjectId
  const scopeLabel = options.questionScopeLabel
    ?? (routeProjectId > 0 ? (projectName || t('agent.context.thisProject')) : t('agent.context.library'))
  // Which project the QUICK ANSWERS are about. An explicit scope wins: the
  // user picked that project on purpose, so the bot should answer about it.
  // Without one the route decides, as it always did.
  const actionProjectId = options.questionProjectId !== undefined && options.questionProjectId > 0
    ? options.questionProjectId
    : routeProjectId

  // The labels for the day-scoped actions, so a card and its chip never
  // disagree about which day they are about.
  const dayLabel = isToday ? t('agent.action.todayCommits') : t('agent.action.codeVolume')
  const projectDayLabel = isToday ? t('agent.action.projectToday') : t('agent.action.projectVolume')

  /** The library-wide day card, in the dashboard's scope. */
  const day = () => todayCard(t, date, starredOnly)

  if (actionProjectId) {
    return {
      label: t('agent.context.project'),
      scopeLabel,
      projectId,
      actions: [
        { key: 'overview', label: t('agent.action.projectOverview'), load: () => projectCard(t, actionProjectId) },
        { key: 'today', label: projectDayLabel, load: () => projectTodayCard(t, actionProjectId, date) },
        { key: 'commits', label: t('agent.action.projectCommits'), load: () => projectCommitsCard(t, actionProjectId) },
        { key: 'stack', label: t('agent.action.projectStack'), load: () => projectStackCard(t, actionProjectId) },
      ],
    }
  }

  if (pathname.startsWith('/dashboard')) {
    return {
      label: t('agent.context.dashboard'),
      scopeLabel,
      projectId,
      actions: [
        { key: 'today', label: dayLabel, load: day },
        { key: 'week', label: t('agent.action.weekCommits'), load: () => weekCard(t, date) },
        { key: 'pending', label: t('agent.action.pending'), load: () => pendingCard(t) },
        { key: 'last', label: t('agent.action.lastCommit'), load: () => lastCommitCard(t) },
      ],
    }
  }

  if (pathname.startsWith('/review')) {
    return {
      label: t('agent.context.review'),
      scopeLabel,
      projectId,
      actions: [
        { key: 'pending', label: t('agent.action.pendingPages'), load: () => reviewCard(t) },
        { key: 'today', label: dayLabel, load: day },
      ],
    }
  }

  if (pathname.startsWith('/knowledge')) {
    return {
      label: t('agent.context.knowledge'),
      scopeLabel,
      projectId,
      actions: [
        { key: 'pending', label: t('agent.action.pending'), load: () => pendingCard(t) },
        { key: 'today', label: dayLabel, load: day },
      ],
    }
  }

  if (pathname.startsWith('/wiki')) {
    return {
      label: t('agent.context.wiki'),
      scopeLabel,
      projectId,
      actions: [
        { key: 'handoff', label: t('agent.action.handoff'), load: () => handoffCard(t) },
        { key: 'today', label: dayLabel, load: day },
      ],
    }
  }

  if (pathname.startsWith('/settings')) {
    return {
      label: t('agent.context.settings'),
      scopeLabel,
      projectId,
      actions: [
        { key: 'install', label: t('agent.action.installInfo'), load: () => installCard(t) },
        { key: 'today', label: dayLabel, load: day },
      ],
    }
  }

  if (pathname === '/') {
    return {
      label: t('agent.context.home'),
      scopeLabel,
      projectId,
      actions: [
        { key: 'handoff', label: t('agent.action.handoff'), load: () => handoffCard(t) },
        { key: 'today', label: dayLabel, load: day },
      ],
    }
  }

  return {
    label: t('agent.context.other'),
    scopeLabel,
    projectId,
    actions: [
      { key: 'today', label: dayLabel, load: day },
    ],
  }
}
