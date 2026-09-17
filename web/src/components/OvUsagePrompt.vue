<script setup lang="ts">
// The one usage statistics prompt, on the Result of the first successful
// action (telemetry-consent#REQ:consent-prompt-placement): Turn on and No
// thanks with equal weight, What's collected? and a close button.
import { onBeforeUnmount, ref } from 'vue'

import { type ApiError } from '../api'
import { t } from '../copy'
import { dismissUsagePrompt, promptVisible, turnOffUsage, turnOnUsage, usage } from '../usage'
import OvButton from './OvButton.vue'
import OvNotice from './OvNotice.vue'

const details = ref(false)
const busy = ref(false)
const answered = ref<'on' | 'off' | null>(null)
const problem = ref<ApiError | null>(null)

async function answer(on: boolean) {
  busy.value = true
  problem.value = on ? await turnOnUsage() : await turnOffUsage()
  busy.value = false
  if (!problem.value) answered.value = on ? 'on' : 'off'
}

// Leaving the Result without an answer is a dismissal.
onBeforeUnmount(dismissUsagePrompt)
</script>

<template>
  <section v-if="promptVisible" data-testid="usage-prompt" aria-labelledby="usage-prompt-title" class="flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5">
    <div class="flex items-start justify-between gap-3">
      <h2 id="usage-prompt-title" class="text-lg font-semibold tracking-tight">{{ t('telemetry.prompt.title') }}</h2>
      <button type="button" data-testid="usage-dismiss" class="min-h-11 min-w-11 rounded-lg text-muted hover:bg-surface-2" :aria-label="t('telemetry.prompt.close')" @click="dismissUsagePrompt">×</button>
    </div>
    <p class="text-muted">{{ t('telemetry.intro') }}</p>
    <div class="flex flex-wrap gap-3">
      <OvButton variant="secondary" data-testid="usage-turn-on" :busy="busy" @click="answer(true)">{{ t('telemetry.button.turn_on') }}</OvButton>
      <OvButton variant="secondary" data-testid="usage-no-thanks" :busy="busy" @click="answer(false)">{{ t('telemetry.button.no_thanks') }}</OvButton>
      <button type="button" data-testid="usage-details" class="min-h-11 px-2 font-medium text-accent hover:underline" :aria-expanded="details" @click="details = !details">
        {{ t('telemetry.prompt.whats_collected') }}
      </button>
    </div>
    <div v-if="details && usage" data-testid="usage-lists" class="grid gap-4 sm:grid-cols-2">
      <div>
        <h3 class="font-semibold">{{ t('telemetry.collected.title') }}</h3>
        <ul class="list-disc pl-5 text-muted"><li v-for="line in usage.collected" :key="line">{{ line }}</li></ul>
      </div>
      <div>
        <h3 class="font-semibold">{{ t('telemetry.never.title') }}</h3>
        <ul class="list-disc pl-5 text-muted"><li v-for="line in usage.never_collected" :key="line">{{ line }}</li></ul>
      </div>
    </div>
    <OvNotice v-if="problem" live tone="problem" :title="problem.message" :reason="problem.reason" :next="problem.next" />
  </section>
  <OvNotice v-else-if="answered === 'on'" live tone="success" :title="t('telemetry.enabled')" />
  <OvNotice v-else-if="answered === 'off'" live :title="t('telemetry.prompt.declined')" />
</template>
