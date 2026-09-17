<script setup lang="ts">
// Home (first-run-onboarding#REQ:home-menu-options, REQ:home-status-line).
// The server decides the status line, the options, their order and their
// state (GET /api/local/v1/home), so the TUI and the web console show the
// same menu; this screen only renders it, using the web wording where the
// document gives one (parity E1).
import { computed, onMounted, ref } from 'vue'

import { api, type HomeDocument, type HomeOption } from '../api'
import OvOptionList, { type Option } from '../components/OvOptionList.vue'
import { t } from '../copy'
import routes from '../../routes.json'
import { navigate } from '../router'
import { recordStart, recordUsage } from '../usage'

const home = ref<HomeDocument | null>(null)

onMounted(async () => {
  recordStart()
  const result = await api<HomeDocument>('GET', '/api/local/v1/home')
  if (result.ok) home.value = result.data
})

function toOption(option: HomeOption): Option & { disabled?: boolean } {
  return {
    id: option.id,
    label: t(option.web_label_key ?? option.label_key),
    help: option.description_key ? t(option.description_key) : undefined,
    badge: option.badge ? { tone: option.badge.tone, label: t(option.badge.label_key) } : undefined,
    to: routes.find((route) => route.screen === option.id)?.path ?? '/',
    disabled: option.disabled,
  }
}

const primary = computed(() => home.value?.options.filter((o) => o.group === 'primary').map(toOption) ?? [])
const secondary = computed(() => home.value?.options.filter((o) => o.group === 'secondary').map(toOption) ?? [])
const running = computed(() => home.value?.status_line.some((part) => part.key === 'home.status.server_running') ?? false)

function open(event: MouseEvent, to: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  const option = routes.find((route) => route.path === to)?.screen
  if (option) recordUsage({ event: 'onboarding_option_selected', option })
  navigate(to)
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <header class="flex flex-col gap-4">
      <p class="max-w-prose text-lg text-muted">{{ t('home.tagline') }}</p>
      <p data-testid="status-line" class="flex min-h-7 items-baseline gap-2.5" aria-live="polite">
        <template v-if="home">
          <span
            aria-hidden="true"
            class="size-2.5 shrink-0 translate-y-[-0.1em] rounded-full"
            :class="running ? 'bg-ok' : 'bg-muted'"
          ></span>
          <span class="min-w-0 break-words">
            <template v-for="(part, index) in home.status_line" :key="part.key">
              <template v-if="index > 0"> · </template>{{ t(part.key, part.params) }}
            </template>
          </span>
        </template>
        <span v-else class="text-muted">{{ t('console.loading') }}</span>
      </p>
    </header>

    <section class="flex flex-col gap-4" aria-labelledby="home-question">
      <h1 id="home-question" tabindex="-1" class="text-2xl font-semibold tracking-tight sm:text-3xl">
        {{ t(home?.question_key ?? 'home.question') }}
      </h1>
      <OvOptionList v-if="primary.length" :options="primary" :label="t('home.question')" />
      <nav v-if="secondary.length" :aria-label="t('home.menu.more')" class="flex flex-wrap gap-x-6 gap-y-2 px-1">
        <template v-for="item in secondary" :key="item.id">
          <!-- A disabled option says why instead of linking. -->
          <span v-if="item.disabled" :data-option="item.id" aria-disabled="true" class="text-muted">
            <span class="font-medium">{{ item.label }}</span> · {{ item.help }}
          </span>
          <a v-else :href="item.to" :data-option="item.id" class="font-medium text-accent hover:underline" @click="open($event, item.to)">
            {{ item.label }}
          </a>
        </template>
      </nav>
    </section>
  </div>
</template>
