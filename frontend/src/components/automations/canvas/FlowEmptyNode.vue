<script setup lang="ts">
/** A question's branch with nothing on it yet: the contact carries straight on. */
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'

defineOptions({ inheritAttrs: false })
defineProps<{ id: string; data: { dimmed?: boolean } }>()
const { t } = useI18n()
</script>

<template>
  <div
    :class="[
      'flex h-full items-center justify-center rounded-full border border-dashed border-white/12 px-3 text-xs text-white/40 transition-opacity light:border-gray-300 light:text-gray-400',
      data.dimmed && 'opacity-35'
    ]"
  >
    <Handle type="target" :position="Position.Top" class="!pointer-events-none !opacity-0" />
    {{ t('automations.canvas.carryOn') }}
    <Handle type="source" :position="Position.Bottom" class="!pointer-events-none !opacity-0" />
  </div>
</template>
