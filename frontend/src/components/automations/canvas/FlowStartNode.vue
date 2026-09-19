<script setup lang="ts">
/**
 * The first card: what starts the automation, and who it is for. Its title
 * is the start of the rule's sentence — "When a tag is added" — so it needs
 * no label of its own.
 *
 * "Only for contacts where…" lives on this card rather than as a step,
 * because it is not something that happens on the path — it decides whether
 * anyone sets foot on it.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'
import { AlertTriangle, Zap, Filter } from 'lucide-vue-next'
import { toneTile } from '../flow/steps'
import type { NodeView } from './context'
import type { Part } from '../flow/sentences'
import type { Component } from 'vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  id: string
  data: { view: NodeView & { icon?: Component; audience?: Part[] } }
}>()

const { t } = useI18n()
const view = computed(() => props.data.view)
</script>

<template>
  <div
    :class="[
      'relative h-full rounded-lg border bg-card text-card-foreground shadow-[0_1px_2px_rgba(0,0,0,0.2),0_6px_16px_-8px_rgba(0,0,0,0.4)] transition-[border-color,box-shadow] duration-200 light:shadow-[0_1px_2px_rgba(0,0,0,0.05),0_6px_16px_-8px_rgba(0,0,0,0.14)]',
      view.selected
        ? 'border-emerald-500/70 ring-2 ring-emerald-500/25 light:border-emerald-500'
        : view.problem
          ? 'border-amber-500/45 light:border-amber-400'
          : 'border-white/[0.09] hover:border-white/20 light:border-gray-200 light:hover:border-gray-300'
    ]"
  >
    <div class="flex items-start gap-3 px-3.5 pt-3">
      <span :class="['mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md', toneTile.trigger]">
        <component :is="view.icon || Zap" class="h-4 w-4" />
      </span>
      <div class="min-w-0 flex-1">
        <p class="line-clamp-2 text-[13.5px] font-medium leading-5">{{ view.title }}</p>
        <p v-if="view.problem" class="mt-0.5 line-clamp-2 text-xs leading-4 text-amber-300 light:text-amber-700">
          <AlertTriangle class="-mt-px mr-1 inline h-3 w-3" aria-hidden="true" />{{ view.problem }}
        </p>
        <p v-else-if="view.parts?.length" class="mt-0.5 line-clamp-2 break-words text-xs leading-4 text-white/50 light:text-gray-500">
          <span
            v-for="(part, i) in view.parts"
            :key="i"
            :class="part.value ? 'font-medium text-white/90 light:text-gray-900' : ''"
          >{{ part.text }}</span>
        </p>
      </div>
    </div>
    <div class="mx-3.5 mt-2.5 flex items-center gap-1.5 border-t border-white/[0.06] pt-2 text-xs leading-4 text-white/50 light:border-gray-100 light:text-gray-500">
      <Filter class="h-3 w-3 shrink-0" />
      <span v-if="view.audience?.length" class="truncate">
        <span
          v-for="(part, i) in view.audience"
          :key="i"
          :class="part.value ? 'font-medium text-white/90 light:text-gray-900' : ''"
        >{{ part.text }}</span>
      </span>
      <span v-else class="truncate">{{ t('automations.canvas.everyone') }}</span>
    </div>
    <Handle type="source" :position="Position.Bottom" class="!pointer-events-none !opacity-0" />
  </div>
</template>
