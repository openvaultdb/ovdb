<script setup lang="ts">
// Renders catalogue copy as text, showing `backticked` commands as code and
// https:// addresses as links. Record values and copy are never rendered as
// HTML (local-server-and-web-console#REQ:security-headers).
import { computed } from 'vue'

const props = defineProps<{ text: string }>()

type Part = { value: string; kind: 'text' | 'code' | 'link' }

const parts = computed(() =>
  props.text.split('`').flatMap((value, index): Part[] =>
    index % 2 === 1
      ? [{ value, kind: 'code' }]
      : value
          .split(/(https:\/\/[^\s)]+[^\s).,;:])/)
          .map((piece, i) => ({ value: piece, kind: i % 2 === 1 ? 'link' : 'text' }) as Part),
  ),
)
</script>

<template>
  <template v-for="(part, index) in parts" :key="index">
    <code v-if="part.kind === 'code'" class="rounded bg-code px-1.5 py-0.5 font-mono text-[0.92em]">{{ part.value }}</code>
    <a
      v-else-if="part.kind === 'link'"
      :href="part.value"
      target="_blank"
      rel="noopener noreferrer"
      class="font-medium [overflow-wrap:anywhere] text-accent underline"
      >{{ part.value }}</a
    >
    <template v-else>{{ part.value }}</template>
  </template>
</template>
