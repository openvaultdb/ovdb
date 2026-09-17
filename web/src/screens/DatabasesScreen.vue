<script setup lang="ts">
// Databases (capabilities 11 and 12): every registered database with its
// storage, location and state from the server, and Remove with a
// confirmation that says the data stays where it is
// (database-setup-and-providers#REQ:list-and-remove).
import { nextTick, onMounted, ref } from 'vue'

import { api, type ApiError, type Database, type DatabaseResult, type DatabasesDocument } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvNotice from '../components/OvNotice.vue'
import OvStatusBadge from '../components/OvStatusBadge.vue'
import { t } from '../copy'
import { navigate } from '../router'

const databases = ref<Database[] | null>(null)
const engineNames = ref<Record<string, string>>({})
const loadProblem = ref<ApiError | null>(null)
const confirming = ref<string | null>(null)
const removing = ref(false)
const removed = ref<DatabaseResult | null>(null)
const problem = ref<ApiError | null>(null)
const outcome = ref<HTMLElement>()

async function load() {
  const response = await api<DatabasesDocument>('GET', '/api/local/v1/databases')
  if (response.ok) databases.value = response.data.databases
  else loadProblem.value = response.error
}

onMounted(async () => {
  const [, engines] = await Promise.all([load(), api<{ engines: { id: string; name: string }[] }>('GET', '/api/local/v1/engines')])
  if (engines.ok) engineNames.value = Object.fromEntries(engines.data.engines.map((engine) => [engine.id, engine.name]))
})

function badge(db: Database) {
  switch (db.state) {
    case 'mounted':
      return { tone: 'ok' as const, label: t('databases.state.mounted') }
    case 'needs_attention':
      return { tone: 'warn' as const, label: t('databases.state.needs_attention') }
    case 'mounting':
      return { tone: 'neutral' as const, label: t('databases.state.mounting') }
    default:
      return { tone: 'neutral' as const, label: t('databases.state.unknown') }
  }
}

async function ask(id: string) {
  confirming.value = id
  removed.value = null
  problem.value = null
  await nextTick()
  document.querySelector<HTMLElement>(`[data-confirm="${id}"] button:last-child`)?.focus()
}

async function cancel(id: string) {
  confirming.value = null
  await nextTick()
  document.querySelector<HTMLElement>(`[data-remove="${id}"]`)?.focus()
}

const reloading = ref<string | null>(null)
const reloaded = ref<DatabaseResult | null>(null)

// Reload loads the database again from its manifest (after editing it or
// restoring its storage) and shows the state it ends in.
async function reload(id: string) {
  reloading.value = id
  removed.value = null
  reloaded.value = null
  problem.value = null
  const response = await api<DatabaseResult>('POST', `/api/local/v1/databases/${encodeURIComponent(id)}/reload`, {})
  reloading.value = null
  if (response.ok) {
    reloaded.value = response.data
    await load()
  } else {
    problem.value = response.error
  }
  await nextTick()
  outcome.value?.focus()
}

async function remove(id: string) {
  reloaded.value = null
  removing.value = true
  const response = await api<DatabaseResult>('DELETE', `/api/local/v1/databases/${encodeURIComponent(id)}`)
  removing.value = false
  confirming.value = null
  if (response.ok) {
    removed.value = response.data
    await load()
  } else {
    problem.value = response.error
  }
  await nextTick()
  outcome.value?.focus()
}

