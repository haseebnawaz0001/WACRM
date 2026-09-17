<script setup lang="ts">
/**
 * A colleague, rendered the same way everywhere (plan 10, S12).
 *
 * Deactivated users keep their tasks, deals and contacts (plan 10, S8), so
 * their name keeps appearing long after they have gone. Saying so in the chip
 * is what lets a manager see at a glance that a queue is owned by somebody who
 * is not coming back.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import EntityLink from './EntityLink.vue'

const props = withDefaults(
  defineProps<{
    id?: string | null
    name?: string | null
    email?: string | null
    /** The user is deactivated or has left the organization. */
    inactive?: boolean
    avatar?: boolean
    size?: 'sm' | 'md'
  }>(),
  { avatar: true, inactive: false, size: 'md' }
)

const { t } = useI18n()
const auth = useAuthStore()

const to = computed(() => {
  if (!props.id || !auth.hasPermission('users', 'read')) return null
  return `/settings/users/${props.id}`
})

const subtitle = computed(() => (props.inactive ? t('common.ownerInactive') : props.email))
</script>

<template>
  <EntityLink
    :name="name"
    :subtitle="subtitle"
    :to="to"
    :avatar="avatar"
    :size="size"
  />
</template>
