<script setup lang="ts">
// Placeholder console entry (spec/features/local-server-and-web-console).
// It proves the embedded multi-page build end to end (S3) and the shared
// copy catalogue (S6); the real Home screen with status and navigation
// arrives in increment 1b.
import { t } from './copy'

const menuItems = [
  t('home.menu.try_demo'),
  t('home.menu.create_database'),
  t('home.menu.connect_database'),
  t('home.menu.start_server'),
]
</script>

<template>
  <main class="mx-auto flex min-h-screen max-w-xl flex-col gap-6 px-6 py-16 text-slate-900">
    <h1 class="text-2xl font-semibold">{{ t('console.placeholder.title') }}</h1>
    <p class="text-slate-600">{{ t('console.placeholder.body', { name: 'ovdb' }) }}</p>
    <ul class="flex flex-col gap-2">
      <li
        v-for="item in menuItems"
        :key="item"
        class="rounded-md border border-slate-200 px-4 py-2"
      >
        {{ item }}
      </li>
    </ul>
  </main>
</template>
