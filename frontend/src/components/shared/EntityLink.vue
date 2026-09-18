<script setup lang="ts">
/**
 * One way to render a reference to something (plan 10, S12).
 *
 * Call logs, transfers, campaign recipients and audit logs each printed a bare
 * name. A name is not a link, so an agent reading "Amara Okafor" in a call log
 * had to go and find her by hand; and a name alone cannot say the record is
 * gone, so a deleted contact and a live one looked identical.
 *
 * EntityLink renders a name, links it when the viewer may open it, and says so
 * when the record is missing. ContactChip and UserChip are thin wrappers that
 * know which route and which fallback each kind wants.
 */
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getInitials, getAvatarColor } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    /** Display name. Empty renders the missing-record state. */
    name?: string | null
    /** Route to open. Omitted renders plain text — which is what a viewer
     *  without permission should see. */
    to?: string | null
    /** Secondary line, such as a phone number or an email. */
    subtitle?: string | null
    /** Show an avatar circle. */
    avatar?: boolean
    /** The record no longer exists; shown struck through. */
    deleted?: boolean
    size?: 'sm' | 'md'
  }>(),
  { avatar: false, deleted: false, size: 'md' }
)

const { t } = useI18n()

// A record with no name is not an error — a contact can arrive with only a
// phone number — so the subtitle is promoted rather than showing a blank.
const label = computed(() => props.name?.trim() || props.subtitle?.trim() || t('common.unknown'))
const showSubtitle = computed(() => !!props.subtitle && props.subtitle !== label.value)
const textSize = computed(() => (props.size === 'sm' ? 'text-xs' : 'text-sm'))
</script>

<template>
  <span class="inline-flex min-w-0 items-center gap-2 align-middle">
    <span
      v-if="avatar"
      :class="[
        'flex shrink-0 items-center justify-center rounded-full text-[10px] font-medium text-white',
        size === 'sm' ? 'h-5 w-5' : 'h-6 w-6',
        getAvatarColor(label)
      ]"
      aria-hidden="true"
    >
      {{ getInitials(label) }}
    </span>

    <span class="min-w-0">
      <component
        :is="to && !deleted ? RouterLink : 'span'"
        v-bind="to && !deleted ? { to } : {}"
        :class="[
          'block truncate',
          textSize,
          to && !deleted && 'text-foreground underline-offset-2 hover:underline',
          deleted && 'text-muted-foreground line-through'
        ]"
      >
        {{ label }}
      </component>
      <span v-if="showSubtitle" class="block truncate text-xs text-muted-foreground">
        {{ subtitle }}
      </span>
    </span>
  </span>
</template>
