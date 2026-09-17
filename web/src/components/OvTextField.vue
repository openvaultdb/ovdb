<script setup lang="ts">
// TextField: a labelled input with help and an error tied to it for
// screen readers.
import { useId } from 'vue'

defineProps<{
  label: string
  help?: string
  error?: string
  inputmode?: 'text' | 'numeric'
  autocomplete?: string
}>()
const model = defineModel<string>({ required: true })
const id = useId()
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <label :for="id" class="font-semibold">{{ label }}</label>
    <p v-if="help" :id="`${id}-help`" class="text-muted">{{ help }}</p>
    <input
      :id="id"
      v-model="model"
      :inputmode="inputmode"
      :autocomplete="autocomplete ?? 'off'"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="[help ? `${id}-help` : '', error ? `${id}-error` : ''].filter(Boolean).join(' ') || undefined"
      class="min-h-11 w-full max-w-60 rounded-lg border bg-surface px-3 text-ink"
      :class="error ? 'border-danger' : 'border-line'"
    />
    <p v-if="error" :id="`${id}-error`" class="font-medium text-danger">{{ error }}</p>
  </div>
</template>
