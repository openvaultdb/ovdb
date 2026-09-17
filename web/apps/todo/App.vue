<script setup lang="ts">
// The TODO demo app (spec/features/todo-demo, decision 0010): To buy and To
// watch from the local demo database, on the console's origin and session.
// It exists to show OpenVaultDB at work — the same lists change here, in a
// terminal and through an AI assistant — so it stays small and says where
// the data lives.
import { computed, onBeforeUnmount, onMounted, watchEffect } from 'vue'

import { connection } from '../../src/api'
import OvCommand from '../../src/components/OvCommand.vue'
import OvNotice from '../../src/components/OvNotice.vue'
import OvText from '../../src/components/OvText.vue'
import { t } from '../../src/copy'
import { quoteArg } from '../../src/datapath'
import TodoList from './TodoList.vue'
import { pollInterval, useTodo } from './todo'

const { demo, lists, loaded, removed, loadProblem, saveProblem, refresh, add, toggle, remove } = useTodo()

watchEffect(() => {
  document.title = t('todo.page_title')
})

// Poll while the tab is visible; refresh at once when it comes back.
let timer: ReturnType<typeof setInterval> | undefined
const visible = () => document.visibilityState !== 'hidden'
function onVisible() {
  if (visible()) void refresh()
}
onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    if (visible()) void refresh()
  }, pollInterval)
  window.addEventListener('focus', onVisible)
  document.addEventListener('visibilitychange', onVisible)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  window.removeEventListener('focus', onVisible)
  document.removeEventListener('visibilitychange', onVisible)
})

const firstList = computed(() => lists.value[0]?.path ?? '/lists/to-buy')
const tryCommand = computed(() => `ovdb list ${quoteArg(firstList.value + '/items')} --db ${quoteArg(demo.value?.database ?? 'todo')}`)
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-line">
      <div class="mx-auto flex max-w-4xl items-center justify-between gap-4 px-4 py-3.5 sm:px-6">
        <a href="/" class="text-lg leading-7 font-bold tracking-tight text-ink">{{ t('app.name') }}</a>
        <span class="rounded-full bg-accent-soft px-3 py-0.5 text-sm font-semibold text-accent">{{ t('todo.badge') }}</span>
      </div>
    </header>

    <main class="mx-auto max-w-4xl px-4 pt-8 pb-16 sm:px-6 sm:pt-12">
      <div v-if="connection === 'session-ended'" data-testid="session-ended" class="flex flex-col gap-4" role="status">
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('console.session_ended.title') }}</h1>
        <p class="text-lg"><OvText :text="t('console.session_ended.next')" /></p>
        <p class="text-lg"><OvText :text="t('todo.come_back')" /></p>
      </div>
      <div v-else-if="connection === 'unreachable'" data-testid="server-stopped" class="flex flex-col gap-4" role="alert">
        <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('server.not_running.message') }}</h1>
        <p class="text-lg"><OvText :text="t('server.stopped_copy')" /></p>
        <p class="text-lg"><OvText :text="t('todo.come_back')" /></p>
      </div>

      <template v-else-if="demo && (!demo.installed || removed)">
        <div data-testid="not-installed" class="flex flex-col gap-4" :role="removed ? 'alert' : undefined">
          <h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ removed ? t('todo.removed.title') : t('todo.not_installed.title') }}</h1>
          <p class="text-lg">{{ t('todo.not_installed.body') }}</p>
          <OvCommand command="ovdb demo install --yes" />
          <p><a href="/demo" class="font-medium text-accent hover:underline">{{ t('demo.title') }}</a></p>
        </div>
      </template>

      <OvNotice v-else-if="loadProblem && !loaded" live tone="problem" :title="loadProblem.message" :reason="loadProblem.reason" :next="loadProblem.next" />
      <p v-else-if="!loaded" class="text-muted">{{ t('console.loading') }}</p>

      <div v-else class="flex min-w-0 flex-col gap-8">
        <div class="flex flex-col gap-2">
          <h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">{{ t('todo.title') }}</h1>
          <p class="max-w-2xl text-lg text-muted">{{ t('todo.intro') }}</p>
        </div>

        <OvNotice v-if="saveProblem" live tone="problem" :title="t('todo.save_failed')" :reason="saveProblem.reason || saveProblem.message" />

        <div class="grid grid-cols-1 items-start gap-5 md:grid-cols-2">
          <TodoList
            v-for="list in lists"
            :key="list.id"
            :list="list"
            @add="(title) => add(list, title)"
            @toggle="(item) => toggle(list, item)"
            @remove="(item) => remove(list, item)"
          />
        </div>

        <footer class="flex min-w-0 flex-col gap-3 rounded-2xl border border-dashed border-line px-5 py-4 text-muted" data-testid="stored">
          <p class="flex items-start gap-2.5">
            <svg aria-hidden="true" viewBox="0 0 20 20" class="mt-1 size-4 shrink-0" fill="none" stroke="currentColor" stroke-width="1.6">
              <ellipse cx="10" cy="4.5" rx="6.5" ry="2.5" />
              <path d="M3.5 4.5v11c0 1.4 2.9 2.5 6.5 2.5s6.5-1.1 6.5-2.5v-11M3.5 10c0 1.4 2.9 2.5 6.5 2.5s6.5-1.1 6.5-2.5" />
            </svg>
            <span class="min-w-0 [overflow-wrap:anywhere]">
              <span class="font-semibold text-ink">{{ t('todo.stored_by') }}</span> ·
              <span data-testid="stored-path">{{ t('todo.stored', { path: demo?.location ?? '' }) }}</span>
            </span>
          </p>
          <div class="flex min-w-0 flex-col gap-1.5">
            <p><OvText :text="t('todo.same_data')" /></p>
            <OvCommand :command="tryCommand" />
          </div>
          <p><a href="/" class="font-medium text-accent hover:underline">{{ t('todo.console_link') }}</a></p>
        </footer>
      </div>
    </main>
  </div>
</template>
