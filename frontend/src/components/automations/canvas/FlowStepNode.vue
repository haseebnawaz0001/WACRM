<script setup lang="ts">
/**
 * One step on the path: an action, a question, or a wait.
 *
 * The card is a sentence — what the step does, and what it does it with —
 * with a tile whose colour says what the step does to the path. The values
 * somebody chose carry weight, so the card scans as "VIP", "Billing", "1 day"
 * rather than as a line of grey. A step that still needs something says what,
 * in amber; a test run marks each card with what happened to that contact;
 * a wait shows who is sitting at it now.
 */
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'
import { AlertTriangle, Copy, Trash2, Check, X, Hourglass, CircleSlash } from 'lucide-vue-next'
import { stepIcons, toneOf, toneTile } from '../flow/steps'
import { CONDITION, WAIT, type FlowStep } from '../flow/tree'
import { flowContextKey, type NodeView } from './context'

defineOptions({ inheritAttrs: false })

const props = defineProps<{ id: string; data: { step: FlowStep; view: NodeView; traceKey?: number } }>()

const { t } = useI18n()
const flow = inject(flowContextKey)!

const step = computed(() => props.data.step)
const view = computed(() => props.data.view)
const tone = computed(() => toneOf(step.value.type))
const icon = computed(() => stepIcons[step.value.type])
const isWait = computed(() => step.value.type === WAIT)
const isQuestion = computed(() => step.value.type === CONDITION)

const traceLabel = computed(() => {
  const trace = view.value.trace
  if (!trace) return ''
  if (isQuestion.value && trace.status === 'succeeded') {
    return trace.branch === 'then' ? t('automations.trace.yes') : t('automations.trace.no')
  }
  if (isWait.value && trace.status === 'succeeded') {
    return trace.dryRun ? t('automations.trace.wouldWait') : t('automations.trace.waited')
  }
  return t(`automations.trace.${trace.status}`)
})
</script>

<template>
  <div
    :class="[
      'group relative h-full rounded-lg border bg-card text-card-foreground shadow-[0_1px_2px_rgba(0,0,0,0.2),0_4px_12px_-6px_rgba(0,0,0,0.35)] transition-[opacity,box-shadow,border-color] duration-200 light:shadow-[0_1px_2px_rgba(0,0,0,0.05),0_4px_12px_-6px_rgba(0,0,0,0.12)]',
      view.selected
        ? 'border-emerald-500/70 ring-2 ring-emerald-500/25 light:border-emerald-500'
        : view.problem
          ? 'border-amber-500/45 light:border-amber-400'
          : 'border-white/[0.09] hover:border-white/20 light:border-gray-200 light:hover:border-gray-300',
      view.dimmed && 'opacity-35',
      isWait ? 'rounded-full' : ''
    ]"
  >
    <Handle type="target" :position="Position.Top" class="!pointer-events-none !opacity-0" />

    <!-- A wait is a pause, not a task: drawn as a slim pill. -->
    <div v-if="isWait" class="flex h-full items-center gap-2.5 px-3">
      <span :class="['flex h-7 w-7 shrink-0 items-center justify-center rounded-full', toneTile.wait]">
        <Hourglass class="h-3.5 w-3.5" />
      </span>
      <span class="min-w-0 flex-1 truncate text-sm font-medium">{{ view.title }}</span>
      <span
        v-if="view.waiting"
        class="shrink-0 rounded-full bg-sky-500/15 px-2 py-0.5 text-[11px] font-medium tabular-nums text-sky-300 light:bg-sky-50 light:text-sky-700"
      >{{ t('automations.canvas.waitingHere', { n: view.waiting }, view.waiting) }}</span>
    </div>

    <div v-else class="flex h-full items-start gap-3 px-3.5 py-3">
      <span :class="['mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md', toneTile[tone]]">
        <component :is="icon" v-if="icon" class="h-4 w-4" />
      </span>
      <div class="min-w-0 flex-1">
        <p class="truncate text-[13.5px] font-medium leading-5">{{ view.title }}</p>
        <p
          v-if="view.problem"
          class="mt-0.5 line-clamp-2 text-xs leading-4 text-amber-300 light:text-amber-700"
        >
          <AlertTriangle class="-mt-px mr-1 inline h-3 w-3" aria-hidden="true" />{{ view.problem }}
        </p>
        <p
          v-else-if="view.parts?.length"
          class="mt-0.5 line-clamp-2 break-words text-xs leading-4 text-white/50 light:text-gray-500"
        >
          <span
            v-for="(part, i) in view.parts"
            :key="i"
            :class="part.value ? 'font-medium text-white/90 light:text-gray-900' : ''"
          >{{ part.text }}</span>
        </p>
      </div>
    </div>

    <!-- What happened to the contact the trace is for. -->
    <span
      v-if="view.trace"
      :key="data.traceKey"
      :style="{ animationDelay: `${view.delay ?? 0}ms` }"
      :class="[
        'flow-mark absolute -top-2.5 right-3 inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium shadow-sm',
        view.trace.status === 'failed'
          ? 'border-red-500/40 bg-red-950 text-red-300 light:border-red-200 light:bg-red-50 light:text-red-700'
          : view.trace.status === 'skipped'
            ? 'border-white/15 bg-neutral-900 text-white/60 light:border-gray-200 light:bg-white light:text-gray-500'
            : view.trace.status === 'waiting'
              ? 'border-sky-500/40 bg-sky-950 text-sky-300 light:border-sky-200 light:bg-sky-50 light:text-sky-700'
              : 'border-emerald-500/40 bg-emerald-950 text-emerald-300 light:border-emerald-200 light:bg-emerald-50 light:text-emerald-700'
      ]"
    >
      <X v-if="view.trace.status === 'failed'" class="h-3 w-3" />
      <CircleSlash v-else-if="view.trace.status === 'skipped'" class="h-3 w-3" />
      <Hourglass v-else-if="view.trace.status === 'waiting'" class="h-3 w-3" />
      <Check v-else class="h-3 w-3" />
      {{ traceLabel }}
    </span>

    <!-- Quick edits, on hover, for people who build by clicking. -->
    <div
      v-if="flow.editable.value && !view.trace"
      class="absolute -right-2 -top-2 flex gap-0.5 rounded-md border border-white/10 bg-popover p-0.5 opacity-0 shadow-md transition-opacity duration-150 group-hover:opacity-100 group-focus-within:opacity-100 light:border-gray-200"
    >
      <button
        type="button"
        class="rounded-sm p-1 text-white/60 hover:bg-white/[0.08] hover:text-white light:text-gray-500 light:hover:bg-gray-100 light:hover:text-gray-900"
        :aria-label="t('automations.canvas.duplicate')"
        :title="t('automations.canvas.duplicate')"
        @click.stop="flow.duplicate(id)"
      >
        <Copy class="h-3.5 w-3.5" />
      </button>
      <button
        type="button"
        class="rounded-sm p-1 text-white/60 hover:bg-red-500/15 hover:text-red-300 light:text-gray-500 light:hover:bg-red-50 light:hover:text-red-700"
        :aria-label="t('automations.canvas.remove')"
        :title="t('automations.canvas.remove')"
        @click.stop="flow.remove(id)"
      >
        <Trash2 class="h-3.5 w-3.5" />
      </button>
    </div>

    <Handle type="source" :position="Position.Bottom" class="!pointer-events-none !opacity-0" />
  </div>
</template>
