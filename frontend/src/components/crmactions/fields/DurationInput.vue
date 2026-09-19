<script setup lang="ts">
/** A length of time, as people say it: "3 days". */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { FIELD } from './styles'

const props = withDefaults(defineProps<{
  modelValue?: { amount?: number; unit?: string } | null
  units?: string[]
  disabled?: boolean
  ariaLabel?: string
}>(), { units: () => ['minutes', 'hours', 'days'] })

const emit = defineEmits<{ 'update:modelValue': [value: { amount: number; unit: string }] }>()
const { t } = useI18n()

const amount = computed(() => props.modelValue?.amount ?? '')
const unit = computed(() => props.modelValue?.unit || 'days')

function set(next: { amount?: number; unit?: string }) {
  emit('update:modelValue', {
    amount: next.amount ?? Number(props.modelValue?.amount ?? 1),
    unit: next.unit ?? unit.value
  })
}
</script>

<template>
  <div class="flex gap-2">
    <input
      type="number"
      min="1"
      inputmode="numeric"
      :class="[FIELD, 'h-10 w-24 tabular-nums']"
      :value="amount"
      :disabled="disabled"
      :aria-label="ariaLabel || t('automations.fields.amount')"
      @input="e => set({ amount: Math.max(0, Number((e.target as HTMLInputElement).value) || 0) })"
    >
    <Select :model-value="unit" :disabled="disabled" @update:model-value="v => set({ unit: String(v) })">
      <SelectTrigger class="w-32" :aria-label="t('automations.fields.unit')"><SelectValue /></SelectTrigger>
      <SelectContent>
        <SelectItem v-for="u in units" :key="u" :value="u">
          {{ t(`automations.units.${u}`, Number(amount) || 2) }}
        </SelectItem>
      </SelectContent>
    </Select>
  </div>
</template>
