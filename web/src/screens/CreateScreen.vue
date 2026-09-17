<script setup lang="ts">
// Create a database (capabilities 8 and 9). The person picks storage from
// the server's catalogue (inGitDB and SQLite pinned, the rest by name, with
// a filter), names the database and keeps or changes the suggested location;
// the server creates it and says what next. PostgreSQL, MySQL and Firestore
// show their manifest steps and never ask for connection details
// (database-setup-and-providers#REQ:manifest-only-engines-are-honest).
import { computed, nextTick, onMounted, ref, watch } from 'vue'

import {
  api,
  type ApiError,
  type DatabaseResult,
  type Engine,
  type EnginesDocument,
  type Next,
  type StatusDocument,
} from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCard from '../components/OvCard.vue'
import OvCommand from '../components/OvCommand.vue'
import OvDatabaseResult from '../components/OvDatabaseResult.vue'
import OvNotice from '../components/OvNotice.vue'
import OvText from '../components/OvText.vue'
import OvTextField from '../components/OvTextField.vue'
import { t } from '../copy'
import { defaultLocation, filterEngines, suggestedName } from '../engines'
import { navigate } from '../router'

const engines = ref<Engine[]>([])
const dataHome = ref('')
const loading = ref(true)
const loadProblem = ref<ApiError | null>(null)

const filter = ref('')
const chosen = ref<Engine | null>(null)
const name = ref('')
const location = ref('')
const locationEdited = ref(false)
const saving = ref(false)
const problem = ref<ApiError | null>(null)
const result = ref<DatabaseResult | null>(null)

const nameField = ref<InstanceType<typeof OvTextField>>()
const locationField = ref<InstanceType<typeof OvTextField>>()
const stepHeading = ref<HTMLElement>()
const outcome = ref<HTMLElement>()
const choices = ref<HTMLElement>()

onMounted(async () => {
  const [catalogue, status] = await Promise.all([
    api<EnginesDocument>('GET', '/api/local/v1/engines'),
    api<StatusDocument>('GET', '/api/local/v1/status'),
  ])
  if (catalogue.ok) engines.value = catalogue.data.engines
  else loadProblem.value = catalogue.error
  if (status.ok) dataHome.value = status.data.locations.data
  loading.value = false
})

const visible = computed(() => filterEngines(engines.value, filter.value))
const pinned = computed(() => visible.value.filter((engine) => engine.pinned))
const others = computed(() => visible.value.filter((engine) => !engine.pinned))

const suggested = computed(() =>
  chosen.value && dataHome.value ? defaultLocation(dataHome.value, chosen.value.id, name.value.trim() || 'notes') : '',
)

// The location follows the name until the person changes it themselves.
watch([name, chosen, dataHome], () => {
  if (!locationEdited.value) location.value = name.value.trim() ? suggested.value : ''
})

function editLocation(value: string) {
  location.value = value
  locationEdited.value = value !== ''
}

async function choose(engine: Engine) {
  chosen.value = engine
  problem.value = null
  result.value = null
  await nextTick()
  if (engine.setup === 'guided') nameField.value?.focus()
  else stepHeading.value?.focus()
}

async function changeStorage() {
  const previous = chosen.value?.id
  chosen.value = null
  problem.value = null
  await nextTick()
  choices.value?.querySelector<HTMLButtonElement>(`[data-engine="${previous}"]`)?.focus()
}

function move(event: KeyboardEvent, step: number) {
  const buttons = Array.from(choices.value?.querySelectorAll<HTMLButtonElement>('button[data-engine]') ?? [])
  const index = buttons.indexOf(event.target as HTMLButtonElement)
  if (index < 0) return
  event.preventDefault()
  buttons[(index + step + buttons.length) % buttons.length]?.focus()
}

