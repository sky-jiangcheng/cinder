// api/transport.ts — Dual-mode transport:
//   - Inside the Wails webview, invokes Go methods via window.go.app.App
//     (package "app" / struct "App" -> go.app.App.MethodName)
//   - Standalone (npm run dev in a plain browser), falls back to HTTP fetch

import type { ImportCompletedEvent } from './types'

interface WailsApp {
  [method: string]: (...args: unknown[]) => Promise<unknown>
}

interface WailsGlobal {
  go?: {
    app?: {
      App?: WailsApp
    }
  }
}

const isWails = (): boolean => {
  if (typeof window === 'undefined') return false
  const w = window as unknown as WailsGlobal
  return !!w.go?.app?.App
}

/**
 * True when the frontend runs inside the Wails desktop webview (as opposed
 * to a plain browser tab talking to the headless server over /api/rpc).
 *
 * A few capabilities exist only in the desktop shell — opening a URL in the
 * OS browser, for one. The frontend needs to know which runtime it is in to
 * pick the right path instead of failing with a Wails-only error.
 */
export function isDesktopRuntime(): boolean {
  return isWails()
}

function wail<T>(method: string, ...args: unknown[]): Promise<T> {
  const w = window as unknown as WailsGlobal
  const app = w.go?.app?.App
  if (!app) {
    throw new Error('Wails runtime not available')
  }
  const fn = app[method]
  if (typeof fn !== 'function') {
    throw new Error(`Wails method not found: ${method}`)
  }
  return fn(...args) as Promise<T>
}

const BASE = '/api'

// Browser/standalone transport: a single JSON-RPC bridge to the bound App
// (httpapi /api/rpc), so every Wails binding works over HTTP without a REST
// route per method. Desktop mode uses window.go.app.App directly (wail()).
async function rpc<T>(method: string, args: unknown[]): Promise<T> {
  const res = await fetch(BASE + '/rpc', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ method, args }),
  })
  const data = await res.json().catch(() => ({ error: 'HTTP ' + res.status }))
  if (!res.ok || data.error) throw new Error(data.error || `RPC ${method} failed`)
  return data.result as T
}

/** A single call routed through whichever transport is active. */
export function call<T>(opts: {
  /** Wails binding method name. */
  method: string
  /** Arguments for the Wails binding call. */
  args?: unknown[]
  /** HTTP path (relative to /api) — unused in RPC mode, kept for compatibility. */
  path?: string
  /** HTTP request options — unused in RPC mode. */
  init?: RequestInit
}): Promise<T> {
  if (isWails()) return wail<T>(opts.method, ...(opts.args ?? []))
  return rpc<T>(opts.method, opts.args ?? [])
}

// --- Connection health tracking ------------------------------------------------

export type ConnectionKind = 'ok' | 'offline' | 'backend-down'

let currentKind: ConnectionKind = 'ok'
const listeners = new Set<(kind: ConnectionKind) => void>()

export function getConnectionKind(): ConnectionKind { return currentKind }

export function subscribeConnection(cb: (kind: ConnectionKind) => void): () => void {
  listeners.add(cb)
  cb(currentKind)
  return () => { listeners.delete(cb) }
}

function notify(kind: ConnectionKind) {
  if (kind === currentKind) return
  currentKind = kind
  for (const l of listeners) l(kind)
}

/** Shared Health-response interpretation: normalizes legacy shapes and
 * publishes the connection state to subscribers. */
function interpretHealth(r: { ok?: boolean; status?: string } | undefined | null): boolean {
  const ok = r?.ok ?? r?.status === 'ok'
  notify(ok ? 'ok' : 'backend-down')
  return ok
}

const onHealthFailure = (): boolean => {
  notify('backend-down')
  return false
}

/** Health check: resolves true when backend responds, false otherwise. */
export function checkHealth(): Promise<boolean> {
  if (!isWails()) {
    // Browser/standalone mode: the RPC bridge is the real transport, so probe
    // it with an actual Health call. A constant true here would keep the
    // connection banner green even when the headless server is down.
    return rpc<{ ok?: boolean; status?: string }>('Health', [])
      .then(interpretHealth)
      .catch(onHealthFailure)
  }
  return wail<{ ok?: boolean; status?: string }>('Health')
    .then(interpretHealth)
    .catch(onHealthFailure)
}

/** Start periodic health polling. Returns a cleanup function. */
export function startHealthPoll(intervalMs = 30_000): () => void {
  let timer: number | null = null
  let stopped = false
  const schedule = () => {
    if (!stopped) timer = window.setTimeout(tick, intervalMs) as unknown as number
  }
  const tick = () => {
    checkHealth().finally(schedule)
  }
  tick()
  return () => {
    stopped = true
    if (timer !== null) clearTimeout(timer)
  }
}

/** Register online/offline listeners so we detect network state changes. */
function initOnlineDetection() {
  const update = () => {
    if (!navigator.onLine) { notify('offline'); return }
    checkHealth()
  }
  window.addEventListener('online', update)
  window.addEventListener('offline', update)
}
initOnlineDetection()

