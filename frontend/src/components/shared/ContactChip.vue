<script setup lang="ts">
/**
 * A contact, rendered the same way everywhere (plan 10, S12).
 *
 * Links to the chat only when the viewer holds a chat permission: a chip is not
 * a place to discover that you cannot open something. The phone number goes
 * through the masking the API already applied, so this component never decides
 * who may see a number — it only shows what it was given.
 */
import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import EntityLink from './EntityLink.vue'

const props = withDefaults(
  defineProps<{
    id?: string | null
    name?: string | null
    phone?: string | null
    avatar?: boolean
    deleted?: boolean
    size?: 'sm' | 'md'
  }>(),
  { avatar: true, deleted: false, size: 'md' }
)

const auth = useAuthStore()

const to = computed(() => {
  if (!props.id || !auth.hasPermission('chat', 'read')) return null
  return `/inbox/${props.id}`
})
</script>

<template>
  <EntityLink
    :name="name"
    :subtitle="phone"
    :to="to"
    :avatar="avatar"
    :deleted="deleted"
    :size="size"
  />
</template>
