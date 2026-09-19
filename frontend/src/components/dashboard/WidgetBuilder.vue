<script setup lang="ts">
/**
 * Building a dashboard widget.
 *
 * The old builder was a form over a query: pick a "data source" (messages,
 * sessions, transfers…), a "metric" (count, sum, avg — with no way to say what
 * to sum), a display type, a group-by column shown as its database name, and
 * filters whose values were typed by hand. Narrowing a widget to one agent
 * meant typing that agent's UUID. Nobody at a front desk could use it, and
 * most of what it could make was not worth pinning.
 *
 * Now it asks three questions in the order a person thinks them:
 *
 *   1. What do you want to see? — a measure, named the way it would be asked
 *      ("Waiting for a reply", "Deals won"), found by area or by search.
 *   2. How should it look? — only the views that measure can take, and for a
 *      split, what to split by.
 *   3. Only include…? — optional, each condition picked from lists, with
 *      "Me" meaning whoever is looking.
 *
 * The name fills itself in from those answers until it is edited. A live
 * preview beside the questions shows the widget as it will land, with the
 * dashboard's own date range, so every choice can be judged by its result.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  Search, X, Plus, ArrowLeft, Hash, LineChart, BarChart3, PieChart, Table2, Trophy, Filter as FunnelIcon,
  Loader2, AlertTriangle, History, Check
} from 'lucide-vue-next'
import {
  widgetsService, type DashboardWidget, type MeasureDim, type WidgetData, type WidgetMeasure
} from '@/services/api'
import { navigationShortcuts } from '@/components/layout/navigation'
import { useAppToast } from '@/composables/useAppToast'
import WidgetBody from './WidgetBody.vue'
import { AREA_ICONS, areaIcon, valueLabel } from './widgetFormat'

const props = defineProps<{
  open: boolean
  /** The widget being edited, or null for a new one. */
  widget: DashboardWidget | null
  /** The dashboard's date range, which the preview uses. */
  range: { from: string; to: string }
  comparisonLabel: string
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  saved: []
  /** Open a widget from the old builder in the old builder. */
  editLegacy: [widget: DashboardWidget]
}>()

const { t, te } = useI18n()
const { success, error: showError } = useAppToast()

// --- The catalog ---

// Loaded once per page: the measures do not change while the page is open.
let catalogPromise: Promise<WidgetMeasure[]> | null = null
const catalog = ref<WidgetMeasure[]>([])
const catalogFailed = ref(false)

async function loadCatalog() {
  catalogFailed.value = false
  catalogPromise ||= widgetsService.catalog().then(res => ((res.data as any).data?.measures || []) as WidgetMeasure[])
  try {
    catalog.value = await catalogPromise
  } catch {
    catalogPromise = null
    catalogFailed.value = true
  }
}
onMounted(loadCatalog)

const AREAS = ['inbox', 'contacts', 'deals', 'followups', 'automations', 'calls', 'messaging', 'chatbot', 'links'] as const
type Area = typeof AREAS[number]

const measureName = (key: string) => t(`dashboard.measures.${key}.name`, key)
const measureDescription = (key: string) => t(`dashboard.measures.${key}.description`, '')
const dimName = (key: string) => t(`dashboard.dims.${key}`, key)

// --- What the person has chosen ---

type Mode = 'measure' | 'links'
const mode = ref<Mode | null>(null)
const measureKey = ref('')
const view = ref('')
const split = ref('')
const conditions = ref<Array<{ field: string; operator: 'equals' | 'not_equals'; value: string }>>([])
const shortcuts = ref<string[]>([])
const name = ref('')
const nameEdited = ref(false)
const isShared = ref(false)
const legacy = ref(false)

const search = ref('')
const area = ref<Area | 'all'>('all')

const measure = computed(() => catalog.value.find(m => m.key === measureKey.value) || null)

function reset() {
  mode.value = null
  measureKey.value = ''
  view.value = ''
  split.value = ''
  conditions.value = []
  shortcuts.value = []
  name.value = ''
  nameEdited.value = false
  isShared.value = false
  legacy.value = false
  search.value = ''
  area.value = 'all'
  preview.value = null
  previewError.value = false
}

