<script setup lang="ts">
/** Where a question's two paths meet again. */
import { Handle, Position } from '@vue-flow/core'

defineOptions({ inheritAttrs: false })
defineProps<{ id: string; data: { dimmed?: boolean } }>()
</script>

<template>
  <div
    :class="[
      'h-full w-full rounded-full border-2 border-white/20 bg-background light:border-gray-300',
      data.dimmed && 'opacity-35'
    ]"
  >
    <Handle type="target" :position="Position.Top" class="!pointer-events-none !opacity-0" />
    <Handle type="source" :position="Position.Bottom" class="!pointer-events-none !opacity-0" />
  </div>
</template>
