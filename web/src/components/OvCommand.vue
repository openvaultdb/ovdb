<script setup lang="ts">
// A command the person can run in a terminal, shown whole and selectable. A
// long one wraps rather than scrolling, so a narrow screen has no scroll
// region a keyboard cannot reach.
defineProps<{ command: string }>()
</script>

<template>
  <pre
    class="rounded-lg whitespace-pre-wrap [overflow-wrap:anywhere] border border-line bg-surface px-4 py-2.5 font-mono text-[0.95rem] leading-relaxed"
  ><code>{{ command }}</code></pre>
</template>
