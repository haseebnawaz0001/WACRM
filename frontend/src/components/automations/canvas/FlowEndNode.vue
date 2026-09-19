<script setup lang="ts">
/** Where the path finishes. The end of the rule, not a step. */
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'

defineOptions({ inheritAttrs: false })
defineProps<{ id: string; data: { reached?: boolean; dimmed?: boolean } }>()
const { t } = useI18n()
</script>

<template>
  <div
    :class="[
      'flex h-full items-center justify-center rounded-full border border-dashed border-white/15 text-xs font-medium text-white/45 transition-opacity light:border-gray-300 light:text-gray-400',
      data.dimmed && 'opacity-35'
    ]"
  >
    <Handle type="target" :position="Position.Top" class="!pointer-events-none !opacity-0" />
    {{ t('automations.canvas.end') }}
  </div>
</template>
