<script setup lang="ts">
/**
 * A stretch of the path, with a + where a step can go.
 *
 * The + is on the line itself because that is where the person is looking
 * when they think "and then…". A question's two edges carry Yes and No near
 * the question, and their + sits at the foot, just above what comes next.
 */
import { computed, inject, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, Position } from '@vue-flow/core'
import { Plus } from 'lucide-vue-next'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import AddStepMenu from './AddStepMenu.vue'
import { flowContextKey } from './context'
import type { FlowEdgeData } from '../flow/layout'

// Vue Flow hands every edge its full state as attributes; this edge draws
// several elements and uses only what it declares.
defineOptions({ inheritAttrs: false })

const props = defineProps<{
  id: string
  sourceX: number
  sourceY: number
  targetX: number
  targetY: number
  data: FlowEdgeData & { taken?: boolean; dimmed?: boolean; delay?: number; traceKey?: number }
}>()

const { t } = useI18n()
const flow = inject(flowContextKey)!
const open = ref(false)

// An open menu is the one thing being worked on; a card still ringed as
// selected elsewhere on the path would compete with it.
watch(open, now => { if (now) flow.deselect() })

const path = computed(() => getSmoothStepPath({
  sourceX: props.sourceX,
  sourceY: props.sourceY,
  sourcePosition: Position.Bottom,
  targetX: props.targetX,
  targetY: props.targetY,
  targetPosition: Position.Top,
  borderRadius: 12,
  // A question's edges turn right under it, so each branch reads as a column.
  centerY: props.data?.branch ? props.sourceY + 24 : undefined
}))

const plusAt = computed(() => props.data?.branch
  ? { x: props.targetX, y: props.targetY - 24 }
  : { x: path.value[1], y: path.value[2] })

// The label sits just below the turn, the + just above what comes next, so
// the two never share a spot however short the branch edge is.
const labelAt = computed(() => ({ x: props.targetX, y: props.sourceY + 50 }))

</script>

<template>
  <BaseEdge
    :id="id"
    :path="path[0]"
    :style="{
      stroke: 'var(--flow-edge)',
      strokeWidth: 1.5,
      opacity: data?.dimmed ? 0.35 : 1,
      transition: 'opacity 200ms'
    }"
  />
  <!-- The stretch a shown contact walked, drawn over the line in order. -->
  <path
    v-if="data?.taken"
    :key="data.traceKey"
    class="flow-lit"
    :d="path[0]"
    path-length="1"
    :style="{ animationDelay: `${data.delay ?? 0}ms` }"
  />
  <EdgeLabelRenderer>
    <div
      v-if="data?.label"
      class="nodrag nopan pointer-events-none absolute"
      :style="{ transform: `translate(-50%, -50%) translate(${labelAt.x}px, ${labelAt.y}px)` }"
    >
      <span
        :class="[
          'rounded-full border px-2 py-0.5 text-[11px] font-semibold',
          data.label === 'yes'
            ? 'border-emerald-500/35 bg-emerald-950 text-emerald-300 light:border-emerald-200 light:bg-emerald-50 light:text-emerald-700'
            : 'border-white/15 bg-neutral-900 text-white/70 light:border-gray-200 light:bg-white light:text-gray-600',
          data.dimmed && 'opacity-40'
        ]"
      >{{ data.label === 'yes' ? t('automations.canvas.yes') : t('automations.canvas.no') }}</span>
    </div>
    <div
      v-if="flow.editable.value && data?.insert"
      class="nodrag nopan absolute"
      :style="{
        transform: `translate(-50%, -50%) translate(${plusAt.x}px, ${plusAt.y}px)`,
        pointerEvents: 'all'
      }"
    >
      <Popover v-model:open="open">
        <PopoverTrigger as-child>
          <button
            type="button"
            :class="[
              'flex h-6 w-6 items-center justify-center rounded-full border shadow-sm transition-[transform,background-color,border-color,color] duration-150 hover:scale-110 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/50',
              open
                ? 'border-primary bg-primary text-primary-foreground'
                : 'border-white/20 bg-neutral-900 text-white/70 hover:border-emerald-500/60 hover:text-emerald-300 light:border-gray-300 light:bg-white light:text-gray-500 light:hover:border-emerald-500 light:hover:text-emerald-600'
            ]"
            :aria-label="t('automations.canvas.addHere')"
            :title="t('automations.canvas.addHere')"
          >
            <Plus class="h-3.5 w-3.5" />
          </button>
        </PopoverTrigger>
        <PopoverContent class="w-80 p-0" side="right" align="start" :side-offset="10" :collision-padding="12">
          <AddStepMenu v-if="open" :point="data.insert" @close="open = false" />
        </PopoverContent>
      </Popover>
    </div>
  </EdgeLabelRenderer>
</template>
