<script setup lang="ts">
// Placeholder TODO app entry (spec/features/todo-demo). It proves the second
// Vite entry serves at /apps/todo/ and shares the console's copy helper; the
// real to-buy/to-watch lists arrive in increment 4.
import { t } from '../../src/copy'
</script>

<template>
  <main class="mx-auto flex min-h-screen max-w-xl flex-col gap-4 px-6 py-16 text-slate-900">
    <h1 class="text-2xl font-semibold">{{ t('todo.placeholder.title') }}</h1>
    <p class="text-slate-600">{{ t('todo.placeholder.body') }}</p>
  </main>
</template>