/** Loads the widget being edited into the questions. */
function loadWidget(w: DashboardWidget | null) {
  reset()
  if (!w) return
  name.value = w.name
  nameEdited.value = true
  isShared.value = w.is_shared
  if (w.display_type === 'shortcuts') {
    mode.value = 'links'
    shortcuts.value = [...new Set(((w.config?.shortcuts as string[]) || []).map(canonicalShortcut))]
    return
  }
  const key = w.config?.measure as string | undefined
  if (!key) {
    legacy.value = true
    return
  }
  mode.value = 'measure'
  measureKey.value = key
  view.value = (w.config?.view as string) || 'number'
  split.value = w.group_by_field || ''
  conditions.value = (w.filters || []).map(f => ({
    field: f.field,
    operator: f.operator === 'not_equals' ? 'not_equals' : 'equals',
    value: f.value
  }))
}

/** Starts over from the first question, keeping the name a widget had. */
function rebuild() {
  const kept = name.value
  reset()
  name.value = kept
  nameEdited.value = !!kept
  isShared.value = props.widget?.is_shared ?? false
}

// --- Step 1: what to see ---

const visibleMeasures = computed(() => {
  const q = search.value.trim().toLowerCase()
  return catalog.value.filter(m => {
    if (area.value !== 'all' && m.area !== area.value) return false
    if (!q) return true
    return `${measureName(m.key)} ${measureDescription(m.key)} ${t(`dashboard.areas.${m.area}`)}`.toLowerCase().includes(q)
  })
})

const showLinksCard = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (area.value !== 'all' && area.value !== 'links') return false
  return !q || `${t('dashboard.builder.linksName')} ${t('dashboard.builder.linksDescription')}`.toLowerCase().includes(q)
})

const areaCount = (a: Area) => (a === 'links' ? 1 : catalog.value.filter(m => m.area === a).length)

function chooseMeasure(m: WidgetMeasure) {
  mode.value = 'measure'
  measureKey.value = m.key
  view.value = m.views[0]
  split.value = ''
  // A condition on something the new measure cannot be narrowed by would be
  // refused on save; it goes rather than lingering unexplained.
  conditions.value = conditions.value.filter(c => m.dims.some(d => d.key === c.field && d.filter))
}

function chooseLinks() {
  mode.value = 'links'
  measureKey.value = ''
  if (!shortcuts.value.length) shortcuts.value = ['inbox', 'contacts', 'tasks']
}

function changeWhat() {
  mode.value = null
}

// --- Step 2: how it looks ---

const VIEW_ICONS: Record<string, any> = {
  number: Hash, trend: LineChart, bar: BarChart3, pie: PieChart, table: Table2, leaderboard: Trophy, funnel: FunnelIcon
}

const splitDims = computed<MeasureDim[]>(() => {
  const m = measure.value
  if (!m) return []
  if (view.value === 'leaderboard') return m.dims.filter(d => d.person)
  return m.dims.filter(d => d.split)
})
const needsSplit = computed(() => ['bar', 'pie', 'table', 'leaderboard'].includes(view.value))

watch([view, measure], () => {
  if (!needsSplit.value) {
    split.value = ''
    return
  }
  if (!splitDims.value.some(d => d.key === split.value)) {
    split.value = view.value === 'leaderboard'
      ? (measure.value?.person || splitDims.value[0]?.key || '')
      : (splitDims.value[0]?.key || '')
  }
})

// --- Step 3: only include ---

const filterDims = computed(() => measure.value?.dims.filter(d => d.filter) || [])

function addCondition() {
  const used = new Set(conditions.value.map(c => c.field))
  const next = filterDims.value.find(d => !used.has(d.key)) || filterDims.value[0]
  if (!next) return
  conditions.value.push({ field: next.key, operator: 'equals', value: next.kind === 'user' ? 'me' : '' })
}

function removeCondition(i: number) {
  conditions.value.splice(i, 1)
}

function onConditionField(i: number, field: string) {
  const d = filterDims.value.find(x => x.key === field)
  conditions.value[i] = { field, operator: conditions.value[i].operator, value: d?.kind === 'user' ? 'me' : '' }
}

