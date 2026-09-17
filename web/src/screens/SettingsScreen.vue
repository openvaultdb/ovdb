<script setup lang="ts">
// Settings (capability 7): the port the OVDB server uses the next time it
// starts. The server validates and saves it; the page shows what it says.
import { onMounted, ref, watch } from 'vue'

import { api, type ApiError, type ConfigDocument, type Next } from '../api'
import OvBackLink from '../components/OvBackLink.vue'
import OvButton from '../components/OvButton.vue'
import OvCard from '../components/OvCard.vue'
import OvNotice from '../components/OvNotice.vue'
import OvTextField from '../components/OvTextField.vue'
import { t } from '../copy'
import { useServer } from '../useServer'

const { server } = useServer()

const port = ref('')
const configured = ref<number | undefined>()
const loaded = ref(false)
const saving = ref(false)
const fieldError = ref('')
const problem = ref<ApiError | null>(null)
const saved = ref<{ port: number; next: Next[] } | null>(null)

onMounted(async () => {
  const result = await api<ConfigDocument>('GET', '/api/local/v1/config')
  if (result.ok) configured.value = result.data.config.server.port
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
  fieldError.value = ''
  problem.value = null
  saved.value = null
  const value = port.value.trim()
  if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > 65535) {
    fieldError.value = t('settings.port.invalid')
    return
  }
  saving.value = true
  const result = await api<ConfigDocument>('PUT', '/api/local/v1/config', { key: 'server.port', value })
  saving.value = false
  if (result.ok) {
    saved.value = { port: result.data.config.server.port ?? Number(value), next: result.data.next }
    port.value = String(saved.value.port)
  } else if (result.error.code === 'invalid_argument') {
    fieldError.value = result.error.reason ?? result.error.message
  } else {
    problem.value = result.error
  }
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
          :error="fieldError"
          inputmode="numeric"
        />
        <div>
          <OvButton type="submit" :busy="saving">{{ t('settings.port.save') }}</OvButton>
        </div>
      </form>
    </OvCard>

    <OvNotice
      v-if="saved"
      live
      tone="success"
      :title="t('settings.port.saved', { port: String(saved.port) })"
      :next="saved.next"
    />
    <OvNotice
      v-if="problem"
      live
      tone="problem"
      :title="problem.message"
      :reason="problem.reason"
      :next="problem.next"
    />
  </div>
</template>
