<script setup lang="ts">
// Card: a quiet surface that groups one topic.
defineProps<{ title?: string }>()
</script>

<template>
  <section class="rounded-2xl border border-line bg-surface p-5 sm:p-6">
    <h2 v-if="title" class="mb-4 text-lg font-semibold tracking-tight">{{ title }}</h2>
    <slot />
  </section>
</template>
