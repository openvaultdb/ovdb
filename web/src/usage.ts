// Usage statistics in the web console (spec/features/telemetry-consent).
// The page never sends anything by itself: while the person hasn't decided
// it keeps up to 100 onboarding events in memory (REQ:pre-consent-buffer),
// shows one prompt after the first successful action
// (REQ:consent-prompt-placement), and posts its buffer to the server only
// after Turn on. The server rebuilds every posted event from the closed
// event set and sends it; closing the page drops the buffer.
import { ref } from 'vue'

import { api, type ApiError, type Next } from './api'

export interface TelemetryStatus {
  state: 'not_asked' | 'enabled' | 'disabled'
  sending: boolean
  reason?: string
  reason_text?: string
  available: boolean
  provider: string
  decided_at?: string
  channel?: string
  has_install_id: boolean
  collected: string[]
  never_collected: string[]
}

export interface TelemetryDocument {
  schema: number
  telemetry: TelemetryStatus
  changed?: boolean
  next: Next[]
}

/** One event as POST /api/local/v1/telemetry/events takes it. */
export interface UsageEvent {
  event: string
  option?: string
  engine?: string
  skill?: string
  harness?: string
  target?: string
  step?: string
  error_code?: string
  success?: boolean
  already_installed?: boolean
  datatug_found?: boolean
  duration_ms?: number
}

export const MAX_BUFFERED = 100

/** The server's document; null until loaded. */
export const usage = ref<TelemetryStatus | null>(null)
/** Whether the prompt is showing now. */
export const promptVisible = ref(false)

let buffer: UsageEvent[] = []
let asked = false
let started = false

/** onboarding_started, once per page. */
export function recordStart() {
  if (started) return
  started = true
  recordUsage({ event: 'onboarding_started' })
}

const forcedOff = ['OVDB_TELEMETRY', 'DO_NOT_TRACK', 'CI']

/**
 * Loads the state (App.vue, once per page; Settings on open). It reads
 * quietly: whether the server is reachable or the session ended is for the
 * screens' own calls to say, and until the state is known nothing is sent.
 */
export async function loadUsage(): Promise<void> {
  try {
    const response = await fetch('/api/local/v1/telemetry', { credentials: 'same-origin' })
    if (!response.ok) return
    apply(((await response.json()) as TelemetryDocument).telemetry)
  } catch {
    // Unknown state: events stay in memory only.
  }
}

function apply(status: TelemetryStatus) {
  usage.value = status
  // Only an undecided page keeps events.
  if (status.state !== 'not_asked') buffer = []
}

/**
 * Records a page event. Events the server reports itself for this page's
 * actions (serverReported: database_created, demo_installed, …) are only
 * ever buffered; page-only events are posted at once when enabled.
 */
export function recordUsage(event: UsageEvent, serverReported = false) {
  const state = usage.value?.state
  if (state === undefined || state === 'not_asked') {
    if (buffer.length < MAX_BUFFERED) buffer.push(event)
    return
  }
  if (state === 'enabled' && !serverReported) {
    void api('POST', '/api/local/v1/telemetry/events', { events: [event] })
  }
}

/** Buffered events (tests). */
export function bufferedUsage(): UsageEvent[] {
  return [...buffer]
}

/**
 * Shows the prompt after a successful action: once per page, only while the
 * person hasn't decided and nothing forces it off here.
 */
export function offerUsagePrompt() {
  const status = usage.value
  if (asked || !status || status.state !== 'not_asked' || forcedOff.includes(status.reason ?? '')) return
  asked = true
  promptVisible.value = true
}

/** Turn on (prompt or Settings): the person's click is the confirmation. */
export async function turnOnUsage(): Promise<ApiError | null> {
  const response = await api<TelemetryDocument>('PUT', '/api/local/v1/telemetry', { state: 'enabled', confirmed_by_user: true })
  if (!response.ok) return response.error
  const events = buffer
  buffer = []
  promptVisible.value = false
  asked = true
  usage.value = response.data.telemetry
  if (events.length > 0) await api('POST', '/api/local/v1/telemetry/events', { events })
  return null
}

/** No thanks, Keep off or Turn off. */
export async function turnOffUsage(): Promise<ApiError | null> {
  buffer = []
  promptVisible.value = false
  asked = true
  const response = await api<TelemetryDocument>('PUT', '/api/local/v1/telemetry', { state: 'disabled' })
  if (!response.ok) return response.error
  apply(response.data.telemetry)
  return null
}

/** Closing the prompt without answering drops the buffer. */
export function dismissUsagePrompt() {
  if (!promptVisible.value) return
  promptVisible.value = false
  buffer = []
}

/** A fresh page (tests). */
export function resetUsage() {
  usage.value = null
  promptVisible.value = false
  buffer = []
  asked = false
  started = false
}
