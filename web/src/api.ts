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

/** GET /api/local/v1/demo and POST /api/local/v1/demo/install (spec/features/todo-demo). */
export interface DemoDocument {
  schema: number
  app: string
  installed: boolean
  already_installed?: boolean
  database?: string
  location: string
  state?: Database['state']
  app_path: string
  lists: string[]
  next: Next[]
}

export interface ConfigDocument {
  schema: number
  config: { server: { port?: number } }
  changed?: boolean
  next: Next[]
}

/** explore-data-handoff: Explore data's intent-first menu, computed without writing anything. */
export interface ExploreMenu {
  schema: number
  database: string
  is_demo: boolean
  datatug_cli_description_key: string
  datatug_app_description_key: string
}

export interface EnvLine {
  name: string
  value: string
}

/** GET /api/local/v1/explore/datatug?db=…: choosing DataTug CLI. */
export interface DataTugCLIDocument {
  schema: number
  on_path: boolean
  collection: string
  descriptor_path: string
  descriptor: { baseUrl: string; databaseId: string; tokenEnv: string; principalId: string }
  env_lines: EnvLine[]
  shell: 'sh' | 'powershell'
  shell_text: string
  token_command: string
  query_command: string
  install_commands?: string[]
  next: Next[]
}

/** Choosing DataTug.app: a static, local document (no server round trip). */
export interface DataTugAppDocument {
  schema: number
  database: string
  url: string
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

/** A body sent as is, such as a DTQL YAML document. */
export interface RawBody {
  text: string
  contentType: string
}

export async function api<T>(method: 'GET' | 'PUT' | 'PATCH' | 'POST' | 'DELETE', path: string, body?: unknown): Promise<Result<T>> {
  const raw = body as RawBody | undefined
  const isRaw = raw !== undefined && typeof raw === 'object' && raw !== null && 'contentType' in raw && 'text' in raw
  let response: Response
  try {
    response = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? {} : { 'Content-Type': isRaw ? raw.contentType : 'application/json' },
      body: body === undefined ? undefined : isRaw ? raw.text : JSON.stringify(body),
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
  // Data API writes answer 204 with no body.
  if (response.ok && response.status === 204) return { ok: true, data: undefined as T }
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
