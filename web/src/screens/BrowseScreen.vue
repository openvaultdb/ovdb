<script setup lang="ts">
// Browse data (capability 15, database-context-navigation
// #REQ:browse-data-read-only): collections → records, 50 at a time → one
// record as formatted JSON, through the same /v1 data API the CLI uses. It is
// read-only (parity E5) and has no cd (E4): the address is the path, and each
// view shows the `ovdb list`/`ovdb get` command that prints the same thing.
// Values are rendered as text only.
import { computed, nextTick, ref, watch } from 'vue'

import { api, type ApiError, type ContextDocument, type DatabaseInfo, type DatabasesDocument, type DataRecord } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCommand from '../components/OvCommand.vue'
import OvNotice from '../components/OvNotice.vue'
import OvStatusBadge from '../components/OvStatusBadge.vue'
import OvText from '../components/OvText.vue'
import OvTextField from '../components/OvTextField.vue'
import { t } from '../copy'
import { browseRoute, display, keyId, keyOf, kindOf, parseBrowseRoute, quoteArg, recordURL, unescapeSegment } from '../datapath'
import { currentPath, navigate } from '../router'

const pageSize = 50

const route = computed(() => parseBrowseRoute(currentPath.value))
const db = computed(() => route.value?.db ?? '')
const segments = computed(() => route.value?.segments ?? [])
const kind = computed(() => kindOf(segments.value))
const path = computed(() => display(segments.value))

const databases = ref<string[] | null>(null)
const defaultDb = ref<string | null>(null)
const loading = ref(true)
const problem = ref<ApiError | null>(null)
const collections = ref<string[]>([])
const records = ref<{ id: string; preview: string }[]>([])
const record = ref<string | null>(null)
const missing = ref(false)
const page = ref(0)
const more = ref(false)
const used = ref<string | null>(null)
const using = ref(false)
const nested = ref('')
const nestedInvalid = ref(false)

const command = computed(() =>
  kind.value === 'record'
    ? `ovdb get ${quoteArg(path.value)} --db ${quoteArg(db.value)}`
    : `ovdb list ${quoteArg(path.value)} --db ${quoteArg(db.value)}`,
)
const crumbs = computed(() =>
  segments.value.map((segment, index) => ({
    label: display([segment]).slice(1),
    to: browseRoute(db.value, segments.value.slice(0, index + 1)),
  })),
)
const firstShown = computed(() => page.value * pageSize + 1)

async function loadContext() {
  const [list, context] = await Promise.all([
    api<DatabasesDocument>('GET', '/api/local/v1/databases'),
    api<ContextDocument>('GET', '/api/local/v1/context'),
  ])
  if (list.ok) databases.value = list.data.databases.map((d) => d.id)
  else problem.value = list.error
  if (context.ok) defaultDb.value = context.data.global?.database ?? null
}

async function load() {
  problem.value = null
  missing.value = false
  record.value = null
  collections.value = []
  records.value = []
  more.value = false
  if (!route.value) {
    loading.value = false
    return
  }
  if (route.value.segments === null) {
    problem.value = { code: 'invalid_argument', message: t('path.invalid', { path: currentPath.value }), next: [{ label: t('next.path_escapes') }] }
    loading.value = false
    return
  }
  loading.value = true
  const target = currentPath.value
  const base = `/v1/databases/${encodeURIComponent(db.value)}`
  if (kind.value === 'root') {
    const result = await api<DatabaseInfo>('GET', base)
    if (target !== currentPath.value) return
    if (result.ok) collections.value = result.data.collections ?? []
    else problem.value = result.error
  } else if (kind.value === 'collection') {
    // One record more than a page says whether there is a next one. A root
    // collection pages on the server (DTQL offset); DTQL takes root
    // collections only, so a nested one reads up to the page's end.
    const offset = page.value * pageSize
    const name = segments.value[segments.value.length - 1]
    // `from` is where this page starts in what came back.
    let result
    let from = 0
    if (segments.value.length === 1 && offset <= 10000) {
      const doc = `from: {name: ${JSON.stringify(name)}}\nlimit: ${pageSize + 1}\noffset: ${offset}\n`
      result = await api<{ records: DataRecord[] }>('POST', base + '/dtql', { text: doc, contentType: 'application/yaml' })
    } else {
      const query = { collection: name, limit: offset + pageSize + 1, parent: keyOf(segments.value.slice(0, -1)) }
      result = await api<{ records: DataRecord[] }>('POST', base + '/query', query)
      from = offset
    }
    if (target !== currentPath.value) return
    if (result.ok) {
      const rows = result.data.records.slice(from)
      const shown = rows.slice(0, pageSize)
      more.value = rows.length > pageSize
      records.value = shown.map((r) => ({ id: keyId(r.key), preview: JSON.stringify(r.data ?? {}) }))
    } else {
      problem.value = result.error
    }
  } else {
    const result = await api<DataRecord>('GET', recordURL(db.value, segments.value))
    if (target !== currentPath.value) return
    if (result.ok) record.value = JSON.stringify(result.data.data ?? {}, null, 2)
    else if (result.error.code === 'not_found' && !result.error.message.startsWith('database not found')) missing.value = true
    else problem.value = result.error
  }
  loading.value = false
}

