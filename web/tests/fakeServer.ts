// A fetch stand-in that answers the local API routes the console calls with
// the documents the Go server returns.
import { vi } from 'vitest'

export const server = {
  state: 'running',
  address: 'http://ovdb.localhost:6832',
  fallback_address: 'http://127.0.0.1:6832',
  port: 6832,
  version: '1.2.3',
  pid: 42,
  started_at: '2026-09-17T10:00:00Z',
  log: '/home/a/.cache/ovdb/run/server.log',
}

export type Handler = (init: RequestInit) => { status: number; body: unknown } | 'network-error'

export function json(status: number, body: unknown) {
  return { status, body }
}

export function installFetch(routes: Record<string, Handler>) {
  const calls: { method: string; path: string; body?: unknown }[] = []
  const fetch = vi.fn(async (path: string, init: RequestInit = {}) => {
    const method = init.method ?? 'GET'
    calls.push({ method, path, body: init.body ? JSON.parse(String(init.body)) : undefined })
    const handler = routes[`${method} ${path}`]
    const answer = handler ? handler(init) : json(404, { schema: 1, error: { code: 'not_found', message: 'nothing', next: [] } })
    if (answer === 'network-error') throw new TypeError('Failed to fetch')
    return new Response(JSON.stringify(answer.body), { status: answer.status, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetch)
  return calls
}

export const defaultRoutes: Record<string, Handler> = {
  'GET /api/local/v1/server': () => json(200, { schema: 1, server }),
  'GET /api/local/v1/status': () =>
    json(200, { schema: 1, version: '1.2.3', server, next: [{ label: 'Open web setup', command: 'ovdb open' }] }),
  'GET /api/local/v1/config': () => json(200, { schema: 1, config: { server: {} }, next: [] }),
}