/** The choices a condition on one dimension offers. */
function optionsFor(field: string): Array<{ value: string; label: string }> {
  const d = filterDims.value.find(x => x.key === field)
  if (!d) return []
  if (d.kind === 'enum') return (d.values || []).map(v => ({ value: v, label: valueLabel(t, te, d.key, v) }))
  const options = d.options || []
  if (d.kind === 'user') return [{ value: 'me', label: t('dashboard.builder.me') }, ...options]
  return options
}

const completeConditions = computed(() => conditions.value.filter(c => c.field && c.value))

// --- The name ---

const autoName = computed(() => {
  if (mode.value === 'links') return t('dashboard.builder.linksName')
  if (!measure.value) return ''
  const base = measureName(measure.value.key)
  if (needsSplit.value && split.value) {
    // A noun rather than the split's label: "Messages sent by person", not
    // "Messages sent by sent by".
    return t('dashboard.builder.nameBy', { measure: base, split: t(`dashboard.dimNouns.${split.value}`, split.value) })
  }
  const mine = completeConditions.value.some(c => c.value === 'me' && c.operator === 'equals')
  return mine ? t('dashboard.builder.nameMine', { measure: base }) : base
})

watch(autoName, value => {
  if (!nameEdited.value) name.value = value
}, { immediate: true })

function onNameInput(value: string | number) {
  name.value = String(value)
  nameEdited.value = name.value.trim() !== '' && name.value !== autoName.value
}

// --- The preview ---

const preview = ref<WidgetData | null>(null)
const previewLoading = ref(false)
const previewError = ref(false)

const payload = computed(() => ({
  name: name.value.trim() || autoName.value,
  description: measure.value ? measureDescription(measure.value.key) : '',
  config: { measure: measureKey.value, view: view.value },
  group_by_field: needsSplit.value ? split.value : '',
  filters: completeConditions.value,
  is_shared: isShared.value
}))

const canPreview = computed(() => mode.value === 'measure' && !!measure.value && (!needsSplit.value || !!split.value))

let previewSeq = 0
const refreshPreview = useDebounceFn(async () => {
  if (!canPreview.value || !props.open) return
  const seq = ++previewSeq
  previewLoading.value = true
  previewError.value = false
  try {
    const res = await widgetsService.preview(payload.value, props.range)
    if (seq === previewSeq) preview.value = ((res.data as any).data ?? res.data) as WidgetData
  } catch {
    if (seq === previewSeq) {
      preview.value = null
      previewError.value = true
    }
  } finally {
    if (seq === previewSeq) previewLoading.value = false
  }
}, 300)

watch(
  () => [props.open, measureKey.value, view.value, split.value, JSON.stringify(completeConditions.value), props.range.from, props.range.to],
  () => {
    if (canPreview.value) {
      previewLoading.value = true
      refreshPreview()
    }
  }
)

/** The widget as the preview draws it. */
const previewWidget = computed(() => {
  if (mode.value === 'links') {
    return { name: name.value || autoName.value, display_type: 'shortcuts', data_source: 'shortcuts', config: { shortcuts: shortcuts.value } }
  }
  const display: Record<string, [string, string]> = {
    number: ['number', ''], trend: ['chart', 'line'], bar: ['chart', 'bar'], pie: ['chart', 'pie'],
    table: ['table', ''], leaderboard: ['leaderboard', ''], funnel: ['funnel', '']
  }
  const [displayType, chartType] = display[view.value] || ['number', '']
  return {
    name: name.value || autoName.value,
    display_type: displayType,
    chart_type: chartType,
    group_by_field: needsSplit.value ? split.value : '',
    data_source: 'measure',
    show_change: view.value === 'number' && !measure.value?.snapshot,
    config: { measure: measureKey.value, view: view.value },
    color: 'blue'
  }
})

const previewIsTall = computed(() => !['number'].includes(view.value) || mode.value === 'links')

// --- Saving ---

const saving = ref(false)

const canSave = computed(() => {
  if (!name.value.trim() && !autoName.value) return false
  if (mode.value === 'links') return shortcuts.value.length > 0
  return canPreview.value
})