watch(
  currentPath,
  () => {
    page.value = 0
    used.value = null
    nested.value = ''
    nestedInvalid.value = false
    void load()
  },
  { immediate: true },
)
void loadContext()

async function turn(step: number) {
  page.value += step
  await load()
  await nextTick()
  document.querySelector<HTMLElement>('[data-testid="records"] a')?.focus()
}

// Use as default: the console sets only the default for all projects;
// project contexts come from a terminal (parity E3).
async function useAsDefault() {
  using.value = true
  const result = await api<ContextDocument>('PUT', '/api/local/v1/context', { scope: 'global', database: db.value })
  using.value = false
  if (result.ok) {
    defaultDb.value = result.data.global?.database ?? db.value
    used.value = result.data.message ?? null
  } else {
    problem.value = result.error
  }
}

function openNested() {
  const name = nested.value.trim()
  const id = unescapeSegment(name)
  if (!name || id === null || name.includes('/')) {
    nestedInvalid.value = true
    return
  }
  navigate(browseRoute(db.value, [...segments.value, id]))
}

function go(event: MouseEvent, to: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(to)
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />
    <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('home.menu.browse') }}</h1>

    <!-- Choose a database -->
    <template v-if="!route">
      <p class="text-lg">{{ t('browse.choose') }}</p>
      <OvNotice v-if="problem" live tone="problem" :title="problem.message" :reason="problem.reason" :next="problem.next" />
      <p v-else-if="!databases" class="text-muted">{{ t('console.loading') }}</p>
      <p v-else-if="!databases.length" class="text-lg">{{ t('databases.empty') }}</p>
      <ul v-else class="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface" :aria-label="t('databases.title')">
        <li v-for="id in databases" :key="id" class="border-line not-last:border-b">
          <a
            :href="browseRoute(id)"
            :data-browse-database="id"
            class="flex items-center justify-between gap-4 px-5 py-4 transition-colors hover:bg-surface-2 focus-visible:rounded-none focus-visible:-outline-offset-3"
            @click="go($event, browseRoute(id))"
          >
            <span class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
              <span class="text-lg font-semibold break-all text-ink">{{ id }}</span>
              <OvStatusBadge v-if="id === defaultDb" tone="ok" :label="t('browse.default')" />
            </span>
            <span aria-hidden="true" class="shrink-0 text-2xl text-muted">›</span>
          </a>
        </li>
      </ul>
    </template>

    <template v-else>
      <nav :aria-label="t('browse.path')" class="flex flex-wrap items-center gap-x-1.5 gap-y-1 font-mono text-[0.95rem]">
        <a :href="browseRoute(db)" class="font-semibold break-all text-accent hover:underline" @click="go($event, browseRoute(db))">{{ db }}:</a>
        <template v-for="(crumb, index) in crumbs" :key="crumb.to">
          <span aria-hidden="true" class="text-muted">/</span>
          <a
            v-if="index < crumbs.length - 1"
            :href="crumb.to"
            class="break-all text-accent hover:underline"
            @click="go($event, crumb.to)"
            >{{ crumb.label }}</a
          >
          <span v-else aria-current="page" class="break-all text-ink">{{ crumb.label }}</span>
        </template>
        <span v-if="!crumbs.length" aria-current="page" class="text-ink">/</span>
      </nav>

      <div class="flex flex-wrap items-center gap-3">
        <OvStatusBadge v-if="defaultDb === db" tone="ok" :label="t('browse.default')" />
        <OvButton v-else variant="secondary" data-testid="use-as-default" :busy="using" @click="useAsDefault">
          {{ t('browse.use_as_default') }}
        </OvButton>
      </div>
      <OvNotice v-if="used" live tone="success" :title="used" />

      <section class="flex flex-col gap-2" :aria-label="t('browse.same_in_terminal')">
        <p class="text-muted">{{ t('browse.same_in_terminal') }}</p>
        <OvCommand :command="command" />
      </section>

      <OvNotice v-if="problem" live tone="problem" :title="problem.message" :reason="problem.reason" :next="problem.next" />
      <p v-else-if="loading" class="text-muted">{{ t('console.loading') }}</p>

      <!-- Collections at the root -->
      <template v-else-if="kind === 'root'">
        <p v-if="!collections.length" data-testid="nothing-here" class="text-lg">{{ t('data.nothing_here') }}</p>
        <ul v-else data-testid="collections" class="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface">
          <li v-for="name in collections" :key="name" class="border-line not-last:border-b">
            <a
              :href="browseRoute(db, [name])"
              :data-collection="name"
              class="flex items-center justify-between gap-4 px-5 py-3.5 font-mono transition-colors hover:bg-surface-2 focus-visible:rounded-none focus-visible:-outline-offset-3"
              @click="go($event, browseRoute(db, [name]))"
            >
              <span class="min-w-0 break-all text-ink">{{ display([name]).slice(1) }}</span>
              <span aria-hidden="true" class="shrink-0 text-xl text-muted">›</span>
            </a>
          </li>
        </ul>
      </template>

      <!-- A collection's records, 50 at a time -->
      <template v-else-if="kind === 'collection'">
        <p v-if="!records.length" data-testid="nothing-here" class="text-lg">{{ t('data.nothing_here') }}</p>
        <template v-else>
          <ul data-testid="records" class="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface">
            <li v-for="item in records" :key="item.id" class="border-line not-last:border-b">
              <a
                :href="browseRoute(db, [...segments, item.id])"
                :data-record="item.id"
                class="grid gap-x-6 gap-y-0.5 px-5 py-3 transition-colors hover:bg-surface-2 focus-visible:rounded-none focus-visible:-outline-offset-3 sm:grid-cols-[minmax(8rem,auto)_1fr]"
                @click="go($event, browseRoute(db, [...segments, item.id]))"
              >
                <span class="font-mono font-semibold break-all text-ink">{{ display([item.id]).slice(1) }}</span>
                <span class="min-w-0 truncate font-mono text-[0.9rem] text-muted">{{ item.preview }}</span>
              </a>
            </li>
          </ul>
          <div class="flex flex-wrap items-center justify-between gap-3">
            <p data-testid="page" class="text-muted" aria-live="polite">
              {{ t('browse.page', { first: String(firstShown), last: String(firstShown + records.length - 1) }) }}
            </p>
            <div class="flex gap-3">
              <OvButton v-if="page > 0" variant="secondary" @click="turn(-1)">{{ t('browse.previous') }}</OvButton>
              <OvButton v-if="more" variant="secondary" @click="turn(1)">{{ t('browse.next') }}</OvButton>
            </div>
          </div>
        </template>
      </template>

      <!-- One record -->
      <template v-else>
        <p v-if="missing" data-testid="nothing-here" class="text-lg">{{ t('data.nothing_here') }}</p>
        <pre
          v-else
          data-testid="record"
          class="rounded-2xl border border-line bg-surface px-5 py-4 font-mono text-[0.95rem] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]"
        ><code>{{ record }}</code></pre>
        <form class="flex flex-col gap-3 sm:flex-row sm:items-end" novalidate @submit.prevent="openNested">
          <div class="flex-1">
            <OvTextField
              v-model="nested"
              :label="t('browse.nested.label')"
              :help="t('browse.nested.help')"
              :error="nestedInvalid ? t('browse.collection_invalid') : undefined"
            />
          </div>
          <OvButton type="submit" variant="secondary">{{ t('browse.nested.open') }}</OvButton>
        </form>
      </template>

      <p class="text-muted"><OvText :text="t('browse.read_only')" /></p>
    </template>
  </div>
</template>
