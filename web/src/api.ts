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
}

export interface StatusDocument {
  schema: number
  version: string
  server: Server
  next: Next[]
}

export interface ConfigDocument {
  schema: number
  config: { server: { port?: number } }
  next: Next[]
}

export type Result<T> = { ok: true; data: T } | { ok: false; error: ApiError }

/**
 * How the console is connected: signed in, signed out (any 401), or unable
 * to reach the server at all (it stopped). App.vue swaps the whole page for
 * the matching copy (local-server-and-web-console#REQ:session-ended-copy).
 */
export const connection = ref<'ok' | 'session-ended' | 'unreachable'>('ok')

export async function api<T>(method: 'GET' | 'PUT', path: string, body?: unknown): Promise<Result<T>> {
  let response: Response
  try {
    response = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    connection.value = 'unreachable'
    return { ok: false, error: { code: 'server_not_running', message: t('server.not_running.message'), next: [] } }
  }
  if (response.status === 401) {
    connection.value = 'session-ended'
  } else if (connection.value === 'unreachable') {
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
    return { ok: false, error: error ?? { code: 'internal', message: t('api.internal'), next: [] } }
  }
  return { ok: true, data: document as T }
}
