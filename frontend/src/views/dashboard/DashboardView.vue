<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { GridLayout, GridItem } from 'grid-layout-plus'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from '@/components/ui/alert-dialog'
import { widgetsService, type DashboardWidget, type WidgetData, type LayoutItem } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { Plus, GripVertical, LayoutDashboard, AlertTriangle } from 'lucide-vue-next'
import { DateRangePicker, PageHeader } from '@/components/shared'
import WidgetCard from '@/components/dashboard/WidgetCard.vue'
import WidgetBuilder from '@/components/dashboard/WidgetBuilder.vue'
import LegacyWidgetDialog from '@/components/dashboard/LegacyWidgetDialog.vue'
import { useDateRange } from '@/composables/useDateRange'
import { useAppToast } from '@/composables/useAppToast'

const { success, error: showError } = useAppToast()
const { t } = useI18n()
const authStore = useAuthStore()

// Permission checks
const canCreateWidget = computed(() => authStore.hasPermission('analytics', 'write'))
const canEditWidget = computed(() => authStore.hasPermission('analytics', 'write'))
const canDeleteWidget = computed(() => authStore.hasPermission('analytics', 'delete'))

// Widgets state
const widgets = ref<DashboardWidget[]>([])
const widgetData = ref<Record<string, WidgetData>>({})

const isLoading = ref(true)
const loadFailed = ref(false)
const isWidgetDataLoading = ref(false)

// The builder, and the old form for widgets made before it.
const builderOpen = ref(false)
const editingWidget = ref<DashboardWidget | null>(null)
const legacyOpen = ref(false)
const legacyWidget = ref<DashboardWidget | null>(null)

// Delete dialog state
const deleteDialogOpen = ref(false)
const widgetToDelete = ref<DashboardWidget | null>(null)

// Time range filter
const {
  selectedRange,
  customDateRange,
  isDatePickerOpen,
  dateRange,
  formatDateRangeDisplay,
  applyCustomRange: applyCustomRangeBase,
} = useDateRange({ storageKey: 'dashboard' })

const comparisonPeriodLabel = computed(() => {
  switch (selectedRange.value) {
    case 'today':
      return t('dashboard.fromYesterday')
    case '7days':
      return t('dashboard.fromPrevious7Days')
    case '30days':
      return t('dashboard.fromPrevious30Days')
    case 'this_month':
      return t('dashboard.fromLastMonth')
    default:
      return t('dashboard.fromPreviousPeriod')
  }
})

// Grid layout state
const GRID_COLS = 12
const GRID_ROW_HEIGHT = 40
const GRID_MARGIN: [number, number] = [16, 16]

const isDragMode = ref(false)
const gridLayout = ref<Array<{ i: string; x: number; y: number; w: number; h: number }>>([])

/**
 * Below this width the dashboard is one column.
 *
 * The grid held all twelve columns at every size, so on a phone four stat
 * cards sat side by side at 65px each: "Chatbot Session" clipped mid-word, the
 * change line broken into "f… la… m…", every message in the table truncated to
 * "We'r…", and the whole page scrolling sideways. A dashboard read on a phone
 * between other things is exactly when it has to be legible.
 */
// Read at once, so a phone never draws the twelve-column layout first and
// then animates every card into one column.
const isNarrow = ref(typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches)
let narrowQuery: MediaQueryList | null = null
function onNarrowChange(e: MediaQueryListEvent | MediaQueryList) {
  isNarrow.value = e.matches
}

/**
 * The layout as rendered, which is not always the layout as saved.
 *
 * Stacking for a phone is a presentation of the saved arrangement, not an edit
 * of it: the order people put their widgets in is kept, and going back to a
 * desktop finds the columns exactly as they were left.
 */
const displayLayout = computed(() => {
  if (!isNarrow.value) return gridLayout.value
  let y = 0
  return [...gridLayout.value]
    .sort((a, b) => a.y - b.y || a.x - b.x)
    .map(item => {
      const placed = { ...item, x: 0, y, w: 1 }
      y += item.h
      return placed
    })
})

const effectiveCols = computed(() => (isNarrow.value ? 1 : GRID_COLS))

const widgetsById = computed(() => new Map(widgets.value.map(w => [w.id, w])))

const LARGE_TYPES = ['chart', 'table', 'shortcuts', 'leaderboard', 'funnel']

