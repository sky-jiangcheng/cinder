// components/AgentBot.tsx — the floating agent entry.
//
// What it is. A small robot you can DRAG anywhere on screen, and a panel that
// opens when you click it. It replaces the old "+" capture button, which sat
// in the bottom-right corner and landed exactly on top of the chat page's
// send button — two controls fighting for the same pixels, with the send
// button the one that matters.
//
// Why it knows things. The panel is not a chat window with a logo: it knows
// which page you are on and offers the questions THAT page can answer — the
// dashboard's commit counts, a project's stack and activity, the pages waiting
// for review. The quick answers come from local data (exact, instant, and they
// work with no AI endpoint configured); free-form questions go to the AI with
// the page's scope attached.
//
// The shape of an answer is a CARD, not a wall of text: a title, a few
// labelled numbers, a short list. A card can be looked at and acted on (it
// links to the page it came from), which a chat bubble cannot.
//
// The position is the user's: drag it, and it stays where they put it
// (localStorage), because a floating control that moves itself is a floating
// control that gets in the way.

import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import QuickCapturePanel from './QuickCapturePanel'
import { buildAgentContext, type AgentCard, type AgentContext } from './agent/agentContext'
import { NAV_ENTRIES } from './navEntries'
import {
  getDashboardScope,
  subscribeDashboardScope,
} from '../state/dashboardScope'
import {
  askAIWithEvidenceStream,
  createNoteWithMeta,
  fileAnswerAsPage,
  getConfig,
  getProjectDetail,
  getProjects,
  listenAskBot,
  type Evidence,
} from '../api/client'
import type { PushToast } from '../hooks/useToast'
import s from './AgentBot.module.css'

interface Props {
  onToast: PushToast
}

/** Where the bot's position is remembered between launches. */
const POS_KEY = 'repotory.agentBot.pos'

/** The bot's own size — the drag clamp and the default offset need it. */
const BOT_SIZE = 52

/** How far the pointer may travel before the gesture counts as a drag. */
const DRAG_THRESHOLD = 4

/** The panel's width; the placement maths needs it. */
const PANEL_WIDTH = 420

interface Point {
  x: number
  y: number
}

/** Read the remembered position, or null when there is none. */
function loadPos(): Point | null {
  try {
    const raw = window.localStorage.getItem(POS_KEY)
    if (!raw) return null
    const p = JSON.parse(raw) as Point
    if (typeof p?.x !== 'number' || typeof p?.y !== 'number') return null
    return p
  } catch {
    return null
  }
}

function savePos(p: Point) {
  try {
    window.localStorage.setItem(POS_KEY, JSON.stringify(p))
  } catch {
    // A window without storage (or a full quota) must not break the app:
    // the bot simply forgets its position next time.
  }
}

/** Keep a point inside the viewport (with a small margin). */
function clampPos(p: Point): Point {
  const margin = 8
  const maxX = Math.max(margin, window.innerWidth - BOT_SIZE - margin)
  const maxY = Math.max(margin, window.innerHeight - BOT_SIZE - margin)
  return {
    x: Math.min(Math.max(p.x, margin), maxX),
    y: Math.min(Math.max(p.y, margin), maxY),
  }
}

/**
 * The default position: the bottom-LEFT of the centre column.
 *
 * Bottom-left, not bottom-right, and that is the whole point of the fix: the
 * chat page's send button lives at the bottom-right of the composer, and the
 * old button sat on top of it. The left column edge is free on every page,
 * and the bot is draggable for anyone who wants it elsewhere.
 */
function defaultPos(): Point {
  const railWidth = 72 // SideRail's width — the app's own constant, not a guess
  return clampPos({
    x: railWidth + 20,
    y: window.innerHeight - BOT_SIZE - 56,
  })
}

