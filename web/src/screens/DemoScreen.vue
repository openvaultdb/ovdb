<script setup lang="ts">
// Try a demo (capabilities 18 and 19, spec/features/todo-demo). The page says
// where the TODO demo's lists will be stored before anything is written,
// installs through the same endpoint as `ovdb demo install`, and then offers
// what the server says comes next: Open TODO app, Install TODO AI skill (the
// skill consent step) and Done. The console is
// already signed in, so Open TODO app is a plain link to /apps/todo/.
import { computed, nextTick, onMounted, ref } from 'vue'

import { api, type ApiError, type DemoDocument, type Next } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCard from '../components/OvCard.vue'
import OvCommand from '../components/OvCommand.vue'
import OvNotice from '../components/OvNotice.vue'
import OvText from '../components/OvText.vue'
import OvUsagePrompt from '../components/OvUsagePrompt.vue'
import { t } from '../copy'
import { navigate } from '../router'
import { offerUsagePrompt, recordUsage } from '../usage'

const document = ref<DemoDocument | null>(null)
const loadProblem = ref<ApiError | null>(null)
const installing = ref(false)
const problem = ref<ApiError | null>(null)
// Set once this page installed it or found it installed.
const result = ref<DemoDocument | null>(null)
const outcome = ref<HTMLElement>()

onMounted(async () => {
  const response = await api<DemoDocument>('GET', '/api/local/v1/demo')
  if (!response.ok) {
    loadProblem.value = response.error
    return
  }
  document.value = response.data
  if (response.data.installed) result.value = { ...response.data, already_installed: true }
})

async function install() {
  problem.value = null
  installing.value = true
  const response = await api<DemoDocument>('POST', '/api/local/v1/demo/install', {})
  installing.value = false
  recordUsage({ event: 'demo_installed', success: response.ok, already_installed: response.ok && response.data.already_installed === true }, true)
  if (response.ok) {
    result.value = response.data
    offerUsagePrompt()
  } else {
    recordUsage({ event: 'onboarding_error', step: 'demo', error_code: response.error.code }, true)
    problem.value = response.error
  }
  await nextTick()
  outcome.value?.focus()
}

const commands = computed(() => result.value?.next.filter((item: Next) => item.command) ?? [])

// Install TODO AI skill opens the skill consent step, which returns here on Not now.
function skillOffer(item: Next): string {
  const id = item.command?.split(' ').pop() ?? ''
  return `/skills?skill=${encodeURIComponent(id)}&from=/demo`
}

function go(event: MouseEvent, path: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(path)
}

function home(event: MouseEvent) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate('/')
}

function explore(event: MouseEvent) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(`/explore?db=${result.value?.database}`)
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />
    <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('demo.title') }}</h1>

    <OvNotice v-if="loadProblem" live tone="problem" :title="loadProblem.message" :reason="loadProblem.reason" :next="loadProblem.next" />
    <p v-else-if="!document" class="text-muted">{{ t('console.loading') }}</p>

    <!-- Result -->
    <div v-else-if="result" ref="outcome" tabindex="-1" data-testid="demo-result" class="flex flex-col gap-6">
      <OvNotice live tone="success" :title="result.already_installed ? t('demo.already.title') : t('demo.ready.title')">
        <p class="break-words">{{ t('demo.ready.stored', { path: result.location }) }}</p>
        <p>{{ t('demo.ready.shared') }}</p>
      </OvNotice>
      <OvUsagePrompt />
      <section class="flex flex-col gap-4" aria-labelledby="demo-next">
        <h2 id="demo-next" class="text-lg font-semibold tracking-tight">{{ t('home.what_next') }}</h2>
        <div class="flex flex-wrap gap-3">
          <template v-for="item in result.next" :key="item.label">
            <a
              v-if="item.action === 'open_app'"
              :href="result.app_path"
              data-testid="open-todo-app"
              @click="recordUsage({ event: 'demo_opened', success: true })"
              class="inline-flex min-h-11 items-center rounded-lg bg-accent px-5 font-semibold text-on-accent hover:bg-accent-hover"
              >{{ item.label }}</a
            >
            <a
              v-else-if="item.action === 'install_skill'"
              :href="skillOffer(item)"
              data-testid="demo-install-skill"
              class="inline-flex min-h-11 items-center rounded-lg border border-line bg-surface px-5 font-semibold text-ink hover:bg-surface-2"
              @click="go($event, skillOffer(item))"
              >{{ item.label }}</a
            >
            <a
              v-else-if="item.action === 'explore'"
              :href="`/explore?db=${result?.database}`"
              data-testid="demo-explore"
              class="inline-flex min-h-11 items-center rounded-lg border border-line bg-surface px-5 font-semibold text-ink hover:bg-surface-2"
              @click="explore($event)"
              >{{ item.label }}</a
            >
            <a
              v-else-if="item.action === 'done'"
              href="/"
              class="inline-flex min-h-11 items-center rounded-lg border border-line bg-surface px-5 font-semibold text-ink hover:bg-surface-2"
              @click="home"
              >{{ item.label }}</a
            >
          </template>
        </div>
        <div v-if="commands.length" class="flex flex-col gap-2">
          <p class="text-muted">{{ t('browse.same_in_terminal') }}</p>
          <OvCommand v-for="item in commands" :key="item.label" :command="item.command!" />
        </div>
      </section>
    </div>

    <!-- Before installing: what it is and where it goes -->
    <template v-else>
      <OvCard>
        <div class="flex flex-col gap-4">
          <p class="text-lg"><OvText :text="t('demo.intro')" /></p>
          <p class="break-words text-muted" data-testid="demo-location">{{ t('demo.will_store', { path: document.location }) }}</p>
          <div>
            <OvButton data-testid="install-demo" :busy="installing" @click="install">{{ t('demo.install') }}</OvButton>
          </div>
        </div>
      </OvCard>
      <div ref="outcome" tabindex="-1">
        <OvNotice v-if="problem" live tone="problem" :title="problem.message" :reason="problem.reason" :next="problem.next" />
      </div>
      <div class="flex flex-col gap-2">
        <p class="text-muted">{{ t('browse.same_in_terminal') }}</p>
        <OvCommand command="ovdb demo install" />
      </div>
    </template>
  </div>
</template>
