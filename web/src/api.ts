// The console's only way to reach the OVDB server: same-origin calls to the
// local API with the session cookie. Every response is one of the shared
// documents (spec/features/configuration-parity#REQ:error-envelope), so the
// console renders what the server says instead of deciding copy itself.
import { ref } from 'vue'

import { t } from './copy'

export interface Next {
  label: string
  command?: string
  action?: string
}

export interface ApiError {
  code: string
  message: string
  reason?: string
  next: Next[]
}

export interface Server {
  state: 'running' | 'not_running' | 'stopping'
  address: string
  fallback_address: string
  port: number
  version?: string
  pid?: number
  started_at?: string
  log: string
}

export interface ServerDocument {
  schema: number
  server: Server
  next: Next[]
}

export interface CopyRef {
  key: string
  params?: Record<string, string>
}

export interface Badge {
  tone: 'ok' | 'warn' | 'neutral'
  label_key: string
}

export interface HomeOption {
  id: string
  group: 'primary' | 'secondary'
  label_key: string
  web_label_key?: string
  description_key?: string
  badge?: Badge
  disabled?: boolean
}

export interface HomeDocument {
  schema: number
  status_line: CopyRef[]
  question_key: string
  options: HomeOption[]
}

export interface StatusDocument {
  schema: number
  version: string
  locations: { home: string; runtime: string; data: string }
  server: Server
  databases: Database[]
  next: Next[]
}

export interface Engine {
  id: string
  name: string
  description: string
  schema_modes: string[]
  pinned: boolean
  setup: 'guided' | 'manifest'
  note?: string
  manifest_steps?: Next[]
}

export interface EnginesDocument {
  schema: number
  engines: Engine[]
  next: Next[]
}

export interface Database {
  id: string
  engine?: string
  location?: string
  state?: 'mounting' | 'mounted' | 'needs_attention' | 'unknown'
  reason?: string
  manifest: string
}

export interface DatabasesDocument {
  schema: number
  databases: Database[]
  next: Next[]
}

export interface DatabaseResult {
  schema: number
  database: Database
  next: Next[]
}

export interface Context {
  database: string
  path: string
  scope: 'flag' | 'environment' | 'project' | 'global' | 'only'
  dir?: string
}

/** GET and PUT /api/local/v1/context (the console sees and sets only the global default: parity E3). */
export interface ContextDocument {
  schema: number
  context: Context | null
  global: Context | null
  databases: string[]
  message?: string
  next: Next[]
}

/** GET /v1/databases/{db} */
export interface DatabaseInfo {
  id: string
  engine: string
  schemaMode: string
  collections: string[] | null
}

/** A record from GET /v1/databases/{db}/records/{key} or a query. */
export interface DataRecord {
  key: string
  data?: Record<string, unknown>
}

export interface ConfigDocument {
  schema: number
  config: { server: { port?: number } }
  changed?: boolean
  next: Next[]
}

export type Result<T> = { ok: true; data: T } | { ok: false; error: ApiError }

/**
 * How the console is connected: signed in, signed out (any 401), or unable
 * to reach the server at all (it stopped). App.vue swaps the whole page for
 * the matching copy (local-server-and-web-console#REQ:session-ended-copy).
 */
export const connection = ref<'ok' | 'session-ended' | 'unreachable'>('ok')

let serverMoving = false

/**
 * Settings calls this after saving a new port: once the server restarts on
 * it, this origin stops answering and the cookie no longer applies, so the
 * page should ask for a new link rather than say the server stopped.
 */
export function expectServerMove() {
  serverMoving = true
}

/** Back to a fresh page's state (tests). */
export function resetConnection() {
  connection.value = 'ok'
  serverMoving = false
}

export async function api<T>(method: 'GET' | 'PUT' | 'POST' | 'DELETE', path: string, body?: unknown): Promise<Result<T>> {
  let response: Response
  try {
    response = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    connection.value = serverMoving ? 'session-ended' : 'unreachable'
    return { ok: false, error: { code: 'server_not_running', message: t('server.not_running.message'), next: [] } }
  }
  if (response.status === 401) {
    connection.value = 'session-ended'
  } else if (connection.value === 'unreachable' || (serverMoving && connection.value === 'session-ended')) {
    connection.value = 'ok'
  }
  let document: unknown
  try {
    document = await response.json()
  } catch {
    return { ok: false, error: { code: 'internal', message: t('api.internal'), next: [] } }
  }
  if (!response.ok) {
    const error = (document as { error?: ApiError }).error
    // /v1 error bodies carry no next list.
    return { ok: false, error: error ? { ...error, next: error.next ?? [] } : { code: 'internal', message: t('api.internal'), next: [] } }
  }
  return { ok: true, data: document as T }
}
