<script setup lang="ts">
// StatusBadge: a state in words with a dot, never colour alone.
defineProps<{ tone: 'ok' | 'warn' | 'danger' | 'neutral'; label: string }>()
</script>

<template>
  <span
    class="inline-flex items-center gap-2 rounded-full px-3 py-0.5 text-sm font-semibold"
    :class="{
      'bg-ok-soft text-ok': tone === 'ok',
      'bg-warn-soft text-warn': tone === 'warn',
      'bg-danger-soft text-danger': tone === 'danger',
      'bg-surface-2 text-muted': tone === 'neutral',
    }"
  >
    <span aria-hidden="true" class="size-2 rounded-full bg-current"></span>
    {{ label }}
  </span>
</template>
