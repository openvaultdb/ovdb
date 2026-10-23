<script setup lang="ts">
// The console shell: brand header, the current screen, and the two states
// that replace any screen — a session that ended (any 401) and a server that
// stopped (local-server-and-web-console#REQ:session-ended-copy).
import { nextTick, onMounted, watch, watchEffect } from 'vue'

import { connection } from './api'
import OvText from './components/OvText.vue'
import { t } from './copy'
import { navigate, screen } from './router'
import HomeScreen from './screens/HomeScreen.vue'
import ServerScreen from './screens/ServerScreen.vue'
import SettingsScreen from './screens/SettingsScreen.vue'
import DatabasesScreen from './screens/DatabasesScreen.vue'
import CreateScreen from './screens/CreateScreen.vue'
import ConnectScreen from './screens/ConnectScreen.vue'
import BrowseScreen from './screens/BrowseScreen.vue'
import DemoScreen from './screens/DemoScreen.vue'
import SkillsScreen from './screens/SkillsScreen.vue'
import ExploreScreen from './screens/ExploreScreen.vue'
import { loadUsage } from './usage'
import { useServer } from './useServer'

// The shell keeps the server poll running on every screen.
useServer()

// Usage statistics state, once per page: until it is known, page events are
// only kept in memory.
onMounted(() => void loadUsage())

const titles = {
  home: () => t('app.name'),
  demo: () => t('console.title', { screen: t('demo.title') }),
  server: () => t('console.title', { screen: t('server.title') }),
  settings: () => t('console.title', { screen: t('settings.title') }),
  databases: () => t('console.title', { screen: t('databases.title') }),
  create: () => t('console.title', { screen: t('create.title') }),
  connect: () => t('console.title', { screen: t('home.menu.connect_database') }),
  browse: () => t('console.title', { screen: t('home.menu.browse') }),
  skills: () => t('console.title', { screen: t('skills.title') }),
  explore: () => t('console.title', { screen: t('explore.title') }),
  'not-found': () => t('app.name'),
}

watchEffect(() => {
  document.title = titles[screen.value]()
})

// Move focus to the new screen's heading so keyboard and screen reader
// users land at the top of what changed.
watch([screen, connection], async () => {
  await nextTick()
  document.querySelector<HTMLElement>('main h1')?.focus()
})

function home(event: MouseEvent) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate('/')
}
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-line">
      <div class="mx-auto flex max-w-3xl items-center justify-between gap-4 px-4 py-3.5 sm:px-6">
        <a href="/" class="text-lg leading-7 font-bold tracking-tight text-ink" @click="home">{{ t('app.name') }}</a>
        <form v-if="connection === 'ok'" method="post" action="/logout">
          <button type="submit" class="rounded-md px-2 leading-7 font-medium text-accent hover:underline">
            {{ t('console.sign_out') }}
          </button>
        </form>
      </div>
    </header>

    <main class="mx-auto max-w-3xl px-4 pt-8 pb-16 sm:px-6 sm:pt-12">
      <div v-if="connection === 'session-ended'" data-testid="session-ended" class="flex flex-col gap-4" role="status">
        <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">
          {{ t('console.session_ended.title') }}
        </h1>
        <p class="text-lg"><OvText :text="t('console.session_ended.next')" /></p>
      </div>
      <div v-else-if="connection === 'unreachable'" data-testid="server-stopped" class="flex flex-col gap-4" role="alert">
        <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">
          {{ t('server.not_running.message') }}
        </h1>
        <p class="text-lg"><OvText :text="t('server.stopped_copy')" /></p>
      </div>
      <HomeScreen v-else-if="screen === 'home'" />
      <DemoScreen v-else-if="screen === 'demo'" />
      <ServerScreen v-else-if="screen === 'server'" />
      <SettingsScreen v-else-if="screen === 'settings'" />
      <DatabasesScreen v-else-if="screen === 'databases'" />
      <CreateScreen v-else-if="screen === 'create'" />
      <ConnectScreen v-else-if="screen === 'connect'" />
      <BrowseScreen v-else-if="screen === 'browse'" />
      <SkillsScreen v-else-if="screen === 'skills'" />
      <ExploreScreen v-else-if="screen === 'explore'" />
      <div v-else class="flex flex-col gap-4">
        <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight">{{ t('api.not_found') }}</h1>
        <p>
          <a href="/" class="font-medium text-accent hover:underline" @click="home">{{ t('nav.home') }}</a>
        </p>
      </div>
    </main>
  </div>
</template>
