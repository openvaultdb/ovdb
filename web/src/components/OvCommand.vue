<script setup lang="ts">
// A command the person can run in a terminal, shown whole and selectable. A
// long one wraps rather than scrolling, so a narrow screen has no scroll
// region a keyboard cannot reach. The copy button (review-inc-7.md F4) is a
// second way to get the exact text without selecting it by hand.
import { ref } from 'vue'

import { t } from '../copy'

const props = defineProps<{ command: string }>()

const copied = ref(false)
let resetTimer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  try {
    await navigator.clipboard.writeText(props.command)
  } catch {
    // Clipboard access can be denied (insecure context, no permission); the
    // command is already shown in full and selectable either way.
    return
  }
  copied.value = true
  clearTimeout(resetTimer)
  resetTimer = setTimeout(() => (copied.value = false), 1500)
}
</script>

<template>
  <div class="relative">
    <pre
      class="rounded-lg whitespace-pre-wrap [overflow-wrap:anywhere] border border-line bg-surface py-2.5 pr-16 pl-4 font-mono text-[0.95rem] leading-relaxed"
    ><code>{{ command }}</code></pre>
    <button
      type="button"
      class="absolute top-2 right-2 rounded-md border border-line bg-surface px-2 py-1 text-xs font-medium text-muted hover:bg-surface-2"
      :aria-label="copied ? t('command.copied') : t('command.copy')"
      @click="copy"
    >
      {{ copied ? t('command.copied') : t('command.copy') }}
    </button>
  </div>
</template>
