<script setup lang="ts">
// The Result of creating or connecting a database: what happened, the
// server's next actions with their commands, and in-page actions as links
// and buttons (database-setup-and-providers#REQ:create-result-next-actions).
import { computed, ref } from 'vue'

import { api, type ApiError, type ContextDocument, type DatabaseResult } from '../api'
import { t } from '../copy'
import { browseRoute } from '../datapath'
import { navigate } from '../router'
import OvButton from './OvButton.vue'
import OvCommand from './OvCommand.vue'
import OvNotice from './OvNotice.vue'
import OvText from './OvText.vue'
import OvUsagePrompt from './OvUsagePrompt.vue'
import { recordUsage } from '../usage'

const props = defineProps<{
  result: DatabaseResult
  title: string
  stored: string
  another: string
  // The onboarding step this Result completes: create or connect.
  step?: string
}>()
defineEmits<{ another: [] }>()

// Commands to repeat in a terminal; in-page actions become links and buttons.
const commands = computed(() => props.result.next.filter((item) => !item.action))

const using = ref(false)
const usedDefault = ref<string | null>(null)
const problem = ref<ApiError | null>(null)

// The console makes a database the default for all projects; choosing one
// for a project happens in a terminal (parity E3).
async function useAsDefault(id: string) {
  using.value = true
  problem.value = null
  const response = await api<ContextDocument>('PUT', '/api/local/v1/context', { scope: 'global', database: id })
  using.value = false
  if (response.ok) usedDefault.value = response.data.message ?? null
  else problem.value = response.error
}

function done(event: MouseEvent) {
  recordUsage({ event: 'onboarding_completed', step: props.step ?? 'create' })
  go(event, '/')
}

function go(event: MouseEvent, path: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(path)
}

const link = 'inline-flex min-h-11 items-center rounded-lg border border-line bg-surface px-5 font-semibold text-ink hover:bg-surface-2'
</script>

<template>
  <div class="flex flex-col gap-6">
    <OvNotice live tone="success" :title="title">
      <p class="break-words"><OvText :text="stored" /></p>
    </OvNotice>
    <OvUsagePrompt />
    <section class="flex flex-col gap-4" aria-labelledby="what-next">
      <h2 id="what-next" class="text-lg font-semibold tracking-tight">{{ t('home.what_next') }}</h2>
      <ul v-if="commands.length" class="flex flex-col gap-4">
        <li v-for="item in commands" :key="item.label">
          <p class="mb-1 break-words"><OvText :text="item.label" /></p>
          <OvCommand v-if="item.command" :command="item.command" />
        </li>
      </ul>
      <div class="flex flex-wrap gap-3">
        <template v-for="item in result.next" :key="item.label">
          <a v-if="item.action === 'browse'" :href="browseRoute(result.database.id)" :class="link" @click="go($event, browseRoute(result.database.id))">{{
            item.label
          }}</a>
          <a v-else-if="item.action === 'databases'" href="/databases" :class="link" @click="go($event, '/databases')">{{ item.label }}</a>
          <a v-else-if="item.action === 'skills'" href="/skills" :class="link" data-testid="connect-agent" @click="go($event, '/skills')">{{
            item.label
          }}</a>
          <OvButton
            v-else-if="item.action === 'use' && !usedDefault"
            variant="secondary"
            data-testid="use-as-default"
            :busy="using"
            @click="useAsDefault(result.database.id)"
          >
            {{ t('browse.use_as_default') }}
          </OvButton>
          <a
            v-else-if="item.action === 'done'"
            href="/"
            class="inline-flex min-h-11 items-center rounded-lg bg-accent px-5 font-semibold text-on-accent hover:bg-accent-hover"
            @click="done($event)"
            >{{ item.label }}</a
          >
        </template>
      </div>
      <OvNotice v-if="usedDefault" live tone="success" :title="usedDefault" />
      <OvNotice v-if="problem" live tone="problem" :title="problem.message" :reason="problem.reason" :next="problem.next" />
      <p>
        <button type="button" class="font-medium text-accent hover:underline" @click="$emit('another')">{{ another }}</button>
      </p>
    </section>
  </div>
</template>
