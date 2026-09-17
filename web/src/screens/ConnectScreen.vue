<script setup lang="ts">
// Connect an existing database (capabilities 10 and 10a). The person picks
// storage from the server's catalogue: an inGitDB folder or SQLite file takes
// its path and a name (suggested from the path), and any engine connects
// with a manifest file's path. Connection details are never asked for
// (database-setup-and-providers#REQ:manifest-only-engines-are-honest); the
// server checks everything and never writes into the storage.
import { computed, nextTick, onMounted, ref, watch } from 'vue'

import { api, type ApiError, type DatabaseResult, type Engine, type EnginesDocument, type Next } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCard from '../components/OvCard.vue'
import OvCommand from '../components/OvCommand.vue'
import OvDatabaseResult from '../components/OvDatabaseResult.vue'
import OvNotice from '../components/OvNotice.vue'
import OvText from '../components/OvText.vue'
import OvTextField from '../components/OvTextField.vue'
import { t } from '../copy'
import { filterEngines, nameFromLocation } from '../engines'

const engines = ref<Engine[]>([])
const loading = ref(true)
const loadProblem = ref<ApiError | null>(null)

const filter = ref('')
const chosen = ref<Engine | null>(null)
const withManifest = ref(false)
const location = ref('')
const name = ref('')
const nameEdited = ref(false)
const manifest = ref('')
const saving = ref(false)
const problem = ref<ApiError | null>(null)
const result = ref<DatabaseResult | null>(null)

const nameField = ref<InstanceType<typeof OvTextField>>()
const locationField = ref<InstanceType<typeof OvTextField>>()
const manifestField = ref<InstanceType<typeof OvTextField>>()
const choices = ref<HTMLElement>()
const outcome = ref<HTMLElement>()

onMounted(async () => {
  const catalogue = await api<EnginesDocument>('GET', '/api/local/v1/engines')
  if (catalogue.ok) engines.value = catalogue.data.engines
  else loadProblem.value = catalogue.error
  loading.value = false
  // Create's manifest steps link here with the engine they were for.
  const engine = new URLSearchParams(window.location.search).get('engine')
  const preset = engines.value.find((item) => item.id === engine && item.setup === 'manifest')
  if (preset) await choose(preset)
})

const visible = computed(() => filterEngines(engines.value, filter.value))
const pinned = computed(() => visible.value.filter((engine) => engine.pinned))
const others = computed(() => visible.value.filter((engine) => !engine.pinned))

// The name follows the folder or file until the person names it.
watch(location, (value) => {
  if (!nameEdited.value) name.value = nameFromLocation(value)
})

function editName(value: string) {
  name.value = value
  nameEdited.value = value !== ''
}

async function choose(engine: Engine | null) {
  chosen.value = engine
  withManifest.value = engine === null || engine.setup === 'manifest'
  problem.value = null
  result.value = null
  await nextTick()
  if (withManifest.value) manifestField.value?.focus()
  else locationField.value?.focus()
}

