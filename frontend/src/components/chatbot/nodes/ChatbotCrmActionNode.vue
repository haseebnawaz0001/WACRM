<script setup lang="ts">
/**
 * CRM action node (plan 10, S7).
 *
 * Runs one or more actions from the shared CRM action library — the same
 * library automation uses — so a flow can tag a contact, create a task or open
 * a deal with what it just collected, instead of stopping at a session
 * variable nobody can act on.
 */
import { computed } from 'vue'
import { Zap } from 'lucide-vue-next'
import BaseNode from '@/components/calling/nodes/BaseNode.vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{ data: any }>()

// Action types are stored as snake_case identifiers; the card shows them the
// way the builder's own action picker labels them.
const labels: Record<string, string> = {
  add_tags: 'Add tags',
  remove_tags: 'Remove tags',
  set_field: 'Set field',
  set_contact_owner: 'Set owner',
  set_conversation_status: 'Set conversation status',
  assign_conversation: 'Assign conversation',
  create_task: 'Create task',
  add_note: 'Add note',
  notify_users: 'Notify users',
  call_webhook: 'Call webhook',
  create_deal: 'Create deal',
  move_deal_stage: 'Move deal stage',
  send_message: 'Send message',
  send_template: 'Send template',
}

const actions = computed<string[]>(() => {
  const list = props.data?.config?.actions
  if (!Array.isArray(list)) return []
  return list.map((a: any) => labels[a?.type] || a?.type).filter(Boolean)
})

const summary = computed(() => {
  if (actions.value.length === 0) return 'No actions configured'
  if (actions.value.length <= 2) return actions.value.join(', ')
  return `${actions.value.slice(0, 2).join(', ')} +${actions.value.length - 2} more`
})

// The error handle only appears when the author has opted into handling
// failure, so the common case stays a single-exit card.
const outputHandles = computed(() =>
  props.data?.config?.show_error_handle || props.data?.config?.continue_on_error === false
    ? [
        { id: 'default', label: 'Done' },
        { id: 'error', label: 'Error' },
      ]
    : [{ id: 'default', label: 'Done' }]
)
</script>

<template>
  <BaseNode
    :label="data?.label || 'CRM action'"
    header-class="bg-emerald-600"
    :output-handles="outputHandles"
    :has-input="!data?.isEntryNode"
  >
    <template #icon><Zap class="w-4 h-4" /></template>
    <p class="truncate" :title="summary">{{ summary }}</p>
  </BaseNode>
</template>
