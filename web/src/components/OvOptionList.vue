<script setup lang="ts">
// OptionList: the "What would you like to do?" choices. Each option is a
// real link, so Tab, Enter, middle-click and screen readers all work; the
// arrow keys also move between options, as in the TUI.
import { ref } from 'vue'

import { navigate } from '../router'
import OvStatusBadge from './OvStatusBadge.vue'

export interface Option {
  id: string
  label: string
  help?: string
  badge?: { tone: 'ok' | 'warn' | 'danger' | 'neutral'; label: string }
  to: string
}

defineProps<{ options: Option[]; label: string }>()

const list = ref<HTMLElement>()

function move(event: KeyboardEvent, step: number) {
  const links = Array.from(list.value?.querySelectorAll<HTMLAnchorElement>('a') ?? [])
  const index = links.indexOf(event.target as HTMLAnchorElement)
  if (index < 0) return
  event.preventDefault()
  links[(index + step + links.length) % links.length]?.focus()
}

function open(event: MouseEvent, to: string) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate(to)
}
</script>

<template>
  <ul
    ref="list"
    :aria-label="label"
    class="flex flex-col overflow-hidden rounded-2xl border border-line bg-surface"
    @keydown.down="move($event, 1)"
    @keydown.up="move($event, -1)"
  >
    <li v-for="option in options" :key="option.id" class="border-line not-last:border-b">
      <a
        :href="option.to"
        :data-option="option.id"
        class="group flex items-center justify-between gap-4 px-5 py-4 transition-colors hover:bg-surface-2 focus-visible:rounded-none focus-visible:-outline-offset-3"
        @click="open($event, option.to)"
      >
        <span class="flex min-w-0 flex-col gap-0.5">
          <span class="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span class="text-lg font-semibold text-ink">{{ option.label }}</span>
            <OvStatusBadge v-if="option.badge" :tone="option.badge.tone" :label="option.badge.label" />
          </span>
          <span v-if="option.help" class="text-muted">{{ option.help }}</span>
        </span>
        <span aria-hidden="true" class="shrink-0 text-2xl text-muted transition-transform group-hover:translate-x-0.5">›</span>
      </a>
    </li>
  </ul>
</template>
