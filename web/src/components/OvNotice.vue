<script setup lang="ts">
// Notice: what happened, why, and what you can do — each next action with
// its command (first-run-onboarding#REQ:problem-pattern and after-action-result).
import { t } from '../copy'
import type { Next } from '../api'
import OvCommand from './OvCommand.vue'
import OvText from './OvText.vue'

withDefaults(
  defineProps<{
    tone?: 'info' | 'success' | 'problem'
    title: string
    reason?: string
    next?: Next[]
    live?: boolean
  }>(),
  { tone: 'info', reason: '', next: () => [], live: false },
)
</script>

<template>
  <div
    :role="live ? (tone === 'problem' ? 'alert' : 'status') : undefined"
    class="rounded-xl border-l-4 px-4 py-3.5 sm:px-5"
    :class="{
      'border-accent bg-accent-soft': tone === 'info',
      'border-ok bg-ok-soft': tone === 'success',
      'border-danger bg-danger-soft': tone === 'problem',
    }"
  >
    <p class="font-semibold"><OvText :text="title" /></p>
    <p v-if="reason" class="mt-1">{{ t('problem.why', { reason }) }}</p>
    <div v-if="$slots.default" class="mt-2"><slot /></div>
    <template v-if="next.length">
      <p v-if="tone !== 'info'" class="mt-3 font-medium">{{ t('problem.what_you_can_do') }}</p>
      <ul class="mt-3 flex flex-col gap-3">
        <li v-for="item in next" :key="item.label">
          <p class="mb-1"><OvText :text="item.label" /></p>
          <OvCommand v-if="item.command" :command="item.command" />
        </li>
      </ul>
    </template>
  </div>
</template>
