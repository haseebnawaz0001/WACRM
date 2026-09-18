<script setup lang="ts">
/**
 * An ordered list of CRM actions, with the picker that adds one
 * (plan 10, S7).
 *
 * The list is the unit every surface actually needs: an automation rule, a
 * chatbot CRM-action node and a keyword rule all configure "these actions, in
 * this order". Sharing it is what keeps the ordering, the limit and the add
 * control behaving the same in all three.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import CrmActionCard from './CrmActionCard.vue'
import { newAction, type CrmActionSpec } from './schema'

const props = withDefaults(
  defineProps<{
    modelValue: CrmActionSpec[]
    /** Action types the backend offers; from the automation catalog. */
    availableTypes: string[]
    editable?: boolean
    maxActions?: number
    showContinueOnError?: boolean
  }>(),
  { editable: true, maxActions: 10, showContinueOnError: true }
)

const emit = defineEmits<{ (e: 'update:modelValue', value: CrmActionSpec[]): void }>()

const { t } = useI18n()

const canAdd = computed(() => props.editable && props.modelValue.length < props.maxActions)

function add(type: string) {
  if (!type) return
  emit('update:modelValue', [...props.modelValue, newAction(type)])
}

function remove(index: number) {
  const next = [...props.modelValue]
  next.splice(index, 1)
  emit('update:modelValue', next)
}

function move(index: number, direction: -1 | 1) {
  const target = index + direction
  if (target < 0 || target >= props.modelValue.length) return
  const next = [...props.modelValue]
  ;[next[index], next[target]] = [next[target], next[index]]
  emit('update:modelValue', next)
}
</script>

<template>
  <div class="space-y-3">
    <div v-if="canAdd" class="flex justify-end">
      <Select @update:model-value="v => add(String(v))">
        <SelectTrigger class="w-48" :aria-label="$t('automations.addAction')">
          <SelectValue :placeholder="t('automations.addAction')" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="type in availableTypes" :key="type" :value="type">
            {{ t(`automations.actions.${type}`, type) }}
          </SelectItem>
        </SelectContent>
      </Select>
    </div>

    <p v-if="!modelValue.length" class="text-sm text-muted-foreground">
      {{ t('automations.noActions') }}
    </p>

    <CrmActionCard
      v-for="(action, index) in modelValue"
      :key="action.id"
      :action="action"
      :index="index"
      :total="modelValue.length"
      :editable="editable"
      :show-continue-on-error="showContinueOnError"
      @remove="remove(index)"
      @move="d => move(index, d)"
    />
  </div>
</template>