const computeGridLayout = (widgetList: DashboardWidget[]) => {
  const layout: Array<{ i: string; x: number; y: number; w: number; h: number }> = []

  // Separate positioned (grid_w > 0) from legacy (grid_w === 0) widgets
  const positioned = widgetList.filter(w => w.grid_w > 0)
  const legacy = widgetList.filter(w => w.grid_w === 0)

  for (const w of positioned) {
    layout.push({ i: w.id, x: w.grid_x, y: w.grid_y, w: w.grid_w, h: w.grid_h })
  }

  // Auto-position widgets saved before the grid existed.
  if (legacy.length > 0) {
    let curY = positioned.length > 0 ? Math.max(...positioned.map(w => w.grid_y + w.grid_h)) : 0
    let curX = 0

    const legacyNumber = legacy.filter(w => !LARGE_TYPES.includes(w.display_type))
    const legacyLarge = legacy.filter(w => LARGE_TYPES.includes(w.display_type))

    for (const w of legacyNumber) {
      if (curX + 3 > GRID_COLS) {
        curX = 0
        curY += 3
      }
      layout.push({ i: w.id, x: curX, y: curY, w: 3, h: 3 })
      curX += 3
    }

    if (legacyNumber.length > 0 && legacyLarge.length > 0) {
      curX = 0
      curY += 3
    }

    for (const w of legacyLarge) {
      const itemH = w.display_type === 'chart' ? 5 : 8
      if (curX + 6 > GRID_COLS) {
        curX = 0
        curY += itemH
      }
      layout.push({ i: w.id, x: curX, y: curY, w: 6, h: itemH })
      curX += 6
    }
  }

  return layout
}

watch(widgets, (val) => {
  gridLayout.value = computeGridLayout(val)
}, { immediate: true })

// Debounced layout save
let layoutSaveTimer: ReturnType<typeof setTimeout> | null = null

const persistLayout = async () => {
  const layoutItems: LayoutItem[] = gridLayout.value.map(item => ({
    id: item.i,
    grid_x: item.x,
    grid_y: item.y,
    grid_w: item.w,
    grid_h: item.h
  }))
  try {
    await widgetsService.saveLayout(layoutItems)
  } catch (error: any) {
    showError(t('common.error'), error.response?.data?.message || t('dashboard.saveLayoutFailed'))
  }
}

const onLayoutUpdate = (newLayout: Array<{ i: string; x: number; y: number; w: number; h: number }>) => {
  // A stacked phone layout is a view of the saved one, so it never writes back
  // — otherwise opening the dashboard on a phone would flatten it everywhere.
  if (isNarrow.value) return
  gridLayout.value = newLayout
  if (!isDragMode.value) return
  if (layoutSaveTimer) clearTimeout(layoutSaveTimer)
  layoutSaveTimer = setTimeout(persistLayout, 500)
}

watch(isDragMode, (newVal, oldVal) => {
  if (oldVal && !newVal) {
    if (layoutSaveTimer) {
      clearTimeout(layoutSaveTimer)
      layoutSaveTimer = null
    }
    persistLayout()
  }
})

// Fetch data
const fetchWidgets = async () => {
  try {
    const response = await widgetsService.list()
    widgets.value = (response.data as any).data?.widgets || []
  } catch (error) {
    // A failure used to go to the console and nothing else, so a dashboard that
    // could not load looked exactly like a dashboard with nothing on it: a
    // header, and the rest of the screen empty.
    console.error('Failed to load widgets:', error)
    widgets.value = []
    loadFailed.value = true
  }
}

const fetchWidgetData = async () => {
  if (widgets.value.length === 0) return

  isWidgetDataLoading.value = true
  try {
    const { from, to } = dateRange.value
    const response = await widgetsService.getAllData({ from, to })
    widgetData.value = (response.data as any).data?.data || {}
  } catch (error) {
    console.error('Failed to load widget data:', error)
    widgetData.value = {}
  } finally {
    isWidgetDataLoading.value = false
  }
}

const fetchDashboardData = async () => {
  isLoading.value = true
  loadFailed.value = false
  try {
    await fetchWidgets()
    await fetchWidgetData()
  } finally {
    isLoading.value = false
  }
}

const applyCustomRange = () => {
  applyCustomRangeBase()
  fetchWidgetData()
}

// Building and editing
const openAddWidgetDialog = () => {
  editingWidget.value = null
  builderOpen.value = true
}

const openEditWidgetDialog = (widget: DashboardWidget) => {
  editingWidget.value = widget
  builderOpen.value = true
}