async function create() {
  if (!chosen.value) return
  problem.value = null
  saving.value = true
  const id = name.value.trim()
  const path = location.value.trim() || (id ? suggested.value : '')
  const response = await api<DatabaseResult>('POST', '/api/local/v1/databases', { id, engine: chosen.value.id, path })
  saving.value = false
  if (response.ok) {
    result.value = response.data
  } else {
    problem.value = response.error
  }
  await nextTick()
  outcome.value?.focus()
}

const nameError = computed(() =>
  problem.value && hasAction(problem.value.next, 'edit_name') && !hasAction(problem.value.next, 'edit_location')
    ? problem.value.reason
    : undefined,
)
const locationError = computed(() =>
  problem.value && hasAction(problem.value.next, 'edit_location') ? problem.value.reason : undefined,
)

function hasAction(next: Next[], action: string) {
  return next.some((item) => item.action === action)
}

function remedy(item: Next) {
  if (item.action === 'edit_name') {
    // "Use the name notes-2 instead" uses notes-2 (setup.SuggestedName).
    const suggested = suggestedName(item)
    if (suggested) name.value = suggested
    nameField.value?.focus()
  }
  if (item.action === 'edit_location') locationField.value?.focus()
}

const problemNext = computed(() => problem.value?.next.filter((item) => item.action !== 'done') ?? [])

function storedText(db: DatabaseResult['database']) {
  const separator = (db.location ?? '').includes('\\') && !(db.location ?? '').includes('/') ? '\\' : '/'
  return db.engine === 'sqlite'
    ? t('database.created.stored.sqlite', { path: db.location ?? '' })
    : t('database.created.stored.ingitdb', { path: (db.location ?? '') + separator })
}

