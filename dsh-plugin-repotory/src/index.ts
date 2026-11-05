// dsh-plugin-repotory
//
// Exposes Cinder's local git analysis to DeepSeek Harness as three model-
// visible tools. All heavy lifting happens in Cinder's shared Go service
// (internal/service), reached through the headless HTTP server (cmd/server,
// `repotory server`). This plugin is a thin, model-facing client — it owns no
// analysis logic of its own, so the desktop App and Harness never diverge.
//
// Lifecycle (user-chosen: plugin auto-starts the server):
//   1. On load, try the headless server at REPOTORY_HTTP_PORT (default 18765).
//   2. If unreachable and REPOTORY_AUTOSTART != "0", spawn REPOTORY_SERVER_BIN
//      with `server --port <port>`; kill it on plugin unload (ctx.effect).
//   3. Tools retry the HTTP call a few times to tolerate server cold-start.

import type { Context } from '@deepseek-ai/cordis'
import { defineTool } from '@deepseek-ai/dsh-tools'
import { spawn, type ChildProcess } from 'node:child_process'

const DEFAULT_PORT = 18765

export const name = 'repotory'
export const inject = ['tools']

export function apply(ctx: Context) {
  const port = Number(process.env.REPOTORY_HTTP_PORT || DEFAULT_PORT)
  const base = `http://127.0.0.1:${port}`
  const binary = process.env.REPOTORY_SERVER_BIN || ''
  const autoStart = (process.env.REPOTORY_AUTOSTART ?? '1') !== '0'

  // Spawn the headless server if configured; the returned disposer kills it
  // when the plugin unloads (HMR / shutdown), keeping the side effect reversible.
  if (autoStart && binary) {
    ctx.effect(() => {
      const child: ChildProcess = spawn(binary, ['server', '--port', String(port)], {
        stdio: 'ignore',
        env: process.env,
      })
      child.on('error', (e) => ctx.logger.warn(`[repotory] failed to start server: ${e.message}`))
      ctx.logger.info(`[repotory] headless server spawned on ${base}`)
      return () => {
        child.kill('SIGTERM')
      }
    })
  } else if (autoStart && !binary) {
    ctx.logger.warn('[repotory] REPOTORY_SERVER_BIN not set; expecting an already-running server at ' + base)
  }

  // HTTP helper with retries to cover server cold-start latency. Only
  // connection-level failures are retried: an HTTP error status means the
  // server is up and the request is wrong (retrying 4xx/5xx five times just
  // delayed the error), and an abort means the caller gave up.
  async function call(path: string, init?: RequestInit, signal?: AbortSignal): Promise<any> {
    let lastErr: unknown
    for (let i = 0; i < 5; i++) {
      try {
        const res = await fetch(base + path, { ...init, signal })
        if (!res.ok) {
          const body = await res.text()
          throw new HttpError(`repotory api ${res.status}: ${body.slice(0, 200)}`, res.status)
        }
        return await res.json()
      } catch (e) {
        if (signal?.aborted) throw e
        if (e instanceof HttpError) throw e
        lastErr = e
        await new Promise((r) => setTimeout(r, 600))
      }
    }
    throw lastErr
  }

  class HttpError extends Error {
    constructor(message: string, readonly status: number) {
      super(message)
    }
  }

  ctx.tools.register(defineTool({
    name: 'repotory_ai_context',
    description:
      "Return Cinder's AI-readable knowledge-base context (llms.txt style) for this machine: a catalog of discovered projects, their tech stacks, README excerpts and recent knowledge notes. Use when you need background on the user's local repositories before proposing changes.",
    parameters: {},
    output: {
      schema: { type: 'string' },
      render: (_args, value: string) => [{ type: 'text', text: value }],
    },
    async execute(_args: Record<string, never>, exec) {
      const data = await call('/api/ai_context', { method: 'POST' }, exec?.signal)
      return data.markdown as string
    },
  }))

  ctx.tools.register(defineTool({
    name: 'repotory_repo_overview',
    description:
      'Get mined knowledge for a Cinder project: tech stack, language breakdown, dependencies, top contributors, activity/heatmap stats and recent commits. Use to understand a specific local project before modifying it.',
    parameters: {
      project_id: { type: 'number', required: true, description: 'Cinder project ID (integer).' },
    },
    output: {
      schema: { type: 'json' },
      render: (_args, value) => [{ type: 'text', text: JSON.stringify(value) }],
    },
    async execute(args: { project_id: number }, exec) {
      return await call(`/api/project/${args.project_id}/overview`, {}, exec?.signal)
    },
  }))

  ctx.tools.register(defineTool({
    name: 'repotory_search',
    description:
      "Full-text search across the user's local Cinder knowledge base (notes and todos). Use to recall prior decisions, notes or tasks by keyword.",
    parameters: {
      query: { type: 'string', required: true, description: 'Search keywords.' },
      include_todos: { type: 'boolean', description: 'Also search todos (default false: notes only).' },
    },
    output: {
      schema: { type: 'json' },
      render: (_args, value) => [{ type: 'text', text: JSON.stringify(value) }],
    },
    async execute(args: { query: string; include_todos?: boolean }, exec) {
      const q = encodeURIComponent(args.query)
      const all = args.include_todos ? '&all=1' : ''
      return await call(`/api/search?q=${q}${all}`, {}, exec?.signal)
    },
  }))
}
