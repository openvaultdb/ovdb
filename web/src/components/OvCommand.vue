<script setup lang="ts">
// A command the person can run in a terminal, shown whole and selectable.
defineProps<{ command: string }>()
</script>

<template>
  <pre
    class="overflow-x-auto rounded-lg border border-line bg-surface px-4 py-2.5 font-mono text-[0.95rem] leading-relaxed"
  ><code>{{ command }}</code></pre>
</template>
