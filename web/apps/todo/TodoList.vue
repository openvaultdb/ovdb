<script setup lang="ts">
// One list: its items with a checkbox and a delete button each, and a field
// to add one. Everything is a native control, so it works with the keyboard
// alone; item titles are text, never HTML.
import { computed, nextTick, ref, useId } from 'vue'

import { t } from '../../src/copy'
import type { Item, List } from './todo'

const props = defineProps<{ list: List }>()
const emit = defineEmits<{
  add: [title: string]
  toggle: [item: Item]
  remove: [item: Item]
}>()

const id = useId()
const draft = ref('')
const section = ref<HTMLElement>()
const input = ref<HTMLInputElement>()

const left = computed(() => {
  const count = props.list.items.filter((item) => !item.done).length
  if (count === 0) return t('todo.left.none')
  return count === 1 ? t('todo.left.one') : t('todo.left.many', { count: String(count) })
})

function submit() {
  const title = draft.value.trim()
  if (!title) return
  draft.value = ''
  emit('add', title)
}

// After a delete, focus goes to the next item, or the previous one, or the
// add field, so a keyboard user never lands at the top of the page.
async function remove(item: Item) {
  const index = props.list.items.findIndex((other) => other.id === item.id)
  const neighbour = props.list.items[index + 1] ?? props.list.items[index - 1]
  emit('remove', item)
  await nextTick()
  const row = Array.from(section.value?.querySelectorAll<HTMLElement>('[data-item]') ?? []).find((li) => li.dataset.item === neighbour?.id)
  ;(row?.querySelector('input') ?? input.value)?.focus()
}
</script>

<template>
  <section ref="section" :aria-labelledby="`${id}-title`" :data-list="list.id" class="flex flex-col rounded-2xl border border-line bg-surface shadow-[0_1px_2px_rgb(0_0_0/0.04)]">
    <header class="flex items-baseline justify-between gap-4 px-5 pt-5 pb-3">
      <h2 :id="`${id}-title`" class="text-xl font-semibold tracking-tight">{{ list.title }}</h2>
      <p class="shrink-0 text-sm font-medium text-muted" data-testid="left">{{ left }}</p>
    </header>

    <ul v-if="list.items.length" class="flex flex-col">
      <li v-for="item in list.items" :key="item.id" :data-item="item.id" class="flex items-center gap-3 border-t border-line px-5 py-2.5">
        <input
          :id="`${id}-${item.id}`"
          type="checkbox"
          :checked="item.done"
          class="size-5 shrink-0 cursor-pointer accent-accent"
          @change="emit('toggle', item)"
        />
        <label
          :for="`${id}-${item.id}`"
          class="min-w-0 flex-1 cursor-pointer py-1 [overflow-wrap:anywhere]"
          :class="item.done ? 'text-muted line-through decoration-1' : 'text-ink'"
          >{{ item.title || t('todo.untitled') }}</label
        >
        <button
          type="button"
          :aria-label="t('todo.delete', { item: item.title || t('todo.untitled') })"
          class="grid size-9 shrink-0 place-items-center rounded-lg text-muted transition-colors hover:bg-danger-soft hover:text-danger"
          @click="remove(item)"
        >
          <svg aria-hidden="true" viewBox="0 0 20 20" class="size-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <path d="M5 5l10 10M15 5L5 15" />
          </svg>
        </button>
      </li>
    </ul>
    <p v-else class="border-t border-line px-5 py-4 text-muted">{{ t('todo.empty') }}</p>

    <form class="mt-auto flex gap-2 border-t border-line p-4" @submit.prevent="submit">
      <label :for="`${id}-add`" class="sr-only">{{ t('todo.add.label', { list: list.title }) }}</label>
      <input
        :id="`${id}-add`"
        ref="input"
        v-model="draft"
        type="text"
        autocomplete="off"
        maxlength="200"
        :placeholder="t('todo.add.placeholder')"
        class="min-h-11 min-w-0 flex-1 rounded-lg border border-line bg-bg px-3 text-ink placeholder:text-muted"
      />
      <button type="submit" class="min-h-11 shrink-0 rounded-lg bg-accent px-4 font-semibold text-on-accent hover:bg-accent-hover">
        {{ t('todo.add.submit') }}
      </button>
    </form>
  </section>
</template>
