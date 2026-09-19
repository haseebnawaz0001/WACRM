<script setup lang="ts">
/**
 * Two or three choices, all visible at once.
 *
 * A dropdown hides the options until somebody opens it, which is the wrong
 * trade when there are only three: "a team / a person / nobody" reads as the
 * question it is when the answers are on the page.
 */
defineProps<{
  modelValue?: string | null
  options: { value: string; label: string }[]
  disabled?: boolean
  ariaLabel?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <div
    role="radiogroup"
    :aria-label="ariaLabel"
    class="inline-flex w-full flex-wrap gap-1 rounded-sm border border-white/[0.1] bg-white/[0.02] p-1 light:border-gray-200 light:bg-gray-50"
  >
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      role="radio"
      :aria-checked="modelValue === option.value"
      :disabled="disabled"
      :class="[
        'min-w-0 flex-1 truncate rounded-sm px-3 py-1.5 text-sm transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40',
        modelValue === option.value
          ? 'bg-white/[0.1] font-medium text-white light:bg-white light:text-gray-900 light:shadow-sm'
          : 'text-white/60 hover:text-white light:text-gray-500 light:hover:text-gray-900'
      ]"
      @click="emit('update:modelValue', option.value)"
    >
      {{ option.label }}
    </button>
  </div>
</template>