async function save() {
  if (!canSave.value || saving.value) return
  saving.value = true
  try {
    const body = mode.value === 'links'
      ? {
          name: name.value.trim() || autoName.value,
          description: t('dashboard.builder.linksDescription'),
          data_source: 'shortcuts',
          metric: 'count',
          display_type: 'shortcuts',
          config: { shortcuts: [...shortcuts.value] },
          is_shared: isShared.value
        }
      : { ...payload.value, data_source: 'measure', metric: 'count' }
    if (props.widget) {
      await widgetsService.update(props.widget.id, body)
      success(t('common.updatedSuccess', { resource: t('resources.Widget') }))
    } else {
      await widgetsService.create(body)
      success(t('dashboard.builder.added', { name: body.name }))
    }
    emit('saved')
    emit('update:open', false)
  } catch (err: any) {
    showError(t('common.error'), err?.response?.data?.message || t('common.failedSave', { resource: t('resources.widget') }))
  } finally {
    saving.value = false
  }
}

/**
 * Each page once. The shortcut list also carries older names for some pages
 * (so widgets saved with them still render); offering both would let the same
 * page be picked twice.
 */
const allShortcuts = computed(() => {
  const seen = new Set<string>()
  return navigationShortcuts().filter(s => !seen.has(s.path) && seen.add(s.path))
})

/** A saved shortcut key, as the key the picker offers for the same page. */
function canonicalShortcut(key: string): string {
  const path = navigationShortcuts().find(s => s.key === key)?.path
  return allShortcuts.value.find(s => s.path === path)?.key || key
}

function toggleShortcut(key: string) {
  const i = shortcuts.value.indexOf(key)
  if (i >= 0) shortcuts.value.splice(i, 1)
  else shortcuts.value.push(key)
}

const step2Ready = computed(() => mode.value !== null)

// Last, so everything loading a widget touches has been declared.
watch(() => props.open, open => {
  if (open) loadWidget(props.widget)
}, { immediate: true })
</script>

