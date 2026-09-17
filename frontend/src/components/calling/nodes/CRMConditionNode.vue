<script setup lang="ts">
import { computed } from 'vue'
import { UserCheck } from 'lucide-vue-next'
import BaseNode from './BaseNode.vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{ data: Record<string, any> }>()

/**
 * A count rather than the conditions themselves: a node on a canvas has room
 * for one line, and half a filter reads as a different filter.
 */
const summary = computed(() => {
  const children = props.data?.config?.filter?.children || []
  if (!children.length) return 'No condition set'
  return children.length === 1 ? '1 condition' : `${children.length} conditions`
})

const outputHandles = [
  { id: 'match', label: 'Match', title: 'The caller matches' },
  { id: 'no_match', label: 'No match', title: 'The caller does not match, or is unknown' },
]
</script>

<template>
  <BaseNode
    :label="data?.label || 'CRM Condition'"
    header-class="bg-indigo-600"
    :output-handles="outputHandles"
    :has-input="!data?.isEntryNode"
  >
    <template #icon><UserCheck class="w-4 h-4" /></template>
    <p class="truncate">{{ summary }}</p>
  </BaseNode>
</template>
