<script setup lang="ts">
// Settings (capabilities 7, 23 and 24): the port the OVDB server uses the
// next time it starts, and Usage statistics with the same state, reason,
// provider and lists as `ovdb telemetry status` and the TUI. The value goes to the server as typed; the server validates it and
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
import { turnOffUsage, turnOnUsage, loadUsage, usage } from '../usage'
import { useServer } from '../useServer'

const { server } = useServer()

const port = ref('')
const configured = ref<number | undefined>()
const loaded = ref(false)
const saving = ref(false)
const problem = ref<ApiError | null>(null)
const result = ref<{ port: number; changed: boolean; moves: boolean; next: Next[] } | null>(null)
const outcome = ref<HTMLElement>()

const usageBusy = ref(false)
const usageProblem = ref<ApiError | null>(null)

async function setUsage(on: boolean) {
  usageBusy.value = true
  usageProblem.value = on ? await turnOnUsage() : await turnOffUsage()
  usageBusy.value = false
}

const usageStates = {
  not_asked: () => t('telemetry.state.not_asked'),
  enabled: () => t('telemetry.state.enabled'),
  disabled: () => t('telemetry.state.disabled'),
}

onMounted(async () => {
  void loadUsage()
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

    <OvCard :title="t('telemetry.title')">
      <div v-if="usage" data-testid="usage-settings" class="flex flex-col gap-4">
        <p class="font-medium" data-testid="usage-state">{{ t('telemetry.status_line', { state: usageStates[usage.state]() }) }}</p>
        <p v-if="usage.reason_text" class="text-muted" data-testid="usage-reason">{{ usage.reason_text }}</p>
        <p>{{ t('telemetry.intro') }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <h3 class="font-semibold">{{ t('telemetry.collected.title') }}</h3>
            <ul class="list-disc pl-5 text-muted"><li v-for="line in usage.collected" :key="line">{{ line }}</li></ul>
          </div>
          <div>
            <h3 class="font-semibold">{{ t('telemetry.never.title') }}</h3>
            <ul class="list-disc pl-5 text-muted"><li v-for="line in usage.never_collected" :key="line">{{ line }}</li></ul>
          </div>
        </div>
        <div class="flex flex-wrap gap-3">
          <OvButton v-if="usage.state !== 'enabled'" variant="secondary" data-testid="usage-settings-on" :busy="usageBusy" @click="setUsage(true)">{{ t('telemetry.button.turn_on') }}</OvButton>
          <OvButton v-if="usage.state === 'not_asked'" variant="secondary" data-testid="usage-settings-off" :busy="usageBusy" @click="setUsage(false)">{{ t('telemetry.button.keep_off') }}</OvButton>
          <OvButton v-if="usage.state === 'enabled'" variant="secondary" data-testid="usage-settings-off" :busy="usageBusy" @click="setUsage(false)">{{ t('telemetry.button.turn_off') }}</OvButton>
        </div>
        <p class="text-muted"><OvText :text="t('telemetry.change_any_time')" /></p>
        <OvNotice v-if="usageProblem" live tone="problem" :title="usageProblem.message" :reason="usageProblem.reason" :next="usageProblem.next" />
      </div>
      <p v-else class="text-muted">{{ t('console.loading') }}</p>
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
