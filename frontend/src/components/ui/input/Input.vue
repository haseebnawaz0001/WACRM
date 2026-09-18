<script setup lang="ts">
/**
 * A disabled control still has to be readable.
 *
 * shadcn's default drops a disabled field to 50% opacity, which dims the value
 * as much as the chrome around it. That is right for an action you cannot take
 * and wrong for a field that is showing you a fact: a campaign that has started
 * locks its name, account, template and audience, so the whole record rendered
 * in placeholder grey and every value on the page looked like an empty box with
 * a hint in it.
 *
 * Non-interactive is carried by the surface and the cursor instead, and the
 * text stays legible.
 */
import { computed } from 'vue'
import { cn } from '@/lib/utils'

const props = defineProps<{
  modelValue?: string | number
  type?: string
  placeholder?: string
  disabled?: boolean
  class?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const modelValue = computed({
  get: () => props.modelValue?.toString() ?? '',
  set: (value) => emit('update:modelValue', value)
})
</script>

<template>
  <input
    v-model="modelValue"
    :type="type ?? 'text'"
    :placeholder="placeholder"
    :disabled="disabled"
    :class="cn(
      'flex h-10 w-full rounded-sm border border-white/[0.1] bg-white/[0.04] px-3 py-2 text-sm text-white transition-all duration-200 placeholder:text-white/40 hover:border-white/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/30 focus-visible:border-emerald-500/60 disabled:cursor-not-allowed disabled:border-white/[0.06] disabled:bg-white/[0.02] disabled:text-white/70 light:disabled:border-gray-200 light:disabled:bg-gray-50 light:disabled:text-gray-600 light:bg-white light:border-gray-200 light:text-gray-900 light:placeholder:text-gray-400 light:hover:border-gray-300 light:focus-visible:ring-emerald-500 light:focus-visible:border-emerald-500',
      props.class
    )"
  />
</template>
