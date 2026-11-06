// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import {
  askAIWithEvidenceStream,
  call,
  checkHealth,
  getConnectionKind,
  subscribeConnection,
} from './transport'

// transport.ts registers online/offline listeners at import time; each test
// runs in a fresh jsdom so the global window is available.

beforeEach(() => {
  delete (window as unknown as { go?: unknown }).go
  vi.stubGlobal('fetch', vi.fn())
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('call routing', () => {
  it('routes through the Wails binding when window.go.app.App exists', async () => {
    const appMethod = vi.fn().mockResolvedValue({ ok: true })
    ;(window as unknown as { go: { app: { App: { Health: typeof appMethod } } } }).go = {
      app: { App: { Health: appMethod } },
    }
    const result = await call<{ ok: boolean }>({ method: 'Health', path: '/health' })
    expect(appMethod).toHaveBeenCalled()
    expect(result).toEqual({ ok: true })
  })

  it('falls back to the /api/rpc bridge when Wails is absent', async () => {
    // Browser/standalone mode posts every binding to one JSON-RPC endpoint
    // (httpapi /api/rpc), so there is no per-method REST path — `path` is
    // carried for the desktop/Wails call signature only.
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ result: { items: [1] } }),
    })
    vi.stubGlobal('fetch', fetchMock)
    const result = await call<{ items: number[] }>({ method: 'ListProjects', args: [], path: '/projects' })
    expect(fetchMock).toHaveBeenCalledWith('/api/rpc', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method: 'ListProjects', args: [] }),
    })
    expect(result).toEqual({ items: [1] })
  })

  it('throws the backend error message on HTTP failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      statusText: 'Server Error',
      json: async () => ({ error: 'boom' }),
    }))
    await expect(call({ method: 'X', path: '/x' })).rejects.toThrow('boom')
  })
})

describe('connection health', () => {
  it('starts as ok', () => {
    expect(getConnectionKind()).toBe('ok')
  })

  it('marks backend-down when the Wails Health call rejects', async () => {
    ;(window as unknown as { go: { app: { App: { Health: () => Promise<never> } } } }).go = {
      app: { App: { Health: () => Promise.reject(new Error('no backend')) } },
    }
    const kinds: string[] = []
    subscribeConnection(k => kinds.push(k))
    const ok = await checkHealth()
    expect(ok).toBe(false)
    expect(kinds).toContain('backend-down')
  })

  it('treats a healthy response as ok', async () => {
    ;(window as unknown as { go: { app: { App: { Health: () => Promise<{ ok: boolean }> } } } }).go = {
      app: { App: { Health: () => Promise.resolve({ ok: true }) } },
    }
    const ok = await checkHealth()
    expect(ok).toBe(true)
    expect(getConnectionKind()).toBe('ok')
  })

  it('probes the RPC bridge in browser mode (no Wails)', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ result: { status: 'ok', version: '2.2.0' } }),
    })
    vi.stubGlobal('fetch', fetchMock)
    const ok = await checkHealth()
    expect(fetchMock).toHaveBeenCalledWith('/api/rpc', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method: 'Health', args: [] }),
    })
    expect(ok).toBe(true)
    expect(getConnectionKind()).toBe('ok')
  })

  it('marks backend-down when the RPC bridge probe fails in browser mode', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const kinds: string[] = []
    subscribeConnection(k => kinds.push(k))
    const ok = await checkHealth()
    expect(ok).toBe(false)
    expect(kinds).toContain('backend-down')
  })
})

// --- Streaming ask (askAIWithEvidenceStream) ----------------------------------

// A fake SSE body: one ReadableStream whose chunks split a frame mid-way and
// include the [done] JSON event, so parsing is exercised across chunk
// boundaries.
function sseStream(chunks: Array<string | Uint8Array>): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(controller) {
      for (const c of chunks) {
        controller.enqueue(typeof c === 'string' ? enc.encode(c) : c)
      }
      controller.close()
    },
  })
}

