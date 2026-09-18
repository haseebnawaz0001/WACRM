<script setup lang="ts">
import { Handle, Position } from '@vue-flow/core'

const props = withDefaults(
  defineProps<{
    label: string
    headerClass: string
    hasInput?: boolean
    outputHandles?: { id: string; label: string; title?: string }[]
  }>(),
  { hasInput: true },
)

</script>

<template>
  <div class="base-node relative bg-background border rounded-lg shadow-md hover:shadow-lg min-w-48 w-max max-w-sm overflow-visible transition-shadow duration-200">
    <!-- Input handle (top) -->
    <Handle
      v-if="hasInput !== false"
      id="input"
      type="target"
      :position="Position.Top"
      class="!w-3.5 !h-3.5 !rounded-full !bg-slate-400 !border-2 !border-background hover:!bg-slate-300 !transition-colors"
      style="z-index: 10;"
    />

    <!-- Header -->
    <div :class="['px-3 py-2 rounded-t-lg text-white text-xs font-semibold flex items-center gap-2 overflow-hidden', headerClass]">
      <slot name="icon" />
      <span class="truncate">{{ label }}</span>
    </div>

    <!-- Body -->
    <div class="px-3 py-2.5 text-xs text-muted-foreground">
      <slot />
    </div>

    <!-- Output handles (bottom) -->
    <template v-if="outputHandles && outputHandles.length > 0">
      <Handle
        v-for="(handle, idx) in outputHandles"
        :key="handle.id"
        type="source"
        :id="handle.id"
        :position="Position.Bottom"
        :title="handle.title || handle.label"
        :style="{
          left: outputHandles.length === 1 ? '50%' : `${((idx + 1) / (outputHandles.length + 1)) * 100}%`,
          zIndex: 10,
        }"
        class="!w-3.5 !h-3.5 !rounded-full !bg-primary !border-2 !border-background hover:!bg-primary/80 !transition-colors"
      />
      <span
        v-for="(handle, idx) in outputHandles"
        :key="'num-' + handle.id"
        class="absolute text-[9px] font-bold text-muted-foreground pointer-events-none"
        :style="{
          left: outputHandles.length === 1 ? '50%' : `${((idx + 1) / (outputHandles.length + 1)) * 100}%`,
          bottom: '-18px',
          transform: 'translateX(-50%)',
        }"
      >{{ idx + 1 }}</span>
    </template>
    <template v-else-if="!outputHandles">
      <Handle
        id="default"
        type="source"
        :position="Position.Bottom"
        class="!w-3.5 !h-3.5 !rounded-full !bg-primary !border-2 !border-background hover:!bg-primary/80 !transition-colors"
        style="z-index: 10;"
      />
    </template>
  </div>
</template>
