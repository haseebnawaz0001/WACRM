<script setup lang="ts">
/**
 * An editor's name, typed where a page's title goes.
 *
 * Editors used to put a labelled "Name" box in their toolbar, each its own
 * size and place. The name is the page's title, so it sits in the title's
 * spot in the page bar, at the title's size, and only shows that it can be
 * edited when pointed at or focused.
 */
defineProps<{
  modelValue?: string
  placeholder?: string
  ariaLabel?: string
  disabled?: boolean
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <input
    :value="modelValue ?? ''"
    :placeholder="placeholder"
    :aria-label="ariaLabel"
    :disabled="disabled"
    class="page-title-input -ml-1.5 h-7 min-w-0 max-w-full truncate rounded-sm border border-transparent bg-transparent px-1.5 text-lg font-semibold leading-6 text-white outline-none transition-colors placeholder:text-white/35 hover:border-white/10 focus:border-emerald-500/60 focus:ring-2 focus:ring-emerald-500/20 disabled:cursor-default disabled:hover:border-transparent light:text-gray-900 light:placeholder:text-gray-400 light:hover:border-gray-200"
    @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
  >
</template>

<style scoped>
/* As wide as the name, where the browser can size a field to its text. */
.page-title-input {
  field-sizing: content;
  min-width: 10ch;
}
</style>
