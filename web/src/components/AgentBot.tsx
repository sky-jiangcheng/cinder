// components/AgentBot.tsx — the floating agent entry.
//
// What it is. A small robot you can DRAG anywhere on screen, and a chat panel
// that opens when you click it: your question, the streamed answer, the tools
// it ran, the references it cited.
//
// Why the panel is only a conversation. It used to mirror the app inside 420
// pixels — five module chips, a scope picker, per-page quick-answer chips and a
// stack of hand-built cards reading the dashboard, the handoff list, the review
// queue. Every one of those showed a fact the model can now fetch itself
// through tool calls (ADR-0051: the tool face is the operation layer). Two
// copies of the app diverge, and the one on screen is the one that is wrong.
// So the chrome went; what is left is the part a conversation cannot get from
// anywhere else — the question, the answer, and what it was built from.
//
// The position is the user's: drag it, and it stays where they put it
// (localStorage), because a floating control that moves itself is a floating
// control that gets in the way.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import {
  askAIWithEvidenceStream,
  createNoteWithMeta,
  fileAnswerAsPage,
  getConfig,
  getProjects,
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

/** The panel's height; the placement maths needs it. */
const PANEL_HEIGHT = 560

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
  const navigate = useNavigate()

  const [pos, setPos] = useState<Point>(() => loadPos() ?? defaultPos())
  const posRef = useRef(pos)
  const [open, setOpen] = useState(false)
  const [question, setQuestion] = useState('')
  const [answer, setAnswer] = useState('')
  const [asking, setAsking] = useState(false)
  const [evidence, setEvidence] = useState<Evidence | null>(null)
  const [toolCalls, setToolCalls] = useState<Array<{ id: string; name: string; done: boolean }>>([])
  const [configured, setConfigured] = useState<boolean | null>(null)
  // Filing an answer needs a project: the first project is where a filed
  // answer lands, the same target the old panel used.
  const [filingProjectId, setFilingProjectId] = useState(0)

  const dragRef = useRef<{
    pointerId: number
    startX: number
    startY: number
    originX: number
    originY: number
    moved: boolean
  } | null>(null)

  const setPosBoth = useCallback((p: Point) => {
    posRef.current = p
    setPos(p)
  }, [])

  // Direct mode needs a configured chat endpoint; without one the panel says
  // so instead of sending a request that can only fail.
  useEffect(() => {
    let cancelled = false
    getConfig().then(d => {
      if (cancelled) return
      const c = d.config || {}
      setConfigured(!!(c.ai_chat_base_url && c.ai_chat_model))
    }).catch(() => { if (!cancelled) setConfigured(false) })
    return () => { cancelled = true }
  }, [])

  // The filing target for "save as note / page".
  useEffect(() => {
    let cancelled = false
    getProjects()
      .then(ps => { if (!cancelled) setFilingProjectId(ps[0]?.id ?? 0) })
      .catch(() => { if (!cancelled) setFilingProjectId(0) })
    return () => { cancelled = true }
  }, [])

  const ask = () => {
    const q = question.trim()
    if (!q || asking) return
    setAsking(true)
    setAnswer('')
    setEvidence(null)
    setToolCalls([])
    // Project 0 is the whole library: the bot is a global assistant, and the
    // model narrows by itself with tools when the question names a project.
    askAIWithEvidenceStream(0, q, {
      onDelta: delta => setAnswer(prev => prev + delta),
      // A tool round's text is a preamble, not the answer: the visible text
      // restarts so the final round reads alone — the same text `onDone`
      // receives. The tool lines above the answer make the wait legible.
      onToolCall: (id, name) => {
        setAnswer('')
        setToolCalls(prev => [...prev, { id, name, done: false }])
      },
      onToolResult: id => {
        setToolCalls(prev => prev.map(tc => (tc.id === id ? { ...tc, done: true } : tc)))
      },
      onDone: res => {
        setEvidence(res.evidence)
        setAsking(false)
        if (res.truncated) {
          onToast({ kind: 'info', title: t('ai.replyTruncated'), message: t('ai.replyTruncatedHint') })
        }
      },
      onError: e => {
        setAsking(false)
        onToast({ kind: 'error', title: t('ai.sendFailed'), message: e instanceof Error ? e.message : undefined })
      },
    })
  }

  // The refs the answer actually cited — positional labels of THIS retrieval,
  // filtered to the ones it issued.
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
    } else {
      setOpen(v => !v)
    }
  }

  // The bot is a button, so it must also answer the keyboard.
  const onKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      setOpen(v => !v)
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
    const top = Math.min(
      Math.max(pos.y - 40, 12),
      Math.max(12, window.innerHeight - PANEL_HEIGHT - 12)
    )
    return { left, top, width: PANEL_WIDTH, maxHeight: PANEL_HEIGHT }
  }, [pos])

  const hasAnswer = answer.trim().length > 0
  const asked = question.trim().length > 0

  return (
    <>
      {open && (
        <div
          className={s.panel}
          role="dialog"
          aria-label={t('agent.title')}
          style={panelStyle}
        >
          <div className={s.panelHead}>
            <span className={s.panelAvatar} aria-hidden="true"><Icon name="bot" size={16} /></span>
            <span className={s.panelTitle}>{t('agent.title')}</span>
            <button className={s.panelClose} onClick={() => setOpen(false)} aria-label={t('common.close')}>
              <Icon name="close" size={14} />
            </button>
          </div>

          <div className={s.thread} role="log">
            {asked && <p className={s.userTurn}>{question.trim()}</p>}

            {!asked && <p className={s.empty}>{t('agent.empty')}</p>}

            {toolCalls.length > 0 && (
              <ul className={s.toolCallList} aria-live="polite" aria-label={t('ai.toolCalling.title')}>
                {toolCalls.map(tc => {
                  // Known tools get their display name from the locales, keyed
                  // by the tool's camelCase name (`query_daily_stats` →
                  // `ai.tool.queryDailyStats`); an unknown one falls back to
                  // its raw function name.
                  const key = `ai.tool.${tc.name.replace(/_([a-z])/g, (_, c: string) => c.toUpperCase())}`
                  const label = t(key)
                  return (
                    <li key={tc.id || tc.name} className={`${s.toolCallItem} ${tc.done ? s.toolCallDone : ''}`}>
                      <span className={s.toolCallName}>{label === key ? tc.name : label}</span>
                      {tc.done
                        ? <span className={s.toolCallDoneLabel}>{t('ai.toolCalling.done')}</span>
                        : <span className={s.toolCallPendingLabel}>{t('ai.toolCalling.calling')}</span>}
                    </li>
                  )
                })}
              </ul>
            )}

            {hasAnswer && <p className={s.answer}>{answer}</p>}

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

            {hasAnswer && (
              <div className={s.answerActions}>
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
            )}
          </div>

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
              disabled={!asked || asking || configured === false}
            >
              {asking ? t('ai.asking') : t('ai.send')}
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
