<script setup lang="ts">
/**
 * CRM condition node (plan 10, S7).
 *
 * Branches on what is true of the *contact* — a saved segment or a filter —
 * rather than on what they just typed. The existing Condition node evaluates an
 * expression over session variables, which cannot answer "is this a returning
 * customer?" without the flow having asked first.
 */
import { computed } from 'vue'
import { Filter } from 'lucide-vue-next'
import BaseNode from '@/components/calling/nodes/BaseNode.vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{ data: any }>()

const summary = computed(() => {
  const cfg = props.data?.config || {}
  if (cfg.segment_name) return `In segment: ${cfg.segment_name}`
  if (cfg.segment_id) return 'In a saved segment'
  const rules = cfg.filter?.rules
  if (Array.isArray(rules) && rules.length > 0) {
    return rules.length === 1 ? '1 condition' : `${rules.length} conditions`
  }
  return 'No condition set'
})

const outputHandles = [
  { id: 'true', label: 'Matches' },
  { id: 'false', label: 'Does not match' },
]
</script>

<template>
  <BaseNode
    :label="data?.label || 'CRM condition'"
    header-class="bg-emerald-700"
    :output-handles="outputHandles"
    :has-input="!data?.isEntryNode"
  >
    <template #icon><Filter class="w-4 h-4" /></template>
    <p class="truncate" :title="summary">{{ summary }}</p>
  </BaseNode>
</template>
