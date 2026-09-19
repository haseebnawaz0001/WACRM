<script setup lang="ts">
/**
 * What a widget shows: its number, chart, table, ranking, funnel or links.
 *
 * One renderer for the dashboard and the builder's preview, so what a person
 * sees while building a widget is what lands on the dashboard. It used to be
 * written out once per display type inside the dashboard page, and funnels and
 * leaderboards — which the server could compute — were never written out at
 * all: a pinned funnel rendered as a card reading "0".
 */
import { computed, ref, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { Skeleton } from '@/components/ui/skeleton'
import { TrendingUp, TrendingDown, Minus, Clock } from 'lucide-vue-next'
import { Line, Bar, Doughnut, chartColors, barLineOptions, pieOptions } from '@/lib/charts'
import { getAvatarColor } from '@/lib/utils'
import { navigationShortcuts } from '@/components/layout/navigation'
import type { WidgetData } from '@/services/api'
import { changeTone, formatValue, splitLabel } from './widgetFormat'

export interface WidgetShape {
  name: string
  display_type: string
  chart_type?: string
  group_by_field?: string
  data_source: string
  metric?: string
  color?: string
  show_change?: boolean
  config?: Record<string, any>
}

const props = withDefaults(defineProps<{
  widget: WidgetShape
  data?: WidgetData | null
  loading?: boolean
  /** "from last month", for the change line. */
  comparisonLabel?: string
  /** Grid columns the card spans, for laying out links. */
  width?: number
}>(), { width: 6 })

const { t, te } = useI18n()

const type = computed(() => props.widget.display_type)
const isNumber = computed(() => !['chart', 'table', 'shortcuts', 'leaderboard', 'funnel'].includes(type.value))
const points = computed(() => props.data?.data_points || [])
const dim = computed(() => props.widget.group_by_field || undefined)

const label = (p: { label: string; key?: string }) => splitLabel(t, te, dim.value, p)
const value = (v: number) => formatValue(v, props.data)

const tone = computed(() => changeTone(props.data?.change || 0, props.data?.lower_is_better))

// --- Charts ---

const COLORS: Record<string, string> = {
  blue: 'rgb(59, 130, 246)',
  green: 'rgb(16, 185, 129)',
  purple: 'rgb(139, 92, 246)',
  orange: 'rgb(245, 158, 11)',
  red: 'rgb(239, 68, 68)',
  cyan: 'rgb(6, 182, 212)'
}

const hasChartData = computed(() => {
  const d = props.data
  return !!d && ((d.chart_data?.length || 0) > 0 || points.value.length > 0 || (d.grouped_series?.datasets?.length || 0) > 0)
})

const chartData = computed(() => {
  const d = props.data
  if (!d) return { labels: [], datasets: [] }
  const w = props.widget
  const series = d.grouped_series

  if (w.chart_type === 'line' && series && series.datasets.length > 0) {
    const colors = chartColors(series.datasets.map(s => s.label))
    return {
      labels: series.labels,
      datasets: series.datasets.map((s, i) => ({
        label: s.label, data: s.data, borderColor: colors[i], backgroundColor: colors[i], fill: false, tension: 0.3
      }))
    }
  }

  // A split: colours follow the raw value (so "failed" is red), words follow
  // the viewer's language.
  if ((w.chart_type === 'pie' || w.chart_type === 'bar') && points.value.length > 0) {
    const keys = points.value.map(p => p.key ?? p.label)
    const colors = chartColors(keys)
    return {
      labels: points.value.map(label),
      datasets: [{
        label: w.name,
        data: points.value.map(p => p.value),
        backgroundColor: colors,
        borderWidth: 0,
        borderRadius: w.chart_type === 'bar' ? 4 : 0,
        maxBarThickness: 48
      }]
    }
  }

  const series1 = d.chart_data || []
  const color = COLORS[w.color || 'blue'] || COLORS.blue
  return {
    labels: series1.map(p => p.label),
    datasets: [{
      label: w.name,
      data: series1.map(p => p.value),
      borderColor: color,
      backgroundColor: w.chart_type === 'bar' ? color : color.replace('rgb', 'rgba').replace(')', ', 0.12)'),
      fill: w.chart_type === 'line',
      tension: 0.3,
      borderRadius: 4,
      maxBarThickness: 48,
      pointRadius: 0,
      pointHoverRadius: 4,
      borderWidth: 2
    }]
  }
})

const chartOptions = computed(() => {
  const series = props.data?.grouped_series?.datasets?.length ?? 0
  const unit = props.data?.unit
  const options: any = barLineOptions({ singleSeries: series <= 1, integer: !unit && props.widget.metric !== 'sum' && props.widget.metric !== 'avg' })
  if (unit) {
    // Money and time read as money and time on the axis and in the tooltip.
    options.scales.y.ticks.callback = (v: number | string) => value(Number(v))
    options.plugins.tooltip = {
      ...options.plugins.tooltip,
      callbacks: { label: (ctx: any) => ` ${value(Number(ctx.parsed.y ?? ctx.parsed))}` }
    }
  }
  return options
})

/**
 * A chart is drawn afresh when what it shows changes, rather than updated in
 * place. Updating in place raced the dashboard re-laying itself out after a
 * widget was added: Chart.js measured a canvas that had already left the page
 * and threw, twice, on every save.
 */
const chartRevision = ref(0)
watch(() => [chartData.value, props.widget.chart_type], () => { chartRevision.value++ })

const doughnutOptions = computed(() => {
  const options: any = pieOptions()
  if (props.data?.unit) {
    options.plugins.tooltip = {
      ...options.plugins.tooltip,
      callbacks: { label: (ctx: any) => ` ${ctx.label}: ${value(Number(ctx.parsed))}` }
    }
  }
  return options
})

// --- Leaderboard and funnel ---

const topValue = computed(() => Math.max(...points.value.map(p => p.value), 0))
const barWidth = (v: number) => (topValue.value > 0 ? `${Math.max(2, (v / topValue.value) * 100)}%` : '0%')

const initials = (name: string) => name.split(/\s+/).filter(Boolean).map(n => n[0]).join('').slice(0, 2).toUpperCase()

/** Each step's share of the one before it. */
const conversion = (i: number) => {
  if (i === 0) return ''
  const before = points.value[i - 1]?.value || 0
  if (!before) return '—'
  return `${Math.round((points.value[i].value / before) * 100)}%`
}

// --- Links ---

const shortcuts = computed(() => {
  const all = new Map(navigationShortcuts().map(s => [s.key, s]))
  const keys = (props.widget.config?.shortcuts as string[] | undefined) || []
  return keys.map(k => all.get(k)).filter((s): s is NonNullable<typeof s> => !!s)
})

const shortcutIcon = (icon: unknown) => icon as Component

const formatTime = (dateStr: string): string => {
  const diffMs = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(diffMs / 60000)
  if (mins < 1) return t('dashboard.justNow')
  if (mins < 60) return t('dashboard.minutesAgo', { count: mins })
  const hours = Math.floor(mins / 60)
  if (hours < 24) return t('dashboard.hoursAgo', { count: hours })
  return t('dashboard.daysAgo', { count: Math.floor(hours / 24) })
}

const emptyText = computed(() => (props.data?.snapshot ? t('dashboard.nothingRightNow') : t('dashboard.noDataInRange')))
</script>

<template>
  <!-- Links to pages -->
  <div v-if="type === 'shortcuts'" :class="['grid gap-2 pt-1', width >= 8 ? 'grid-cols-3' : width >= 5 ? 'grid-cols-2' : 'grid-cols-1']">
    <RouterLink
      v-for="s in shortcuts"
      :key="s.key"
      :to="s.path"
      class="card-interactive flex items-center gap-3 rounded-lg border border-white/[0.08] bg-white/[0.02] px-3 py-2.5 light:border-gray-200 light:bg-gray-50"
    >
      <component :is="shortcutIcon(s.icon)" class="h-4 w-4 shrink-0 text-white/55 light:text-gray-500" />
      <span class="truncate text-sm font-medium text-white light:text-gray-900">{{ t(s.name) }}</span>
    </RouterLink>
  </div>

  <!-- One number -->
  <div v-else-if="isNumber" class="pt-2">
    <Skeleton v-if="loading" class="h-8 w-24 bg-white/[0.08] light:bg-gray-200" />
    <template v-else>
      <div class="text-3xl font-bold tabular-nums text-white light:text-gray-900">
        <Transition name="counter-fade" mode="out-in">
          <span :key="data?.value">{{ value(data?.value || 0) }}</span>
        </Transition>
      </div>
      <!-- A count of right now has no earlier period; it says so instead. -->
      <div v-if="data?.snapshot" class="mt-1 flex items-center gap-1.5 text-xs text-white/50 light:text-gray-500">
        <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 light:bg-emerald-600" aria-hidden="true" />
        {{ t('dashboard.rightNow') }}
      </div>
      <div v-else-if="widget.show_change && data" class="mt-1 flex items-center text-xs text-white/50 light:text-gray-500">
        <component
          :is="data.change > 0 ? TrendingUp : data.change < 0 ? TrendingDown : Minus"
          :class="['mr-1 h-3 w-3', tone === 'good' ? 'text-emerald-400 light:text-emerald-600' : tone === 'bad' ? 'text-red-400 light:text-red-600' : 'text-white/30 light:text-gray-400']"
        />
        <span :class="tone === 'good' ? 'text-emerald-400 light:text-emerald-600' : tone === 'bad' ? 'text-red-400 light:text-red-600' : ''">
          {{ data.change ? `${Math.abs(data.change).toFixed(1)}%` : t('dashboard.noChange') }}
        </span>
        <span class="ml-1">{{ comparisonLabel }}</span>
      </div>
    </template>
  </div>

  <!-- Chart -->
  <div v-else-if="type === 'chart'" class="h-full min-h-0">
    <Skeleton v-if="loading" class="h-full w-full bg-white/[0.08] light:bg-gray-200" />
    <template v-else-if="hasChartData">
      <Line v-if="widget.chart_type === 'line'" :key="chartRevision" :data="chartData" :options="chartOptions" />
      <Bar v-else-if="widget.chart_type === 'bar'" :key="chartRevision" :data="chartData" :options="chartOptions" />
      <Doughnut v-else-if="widget.chart_type === 'pie'" :key="chartRevision" :data="chartData" :options="doughnutOptions" />
    </template>
    <div v-else class="flex h-full items-center justify-center text-sm text-white/50 light:text-gray-500">{{ emptyText }}</div>
  </div>

  <!-- Ranking of people -->
  <div v-else-if="type === 'leaderboard'" class="h-full min-h-0 overflow-y-auto">
    <div v-if="loading" class="space-y-3">
      <Skeleton v-for="i in 4" :key="i" class="h-8 w-full bg-white/[0.08] light:bg-gray-200" />
    </div>
    <ol v-else-if="points.length" class="space-y-3">
      <li v-for="(p, i) in points" :key="p.key || p.label" class="flex items-center gap-3">
        <span class="w-4 shrink-0 text-right text-xs tabular-nums text-white/40 light:text-gray-400">{{ i + 1 }}</span>
        <span :class="['flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-[11px] font-medium text-white', getAvatarColor(p.label)]">
          {{ initials(p.label) }}
        </span>
        <div class="min-w-0 flex-1">
          <div class="flex items-baseline justify-between gap-2">
            <span class="truncate text-sm text-white light:text-gray-900">{{ label(p) }}</span>
            <span class="shrink-0 text-sm font-semibold tabular-nums text-white light:text-gray-900">{{ value(p.value) }}</span>
          </div>
          <div class="mt-1 h-1 rounded-full bg-white/[0.06] light:bg-gray-100">
            <div class="h-1 rounded-full bg-emerald-500/70 light:bg-emerald-500" :style="{ width: barWidth(p.value) }" />
          </div>
        </div>
      </li>
    </ol>
    <div v-else class="flex h-full items-center justify-center text-sm text-white/50 light:text-gray-500">{{ emptyText }}</div>
  </div>

  <!-- Funnel -->
  <div v-else-if="type === 'funnel'" class="h-full min-h-0 overflow-y-auto">
    <div v-if="loading" class="space-y-3">
      <Skeleton v-for="i in 4" :key="i" class="h-8 w-full bg-white/[0.08] light:bg-gray-200" />
    </div>
    <ol v-else-if="points.length && points[0].value > 0" class="space-y-3">
      <li v-for="(p, i) in points" :key="p.key || p.label">
        <div class="flex items-baseline justify-between gap-2 text-sm">
          <span class="truncate text-white light:text-gray-900">{{ p.label }}</span>
          <span class="shrink-0 font-semibold tabular-nums text-white light:text-gray-900">{{ value(p.value) }}</span>
        </div>
        <div class="mt-1 flex items-center gap-2">
          <div class="h-2 flex-1 rounded-sm bg-white/[0.06] light:bg-gray-100">
            <div class="h-2 rounded-sm bg-emerald-500/70 light:bg-emerald-500" :style="{ width: barWidth(p.value) }" />
          </div>
          <span class="w-10 shrink-0 text-right text-xs tabular-nums text-white/50 light:text-gray-500" :title="i ? t('dashboard.conversionHint') : undefined">{{ conversion(i) }}</span>
        </div>
      </li>
    </ol>
    <div v-else class="flex h-full items-center justify-center text-sm text-white/50 light:text-gray-500">{{ emptyText }}</div>
  </div>

  <!-- Table: groups as rows, or the latest records -->
  <div v-else-if="type === 'table'" class="h-full min-h-0 overflow-auto">
    <Skeleton v-if="loading" class="h-full w-full bg-white/[0.08] light:bg-gray-200" />
    <table v-else-if="widget.group_by_field && points.length" class="w-full">
      <tbody>
        <tr v-for="p in points" :key="p.key || p.label" class="border-b border-white/[0.04] last:border-0 light:border-gray-100">
          <td class="py-2 text-sm text-white/70 light:text-gray-700">{{ label(p) }}</td>
          <td class="py-2 text-right text-sm font-medium tabular-nums text-white light:text-gray-900">{{ value(p.value) }}</td>
        </tr>
      </tbody>
    </table>
    <div v-else-if="data?.table_rows?.length" class="space-y-3">
      <div v-for="row in data.table_rows" :key="row.id" class="flex items-start gap-3 rounded-lg p-3 transition-colors hover:bg-white/[0.04] light:hover:bg-gray-50">
        <div :class="['flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-sm font-medium text-white', getAvatarColor(row.label)]">
          {{ initials(row.label) }}
        </div>
        <div class="min-w-0 flex-1">
          <div class="flex items-center justify-between">
            <p class="truncate text-sm font-medium text-white light:text-gray-900">{{ row.label }}</p>
            <span class="flex shrink-0 items-center gap-1 text-xs text-white/50 light:text-gray-500">
              <Clock class="h-3 w-3" />{{ formatTime(row.created_at) }}
            </span>
          </div>
          <p class="truncate text-sm text-white/50 light:text-gray-600">{{ row.sub_label }}</p>
          <div class="mt-1 flex items-center gap-2">
            <span
              v-if="row.direction"
              :class="[
                'rounded-full px-1.5 py-0.5 text-[10px] font-medium',
                row.direction === 'incoming' ? 'bg-emerald-500/20 text-emerald-400 light:bg-emerald-100 light:text-emerald-700' : 'bg-blue-500/20 text-blue-400 light:bg-blue-100 light:text-blue-700'
              ]"
            >{{ row.direction }}</span>
            <span
              v-if="row.status"
              :class="[
                'rounded-full px-1.5 py-0.5 text-[10px] font-medium',
                row.status === 'delivered' ? 'bg-blue-500/20 text-blue-400 light:bg-blue-100 light:text-blue-700'
                : row.status === 'read' ? 'bg-emerald-500/20 text-emerald-400 light:bg-emerald-100 light:text-emerald-700'
                : row.status === 'failed' ? 'bg-red-500/20 text-red-400 light:bg-red-100 light:text-red-700'
                : 'bg-white/10 text-white/50 light:bg-gray-100 light:text-gray-600'
              ]"
            >{{ row.status }}</span>
          </div>
        </div>
      </div>
    </div>
    <div v-else class="flex h-full items-center justify-center text-sm text-white/50 light:text-gray-500">{{ emptyText }}</div>
  </div>
</template>