describe('askAIWithEvidenceStream (browser/standalone, SSE)', () => {
  it('streams deltas and resolves with the done payload', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      body: sseStream([
        'data: {"delta":"你"}',
        '\n\n', // frame split mid-way: "你" + "\n\n" arrive in separate chunks
        'data: {"delta":"好"}\n\n',
        'data: {"done":true,"reply":"你好","evidence":{"items":[{"ref":"P1","type":"page","id":1,"title":"t","kind":"k","snippet":"s","rank":1}],"dropped":0,"elapsed_ms":3,"truncated":false},"truncated":false}\n\n',
      ]),
    })
    vi.stubGlobal('fetch', fetchMock)

    const deltas: string[] = []
    let done: { reply: string; truncated: boolean } | null = null
    const errs: Error[] = []

    await new Promise<void>((resolve) => {
      askAIWithEvidenceStream(9, 'how does this work?', {
        onDelta: d => deltas.push(d),
        onToolCall: (_id, _name) => {},
        onToolResult: (_id, _result) => {},
        onDone: ans => {
          done = { reply: ans.reply, truncated: ans.truncated ?? false }
          resolve()
        },
        onError: e => {
          errs.push(e)
          resolve()
        },
      })
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/ai/ask-stream', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ project_id: 9, question: 'how does this work?' }),
    }))
    expect(deltas.join('')).toBe('你好')
    expect(done).toEqual({ reply: '你好', truncated: false })
    expect(errs).toHaveLength(0)
  })

  it('reports the backend error frame instead of done', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      body: sseStream([
        'data: {"delta":"partial"}',
        '\n\n',
        'data: {"error":"AI endpoint unreachable"}\n\n',
      ]),
    }))

    const deltas: string[] = []
    let errMsg = ''
    await new Promise<void>((resolve) => {
      askAIWithEvidenceStream(9, 'q', {
        onDelta: d => deltas.push(d),
        onToolCall: (_id, _name) => {},
        onToolResult: (_id, _result) => {},
        onDone: () => resolve(),
        onError: e => { errMsg = e.message; resolve() },
      })
    })
    expect(deltas.join('')).toBe('partial')
    expect(errMsg).toBe('AI endpoint unreachable')
  })

  it('does not call handlers after cancel', async () => {
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(new Promise(() => {}))) // never resolves
    let called = false
    const cancel = askAIWithEvidenceStream(9, 'q', {
      onDelta: () => { called = true },
      onToolCall: (_id, _name) => {},
      onToolResult: (_id, _result) => {},
      onDone: () => { called = true },
      onError: () => { called = true },
    })
    cancel()
    await new Promise(r => setTimeout(r, 20))
    expect(called).toBe(false)
  })
})

describe('askAIWithEvidenceStream (Wails)', () => {
  it('replays backend events tagged with the stream id', async () => {
    // The binding resolves with a stream id; the runtime then delivers events.
    const appMethod = vi.fn().mockResolvedValue({ stream_id: 'ask-123' })
      ;(window as unknown as { go: { app: { App: { StartAskStream: typeof appMethod } } } }).go = {
        app: { App: { StartAskStream: appMethod } },
      }
    let listener: ((data: unknown) => void) | null = null
      ;(window as unknown as { runtime: { EventsOn: (n: string, cb: (d: unknown) => void) => () => void } }).runtime = {
        EventsOn: (_n, cb) => {
          listener = cb
          return () => {}
        },
      }

    const deltas: string[] = []
    let doneReply = ''
    const errs: Error[] = []
    await new Promise<void>((resolve) => {
      askAIWithEvidenceStream(9, 'q', {
        onDelta: d => deltas.push(d),
        onToolCall: (_id, _name) => {},
        onToolResult: (_id, _result) => {},
        onDone: ans => { doneReply = ans.reply; resolve() },
        onError: e => { errs.push(e); resolve() },
      })
      // Wait for the StartAskStream promise to settle (then handler wiring).
      setTimeout(() => {
        expect(listener).not.toBeNull()
        listener!({ stream_id: 'ask-123', delta: '归' })
        listener!({ stream_id: 'ask-999', delta: 'ignored' }) // another stream
        listener!({ stream_id: 'ask-123', delta: '来' })
        listener!({ stream_id: 'ask-123', done: true, reply: '归来', evidence: { items: [], dropped: 0, elapsed_ms: 0, truncated: false }, truncated: false })
      }, 0)
    })
    expect(appMethod).toHaveBeenCalledWith(9, 'q')
    expect(deltas.join('')).toBe('归来')
    expect(doneReply).toBe('归来')
    expect(errs).toHaveLength(0)
  })

  it('surfaces binding failures as onError', async () => {
    ;(window as unknown as { go: { app: { App: { StartAskStream: () => Promise<never> } } } }).go = {
      app: { App: { StartAskStream: () => Promise.reject(new Error('boom')) } },
    }
    let errMsg = ''
    await new Promise<void>((resolve) => {
      askAIWithEvidenceStream(9, 'q', {
        onDelta: () => {},
        onToolCall: (_id, _name) => {},
        onToolResult: (_id, _result) => {},
        onDone: () => resolve(),
        onError: e => { errMsg = e.message; resolve() },
      })
    })
    expect(errMsg).toBe('boom')
  })
})