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

export const serverNext = [
  { label: 'Stop the OVDB server', command: 'ovdb server stop' },
  { label: 'Restart the OVDB server', command: 'ovdb server restart' },
]

export const home = {
  schema: 1,
  status_line: [
    { key: 'home.status.server_running', params: { address: server.address } },
    { key: 'home.status.databases_none' },
  ],
  question_key: 'home.question',
  options: [
    { id: 'create', group: 'primary', label_key: 'home.menu.create_database', description_key: 'home.menu.create_database_help' },
    {
      id: 'server',
      group: 'primary',
      label_key: 'home.menu.start_server',
      web_label_key: 'home.menu.server',
      description_key: 'home.menu.server_help',
      badge: { tone: 'ok', label_key: 'server.badge.running' },
    },
    { id: 'settings', group: 'secondary', label_key: 'home.menu.settings' },
  ],
}

export const defaultRoutes: Record<string, Handler> = {
  'GET /api/local/v1/server': () => json(200, { schema: 1, server, next: serverNext }),
  'GET /api/local/v1/home': () => json(200, home),
  'GET /api/local/v1/status': () =>
    json(200, { schema: 1, version: '1.2.3', server, next: [{ label: 'Open web setup', command: 'ovdb open' }] }),
  'GET /api/local/v1/config': () => json(200, { schema: 1, config: { server: {} }, next: [] }),
}

// GET /api/local/v1/engines as the Go catalogue builds it.
export const engines = {
  schema: 1,
  engines: [
    { id: 'ingitdb', name: 'inGitDB', description: 'Readable files in a folder, with Git history. Recommended to start.', schema_modes: ['strict', 'partial', 'schemaless'], pinned: true, setup: 'guided', note: 'Advanced: inGitDB stored directly in a GitHub repository is set up with a manifest file.' },
    { id: 'sqlite', name: 'SQLite', description: 'One fast local file. You describe your data (a schema) before storing records.', schema_modes: ['strict'], pinned: true, setup: 'guided' },
    ...[
      ['firestore', 'Firestore', 'Google Cloud document database. Set up with a manifest.'],
      ['mysql', 'MySQL', 'A MySQL server you run. Set up with a manifest.'],
      ['postgres', 'PostgreSQL', 'A PostgreSQL server you run. Set up with a manifest.'],
    ].map(([id, name, description]) => ({
      id,
      name,
      description,
      schema_modes: ['strict'],
      pinned: false,
      setup: 'manifest',
      manifest_steps: [
        { label: 'Write a manifest file, then edit it', command: `ovdb init --engine ${id} --id <name>` },
        { label: 'Put the edited file in /home/a/.config/ovdb/databases, then load it', command: 'ovdb databases reload <name>' },
        { label: 'Guided connect is coming.' },
        { label: 'Read how manifest files work: https://github.com/openvaultdb/openvaultdb-go#manifest-examples' },
      ],
    })),
  ],
  next: [],
}

export const status = {
  schema: 1,
  version: '1.2.3',
  locations: { home: '/home/a/.config/ovdb', runtime: '/home/a/.cache/ovdb/run', data: '/home/a/ovdb' },
  server,
  databases: [],
  next: [],
}

export const databaseRoutes: Record<string, Handler> = {
  'GET /api/local/v1/engines': () => json(200, engines),
  'GET /api/local/v1/status': () => json(200, status),
}
