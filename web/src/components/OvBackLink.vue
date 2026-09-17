<script setup lang="ts">
import { t } from '../copy'
import { navigate } from '../router'

function open(event: MouseEvent) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  event.preventDefault()
  navigate('/')
}
</script>

<template>
  <a href="/" class="inline-flex items-center gap-1.5 font-medium text-accent hover:underline" @click="open">
    <span aria-hidden="true">‹</span>{{ t('nav.home') }}
  </a>
</template>