function go(event: MouseEvent, path: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(path)
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />
    <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-3">
      <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('databases.title') }}</h1>
      <a
        href="/databases/new"
        class="inline-flex min-h-11 items-center rounded-lg border border-line bg-surface px-5 font-semibold text-ink hover:bg-surface-2"
        @click="go($event, '/databases/new')"
        >{{ t('home.menu.create_database') }}</a
      >
    </div>

    <div ref="outcome" tabindex="-1">
      <OvNotice v-if="removed" live tone="success" :title="t('database.removed.title', { name: removed.database.id })">
        <p class="break-words">
          {{
            removed.database.location
              ? t('database.removed.data_kept', { location: removed.database.location })
              : t('database.removed.data_kept_elsewhere')
          }}
        </p>
      </OvNotice>
      <OvNotice
        v-if="reloaded"
        live
        :tone="reloaded.database.state === 'mounted' ? 'success' : 'problem'"
        :title="t('database.reloaded.title', { name: reloaded.database.id }) + ' · ' + badge(reloaded.database).label"
        :reason="reloaded.database.reason"
      />
      <OvNotice v-if="problem" live tone="problem" :title="problem.message" :reason="problem.reason" :next="problem.next" />
    </div>

    <OvNotice v-if="loadProblem" live tone="problem" :title="loadProblem.message" :reason="loadProblem.reason" :next="loadProblem.next" />
    <p v-else-if="!databases" class="text-muted">{{ t('console.loading') }}</p>
    <p v-else-if="!databases.length" data-testid="databases-empty" class="text-lg">{{ t('databases.empty') }}</p>
    <ul v-else class="flex flex-col gap-4" :aria-label="t('databases.title')">
      <li
        v-for="db in databases"
        :key="db.manifest"
        :data-database="db.id"
        class="flex flex-col gap-4 rounded-2xl border border-line bg-surface p-5 sm:p-6"
      >
        <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <h2 class="text-lg font-semibold tracking-tight break-all">{{ db.id }}</h2>
            <OvStatusBadge :tone="badge(db).tone" :label="badge(db).label" />
          </div>
          <div v-if="confirming !== db.id" class="flex flex-wrap gap-3">
            <OvButton variant="secondary" :data-reload="db.id" :busy="reloading === db.id" @click="reload(db.id)">
              {{ t('next.reload_database') }}
            </OvButton>
            <OvButton variant="secondary" :data-remove="db.id" @click="ask(db.id)">
              {{ t('database.remove.button') }}
            </OvButton>
          </div>
        </div>
        <dl class="flex flex-col gap-3">
          <div v-if="db.engine" class="grid gap-x-8 gap-y-0.5 sm:grid-cols-[9rem_1fr]">
            <dt class="font-semibold text-muted">{{ t('databases.detail.engine') }}</dt>
            <dd>{{ engineNames[db.engine] ?? db.engine }}</dd>
          </div>
          <div v-if="db.location" class="grid gap-x-8 gap-y-0.5 sm:grid-cols-[9rem_1fr]">
            <dt class="font-semibold text-muted">{{ t('databases.detail.location') }}</dt>
            <dd class="min-w-0 font-mono text-[0.95rem] break-all">{{ db.location }}</dd>
          </div>
          <div v-if="db.state === 'needs_attention'" class="grid gap-x-8 gap-y-0.5 sm:grid-cols-[9rem_1fr]">
            <dt class="font-semibold text-muted">{{ t('databases.detail.manifest') }}</dt>
            <dd class="min-w-0 font-mono text-[0.95rem] break-all">{{ db.manifest }}</dd>
          </div>
        </dl>
        <p v-if="db.reason" class="break-words text-warn">{{ t('problem.why', { reason: db.reason }) }}</p>
        <div
          v-if="confirming === db.id"
          :data-confirm="db.id"
          role="group"
          :aria-label="t('database.remove.confirm_title', { name: db.id })"
          class="flex flex-col gap-3 rounded-xl border-l-4 border-danger bg-danger-soft px-4 py-3.5 sm:px-5"
        >
          <p class="font-semibold">{{ t('database.remove.confirm_title', { name: db.id }) }}</p>
          <p class="break-words">{{ t('database.remove.confirm_body', { location: db.location || t('database.removed.data_kept_elsewhere') }) }}</p>
          <div class="flex flex-wrap gap-3">
            <OvButton :busy="removing" @click="remove(db.id)">{{ t('database.remove.button') }}</OvButton>
            <OvButton variant="secondary" @click="cancel(db.id)">{{ t('database.remove.cancel') }}</OvButton>
          </div>
        </div>
      </li>
    </ul>
  </div>
</template>
