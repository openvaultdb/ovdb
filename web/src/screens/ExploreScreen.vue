<script setup lang="ts">
// Explore data (capability 22, spec/features/explore-data-handoff): an
// intent-first menu — DataTug CLI or DataTug.app, each with a current-state
// description — before any file is written. Choosing DataTug CLI checks
// datatug on PATH and writes a four-key descriptor with no token
// (REQ:prepare-datatug-cli-connection); DataTug.app states its honest
// limitation and offers no control implying OVDB can open a database there
// (REQ:honest-datatug-app-state).
import { computed, onMounted, ref } from 'vue'

import { api, type ApiError, type Context, type ContextDocument, type DataTugCLIDocument, type DemoDocument, type ExploreMenu } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCard from '../components/OvCard.vue'
import OvCommand from '../components/OvCommand.vue'
import OvNotice from '../components/OvNotice.vue'
import { t } from '../copy'

type View = 'menu' | 'cli' | 'app'

const database = ref<string | null>(null)
const menu = ref<ExploreMenu | null>(null)
const loadProblem = ref<ApiError | null>(null)
const view = ref<View>('menu')
const cli = ref<DataTugCLIDocument | null>(null)
const cliLoading = ref(false)
const cliProblem = ref<ApiError | null>(null)

const datatugCLIHelp = computed(() => (menu.value ? t(menu.value.datatug_cli_description_key) : ''))

async function currentDatabase(): Promise<string | null> {
  const fromQuery = new URLSearchParams(window.location.search).get('db')
  if (fromQuery) return fromQuery
  const response = await api<ContextDocument>('GET', '/api/local/v1/context')
  return response.ok ? ((response.data.global as Context | null)?.database ?? null) : null
}

onMounted(async () => {
  const db = await currentDatabase()
  if (!db) {
    loadProblem.value = { code: 'not_found', message: t('explore.failed'), next: [] }
    return
  }
  database.value = db
  const demo = await api<DemoDocument>('GET', '/api/local/v1/demo')
  const isDemo = demo.ok && demo.data.installed && demo.data.database === db
  menu.value = { schema: 1, database: db, is_demo: isDemo, datatug_cli_description_key: isDemo ? 'explore.menu.datatug_cli_demo' : 'explore.menu.datatug_cli', datatug_app_description_key: 'explore.menu.datatug_app_help' }
})

async function chooseDataTugCLI() {
  view.value = 'cli'
  if (cli.value || !database.value) return
  cliLoading.value = true
  cliProblem.value = null
  const response = await api<DataTugCLIDocument>('GET', `/api/local/v1/explore/datatug?db=${encodeURIComponent(database.value)}`)
  cliLoading.value = false
  if (response.ok) cli.value = response.data
  else cliProblem.value = response.error
}

function chooseDataTugApp() {
  view.value = 'app'
}

function backToMenu() {
  view.value = 'menu'
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />
    <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('explore.title') }}</h1>

    <OvNotice v-if="loadProblem" live tone="problem" :title="loadProblem.message" :reason="loadProblem.reason" :next="loadProblem.next" />
    <p v-else-if="!menu" class="text-muted">{{ t('console.loading') }}</p>

    <!-- Intent-first menu -->
    <template v-else-if="view === 'menu'">
      <p class="text-lg">{{ t('explore.question', { database: menu.database }) }}</p>
      <div class="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface">
        <button
          type="button"
          data-option="datatug-cli"
          class="flex flex-col gap-1 border-b border-line px-5 py-4 text-left transition-colors hover:bg-surface-2"
          @click="chooseDataTugCLI"
        >
          <span class="text-lg font-semibold text-ink">{{ t('explore.menu.datatug_cli') }}</span>
          <span class="text-muted">{{ datatugCLIHelp }}</span>
        </button>
        <button
          type="button"
          data-option="datatug-app"
          class="flex flex-col gap-1 px-5 py-4 text-left transition-colors hover:bg-surface-2"
          @click="chooseDataTugApp"
        >
          <span class="text-lg font-semibold text-ink">{{ t('explore.menu.datatug_app') }}</span>
          <span class="text-muted">{{ t('explore.menu.datatug_app_help') }}</span>
        </button>
      </div>
    </template>

    <!-- DataTug CLI -->
    <template v-else-if="view === 'cli'">
      <button type="button" class="self-start font-medium text-accent hover:underline" @click="backToMenu">‹ {{ t('explore.menu.back') }}</button>
      <p v-if="cliLoading" class="text-muted">{{ t('console.loading') }}</p>
      <OvNotice v-else-if="cliProblem" live tone="problem" :title="cliProblem.message" :reason="cliProblem.reason" :next="cliProblem.next" />
      <div v-else-if="cli" data-testid="explore-datatug-cli" class="flex flex-col gap-4">
        <OvNotice live :tone="cli.on_path ? 'success' : 'problem'" :title="cli.on_path ? t('explore.datatug_cli.ready') : t('explore.datatug_cli.missing')">
          <div v-if="!cli.on_path" class="flex flex-col gap-2">
            <OvCommand v-for="install in cli.install_commands" :key="install" :command="install" />
          </div>
        </OvNotice>
        <OvCard>
          <div class="flex flex-col gap-4">
            <p class="break-words">{{ t('explore.datatug_cli.descriptor_saved', { database: menu!.database, path: cli.descriptor_path }) }}</p>
            <div class="flex flex-col gap-2">
              <p class="font-semibold">{{ t('explore.datatug_cli.env_vars') }}</p>
              <OvCommand :command="cli.shell_text" />
            </div>
            <div class="flex flex-col gap-2">
              <p class="font-semibold">{{ t('explore.datatug_cli.token_intro') }}</p>
              <OvCommand :command="cli.token_command" />
            </div>
            <div class="flex flex-col gap-2">
              <p class="font-semibold">{{ t('explore.datatug_cli.query_intro') }}</p>
              <OvCommand :command="cli.query_command" />
            </div>
          </div>
        </OvCard>
      </div>
    </template>

    <!-- DataTug.app: the honest limitation, no control implying OVDB opens a database there -->
    <template v-else-if="view === 'app'">
      <button type="button" class="self-start font-medium text-accent hover:underline" @click="backToMenu">‹ {{ t('explore.menu.back') }}</button>
      <OvNotice live tone="info" :title="t('explore.datatug_app.honesty')" />
      <div class="flex flex-wrap gap-3">
        <OvButton data-testid="datatug-cli-instead" @click="chooseDataTugCLI">{{ t('explore.datatug_app.use_cli_instead') }}</OvButton>
        <a
          href="https://datatug.app"
          target="_blank"
          rel="noopener"
          data-testid="open-datatug-app"
          class="inline-flex min-h-11 items-center rounded-lg border border-line bg-surface px-5 font-semibold text-ink hover:bg-surface-2"
          >{{ t('explore.datatug_app.open') }}</a
        >
      </div>
    </template>
  </div>
</template>