async function changeStorage() {
  const previous = chosen.value?.id ?? 'manifest'
  chosen.value = null
  withManifest.value = false
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

async function connect() {
  problem.value = null
  saving.value = true
  const body = withManifest.value
    ? { manifest: manifest.value.trim() }
    : { id: name.value.trim(), engine: chosen.value?.id, path: location.value.trim() }
  const response = await api<DatabaseResult>('POST', '/api/local/v1/databases/connect', body)
  saving.value = false
  if (response.ok) result.value = response.data
  else problem.value = response.error
  await nextTick()
  outcome.value?.focus()
}

function hasAction(next: Next[], action: string) {
  return next.some((item) => item.action === action)
}

const nameError = computed(() => (problem.value && hasAction(problem.value.next, 'edit_name') ? problem.value.reason : undefined))
const locationError = computed(() =>
  problem.value && !withManifest.value && hasAction(problem.value.next, 'edit_location') ? problem.value.reason : undefined,
)
const manifestError = computed(() =>
  problem.value && withManifest.value && hasAction(problem.value.next, 'edit_manifest') ? problem.value.reason : undefined,
)
const fieldError = computed(() => nameError.value || locationError.value || manifestError.value)
const remedies = computed(() => problem.value?.next.filter((item) => item.action?.startsWith('edit_')) ?? [])

function remedy(item: Next) {
  if (item.action === 'edit_name') nameField.value?.focus()
  if (item.action === 'edit_location') locationField.value?.focus()
  if (item.action === 'edit_manifest') manifestField.value?.focus()
}

function another() {
  result.value = null
  chosen.value = null
  withManifest.value = false
  location.value = ''
  name.value = ''
  nameEdited.value = false
  manifest.value = ''
  filter.value = ''
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />
    <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('home.menu.connect_database') }}</h1>

    <p v-if="loading" class="text-muted">{{ t('console.loading') }}</p>
    <OvNotice v-else-if="loadProblem" live tone="problem" :title="loadProblem.message" :reason="loadProblem.reason" :next="loadProblem.next" />

    <!-- Result -->
    <div v-else-if="result" ref="outcome" tabindex="-1" data-testid="connect-result">
      <OvDatabaseResult
        :result="result"
        :title="t('database.connected.title', { name: result.database.id })"
        :stored="t('database.connected.stored', { location: result.database.location ?? '' })"
        :another="t('connect.another')"
        @another="another"
      />
    </div>

    <!-- Choose storage -->
    <section v-else-if="!chosen && !withManifest" class="flex flex-col gap-5" aria-labelledby="connect-question">
      <h2 id="connect-question" class="text-xl font-semibold tracking-tight">{{ t('connect.question') }}</h2>
      <OvTextField v-model="filter" :label="t('create.filter.label')" />
      <div ref="choices" class="flex flex-col gap-5" @keydown.down="move($event, 1)" @keydown.up="move($event, -1)">
        <template
          v-for="group in [
            { id: 'pinned', title: t('create.recommended'), items: pinned },
            { id: 'others', title: t('create.others'), items: others },
          ]"
          :key="group.id"
        >
          <div v-if="group.items.length" class="flex flex-col gap-2">
            <h3 :id="`connect-engines-${group.id}`" class="text-sm font-semibold tracking-wide text-muted uppercase">{{ group.title }}</h3>
            <ul :aria-labelledby="`connect-engines-${group.id}`" :data-group="group.id" class="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface">
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
                  </span>
                  <span aria-hidden="true" class="shrink-0 text-2xl text-muted transition-transform group-hover:translate-x-0.5">›</span>
                </button>
              </li>
            </ul>
          </div>
        </template>
        <p v-if="!visible.length" role="status" class="text-muted">{{ t('create.filter.none', { filter }) }}</p>
        <button
          type="button"
          data-engine="manifest"
          class="group flex w-full items-center justify-between gap-4 rounded-2xl border border-line bg-surface px-5 py-4 text-left transition-colors hover:bg-surface-2"
          @click="choose(null)"
        >
          <span class="flex min-w-0 flex-col gap-0.5">
            <span class="text-lg font-semibold text-ink">{{ t('connect.manifest_option') }}</span>
            <span class="text-muted">{{ t('connect.manifest_option_help') }}</span>
          </span>
          <span aria-hidden="true" class="shrink-0 text-2xl text-muted transition-transform group-hover:translate-x-0.5">›</span>
        </button>
      </div>
    </section>

    <!-- A folder or file, or a manifest file -->
    <section v-else class="flex flex-col gap-5">
      <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p class="font-medium" data-testid="chosen">{{ chosen ? t('create.chosen', { engine: chosen.name }) : t('connect.manifest_option') }}</p>
        <button type="button" class="font-medium text-accent hover:underline" @click="changeStorage">{{ t('create.change_storage') }}</button>
      </div>
      <OvCard v-if="chosen && chosen.setup === 'manifest'" data-testid="manifest-steps">
        <h2 class="mb-2 text-xl font-semibold tracking-tight">{{ t('engine.manifest.title') }}</h2>
        <p class="mb-5 text-muted">{{ t('engine.manifest.intro', { engine: chosen.name }) }}</p>
        <ol class="flex list-decimal flex-col gap-4 pl-6 marker:font-semibold">
          <li v-for="step in chosen.manifest_steps" :key="step.label" class="pl-1">
            <p class="mb-1 break-words"><OvText :text="step.label" /></p>
            <OvCommand v-if="step.command" :command="step.command" />
          </li>
        </ol>
      </OvCard>
      <OvCard>
        <form class="flex flex-col gap-5" novalidate @submit.prevent="connect">
          <template v-if="withManifest">
            <OvTextField
              ref="manifestField"
              v-model="manifest"
              :label="t('connect.manifest.label')"
              :help="t('connect.manifest.help')"
              :error="manifestError"
              placeholder="/home/you/crm.yaml"
              wide
              mono
            />
          </template>
          <template v-else-if="chosen">
            <OvTextField
              ref="locationField"
              v-model="location"
              :label="chosen.id === 'sqlite' ? t('connect.location.label.sqlite') : t('connect.location.label.ingitdb')"
              :help="chosen.id === 'sqlite' ? t('connect.location.help.sqlite') : t('connect.location.help.ingitdb')"
              :error="locationError"
              :placeholder="chosen.id === 'sqlite' ? '/home/you/shop.sqlite' : '/home/you/notes'"
              wide
              mono
            />
            <OvTextField
              ref="nameField"
              :model-value="name"
              :label="t('create.name.label')"
              :help="t('create.name.help')"
              :error="nameError"
              placeholder="notes"
              @update:model-value="editName"
            />
          </template>
          <div>
            <OvButton type="submit" :busy="saving">{{ t('connect.submit') }}</OvButton>
          </div>
        </form>
      </OvCard>
      <div ref="outcome" tabindex="-1">
        <div v-if="problem" role="alert" class="flex flex-col gap-3">
          <OvNotice
            tone="problem"
            :title="problem.message"
            :reason="fieldError ? undefined : problem.reason"
            :next="problem.next.filter((item) => item.action !== 'done')"
          />
          <div v-if="remedies.length" class="flex flex-wrap gap-3">
            <OvButton v-for="item in remedies" :key="item.label" variant="secondary" @click="remedy(item)">{{ item.label }}</OvButton>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
