<script setup lang="ts">
// Home (first-run-onboarding#REQ:home-menu-options, REQ:home-status-line):
// the status line, the one question, and only the options implemented so
// far, in the founder's order. The web console shows "OVDB server" instead
// of "Start the OVDB server": a page served by the server cannot start it
// (configuration-parity exception E1).
import { computed, onMounted, ref } from 'vue'

import { api, type StatusDocument } from '../api'
import OvOptionList, { type Option } from '../components/OvOptionList.vue'
import { t } from '../copy'
import { navigate } from '../router'

const status = ref<StatusDocument | null>(null)

onMounted(async () => {
  const result = await api<StatusDocument>('GET', '/api/local/v1/status')
  if (result.ok) status.value = result.data
})

const primary = computed<Option[]>(() => [
  {
    id: 'server',
    label: t('home.menu.server'),
    help: t('home.menu.server_help'),
    badge: status.value ? { tone: 'ok', label: t('server.badge.running') } : undefined,
    to: '/server',
  },
])

const secondary = [{ id: 'settings', label: t('home.menu.settings'), to: '/settings' }]

function open(event: MouseEvent, to: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(to)
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <header class="flex flex-col gap-4">
      <p class="max-w-prose text-lg text-muted">{{ t('home.tagline') }}</p>
      <p data-testid="status-line" class="flex min-h-7 items-baseline gap-2.5" aria-live="polite">
        <template v-if="status">
          <span aria-hidden="true" class="size-2.5 shrink-0 translate-y-[-0.1em] rounded-full bg-ok"></span>
          <span class="min-w-0 break-words">{{ t('home.status.server_running', { address: status.server.address }) }}</span>
        </template>
        <span v-else class="text-muted">{{ t('console.loading') }}</span>
      </p>
    </header>

    <section class="flex flex-col gap-4" aria-labelledby="home-question">
      <h1 id="home-question" tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">
        {{ t('home.question') }}
      </h1>
      <OvOptionList :options="primary" :label="t('home.question')" />
      <nav :aria-label="t('home.menu.more')" class="flex flex-wrap gap-x-6 gap-y-2 px-1">
        <a
          v-for="item in secondary"
          :key="item.id"
          :href="item.to"
          :data-option="item.id"
          class="font-medium text-accent hover:underline"
          @click="open($event, item.to)"
        >
          {{ item.label }}
        </a>
      </nav>
    </section>
  </div>
</template>
