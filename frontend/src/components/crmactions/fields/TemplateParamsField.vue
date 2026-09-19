<script setup lang="ts">
/**
 * What goes into each blank of the chosen template.
 *
 * Meta rejects a template sent with an empty placeholder, so every blank the
 * template has is listed, with the template's own text above for context.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useLookups } from '@/components/automations/useLookups'
import { templateParamNames } from '../schema'
import VariableText from './VariableText.vue'

const props = defineProps<{
  modelValue?: Record<string, string> | null
  templateId?: string
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: Record<string, string>] }>()
const { t } = useI18n()
const lookups = useLookups()

const template = computed(() => lookups.template(props.templateId))
const params = computed(() => templateParamNames(template.value?.body_content))

function token(name: string): string {
  return `${'{'}${'{'}${name}${'}'}${'}'}`
}

function set(name: string, value: string) {
  emit('update:modelValue', { ...(props.modelValue || {}), [name]: value })
}
</script>

<template>
  <div v-if="template" class="space-y-3">
    <p
      v-if="template.body_content"
      class="whitespace-pre-line rounded-sm border border-white/[0.08] bg-white/[0.02] p-3 text-xs leading-relaxed text-white/70 light:border-gray-200 light:bg-gray-50 light:text-gray-600"
    >{{ template.body_content }}</p>
    <p v-if="!params.length" class="text-xs text-muted-foreground">{{ t('automations.fields.noParams') }}</p>
    <div v-for="name in params" :key="name" class="space-y-1.5">
      <p class="font-mono text-xs text-muted-foreground">{{ token(name) }}</p>
      <VariableText
        :model-value="modelValue?.[name] ?? ''"
        :disabled="disabled"
        :aria-label="token(name)"
        :placeholder="t('automations.fields.paramPlaceholder')"
        @update:model-value="v => set(name, v)"
      />
    </div>
  </div>
</template>
