<script setup lang="ts">
// Button: one primary action per screen, secondary for the rest.
withDefaults(defineProps<{ variant?: 'primary' | 'secondary'; type?: 'button' | 'submit'; busy?: boolean }>(), {
  variant: 'primary',
  type: 'button',
  busy: false,
})
</script>

<template>
  <button
    :type="type"
    :aria-busy="busy || undefined"
    :disabled="busy"
    class="inline-flex min-h-11 items-center justify-center gap-2 rounded-lg px-5 font-semibold transition-colors disabled:cursor-wait disabled:opacity-70"
    :class="
      variant === 'primary'
        ? 'bg-accent text-on-accent hover:bg-accent-hover'
        : 'border border-line bg-surface text-ink hover:bg-surface-2'
    "
  >
    <slot />
  </button>
</template>
