<script setup lang="ts">
/**
 * A widget on the dashboard: its title, the area it belongs to, and what it
 * shows. One frame for every kind of widget, where the page used to write the
 * same header, hover actions and drag handle out four times.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Pencil, Trash2, GripVertical } from 'lucide-vue-next'
import type { DashboardWidget, WidgetData } from '@/services/api'
import WidgetBody from './WidgetBody.vue'
import { areaIcon, widgetArea } from './widgetFormat'

const props = defineProps<{
  widget: DashboardWidget
  data?: WidgetData | null
  loading?: boolean
  comparisonLabel?: string
  width?: number
  dragMode?: boolean
  canEdit?: boolean
  canDelete?: boolean
}>()

const emit = defineEmits<{ edit: []; delete: [] }>()

const { t } = useI18n()

const TILE: Record<string, string> = {
  blue: 'bg-blue-500/15 text-blue-400 light:bg-blue-50 light:text-blue-600',
  green: 'bg-emerald-500/15 text-emerald-400 light:bg-emerald-50 light:text-emerald-600',
  purple: 'bg-purple-500/15 text-purple-400 light:bg-purple-50 light:text-purple-600',
  orange: 'bg-orange-500/15 text-orange-400 light:bg-orange-50 light:text-orange-600',
  red: 'bg-red-500/15 text-red-400 light:bg-red-50 light:text-red-600',
  cyan: 'bg-cyan-500/15 text-cyan-400 light:bg-cyan-50 light:text-cyan-600'
}

const isNumber = computed(() => !['chart', 'table', 'shortcuts', 'leaderboard', 'funnel'].includes(props.widget.display_type))
const icon = computed(() => areaIcon(widgetArea(props.widget)))
const tile = computed(() => TILE[props.widget.color] || TILE.blue)
const showTile = computed(() => !['table', 'shortcuts'].includes(props.widget.display_type))
</script>

<template>
  <div
    class="group relative flex h-full flex-col overflow-hidden rounded-lg border border-white/[0.08] bg-white/[0.04] p-5 transition-colors card-depth hover:bg-white/[0.06] light:border-gray-200 light:bg-white light:hover:bg-gray-50"
  >
    <div v-if="dragMode" class="widget-drag-handle absolute left-1.5 top-1.5 z-10 cursor-grab text-white/25 active:cursor-grabbing light:text-gray-300">
      <GripVertical class="h-4 w-4" />
    </div>

    <div class="flex items-start justify-between gap-3 pb-2">
      <div class="min-w-0">
        <p class="truncate text-sm font-medium text-white/60 light:text-gray-500" :title="widget.name">{{ widget.name }}</p>
        <p
          v-if="widget.description && !isNumber"
          class="mt-0.5 truncate text-xs text-white/35 light:text-gray-400"
          :title="widget.description"
        >{{ widget.description }}</p>
      </div>
      <div class="flex shrink-0 items-center gap-1.5">
        <div v-if="!dragMode && (canEdit || canDelete)" class="flex items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          <Button
            v-if="canEdit"
            variant="ghost"
            size="icon"
            class="h-7 w-7 text-white/40 hover:bg-white/[0.08] hover:text-white light:text-gray-400 light:hover:bg-gray-100 light:hover:text-gray-700"
            :aria-label="t('dashboard.editWidgetTooltip')"
            :title="t('dashboard.editWidgetTooltip')"
            @click.stop="emit('edit')"
          >
            <Pencil class="h-3.5 w-3.5" />
          </Button>
          <Button
            v-if="canDelete"
            variant="ghost"
            size="icon"
            class="h-7 w-7 text-white/40 hover:bg-red-500/10 hover:text-red-400 light:text-gray-400 light:hover:bg-red-50 light:hover:text-red-600"
            :aria-label="t('dashboard.deleteWidgetTooltip')"
            :title="t('dashboard.deleteWidgetTooltip')"
            @click.stop="emit('delete')"
          >
            <Trash2 class="h-3.5 w-3.5" />
          </Button>
        </div>
        <div v-if="showTile" :class="['flex h-9 w-9 items-center justify-center rounded-lg', tile]">
          <component :is="icon" class="h-4 w-4" />
        </div>
      </div>
    </div>

    <div class="min-h-0 flex-1">
      <WidgetBody :widget="widget" :data="data" :loading="loading" :comparison-label="comparisonLabel" :width="width" />
    </div>
  </div>
</template>
