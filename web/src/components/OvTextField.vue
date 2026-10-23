<script setup lang="ts">
// TextField: a labelled input with help and an error tied to it for
// screen readers.
import { ref, useId } from 'vue'

defineProps<{
  label: string
  help?: string
  error?: string
  inputmode?: 'text' | 'numeric'
  autocomplete?: string
  wide?: boolean
  mono?: boolean
  placeholder?: string
}>()
const model = defineModel<string>({ required: true })
const id = useId()
const input = ref<HTMLInputElement>()

/** Moves focus to the input and selects its text, for "choose another …" remedies. */
function focus() {
  input.value?.focus()
  input.value?.select()
}
defineExpose({ focus })
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <label :for="id" class="font-semibold">{{ label }}</label>
    <p v-if="help" :id="`${id}-help`" class="text-muted">{{ help }}</p>
    <input
      :id="id"
      ref="input"
      v-model="model"
      :inputmode="inputmode"
      :autocomplete="autocomplete ?? 'off'"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="[help ? `${id}-help` : '', error ? `${id}-error` : ''].filter(Boolean).join(' ') || undefined"
      :placeholder="placeholder"
      spellcheck="false"
      class="min-h-11 w-full rounded-lg border bg-surface px-3 text-ink"
      :class="[error ? 'border-danger' : 'border-line', wide ? '' : 'max-w-60', mono ? 'font-mono text-[0.95rem]' : '']"
    />
    <p v-if="error" :id="`${id}-error`" class="font-medium text-danger">{{ error }}</p>
  </div>
</template>
