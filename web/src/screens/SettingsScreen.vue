<script setup lang="ts">
// Settings (capability 7): the port the OVDB server uses the next time it
// starts. The value goes to the server as typed; the server validates it and
// says what happened, and this page renders that
// (configuration-parity#REQ:error-envelope).
import { nextTick, onMounted, ref, watch } from 'vue'

import { api, expectServerMove, type ApiError, type ConfigDocument, type Next } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCard from '../components/OvCard.vue'
import OvNotice from '../components/OvNotice.vue'
import OvText from '../components/OvText.vue'
import OvTextField from '../components/OvTextField.vue'
import { t } from '../copy'
import { useServer } from '../useServer'

const { server } = useServer()

const port = ref('')
const configured = ref<number | undefined>()
const loaded = ref(false)
const saving = ref(false)
const problem = ref<ApiError | null>(null)
const result = ref<{ port: number; changed: boolean; moves: boolean; next: Next[] } | null>(null)
const outcome = ref<HTMLElement>()

onMounted(async () => {
  const response = await api<ConfigDocument>('GET', '/api/local/v1/config')
  if (response.ok) configured.value = response.data.config.server.port
  loaded.value = true
})

// The field starts at the configured port or, until one is configured, the
// port the server runs on; after that it is the person's.
const unwatch = watch(
  [loaded, server],
  () => {
    if (!loaded.value) return
    const initial = configured.value ?? server.value?.port
    if (initial !== undefined) {
      port.value = String(initial)
      unwatch()
    }
  },
  { flush: 'sync' },
)

async function save() {
  problem.value = null
  result.value = null
  saving.value = true
  const response = await api<ConfigDocument>('PUT', '/api/local/v1/config', { key: 'server.port', value: port.value })
  saving.value = false
  if (response.ok) {
    const saved = response.data.config.server.port ?? server.value?.port ?? 0
    const moves = response.data.changed !== false && saved !== server.value?.port
    if (moves) expectServerMove()
    result.value = { port: saved, changed: response.data.changed !== false, moves, next: response.data.next }
    port.value = String(saved)
  } else {
    problem.value = response.error
  }
  await nextTick()
  outcome.value?.focus()
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvBackLink />
    <h1 tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('settings.title') }}</h1>

    <OvCard :title="t('server.title')">
      <form class="flex flex-col gap-5" novalidate @submit.prevent="save">
        <OvTextField
          v-model="port"
          :label="t('settings.port.label')"
          :help="server ? t('settings.port.help', { port: String(server.port) }) : undefined"
          :error="problem?.code === 'invalid_argument' ? (problem.reason ?? problem.message) : undefined"
          inputmode="numeric"
        />
        <div>
          <OvButton type="submit" :busy="saving">{{ t('settings.port.save') }}</OvButton>
        </div>
      </form>
    </OvCard>

    <div ref="outcome" tabindex="-1">
      <template v-if="result">
        <OvNotice v-if="!result.changed" live :title="t('settings.port.unchanged', { port: String(result.port) })" />
        <OvNotice
          v-else
          live
          tone="success"
          :title="t('settings.port.saved', { port: String(result.port) })"
          :next="result.next"
        >
          <p v-if="result.moves"><OvText :text="t('settings.port.after_restart')" /></p>
        </OvNotice>
      </template>
      <OvNotice
        v-if="problem"
        live
        tone="problem"
        :title="problem.message"
        :reason="problem.code === 'invalid_argument' ? undefined : problem.reason"
        :next="problem.next"
      />
    </div>
  </div>
</template>
