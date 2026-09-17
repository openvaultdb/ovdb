<script setup lang="ts">
// The console shell: brand header, the current screen, and the two states
// that replace any screen — a session that ended (any 401) and a server that
// stopped (local-server-and-web-console#REQ:session-ended-copy).
import { nextTick, watch, watchEffect } from 'vue'

import { connection } from './api'
import OvNotice from './components/OvNotice.vue'
import OvText from './components/OvText.vue'
import { t } from './copy'
import { navigate, screen } from './router'
import HomeScreen from './screens/HomeScreen.vue'
import ServerScreen from './screens/ServerScreen.vue'
import SettingsScreen from './screens/SettingsScreen.vue'
import { useServer } from './useServer'

// The shell keeps the server poll running on every screen.
useServer()

const titles = {
  home: () => t('app.name'),
  server: () => t('console.title', { screen: t('server.title') }),
  settings: () => t('console.title', { screen: t('settings.title') }),
  'not-found': () => t('app.name'),
}

watchEffect(() => {
  document.title = titles[screen.value]()
})

// Move focus to the new screen's heading so keyboard and screen reader
// users land at the top of what changed.
watch(screen, async () => {
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
      <div class="mx-auto flex max-w-3xl items-center px-4 py-3.5 sm:px-6">
        <a href="/" class="text-lg font-bold tracking-tight text-ink" @click="home">{{ t('app.name') }}</a>
      </div>
    </header>

    <main class="mx-auto max-w-3xl px-4 pt-8 pb-16 sm:px-6 sm:pt-12">
      <div v-if="connection === 'session-ended'" data-testid="session-ended" class="flex flex-col gap-4">
        <h1 tabindex="-1" class="sr-only">{{ t('app.name') }}</h1>
        <OvNotice live :title="t('console.session_ended')" />
      </div>
      <div v-else-if="connection === 'unreachable'" data-testid="server-stopped" class="flex flex-col gap-4">
        <h1 tabindex="-1" class="sr-only">{{ t('app.name') }}</h1>
        <OvNotice live tone="problem" :title="t('server.not_running.message')">
          <p><OvText :text="t('server.stopped_copy')" /></p>
        </OvNotice>
      </div>
      <HomeScreen v-else-if="screen === 'home'" />
      <ServerScreen v-else-if="screen === 'server'" />
      <SettingsScreen v-else-if="screen === 'settings'" />
      <div v-else class="flex flex-col gap-4">
        <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight">{{ t('api.not_found') }}</h1>
        <p>
          <a href="/" class="font-medium text-accent hover:underline" @click="home">{{ t('nav.home') }}</a>
        </p>
      </div>
    </main>
  </div>
</template>