export default function AgentBot({ onToast }: Props) {
  const { t } = useTranslation()
  const location = useLocation()
  const navigate = useNavigate()

  const [pos, setPos] = useState<Point>(() => loadPos() ?? defaultPos())
  const posRef = useRef(pos)
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<'ask' | 'capture'>('ask')
  const [cards, setCards] = useState<AgentCard[]>([])
  const [loadingKey, setLoadingKey] = useState<string | null>(null)
  const [question, setQuestion] = useState('')
  const [answer, setAnswer] = useState('')
  const [asking, setAsking] = useState(false)
  const [evidence, setEvidence] = useState<Evidence | null>(null)
  const [configured, setConfigured] = useState<boolean | null>(null)
  // Project names, fetched per project page: the name belongs to the
  // PROJECT, and a project page can change while the panel is open.
  const [projectNames, setProjectNames] = useState<Record<number, string>>({})
  // Filing an answer needs a project; the whole-library scope (0) is not a
  // filing target. The first project is the home for a filed answer, the
  // same fallback the old ask panel used.
  const [filingProjectId, setFilingProjectId] = useState(0)
  // The bot's own scope (ADR-0045 decision 4): the whole library by
  // default, any project on request. It is deliberately NOT the page's
  // scope — the desktop bot is a global assistant, the page is only its
  // current topic.
  const [scopeProjectId, setScopeProjectId] = useState(0)
  // The scope picker is open.
  const [scopeOpen, setScopeOpen] = useState(false)
  // The projects the scope picker offers.
  const [projects, setProjects] = useState<Array<{ id: number; name: string }>>([])

  const dragRef = useRef<{
    pointerId: number
    startX: number
    startY: number
    originX: number
    originY: number
    moved: boolean
  } | null>(null)

  // The quick action whose card is on screen. The KEY is remembered, not the
  // action itself: the action closures are rebuilt whenever the scope
  // changes, and re-running a stale closure would re-read the old scope.
  const lastActionKeyRef = useRef<string | null>(null)

  const setPosBoth = useCallback((p: Point) => {
    posRef.current = p
    setPos(p)
  }, [])

  // The context follows the route: the panel's questions are the ones the
  // CURRENT page can answer.
  const routeProjectId = Number(location.pathname.match(/^\/project\/(\d+)/)?.[1] || 0)
  const projectName = projectNames[routeProjectId] ?? ''
  // The scope project's name — the chip's text and the question's scope.
  const scopeProjectName = scopeProjectId > 0
    ? (projects.find(p => p.id === scopeProjectId)?.name ?? '')
    : ''

  // The dashboard's scope — the day it shows and whether it counts only the
  // starred projects. The cards that answer "today's code" are built from it,
  // so the agent's numbers and the dashboard's numbers are the same numbers.
  const scope = useSyncExternalStore(subscribeDashboardScope, getDashboardScope, getDashboardScope)

  const context: AgentContext = useMemo(
    () => buildAgentContext(location.pathname, t, projectName, scope, {
      // The question's scope is the bot's OWN, not the page's: the desktop
      // bot asks about the whole library unless it is told otherwise
      // (ADR-0045 decision 4 — an explicit override of ADR-0041's
      // "a project page asks about that project").
      questionProjectId: scopeProjectId,
      questionScopeLabel: scopeProjectId > 0
        ? (scopeProjectName || t('agent.context.thisProject'))
        : t('agent.context.library'),
    }),
    [location.pathname, t, projectName, scope, scopeProjectId, scopeProjectName]
  )

  // What the cards on screen were read in. A card that outlives its
  // context disagrees with the page — the exact thing the shared scope
  // exists to prevent (ADR-0041 decision 5).
  const contextKey = `${location.pathname}#${scopeProjectId}`

  // Direct mode needs a configured chat endpoint; without one the quick
  // actions still work (they are local) and the question box says so.
  useEffect(() => {
    let cancelled = false
    getConfig().then(d => {
      if (cancelled) return
      const c = d.config || {}
      setConfigured(!!(c.ai_chat_base_url && c.ai_chat_model))
    }).catch(() => { if (!cancelled) setConfigured(false) })
    return () => { cancelled = true }
  }, [])

  // The project name is only needed to label the scope, so it is fetched
  // when — and only when — the user is on a project page.
  useEffect(() => {
    const m = location.pathname.match(/^\/project\/(\d+)/)
    if (!m) return
    const id = Number(m[1])
    let cancelled = false
    getProjectDetail(id)
      .then(d => { if (!cancelled) setProjectNames(prev => ({ ...prev, [id]: d.name })) })
      .catch(() => { /* the scope falls back to the generic label */ })
    return () => { cancelled = true }
  }, [location.pathname])

  // The project list: the filing target is its first entry, and the scope
  // picker offers the rest. One read serves both.
  useEffect(() => {
    let cancelled = false
    getProjects()
      .then(ps => {
        if (cancelled) return
        setProjects(ps.map(p => ({ id: p.id, name: p.name })))
        setFilingProjectId(ps[0]?.id ?? 0)
      })
      .catch(() => { if (!cancelled) setFilingProjectId(0) })
    return () => { cancelled = true }
  }, [])

  // Opening the panel immediately shows ONE card — the page's headline
  // answer. An agent that opens to an empty box has told the user nothing;
  // one that opens to "today: 12 commits" has already answered a question
  // they were going to ask.
  //
  // The load is triggered by the OPEN itself rather than by an effect: the
  // panel is opened by a click, and a click can set state synchronously
  // where an effect cannot (the repo's react-hooks rule).
  const openPanel = () => {
    setOpen(true)
    const first = context.actions[0]
    if (!first) return
    lastActionKeyRef.current = first.key
    setLoadingKey(first.key)
    first.load()
      .then(card => setCards([card]))
      .catch(() => setCards([]))
      .finally(() => setLoadingKey(null))
  }

  // The desktop app's menu bar is its status bar: with the window closed
  // the app keeps running, and "Ask Repotory…" brings the bot back
  // (ADR-0045 decision 2). The event exists only in the desktop shell,
  // so in a plain browser tab the listener is a no-op.
  const openPanelRef = useRef<() => void>(() => {})
  useEffect(() => { openPanelRef.current = openPanel })
  useEffect(() => listenAskBot(() => { openPanelRef.current() }), [])

  const runAction = (key: string, load: () => Promise<AgentCard>) => {
    lastActionKeyRef.current = key
    setLoadingKey(key)
    load()
      .then(card => setCards(prev => [card, ...prev].slice(0, 5)))
      .catch(() => onToast({ kind: 'error', title: t('agent.error.title'), message: t('agent.error.load') }))
      .finally(() => setLoadingKey(null))
  }

  // A card on screen must not outlive the scope it was read in: the user can
  // change the dashboard's day or its starred filter while the panel is open,
  // and a card that still shows the old scope's numbers would disagree with
  // the dashboard again — the exact thing the shared scope exists to prevent.
  const scopeRef = useRef(scope)
  const contextKeyRef = useRef(contextKey)
  useEffect(() => {
    const scopeChanged = scopeRef.current.date !== scope.date
      || scopeRef.current.starredOnly !== scope.starredOnly
    const contextChanged = contextKeyRef.current !== contextKey
    scopeRef.current = scope
    contextKeyRef.current = contextKey
    if ((!scopeChanged && !contextChanged) || !open) return
    const key = lastActionKeyRef.current
    const action = key ? context.actions.find(a => a.key === key) : undefined
    // A new context may not answer the old question: the module changed,
    // so the action that was on screen is gone. The new context's own
    // first action takes its place.
    const next = action ?? context.actions[0]
    if (!next) return
    // The re-read runs in a microtask rather than in the effect body: the
    // repo's react-hooks rule forbids a synchronous setState in an effect.
    Promise.resolve()
      .then(() => { setLoadingKey(next.key); return next.load() })
      .then(card => setCards(prev => (contextChanged ? [card] : [card, ...prev].slice(0, 5))))
      .catch(() => {})
      .finally(() => setLoadingKey(null))
  }, [scope, open, context, contextKey])

  const ask = () => {
    const q = question.trim()
    if (!q || asking) return
    setAsking(true)
    setAnswer('')
    setEvidence(null)
    askAIWithEvidenceStream(context.projectId, q, {
      onDelta: delta => setAnswer(prev => prev + delta),
      onDone: res => {
        setEvidence(res.evidence)
        setAsking(false)
        const n = res.evidence?.items?.length ?? 0
        onToast(res.truncated
          ? { kind: 'info', title: t('ai.replyTruncated'), message: t('ai.replyTruncatedHint') }
          : n > 0
            ? { kind: 'success', title: t('ai.replyReceived'), message: t('ai.evidenceCount', { n }) }
            : { kind: 'success', title: t('ai.replyReceived'), message: t('ai.evidenceNone') })
      },
      onError: e => {
        setAsking(false)
        onToast({ kind: 'error', title: t('ai.sendFailed'), message: e instanceof Error ? e.message : undefined })
      },
    })
  }

  // The refs the answer actually cited — the same rule as the old ask panel:
  // positional labels of THIS retrieval, filtered to the ones it issued.
  const citedRefs = useMemo(() => {
    const all = (evidence?.items ?? []).map(i => i.ref)
    const found = Array.from(new Set(
      (answer.match(/\[[PN]\d+\]/g) ?? []).map(x => x.slice(1, -1).toUpperCase()),
    ))
    if (found.length === 0) return all
    return found.filter(r => all.includes(r))
  }, [answer, evidence])

  const saveAnswerAsNote = async () => {
    const q = question.trim()
    if (!q || !answer.trim() || !filingProjectId) return
    try {
      await createNoteWithMeta(
        filingProjectId,
        `## ${t('ai.questionLabel')}\n\n${q}\n\n## ${t('ai.answerLabel')}\n\n${answer.trim()}`,
        { title: q.split('\n')[0].slice(0, 80), kind: 'knowledge', source: 'ai' }
      )
      onToast({ kind: 'success', title: t('ai.saved') })
    } catch (e) {
      onToast({ kind: 'error', title: t('fab.saveFailed'), message: e instanceof Error ? e.message : undefined })
    }
  }

  const saveAnswerAsPage = async () => {
    const q = question.trim()
    if (!q || !answer.trim() || !evidence || !filingProjectId) return
    try {
      const pageId = await fileAnswerAsPage(filingProjectId, q, answer.trim(), evidence, citedRefs)
      onToast({
        kind: 'success',
        title: t('ai.pageSaved'),
        message: t('ai.pageSavedDetail', { id: pageId, refs: citedRefs.length }),
      })
    } catch (e) {
      onToast({ kind: 'error', title: t('ai.pageSaveFailed'), message: e instanceof Error ? e.message : undefined })
    }
  }

  // --- drag ----------------------------------------------------------------

  const onPointerDown = (e: React.PointerEvent<HTMLButtonElement>) => {
    if (e.button !== 0) return
    const p = posRef.current
    dragRef.current = {
      pointerId: e.pointerId,
      startX: e.clientX,
      startY: e.clientY,
      originX: p.x,
      originY: p.y,
      moved: false,
    }
    // Pointer capture keeps the drag alive when the pointer leaves the
    // button. It is optional: an environment without it (a test DOM) simply
    // loses the drag once the pointer leaves.
    e.currentTarget.setPointerCapture?.(e.pointerId)
  }

  const onPointerMove = (e: React.PointerEvent<HTMLButtonElement>) => {
    const d = dragRef.current
    if (!d || d.pointerId !== e.pointerId) return
    const dx = e.clientX - d.startX
    const dy = e.clientY - d.startY
    if (!d.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return
    d.moved = true
    setPosBoth(clampPos({ x: d.originX + dx, y: d.originY + dy }))
  }

  const onPointerUp = (e: React.PointerEvent<HTMLButtonElement>) => {
    const d = dragRef.current
    if (!d || d.pointerId !== e.pointerId) return
    dragRef.current = null
    if (d.moved) {
      // The position is the user's from now on.
      savePos(posRef.current)
    } else if (open) {
      setOpen(false)
    } else {
      openPanel()
    }
  }

  // The bot is a button, so it must also answer the keyboard.
  const onKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      if (open) setOpen(false)
      else openPanel()
    }
  }

  // A window resize can leave the bot outside the new viewport.
  useEffect(() => {
    const onResize = () => setPosBoth(clampPos(posRef.current))
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [setPosBoth])

  // Escape closes the panel — the same affordance every dialog in the app has.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  // The panel is anchored to the bot, but clamped into the window: a bot in
  // the right third flips the panel to its left instead of hanging off the
  // screen edge.
  const panelStyle = useMemo(() => {
    let left = pos.x + BOT_SIZE + 10
    if (left + PANEL_WIDTH > window.innerWidth - 12) left = pos.x - PANEL_WIDTH - 10
    left = Math.min(Math.max(left, 12), Math.max(12, window.innerWidth - PANEL_WIDTH - 12))
    const panelHeight = 560
    const top = Math.min(
      Math.max(pos.y - 40, 12),
      Math.max(12, window.innerHeight - panelHeight - 12)
    )
    return { left, top, width: PANEL_WIDTH }
  }, [pos])

  return (
    <>
      {open && (
        <div
          className={s.panel}
          role="dialog"
          aria-label={t('agent.title')}
          style={{ left: panelStyle.left, top: panelStyle.top, width: panelStyle.width }}
        >
          <div className={s.panelHead}>
            <span className={s.panelAvatar} aria-hidden="true"><Icon name="bot" size={16} /></span>
            <span className={s.panelTitle}>{t('agent.title')}</span>
            <span className={s.contextChip}>{context.label}</span>
            <button className={s.panelClose} onClick={() => setOpen(false)} aria-label={t('common.close')}>
              <Icon name="close" size={14} />
            </button>
          </div>

          {/* The module chips (ADR-0045 decision 3): the bot's own switch
              between the app's five top-level modules. Clicking one moves
              the main area there and the context follows — the bot leads
              the way, it does not keep a private map. */}
          <div className={s.modules} role="group" aria-label={t('agent.modulesTitle')}>
            {NAV_ENTRIES.map(entry => (
              <button
                key={entry.to}
                className={`${s.moduleChip} ${entry.active(location.pathname) ? s.moduleChipActive : ''}`}
                onClick={() => { setScopeOpen(false); navigate(entry.to) }}
              >
                {t(entry.labelKey)}
              </button>
            ))}
          </div>

          {/* The scope chip (ADR-0045 decision 4): what a free-form
              question is about. The whole library by default — the
              desktop bot is a global assistant. */}
          <div className={s.scopeRow}>
            <button
              className={`${s.scopeChip} ${scopeProjectId > 0 ? s.scopeChipLocked : ''}`}
              onClick={() => setScopeOpen(v => !v)}
              aria-expanded={scopeOpen}
              aria-label={t('agent.scope', { scope: context.scopeLabel })}
            >
              <span className={s.scopeName}>{t('agent.scopeLabel')}</span>
              <span className={s.scopeValue}>{context.scopeLabel}</span>
              <Icon name="arrow-down" size={12} />
            </button>
            {scopeOpen && (
              <div className={s.scopeMenu} role="menu">
                <button
                  role="menuitemradio"
                  aria-checked={scopeProjectId === 0}
                  className={`${s.scopeItem} ${scopeProjectId === 0 ? s.scopeItemActive : ''}`}
                  onClick={() => { setScopeProjectId(0); setScopeOpen(false) }}
                >
                  {t('agent.context.library')}
                </button>
                {routeProjectId > 0 && (
                  <button
                    role="menuitemradio"
                    aria-checked={scopeProjectId === routeProjectId}
                    className={`${s.scopeItem} ${scopeProjectId === routeProjectId ? s.scopeItemActive : ''}`}
                    onClick={() => { setScopeProjectId(routeProjectId); setScopeOpen(false) }}
                  >
                    {t('agent.context.thisProject')} · {projectName}
                  </button>
                )}
                {projects.filter(p => p.id !== routeProjectId).map(p => (
                  <button
                    key={p.id}
                    role="menuitemradio"
                    aria-checked={scopeProjectId === p.id}
                    className={`${s.scopeItem} ${scopeProjectId === p.id ? s.scopeItemActive : ''}`}
                    onClick={() => { setScopeProjectId(p.id); setScopeOpen(false) }}
                  >
                    {p.name}
                  </button>
                ))}
              </div>
            )}
          </div>

          {mode === 'ask' ? (
            <>
              <div className={s.actions} role="group" aria-label={t('agent.actionsTitle')}>
                {context.actions.map(a => (
                  <button
                    key={a.key}
                    className={`${s.actionChip} ${loadingKey === a.key ? s.actionChipBusy : ''}`}
                    onClick={() => runAction(a.key, a.load)}
                    disabled={loadingKey !== null}
                  >
                    {loadingKey === a.key ? t('agent.loading') : a.label}
                  </button>
                ))}
              </div>

              <div className={s.cards}>
                {cards.map((c, i) => (
                  <AgentCardView
                    key={`${c.title}-${i}`}
                    card={c}
                    onGo={to => { setOpen(false); navigate(to) }}
                  />
                ))}
              </div>

              {answer.trim() && (
                <div className={s.card}>
                  <div className={s.cardHead}>
                    <span className={s.cardTitle}>{t('agent.card.answer.title')}</span>
                  </div>
                  <div className={s.answerBody}>{answer}</div>
                  {evidence && evidence.items.length > 0 && (
                    <div className={s.evidence}>
                      <div className={s.evidenceHead}>
                        {t('ai.evidenceTitle')} · {t('ai.evidenceCount', { n: evidence.items.length })}
                      </div>
                      <ul className={s.evidenceList}>
                        {evidence.items.map(it => (
                          <li key={`${it.ref}-${it.id}`} className={s.evidenceRow}>
                            <span className={s.evidenceRef}>{`[${it.ref}]`}</span>
                            <span className={s.evidenceTitle}>{it.title}</span>
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                  <div className={s.cardActions}>
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={saveAnswerAsNote}
                      disabled={!filingProjectId}
                    >
                      {t('ai.saveAsNote')}
                    </button>
                    {evidence && evidence.items.length > 0 && (
                      <button
                        className="btn btn-secondary btn-sm"
                        onClick={saveAnswerAsPage}
                        disabled={!filingProjectId}
                      >
                        {t('ai.saveAsPage')}
                        {citedRefs.length > 0 && <span className={s.refCount}>{citedRefs.length}</span>}
                      </button>
                    )}
                  </div>
                </div>
              )}

              <div className={s.askBox}>
                {configured === false && (
                  <p className={s.hint}>
                    {t('ai.notConfigured')}{' '}
                    <button className={s.linkBtn} onClick={() => { setOpen(false); navigate('/settings?tab=ai') }}>
                      {t('ai.goSettings')}
                    </button>
                  </p>
                )}
                <div className={s.askRow}>
                  <textarea
                    className="form-input"
                    rows={2}
                    value={question}
                    onChange={e => setQuestion(e.target.value)}
                    placeholder={t('agent.questionPlaceholder')}
                    aria-label={t('ai.question')}
                    onKeyDown={e => {
                      if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); ask() }
                    }}
                  />
                  <button
                    className="btn btn-primary btn-sm"
                    onClick={ask}
                    disabled={!question.trim() || asking || configured === false}
                  >
                    {asking ? t('ai.asking') : t('ai.send')}
                  </button>
                </div>
              </div>
            </>
          ) : (
            <QuickCapturePanel onToast={onToast} onDone={() => setMode('ask')} />
          )}

          <div className={s.panelFoot}>
            <button className={s.linkBtn} onClick={() => setMode(m => (m === 'ask' ? 'capture' : 'ask'))}>
              {mode === 'ask' ? t('agent.capture') : t('agent.backToAsk')}
            </button>
            <button className={s.linkBtn} onClick={() => { setOpen(false); navigate('/') }}>
              {t('agent.openConsole')}
            </button>
          </div>
        </div>
      )}

      <button
        className={`${s.bot} ${open ? s.botOpen : ''}`}
        style={{ left: pos.x, top: pos.y }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        onKeyDown={onKeyDown}
        aria-label={t('agent.open')}
        title={t('agent.open')}
      >
        <Icon name="bot" size={24} />
      </button>
    </>
  )
}

/** One card: a title, some numbers, a short list. */
function AgentCardView({ card, onGo }: { card: AgentCard; onGo: (to: string) => void }) {
  return (
    <div className={s.card}>
      <div className={s.cardHead}>
        <span className={s.cardTitle}>{card.title}</span>
        {card.subtitle && <span className={s.cardSubtitle}>{card.subtitle}</span>}
      </div>

      {card.metrics && card.metrics.length > 0 && (
        <div className={s.metrics}>
          {card.metrics.map(m => (
            <div key={m.label} className={s.metric}>
              <span className={s.metricValue}>{m.value}</span>
              <span className={s.metricLabel}>{m.label}</span>
            </div>
          ))}
        </div>
      )}

      {card.body && <p className={s.cardBody}>{card.body}</p>}

      {card.items && card.items.length > 0 && (
        <ul className={s.cardList}>
          {card.items.map((it, i) => (
            <li key={`${it.title}-${i}`} className={s.cardItem}>
              {it.to ? (
                <button className={s.cardItemLink} onClick={() => onGo(it.to!)}>{it.title}</button>
              ) : (
                <span className={s.cardItemTitle}>{it.title}</span>
              )}
              {it.meta && <span className={s.cardItemMeta}>{it.meta}</span>}
            </li>
          ))}
        </ul>
      )}

      {card.footer && <p className={s.cardFooter}>{card.footer}</p>}

      {card.to && card.toLabel && (
        <button className={s.cardGo} onClick={() => onGo(card.to!)}>
          {card.toLabel}
          <Icon name="arrow-up" size={12} />
        </button>
      )}
    </div>
  )
}
