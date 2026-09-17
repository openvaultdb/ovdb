<script setup lang="ts">
// Renders catalogue copy as text, showing `backticked` commands as code.
// Record values and copy are never rendered as HTML
// (local-server-and-web-console#REQ:security-headers).
import { computed } from 'vue'

const props = defineProps<{ text: string }>()

const parts = computed(() =>
  props.text.split('`').map((value, index) => ({ value, code: index % 2 === 1 })),
)
</script>

<template>
  <template v-for="(part, index) in parts" :key="index">
    <code v-if="part.code" class="rounded bg-code px-1.5 py-0.5 font-mono text-[0.92em]">{{ part.value }}</code>
    <template v-else>{{ part.value }}</template>
  </template>
</template>