function editTheOldWay(widget: DashboardWidget) {
  builderOpen.value = false
  legacyWidget.value = widget
  legacyOpen.value = true
}

async function onWidgetSaved() {
  await fetchWidgets()
  await fetchWidgetData()
}

const openDeleteDialog = (widget: DashboardWidget) => {
  widgetToDelete.value = widget
  deleteDialogOpen.value = true
}

const confirmDeleteWidget = async () => {
  if (!widgetToDelete.value) return

  try {
    await widgetsService.delete(widgetToDelete.value.id)
    success(t('common.deletedSuccess', { resource: t('resources.Widget') }))
    deleteDialogOpen.value = false
    widgetToDelete.value = null
    await fetchWidgets()
    await fetchWidgetData()
  } catch (error: any) {
    showError(t('common.error'), error.response?.data?.message || t('common.failedDelete', { resource: t('resources.widget') }))
  }
}

watch(selectedRange, (newValue) => {
  if (newValue !== 'custom') {
    fetchWidgetData()
  }
})

onMounted(() => {
  narrowQuery = window.matchMedia('(max-width: 767px)')
  onNarrowChange(narrowQuery)
  narrowQuery.addEventListener('change', onNarrowChange)
  fetchDashboardData()
})

onUnmounted(() => {
  narrowQuery?.removeEventListener('change', onNarrowChange)
  narrowQuery = null
})
</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <!-- View controls first, then the page's own actions. -->
    <PageHeader :title="$t('dashboard.title')" :description="$t('dashboard.subtitle')" :icon="LayoutDashboard">
      <template #actions>
        <DateRangePicker
          v-model:selected-range="selectedRange"
          v-model:custom-date-range="customDateRange"
          v-model:is-date-picker-open="isDatePickerOpen"
          :format-date-range-display="formatDateRangeDisplay"
          @apply-custom="applyCustomRange"
        />
        <Button
          v-if="canEditWidget && widgets.length > 1"
          :variant="isDragMode ? 'active' : 'outline'"
          size="sm"
          @click="isDragMode = !isDragMode"
        >
          <GripVertical class="h-4 w-4 mr-2" />
          {{ isDragMode ? $t('common.done') : $t('dashboard.editLayout') }}
        </Button>
        <Button v-if="canCreateWidget" size="sm" @click="openAddWidgetDialog">
          <Plus class="h-4 w-4 mr-2" />
          {{ $t('dashboard.addWidget') }}
        </Button>
      </template>
    </PageHeader>

    <!-- Content -->
    <ScrollArea class="flex-1">
      <div class="p-6 space-y-6 max-md:p-4">
        <!-- Loading Skeleton -->
        <div v-if="isLoading" class="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          <div v-for="i in 4" :key="i" class="rounded-lg border border-white/[0.08] bg-white/[0.02] p-6 light:bg-white light:border-gray-200">
            <div class="flex flex-row items-center justify-between space-y-0 pb-2">
              <Skeleton class="h-4 w-24 bg-white/[0.08] light:bg-gray-200" />
              <Skeleton class="h-10 w-10 rounded-lg bg-white/[0.08] light:bg-gray-200" />
            </div>
            <div class="pt-2">
              <Skeleton class="h-8 w-20 mb-2 bg-white/[0.08] light:bg-gray-200" />
              <Skeleton class="h-3 w-32 bg-white/[0.08] light:bg-gray-200" />
            </div>
          </div>
        </div>

        <!-- Something went wrong, said on the page rather than in the console. -->
        <div v-else-if="loadFailed" class="flex flex-col items-center justify-center py-20 text-center">
          <AlertTriangle class="mb-3 h-8 w-8 text-destructive" aria-hidden="true" />
          <p class="font-medium">{{ $t('dashboard.loadFailed') }}</p>
          <p class="mt-1 text-sm text-muted-foreground">{{ $t('dashboard.loadFailedDesc') }}</p>
          <Button variant="outline" size="sm" class="mt-4" @click="fetchDashboardData">
            {{ $t('common.retry') }}
          </Button>
        </div>

        <!-- Nothing here yet, which for a new organisation is every first visit.
             An empty state that teaches the page beats one that reports it. -->
        <div v-else-if="!displayLayout.length" class="flex flex-col items-center justify-center py-20 text-center">
          <LayoutDashboard class="mb-3 h-8 w-8 text-white/25 light:text-gray-300" aria-hidden="true" />
          <p class="font-medium">{{ $t('dashboard.noWidgets') }}</p>
          <p class="mt-1 text-sm text-muted-foreground">{{ $t('dashboard.noWidgetsDesc') }}</p>
          <Button v-if="canCreateWidget" size="sm" class="mt-4" @click="openAddWidgetDialog">
            <Plus class="mr-2 h-4 w-4" />{{ $t('dashboard.addWidget') }}
          </Button>
        </div>

        <!-- Widget Grid Layout -->
        <GridLayout
          v-if="!isLoading && displayLayout.length > 0"
          :layout="displayLayout"
          :col-num="effectiveCols"
          :row-height="GRID_ROW_HEIGHT"
          :margin="GRID_MARGIN"
          :is-draggable="isDragMode && !isNarrow"
          :is-resizable="isDragMode && !isNarrow"
          :vertical-compact="true"
          :use-css-transforms="true"
          @layout-updated="onLayoutUpdate"
        >
          <GridItem
            v-for="item in displayLayout"
            :key="item.i"
            :i="item.i"
            :x="item.x"
            :y="item.y"
            :w="item.w"
            :h="item.h"
            :min-w="2"
            :min-h="2"
            drag-allow-from=".widget-drag-handle"
          >
            <WidgetCard
              v-if="widgetsById.get(item.i)"
              :widget="widgetsById.get(item.i)!"
              :data="widgetData[item.i]"
              :loading="isWidgetDataLoading"
              :comparison-label="comparisonPeriodLabel"
              :width="isNarrow ? 12 : item.w"
              :drag-mode="isDragMode"
              :can-edit="canEditWidget"
              :can-delete="canDeleteWidget"
              @edit="openEditWidgetDialog(widgetsById.get(item.i)!)"
              @delete="openDeleteDialog(widgetsById.get(item.i)!)"
            />
          </GridItem>
        </GridLayout>
      </div>
    </ScrollArea>

    <WidgetBuilder
      v-model:open="builderOpen"
      :widget="editingWidget"
      :range="dateRange"
      :comparison-label="comparisonPeriodLabel"
      @saved="onWidgetSaved"
      @edit-legacy="editTheOldWay"
    />
    <LegacyWidgetDialog v-model:open="legacyOpen" :widget="legacyWidget" @saved="onWidgetSaved" />

    <!-- Delete Confirmation Dialog -->
    <AlertDialog v-model:open="deleteDialogOpen">
      <AlertDialogContent class="bg-[#141414] border-white/[0.08] light:bg-white light:border-gray-200">
        <AlertDialogHeader>
          <AlertDialogTitle class="text-white light:text-gray-900">{{ $t('dashboard.deleteWidgetTitle') }}</AlertDialogTitle>
          <AlertDialogDescription class="text-white/60 light:text-gray-500">
            {{ $t('dashboard.deleteWidgetConfirm', { name: widgetToDelete?.name }) }}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel class="bg-transparent border-white/[0.1] text-white/70 hover:bg-white/[0.08] light:border-gray-300 light:text-gray-700 light:hover:bg-gray-100">
            {{ $t('common.cancel') }}
          </AlertDialogCancel>
          <AlertDialogAction @click="confirmDeleteWidget" class="bg-red-600 text-white hover:bg-red-700">
            {{ $t('common.delete') }}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>

<style>
/*
 * The grid's own look, through the variables grid-layout-plus reads. These
 * rules used to target .vue-grid-item — the class of a different library — so
 * none of them applied, and dragging a widget in Edit Layout showed the
 * library's default: a red block.
 */
.vgl-layout {
  --vgl-placeholder-bg: rgba(16, 185, 129, 0.12);
  --vgl-placeholder-opacity: 100%;
  --vgl-resizer-size: 16px;
  --vgl-resizer-border-color: rgba(255, 255, 255, 0.25);
}
.light .vgl-layout {
  --vgl-resizer-border-color: rgba(0, 0, 0, 0.25);
}
.vgl-item--placeholder {
  border: 2px dashed rgba(16, 185, 129, 0.5);
  border-radius: var(--radius);
}
.vgl-item__resizer {
  bottom: 4px;
  right: 4px;
}

/* Animated counter transition */
.counter-fade-enter-active,
.counter-fade-leave-active {
  transition: opacity 0.25s ease, transform 0.25s ease;
}
.counter-fade-enter-from {
  opacity: 0;
  transform: translateY(4px);
}
.counter-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
