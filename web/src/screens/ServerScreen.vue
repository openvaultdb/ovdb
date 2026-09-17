<script setup lang="ts">
// The OVDB server panel (capability 4): state, address with its fallback,
// version, start time and log. Stopping and restarting stay in the
// terminal (configuration-parity exception E2), so the panel says how.
import { computed } from 'vue'

import OvBackLink from '../components/OvBackLink.vue'
import OvCard from '../components/OvCard.vue'
import OvNotice from '../components/OvNotice.vue'
import OvStatusBadge from '../components/OvStatusBadge.vue'
import { t } from '../copy'
import { useServer } from '../useServer'

const { server } = useServer()

const started = computed(() => {
  const at = server.value?.started_at
  return at ? new Date(at).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : ''
})

const stopNext = [
  { label: t('next.stop_server'), command: 'ovdb server stop' },
  { label: t('next.restart_server'), command: 'ovdb server restart' },
]
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
      <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('server.title') }}</h1>
      <OvStatusBadge v-if="server" tone="ok" :label="t('server.badge.running')" />
    </div>

    <OvCard v-if="server">
      <dl class="flex flex-col gap-4">
        <div class="grid gap-x-8 gap-y-0.5 sm:grid-cols-[9rem_1fr]">
          <dt class="font-semibold text-muted">{{ t('server.detail.address') }}</dt>
          <dd class="min-w-0">
            <span data-testid="address" class="block font-medium break-all">{{ server.address }}</span>
            <span class="block text-muted break-all">{{ t('server.also_at', { address: server.fallback_address }) }}</span>
          </dd>
        </div>
        <div v-if="server.version" class="grid gap-x-8 gap-y-0.5 sm:grid-cols-[9rem_1fr]">
          <dt class="font-semibold text-muted">{{ t('server.detail.version') }}</dt>
          <dd class="min-w-0 break-words">{{ server.version }}</dd>
        </div>
        <div v-if="started" class="grid gap-x-8 gap-y-0.5 sm:grid-cols-[9rem_1fr]">
          <dt class="font-semibold text-muted">{{ t('server.detail.started') }}</dt>
          <dd>{{ started }}</dd>
        </div>
        <div class="grid gap-x-8 gap-y-0.5 sm:grid-cols-[9rem_1fr]">
          <dt class="font-semibold text-muted">{{ t('server.detail.log') }}</dt>
          <dd class="min-w-0 font-mono text-[0.95rem] break-all">{{ server.log }}</dd>
        </div>
      </dl>
    </OvCard>
    <p v-else class="text-muted">{{ t('console.loading') }}</p>

    <OvNotice data-testid="stop-help" :title="t('server.stop.title')" :next="stopNext">
      <p>{{ t('server.stop.body') }}</p>
    </OvNotice>
  </div>
</template>