function another() {
  result.value = null
  chosen.value = null
  name.value = ''
  locationEdited.value = false
  filter.value = ''
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
    <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('create.title') }}</h1>

    <p v-if="loading" class="text-muted">{{ t('console.loading') }}</p>
    <OvNotice v-else-if="loadProblem" live tone="problem" :title="loadProblem.message" :reason="loadProblem.reason" :next="loadProblem.next" />

    <!-- Result -->
    <div v-else-if="result" ref="outcome" tabindex="-1" data-testid="create-result">
      <OvDatabaseResult
        :result="result"
        :title="t('database.created.title', { name: result.database.id })"
        :stored="storedText(result.database)"
        :another="t('create.another')"
        @another="another"
      />
    </div>

    <!-- Choose storage -->
    <section v-else-if="!chosen" class="flex flex-col gap-5" aria-labelledby="create-question">
      <h2 id="create-question" class="text-xl font-semibold tracking-tight">{{ t('create.question') }}</h2>
      <OvTextField v-model="filter" :label="t('create.filter.label')" />
      <div ref="choices" class="flex flex-col gap-5" @keydown.down="move($event, 1)" @keydown.up="move($event, -1)">
        <template v-for="group in [{ id: 'pinned', title: t('create.recommended'), items: pinned }, { id: 'others', title: t('create.others'), items: others }]" :key="group.id">
          <div v-if="group.items.length" class="flex flex-col gap-2">
            <h3 :id="`engines-${group.id}`" class="text-sm font-semibold tracking-wide text-muted uppercase">{{ group.title }}</h3>
            <ul :aria-labelledby="`engines-${group.id}`" :data-group="group.id" class="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface">
              <li v-for="engine in group.items" :key="engine.id" class="border-line not-last:border-b">
                <button
                  type="button"
                  :data-engine="engine.id"
                  class="group flex w-full items-center justify-between gap-4 px-5 py-4 text-left transition-colors hover:bg-surface-2 focus-visible:rounded-none focus-visible:-outline-offset-3"
                  @click="choose(engine)"
                >
                  <span class="flex min-w-0 flex-col gap-0.5">
                    <span class="text-lg font-semibold text-ink">{{ engine.name }}</span>
                    <span class="text-muted">{{ engine.description }}</span>
                    <span v-if="engine.note" class="text-sm text-muted">{{ engine.note }}</span>
                  </span>
                  <span aria-hidden="true" class="shrink-0 text-2xl text-muted transition-transform group-hover:translate-x-0.5">›</span>
                </button>
              </li>
            </ul>
          </div>
        </template>
        <p v-if="!visible.length" role="status" class="text-muted">{{ t('create.filter.none', { filter }) }}</p>
      </div>
    </section>

    <!-- A manifest-only engine: steps, no connection details -->
    <section v-else-if="chosen.setup === 'manifest'" data-testid="manifest-steps" class="flex flex-col gap-5" aria-labelledby="manifest-title">
      <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p class="font-medium">{{ t('create.chosen', { engine: chosen.name }) }}</p>
        <button type="button" class="font-medium text-accent hover:underline" @click="changeStorage">{{ t('create.change_storage') }}</button>
      </div>
      <OvCard>
        <h2 id="manifest-title" ref="stepHeading" tabindex="-1" class="mb-2 text-xl font-semibold tracking-tight">{{ t('engine.manifest.title') }}</h2>
        <p class="mb-5 text-muted">{{ t('engine.manifest.intro', { engine: chosen.name }) }}</p>
        <ol class="flex list-decimal flex-col gap-4 pl-6 marker:font-semibold">
          <li v-for="step in chosen.manifest_steps" :key="step.label" class="pl-1">
            <p class="mb-1 break-words"><OvText :text="step.label" /></p>
            <OvCommand v-if="step.command" :command="step.command" />
          </li>
        </ol>
        <p class="mt-5">
          <a
            :href="`/databases/connect?engine=${chosen.id}`"
            class="inline-flex min-h-11 items-center rounded-lg bg-accent px-5 font-semibold text-on-accent hover:bg-accent-hover"
            @click="go($event, `/databases/connect?engine=${chosen.id}`)"
            >{{ t('connect.manifest_option') }}</a
          >
        </p>
      </OvCard>
    </section>

    <!-- Name and location -->
    <section v-else class="flex flex-col gap-5">
      <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p class="font-medium" data-testid="chosen">{{ t('create.chosen', { engine: chosen.name }) }}</p>
        <button type="button" class="font-medium text-accent hover:underline" @click="changeStorage">{{ t('create.change_storage') }}</button>
      </div>
      <OvCard>
        <form class="flex flex-col gap-5" novalidate @submit.prevent="create">
          <OvTextField
            ref="nameField"
            v-model="name"
            :label="t('create.name.label')"
            :help="t('create.name.help')"
            :error="nameError"
            placeholder="notes"
          />
          <OvTextField
            ref="locationField"
            :model-value="location"
            :label="t('create.location.label')"
            :help="chosen.id === 'sqlite' ? t('create.location.help.sqlite') : t('create.location.help.ingitdb')"
            :error="locationError"
            :placeholder="suggested"
            wide
            mono
            @update:model-value="editLocation"
          />
          <div>
            <OvButton type="submit" :busy="saving">{{ t('create.submit') }}</OvButton>
          </div>
        </form>
      </OvCard>
      <div ref="outcome" tabindex="-1">
        <!-- The field shows the reason when the problem is about a field. -->
        <div v-if="problem" role="alert" class="flex flex-col gap-3">
          <OvNotice tone="problem" :title="problem.message" :reason="nameError || locationError ? undefined : problem.reason" :next="problemNext" />
          <div v-if="problem.next.some((item) => item.action === 'edit_name' || item.action === 'edit_location')" class="flex flex-wrap gap-3">
            <template v-for="item in problem.next" :key="item.label">
              <OvButton v-if="item.action === 'edit_name' || item.action === 'edit_location'" variant="secondary" @click="remedy(item)">
                {{ item.action === 'edit_location' ? t('next.choose_location') : suggestedName(item) ? item.label : t('next.choose_name') }}
              </OvButton>
            </template>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