// --- Streaming evidence-backed ask ---------------------------------------------
//
// The one call that cannot go through call(): a multi-minute LLM reply should
// render as it forms, not after a spinner. Two transports, same wire contract:
//
//   - Wails mode: StartAskStream returns a stream id immediately; the backend
//     pushes "ai.ask.stream" events tagged with it (delta / done / error).
//   - Browser/standalone: POST /api/ai/ask-stream, an SSE stream of the same
//     events (done carries reply + evidence + truncated, mirroring the
//     AskAIWithEvidence JSON shape).
//
// Returns a cancel function: unsubscribe the event / abort the fetch.

import type { Evidence, EvidenceAnswer } from './types'

export interface AskStreamHandlers {
  /** Called once per content fragment as it decodes. */
  onDelta: (delta: string) => void
  /** Terminal success — reply, evidence and truncation flag. */
  onDone: (ans: EvidenceAnswer) => void
  /** Terminal failure (transport or backend-reported error). */
  onError: (err: Error) => void
}

function normalizeDone(ev: Record<string, unknown>): EvidenceAnswer {
  return {
    reply: String(ev.reply ?? ''),
    evidence: (ev.evidence ?? { items: [], dropped: 0, elapsed_ms: 0, truncated: false }) as Evidence,
    truncated: !!ev.truncated,
  }
}

function wailAskStream(projectId: number, question: string, handlers: AskStreamHandlers): () => void {
  let disposed = false
  let off: (() => void) | null = null
  wail<{ stream_id: string }>('StartAskStream', projectId, question)
    .then(({ stream_id }) => {
      if (disposed) return
      const w = window as unknown as RuntimeGlobal
      if (!w.runtime?.EventsOn) {
        handlers.onError(new Error('Wails runtime events unavailable'))
        return
      }
      off = w.runtime.EventsOn('ai.ask.stream', (data) => {
        const ev = (data ?? {}) as Record<string, unknown>
        if (String(ev.stream_id) !== stream_id) return
        if (typeof ev.delta === 'string') {
          handlers.onDelta(ev.delta)
        } else if (ev.error) {
          handlers.onError(new Error(String(ev.error)))
        } else if (ev.done) {
          handlers.onDone(normalizeDone(ev))
        }
      })
    })
    .catch(e => handlers.onError(e instanceof Error ? e : new Error(String(e))))
  return () => {
    disposed = true
    off?.()
  }
}

function httpAskStream(projectId: number, question: string, handlers: AskStreamHandlers): () => void {
  const ctrl = new AbortController()
  fetch(`${BASE}/ai/ask-stream`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ project_id: projectId, question }),
    signal: ctrl.signal,
  })
    .then(async (res) => {
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
      // SSE parse: frames are "data: {...}\n\n"; a frame may split across
      // chunks, so decode into a buffer and cut on the blank line.
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      const handleFrame = (frame: string) => {
        for (const line of frame.split('\n')) {
          if (!line.startsWith('data:')) continue
          const payload = line.slice(5).trim()
          if (!payload) continue
          let ev: Record<string, unknown>
          try {
            ev = JSON.parse(payload) as Record<string, unknown>
          } catch {
            continue // non-JSON data: line (e.g. a stray comment) — skip
          }
          if (typeof ev.delta === 'string') {
            handlers.onDelta(ev.delta)
          } else if (ev.error) {
            handlers.onError(new Error(String(ev.error)))
          } else if (ev.done) {
            handlers.onDone(normalizeDone(ev))
          }
        }
      }
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let idx: number
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          handleFrame(buf.slice(0, idx))
          buf = buf.slice(idx + 2)
        }
      }
      if (buf.trim()) handleFrame(buf) // trailing frame without final blank line
    })
    .catch(e => {
      if (e instanceof DOMException && e.name === 'AbortError') return
      handlers.onError(e instanceof Error ? e : new Error(String(e)))
    })
  return () => ctrl.abort()
}

/**
 * Streaming evidence-backed ask, routed per transport. The returned cancel
 * function stops delivery (unsubscribes / aborts); the terminal handler is not
 * called after cancel.
 */
export function askAIWithEvidenceStream(
  projectId: number,
  question: string,
  handlers: AskStreamHandlers,
): () => void {
  if (isWails()) return wailAskStream(projectId, question, handlers)
  return httpAskStream(projectId, question, handlers)
}

// --- Wails runtime events -----------------------------------------------------

interface RuntimeGlobal {
  runtime?: {
    EventsOn?: (name: string, cb: (data: unknown) => void) => () => void
  }
}

/**
 * Subscribe to the import.completed Wails event. Returns an unsubscribe
 * function when the runtime provides one (Wails v2.5+); callers must invoke
 * it on cleanup to avoid stacking duplicate listeners.
 */
export function listenImportCompleted(cb: (data: ImportCompletedEvent) => void): () => void {
  const w = window as unknown as RuntimeGlobal
  if (w.runtime?.EventsOn) {
    const off = w.runtime.EventsOn('import.completed', (data) => {
      const d = (data ?? {}) as Partial<ImportCompletedEvent>
      cb({
        source: d.source ?? 'unknown',
        created: d.created ?? 0,
        updated: d.updated ?? 0,
        skipped: d.skipped ?? 0,
        error: d.error,
      })
    })
    return () => off?.()
  }
  return () => {}
}