<template>
  <Dialog :open="open" @update:open="v => emit('update:open', v)">
    <!-- A fixed height, so narrowing the list does not make the dialog jump. -->
    <DialogContent class="flex h-[min(780px,92vh)] w-[calc(100vw-2rem)] max-w-5xl flex-col gap-0 overflow-hidden p-0 sm:max-w-5xl">
      <DialogHeader class="border-b border-white/[0.08] px-6 py-4 text-left light:border-gray-200">
        <DialogTitle>{{ widget ? t('dashboard.builder.titleEdit') : t('dashboard.builder.titleNew') }}</DialogTitle>
        <DialogDescription>{{ t('dashboard.builder.subtitle') }}</DialogDescription>
      </DialogHeader>

      <div class="grid min-h-0 flex-1 md:grid-cols-[minmax(0,1fr)_340px]">
        <!-- The questions -->
        <div class="min-h-0 overflow-y-auto px-6 py-5">
          <!-- A widget from the old builder: rebuild it here, or keep editing it there. -->
          <div
            v-if="legacy"
            class="mb-5 rounded-md border border-amber-500/30 bg-amber-500/[0.07] p-4 text-sm light:border-amber-200 light:bg-amber-50"
          >
            <p class="flex items-start gap-2 text-amber-100 light:text-amber-900">
              <History class="mt-0.5 h-4 w-4 shrink-0" />
              {{ t('dashboard.builder.legacyNotice') }}
            </p>
            <div class="mt-3 flex flex-wrap gap-2">
              <Button size="sm" @click="rebuild">{{ t('dashboard.builder.rebuild') }}</Button>
              <Button size="sm" variant="outline" @click="widget && emit('editLegacy', widget)">{{ t('dashboard.builder.editOldWay') }}</Button>
            </div>
          </div>

          <template v-if="!legacy">
            <!-- 1. What do you want to see? -->
            <section>
              <h3 class="flex items-center gap-2 text-sm font-semibold text-white light:text-gray-900">
                <span class="flex h-5 w-5 items-center justify-center rounded-full bg-white/[0.08] text-[11px] tabular-nums light:bg-gray-100">1</span>
                {{ t('dashboard.builder.stepWhat') }}
              </h3>

              <!-- Chosen: a summary with a way back. -->
              <div
                v-if="mode"
                class="mt-3 flex items-center gap-3 rounded-md border border-white/[0.1] bg-white/[0.03] p-3 light:border-gray-200 light:bg-gray-50"
              >
                <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-white/[0.06] text-white/70 light:bg-white light:text-gray-600">
                  <component :is="mode === 'links' ? AREA_ICONS.links : areaIcon(measure?.area || '')" class="h-4 w-4" />
                </span>
                <div class="min-w-0 flex-1">
                  <p class="truncate text-sm font-medium text-white light:text-gray-900">
                    {{ mode === 'links' ? t('dashboard.builder.linksName') : measureName(measureKey) }}
                  </p>
                  <p class="truncate text-xs text-muted-foreground">
                    {{ mode === 'links' ? t('dashboard.builder.linksDescription') : measureDescription(measureKey) }}
                  </p>
                </div>
                <Button variant="ghost" size="sm" @click="changeWhat">{{ t('dashboard.builder.change') }}</Button>
              </div>

              <!-- Choosing: find it by area or by name. -->
              <template v-else>
                <div class="relative mt-3">
                  <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                  <Input v-model="search" class="pl-9" :placeholder="t('dashboard.builder.searchPlaceholder')" :aria-label="t('dashboard.builder.searchPlaceholder')" />
                </div>
                <div class="mt-3 flex flex-wrap gap-1.5" role="tablist" :aria-label="t('dashboard.builder.areas')">
                  <button
                    v-for="a in (['all', ...AREAS] as const)"
                    :key="a"
                    type="button"
                    role="tab"
                    :aria-selected="area === a"
                    :class="[
                      'rounded-full border px-3 py-1 text-xs font-medium transition-colors',
                      area === a
                        ? 'border-emerald-500/50 bg-emerald-500/15 text-emerald-200 light:border-emerald-300 light:bg-emerald-50 light:text-emerald-800'
                        : 'border-white/[0.1] text-white/60 hover:border-white/20 hover:text-white light:border-gray-200 light:text-gray-600 light:hover:text-gray-900'
                    ]"
                    @click="area = a"
                  >
                    {{ a === 'all' ? t('dashboard.builder.allAreas') : t(`dashboard.areas.${a}`) }}
                    <span v-if="a !== 'all'" class="ml-0.5 tabular-nums opacity-60">{{ areaCount(a) }}</span>
                  </button>
                </div>

                <div v-if="catalogFailed" class="mt-6 flex flex-col items-center gap-2 text-center text-sm text-muted-foreground">
                  <AlertTriangle class="h-5 w-5 text-destructive" />
                  {{ t('dashboard.builder.catalogFailed') }}
                  <Button size="sm" variant="outline" @click="loadCatalog">{{ t('common.retry') }}</Button>
                </div>
                <div v-else-if="!catalog.length" class="mt-6 flex justify-center">
                  <Loader2 class="h-5 w-5 animate-spin text-muted-foreground" />
                </div>
                <div v-else class="mt-3 grid gap-2 sm:grid-cols-2">
                  <button
                    v-for="m in visibleMeasures"
                    :key="m.key"
                    type="button"
                    class="group flex items-start gap-3 rounded-md border border-white/[0.08] p-3 text-left transition-colors hover:border-white/20 hover:bg-white/[0.04] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40 light:border-gray-200 light:hover:border-gray-300 light:hover:bg-gray-50"
                    @click="chooseMeasure(m)"
                  >
                    <span class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-white/[0.06] text-white/70 light:bg-gray-100 light:text-gray-600">
                      <component :is="areaIcon(m.area)" class="h-4 w-4" />
                    </span>
                    <span class="min-w-0 flex-1">
                      <span class="block text-sm font-medium text-white light:text-gray-900">{{ measureName(m.key) }}</span>
                      <span class="mt-0.5 block text-xs leading-snug text-muted-foreground">{{ measureDescription(m.key) }}</span>
                      <span class="mt-1.5 inline-flex items-center gap-1 text-[11px] text-white/45 light:text-gray-500">
                        <span :class="['h-1.5 w-1.5 rounded-full', m.snapshot ? 'bg-emerald-400' : 'bg-white/30 light:bg-gray-400']" />
                        {{ m.snapshot ? t('dashboard.builder.rightNow') : m.views.includes('funnel') ? t('dashboard.builder.stepByStep') : t('dashboard.builder.overPeriod') }}
                      </span>
                    </span>
                  </button>
                  <button
                    v-if="showLinksCard"
                    type="button"
                    class="flex items-start gap-3 rounded-md border border-white/[0.08] p-3 text-left transition-colors hover:border-white/20 hover:bg-white/[0.04] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40 light:border-gray-200 light:hover:border-gray-300 light:hover:bg-gray-50"
                    @click="chooseLinks"
                  >
                    <span class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-white/[0.06] text-white/70 light:bg-gray-100 light:text-gray-600">
                      <component :is="AREA_ICONS.links" class="h-4 w-4" />
                    </span>
                    <span class="min-w-0 flex-1">
                      <span class="block text-sm font-medium text-white light:text-gray-900">{{ t('dashboard.builder.linksName') }}</span>
                      <span class="mt-0.5 block text-xs leading-snug text-muted-foreground">{{ t('dashboard.builder.linksDescription') }}</span>
                    </span>
                  </button>
                  <p v-if="!visibleMeasures.length && !showLinksCard" class="col-span-full py-6 text-center text-sm text-muted-foreground">
                    {{ t('dashboard.builder.noMatch') }}
                  </p>
                </div>
              </template>
            </section>

            <!-- 2. How should it look? (links: which pages) -->
            <section v-if="step2Ready" class="mt-7">
              <h3 class="flex items-center gap-2 text-sm font-semibold text-white light:text-gray-900">
                <span class="flex h-5 w-5 items-center justify-center rounded-full bg-white/[0.08] text-[11px] tabular-nums light:bg-gray-100">2</span>
                {{ mode === 'links' ? t('dashboard.builder.pickPages') : t('dashboard.builder.stepHow') }}
              </h3>

              <div v-if="mode === 'links'" class="mt-3 grid gap-1.5 sm:grid-cols-2">
                <button
                  v-for="s in allShortcuts"
                  :key="s.key"
                  type="button"
                  :aria-pressed="shortcuts.includes(s.key)"
                  :class="[
                    'flex items-center gap-2.5 rounded-md border px-3 py-2 text-left text-sm transition-colors',
                    shortcuts.includes(s.key)
                      ? 'border-emerald-500/50 bg-emerald-500/10 text-white light:border-emerald-300 light:bg-emerald-50 light:text-gray-900'
                      : 'border-white/[0.08] text-white/70 hover:border-white/20 light:border-gray-200 light:text-gray-700 light:hover:border-gray-300'
                  ]"
                  @click="toggleShortcut(s.key)"
                >
                  <component :is="s.icon" class="h-4 w-4 shrink-0 opacity-70" />
                  <span class="min-w-0 flex-1 truncate">{{ t(s.name) }}</span>
                  <Check v-if="shortcuts.includes(s.key)" class="h-4 w-4 shrink-0 text-emerald-400 light:text-emerald-600" />
                </button>
              </div>

              <template v-else-if="measure">
                <div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
                  <button
                    v-for="v in measure.views"
                    :key="v"
                    type="button"
                    :aria-pressed="view === v"
                    :class="[
                      'flex flex-col items-start gap-1 rounded-md border p-3 text-left transition-colors',
                      view === v
                        ? 'border-emerald-500/60 bg-emerald-500/10 light:border-emerald-400 light:bg-emerald-50'
                        : 'border-white/[0.08] hover:border-white/20 light:border-gray-200 light:hover:border-gray-300'
                    ]"
                    @click="view = v"
                  >
                    <component :is="VIEW_ICONS[v]" :class="['h-4 w-4', view === v ? 'text-emerald-400 light:text-emerald-600' : 'text-white/60 light:text-gray-500']" />
                    <span class="text-sm font-medium text-white light:text-gray-900">{{ t(`dashboard.views.${v}.name`) }}</span>
                    <span class="text-[11px] leading-snug text-muted-foreground">{{ t(`dashboard.views.${v}.hint`) }}</span>
                  </button>
                </div>

                <div v-if="needsSplit" class="mt-4">
                  <p class="text-xs font-medium text-white/70 light:text-gray-700">
                    {{ view === 'leaderboard' ? t('dashboard.builder.rankBy') : t('dashboard.builder.splitBy') }}
                  </p>
                  <div class="mt-2 flex flex-wrap gap-1.5">
                    <button
                      v-for="d in splitDims"
                      :key="d.key"
                      type="button"
                      :aria-pressed="split === d.key"
                      :class="[
                        'rounded-full border px-3 py-1 text-xs font-medium transition-colors',
                        split === d.key
                          ? 'border-emerald-500/50 bg-emerald-500/15 text-emerald-200 light:border-emerald-300 light:bg-emerald-50 light:text-emerald-800'
                          : 'border-white/[0.1] text-white/60 hover:border-white/20 hover:text-white light:border-gray-200 light:text-gray-600 light:hover:text-gray-900'
                      ]"
                      @click="split = d.key"
                    >{{ dimName(d.key) }}</button>
                  </div>
                </div>
              </template>
            </section>

            <!-- On a phone there is no side panel, so the preview sits here. -->
            <div v-if="mode" class="mt-5 md:hidden">
              <p class="text-xs font-medium uppercase tracking-wide text-white/40 light:text-gray-400">{{ t('dashboard.builder.preview') }}</p>
              <div
                :class="[
                  'mt-2 flex flex-col overflow-hidden rounded-lg border border-white/[0.08] bg-[#0f0f10] p-4 light:border-gray-200 light:bg-white',
                  previewIsTall ? 'h-[280px]' : 'h-[130px]'
                ]"
              >
                <p class="truncate pb-2 text-sm font-medium text-white/60 light:text-gray-500">{{ name || autoName }}</p>
                <div class="min-h-0 flex-1">
                  <div v-if="previewError" class="flex h-full items-center justify-center text-center text-sm text-muted-foreground">
                    {{ t('dashboard.builder.previewFailed') }}
                  </div>
                  <WidgetBody
                    v-else
                    :widget="previewWidget"
                    :data="preview"
                    :loading="mode === 'measure' && (previewLoading || !preview)"
                    :comparison-label="comparisonLabel"
                    :width="12"
                  />
                </div>
              </div>
            </div>

            <!-- 3. Only include… -->
            <section v-if="mode === 'measure' && filterDims.length" class="mt-7">
              <h3 class="flex items-center gap-2 text-sm font-semibold text-white light:text-gray-900">
                <span class="flex h-5 w-5 items-center justify-center rounded-full bg-white/[0.08] text-[11px] tabular-nums light:bg-gray-100">3</span>
                {{ t('dashboard.builder.stepOnly') }}
                <span class="font-normal text-muted-foreground">{{ t('dashboard.builder.optional') }}</span>
              </h3>
              <p v-if="!conditions.length" class="mt-1.5 text-xs text-muted-foreground">{{ t('dashboard.builder.onlyHint') }}</p>

              <div class="mt-3 space-y-2">
                <div v-for="(c, i) in conditions" :key="i" class="flex flex-wrap items-center gap-2 sm:flex-nowrap">
                  <Select :model-value="c.field" @update:model-value="v => onConditionField(i, String(v))">
                    <SelectTrigger class="h-9 w-full sm:w-40" :aria-label="t('dashboard.builder.conditionField')"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="d in filterDims" :key="d.key" :value="d.key">{{ dimName(d.key) }}</SelectItem>
                    </SelectContent>
                  </Select>
                  <Select :model-value="c.operator" @update:model-value="v => c.operator = v as 'equals' | 'not_equals'">
                    <SelectTrigger class="h-9 w-24" :aria-label="t('dashboard.builder.conditionOperator')"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="equals">{{ t('dashboard.builder.is') }}</SelectItem>
                      <SelectItem value="not_equals">{{ t('dashboard.builder.isNot') }}</SelectItem>
                    </SelectContent>
                  </Select>
                  <Select :model-value="c.value" :disabled="!optionsFor(c.field).length" @update:model-value="v => c.value = String(v)">
                    <SelectTrigger class="h-9 min-w-0 flex-1" :aria-label="t('dashboard.builder.conditionValue')">
                      <SelectValue :placeholder="optionsFor(c.field).length ? t('dashboard.builder.pickValue') : t('dashboard.builder.noOptions')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="o in optionsFor(c.field)" :key="o.value" :value="o.value">{{ o.label }}</SelectItem>
                    </SelectContent>
                  </Select>
                  <Button variant="ghost" size="icon" class="h-9 w-9 shrink-0" :aria-label="t('dashboard.builder.removeCondition')" @click="removeCondition(i)">
                    <X class="h-4 w-4" />
                  </Button>
                </div>
              </div>
              <Button variant="outline" size="sm" class="mt-2" @click="addCondition">
                <Plus class="mr-1.5 h-4 w-4" />{{ t('dashboard.builder.addCondition') }}
              </Button>
            </section>

            <!-- Name and sharing -->
            <section v-if="step2Ready" class="mt-7">
              <h3 class="text-sm font-semibold text-white light:text-gray-900">{{ t('dashboard.builder.stepName') }}</h3>
              <Input
                class="mt-2"
                :model-value="name"
                :placeholder="autoName"
                :aria-label="t('dashboard.builder.stepName')"
                @update:model-value="onNameInput"
              />
              <label class="mt-4 flex items-start justify-between gap-4">
                <span>
                  <span class="block text-sm text-white light:text-gray-900">{{ t('dashboard.builder.share') }}</span>
                  <span class="block text-xs text-muted-foreground">{{ isShared ? t('dashboard.builder.shareOn') : t('dashboard.builder.shareOff') }}</span>
                </span>
                <Switch v-model:checked="isShared" />
              </label>
            </section>
          </template>
        </div>

        <!-- The preview -->
        <aside class="hidden min-h-0 flex-col border-l border-white/[0.08] bg-white/[0.02] p-5 md:flex light:border-gray-200 light:bg-gray-50">
          <p class="text-xs font-medium uppercase tracking-wide text-white/40 light:text-gray-400">{{ t('dashboard.builder.preview') }}</p>
          <div
            v-if="mode"
            :class="[
              'mt-3 flex flex-col overflow-hidden rounded-lg border border-white/[0.08] bg-[#0f0f10] p-5 light:border-gray-200 light:bg-white',
              previewIsTall ? 'h-[330px]' : 'h-[150px]'
            ]"
          >
            <p class="truncate pb-2 text-sm font-medium text-white/60 light:text-gray-500">{{ name || autoName }}</p>
            <div class="min-h-0 flex-1">
              <div v-if="previewError" class="flex h-full items-center justify-center text-center text-sm text-muted-foreground">
                {{ t('dashboard.builder.previewFailed') }}
              </div>
              <WidgetBody
                v-else
                :widget="previewWidget"
                :data="preview"
                :loading="mode === 'measure' && (previewLoading || !preview)"
                :comparison-label="comparisonLabel"
                :width="6"
              />
            </div>
          </div>
          <p v-else class="mt-3 text-sm text-muted-foreground">{{ t('dashboard.builder.previewEmpty') }}</p>
          <p v-if="mode === 'measure' && measure?.snapshot" class="mt-3 text-xs leading-relaxed text-muted-foreground">
            {{ t('dashboard.builder.snapshotNote') }}
          </p>
          <p v-else-if="mode === 'measure'" class="mt-3 text-xs leading-relaxed text-muted-foreground">
            {{ t('dashboard.builder.rangeNote') }}
          </p>
        </aside>
      </div>

      <div class="flex items-center justify-between gap-3 border-t border-white/[0.08] px-6 py-3 light:border-gray-200">
        <Button v-if="mode && !widget" variant="ghost" size="sm" @click="changeWhat">
          <ArrowLeft class="mr-1.5 h-4 w-4" />{{ t('dashboard.builder.back') }}
        </Button>
        <span v-else />
        <div class="flex items-center gap-2">
          <Button variant="outline" size="sm" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button v-if="!legacy" size="sm" :disabled="!canSave || saving" @click="save">
            <Loader2 v-if="saving" class="mr-1.5 h-4 w-4 animate-spin" />
            {{ widget ? t('dashboard.builder.save') : t('dashboard.builder.add') }}
          </Button>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
