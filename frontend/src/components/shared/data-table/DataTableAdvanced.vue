<script setup lang="ts" generic="T extends Record<string, any>">
/**
 * The table for lists that get wide.
 *
 * The shared DataTable renders a fixed set of columns and sorts them server
 * side, which is the right amount of table for a settings list of six rows. A
 * contact list is a different problem: it grows a column every time somebody
 * adds a custom field, so it runs past the edge of the screen, and the column
 * that matters — whose row is this — is the first one to scroll away.
 *
 * So this one adds the four things that problem needs, and nothing else:
 *
 *   - the identifying column stays pinned while the rest scrolls sideways
 *   - columns resize, because an email and a lifecycle stage do not want the
 *     same width
 *   - columns hide, because nobody needs eleven custom fields at once
 *   - the header sticks, because the list is long
 *
 * It is deliberately the same table to look at as the shared one — same row
 * rhythm, same header weight, same borders. What differs is what it can do, not
 * what it is. Sorting and pagination stay the parent's job, as they are there:
 * the table asks, the server answers, and a page of twenty rows never pretends
 * to be sorted when only those twenty were re-ordered.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useTable, FlexRender } from '@tanstack/vue-table'
import {
  coreFeatures,
  columnVisibilityFeature,
  columnSizingFeature,
  columnResizingFeature,
  columnPinningFeature,
  type ColumnVisibilityState,
  type ColumnSizingState
} from '@tanstack/table-core'
import { Skeleton } from '@/components/ui/skeleton'
import { ArrowUp, ArrowDown, ChevronsUpDown } from 'lucide-vue-next'
import PaginationControls from '../PaginationControls.vue'
import DataTableViewOptions from './DataTableViewOptions.vue'
import type { Component } from 'vue'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  rows: T[]
  columns: any[]
  isLoading?: boolean

  /** Persists column visibility, widths and density under this key. */
  storageKey?: string

  /** The column that stays put while the rest scrolls. */
  pinnedColumnId?: string

  /** Sorting is the server's; these mirror its current answer. */
  sortKey?: string
  sortDirection?: 'asc' | 'desc'

  currentPage?: number
  totalItems?: number
  pageSize?: number
  itemName?: string

  emptyIcon?: Component
  emptyTitle?: string
  emptyDescription?: string
  error?: string | null
}>(), {
  isLoading: false,
  currentPage: 1,
  totalItems: 0,
  pageSize: 20,
  sortDirection: 'desc'
})

const emit = defineEmits<{
  'update:sortKey': [key: string]
  'update:sortDirection': [direction: 'asc' | 'desc']
  'page-change': [page: number]
  'row-click': [row: T]
}>()

// ── What the viewer has chosen, kept for next time ──────────────────────────
//
// Per browser, not per account: a column you hid on your laptop is a choice
// about this screen, and localStorage is the honest place for it. Every read is
// guarded — a private window throws rather than returning null.
type Density = 'comfortable' | 'compact'

function readStored<V>(suffix: string, fallback: V): V {
  if (!props.storageKey) return fallback
  try {
    const raw = localStorage.getItem(`${props.storageKey}:${suffix}`)
    return raw ? (JSON.parse(raw) as V) : fallback
  } catch {
    return fallback
  }
}

function writeStored(suffix: string, value: unknown) {
  if (!props.storageKey) return
  try {
    localStorage.setItem(`${props.storageKey}:${suffix}`, JSON.stringify(value))
  } catch {
    // A viewer with site data blocked still gets a working table.
  }
}

const columnVisibility = ref<ColumnVisibilityState>(readStored('columns', {}))
const columnSizing = ref<ColumnSizingState>(readStored('sizing', {}))
const density = ref<Density>(readStored('density', 'comfortable'))

watch(columnVisibility, value => writeStored('columns', value), { deep: true })
watch(columnSizing, value => writeStored('sizing', value), { deep: true })
watch(density, value => writeStored('density', value))

const table = useTable({
  // Only the features this table uses, so the rest is never shipped.
  features: {
    ...coreFeatures,
    columnVisibilityFeature,
    columnSizingFeature,
    columnResizingFeature,
    columnPinningFeature
  },
  get data() {
    return props.rows
  },
  get columns() {
    return props.columns
  },
  state: {
    get columnVisibility() {
      return columnVisibility.value
    },
    get columnSizing() {
      return columnSizing.value
    },
    get columnPinning() {
      return props.pinnedColumnId
        ? { start: [props.pinnedColumnId], end: [] }
        : { start: [], end: [] }
    }
  },
  onColumnVisibilityChange: updater => {
    columnVisibility.value =
      typeof updater === 'function' ? updater(columnVisibility.value) : updater
  },
  onColumnSizingChange: updater => {
    columnSizing.value = typeof updater === 'function' ? updater(columnSizing.value) : updater
  },
  columnResizeMode: 'onChange',
  defaultColumn: { minSize: 90, size: 180, maxSize: 640 }
})

// ── Sorting: ask, do not do ─────────────────────────────────────────────────
function sortStateFor(columnId: string, sortKey?: string) {
  const key = sortKey ?? columnId
  return props.sortKey === key ? props.sortDirection : null
}

function toggleSort(columnId: string, sortKey?: string) {
  const key = sortKey ?? columnId
  if (props.sortKey === key) {
    emit('update:sortDirection', props.sortDirection === 'asc' ? 'desc' : 'asc')
    return
  }
  emit('update:sortKey', key)
  emit('update:sortDirection', 'asc')
}

// ── The view-options menu's picture of the columns ─────────────────────────
const viewColumns = computed(() =>
  table.getAllLeafColumns().map(column => ({
    id: column.id,
    label: String((column.columnDef.meta as any)?.label ?? column.id),
    canHide: column.getCanHide() && column.id !== props.pinnedColumnId,
    visible: column.getIsVisible()
  }))
)

function toggleColumn(id: string, visible: boolean) {
  table.getColumn(id)?.toggleVisibility(visible)
}

function resetView() {
  columnVisibility.value = {}
  columnSizing.value = {}
  density.value = 'comfortable'
}

const rowPadding = computed(() => (density.value === 'compact' ? 'py-1.5' : 'py-3'))

/**
 * Where a pinned column sits, and the edge that says it is pinned.
 *
 * The shadow only appears once the table has actually been scrolled sideways —
 * a permanent seam down a table that fits on screen is just a line.
 */
const isScrolledX = ref(false)
function onScroll(event: Event) {
  isScrolledX.value = (event.target as HTMLElement).scrollLeft > 0
}

function pinnedStyle(columnId: string) {
  if (columnId !== props.pinnedColumnId) return undefined
  return { position: 'sticky' as const, left: '0px', zIndex: 2 }
}
</script>

<template>
  <div class="space-y-3">
    <!-- Whatever the page puts beside the view options: a search box, filters. -->
    <div class="flex flex-wrap items-center gap-2">
      <div class="min-w-0 flex-1"><slot name="toolbar" /></div>
      <DataTableViewOptions
        :columns="viewColumns"
        :density="density"
        @toggle-column="toggleColumn"
        @update:density="value => (density = value)"
        @reset="resetView"
      />
    </div>

    <!-- The surface is solid, because the pinned column paints over whatever
         scrolls beneath it and has to be the same colour as the rows around
         it. On a translucent card it read a shade darker, which drew a seam
         down the table that looked like a border nobody asked for. -->
    <div class="overflow-hidden rounded-lg border border-white/[0.08] bg-[#0a0a0b] light:border-gray-200 light:bg-white">
      <div class="overflow-x-auto" @scroll="onScroll">
        <table class="w-full caption-bottom text-sm" :style="{ width: `${table.getTotalSize()}px`, minWidth: '100%' }">
          <thead class="sticky top-0 z-[3] bg-[#0a0a0b] light:bg-white">
            <tr class="border-b border-white/[0.08] light:border-gray-200">
              <th
                v-for="header in table.getHeaderGroups()[0]?.headers ?? []"
                :key="header.id"
                :style="{ width: `${header.getSize()}px`, ...pinnedStyle(header.column.id) }"
                :class="[
                  'group/head relative h-10 px-3 text-left align-middle font-medium text-muted-foreground',
                  header.column.id === pinnedColumnId && 'bg-[#0a0a0b] light:bg-white',
                  header.column.id === pinnedColumnId && isScrolledX && 'after:absolute after:inset-y-0 after:-right-px after:w-px after:bg-white/[0.12] light:after:bg-gray-200'
                ]"
              >
                <button
                  v-if="(header.column.columnDef.meta as any)?.sortKey"
                  type="button"
                  class="-mx-1 inline-flex items-center gap-1 rounded px-1 py-0.5 hover:text-foreground"
                  @click="toggleSort(header.column.id, (header.column.columnDef.meta as any)?.sortKey)"
                >
                  <span class="truncate">{{ (header.column.columnDef.meta as any)?.label }}</span>
                  <ArrowUp v-if="sortStateFor(header.column.id, (header.column.columnDef.meta as any)?.sortKey) === 'asc'" class="h-3.5 w-3.5 shrink-0 text-foreground" />
                  <ArrowDown v-else-if="sortStateFor(header.column.id, (header.column.columnDef.meta as any)?.sortKey) === 'desc'" class="h-3.5 w-3.5 shrink-0 text-foreground" />
                  <!-- Faint, not absent. A hover-only affordance tells a
                       touch device nothing, and "which columns can I sort by"
                       is the question this icon exists to answer. -->
                  <ChevronsUpDown v-else class="h-3.5 w-3.5 shrink-0 opacity-25 transition-opacity group-hover/head:opacity-60" />
                </button>
                <span v-else class="truncate">{{ (header.column.columnDef.meta as any)?.label }}</span>

                <!-- The resize handle. Invisible until the pointer is near it,
                     because a column edge that is always drawn reads as a
                     border and this table already has those. -->
                <span
                  v-if="header.column.getCanResize()"
                  role="separator"
                  :aria-label="t('dataTable.resizeColumn', { column: (header.column.columnDef.meta as any)?.label })"
                  class="absolute right-0 top-0 h-full w-1.5 cursor-col-resize touch-none select-none opacity-0 transition-opacity after:absolute after:inset-y-2 after:right-0 after:w-px after:bg-primary group-hover/head:opacity-100"
                  :class="header.column.getIsResizing() && 'opacity-100'"
                  @pointerdown="header.getResizeHandler()($event)"
                  @dblclick="header.column.resetSize()"
                />
              </th>
            </tr>
          </thead>

          <tbody>
            <!-- Loading: rows in the shape of the answer, at the width the
                 columns actually are. -->
            <template v-if="isLoading">
              <tr v-for="i in 8" :key="`skeleton-${i}`" class="border-b border-white/[0.06] light:border-gray-100">
                <td
                  v-for="header in table.getHeaderGroups()[0]?.headers ?? []"
                  :key="header.id"
                  :class="['px-3', rowPadding]"
                  :style="pinnedStyle(header.column.id)"
                >
                  <Skeleton class="h-4" :style="{ width: `${Math.min(70, 30 + ((i * 13) % 45))}%` }" />
                </td>
              </tr>
            </template>

            <tr v-else-if="error">
              <td :colspan="table.getVisibleLeafColumns().length" class="px-3 py-16 text-center">
                <p class="text-sm text-destructive">{{ error }}</p>
              </td>
            </tr>

            <tr v-else-if="!rows.length">
              <td :colspan="table.getVisibleLeafColumns().length" class="px-3 py-16 text-center">
                <component :is="emptyIcon" v-if="emptyIcon" class="mx-auto mb-3 h-8 w-8 text-white/20 light:text-gray-300" aria-hidden="true" />
                <p class="font-medium">{{ emptyTitle }}</p>
                <p v-if="emptyDescription" class="mt-1 text-sm text-muted-foreground">{{ emptyDescription }}</p>
              </td>
            </tr>

            <tr
              v-for="row in table.getRowModel().rows"
              v-else
              :key="row.id"
              class="group/row cursor-pointer border-b border-white/[0.06] transition-colors duration-150 last:border-0 hover:bg-white/[0.03] light:border-gray-100 light:hover:bg-gray-50"
              @click="emit('row-click', row.original as T)"
            >
              <td
                v-for="cell in row.getVisibleCells()"
                :key="cell.id"
                :style="{ width: `${cell.column.getSize()}px`, ...pinnedStyle(cell.column.id) }"
                :class="[
                  'px-3 align-middle',
                  rowPadding,
                  cell.column.id === pinnedColumnId && 'bg-[#0a0a0b] group-hover/row:bg-[#101012] light:bg-white light:group-hover/row:bg-gray-50',
                  cell.column.id === pinnedColumnId && isScrolledX && 'after:absolute after:inset-y-0 after:-right-px after:w-px after:bg-white/[0.12] light:after:bg-gray-200'
                ]"
              >
                <slot :name="`cell-${cell.column.id}`" :row="row.original as T" :value="cell.getValue()">
                  <FlexRender :render="cell.column.columnDef.cell" :props="cell.getContext()" />
                </slot>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <PaginationControls
        v-if="totalItems > 0"
        :current-page="currentPage"
        :total-pages="Math.max(1, Math.ceil(totalItems / pageSize))"
        :total-items="totalItems"
        :page-size="pageSize"
        :item-name="itemName"
        class="border-t border-white/[0.08] px-3 py-2 light:border-gray-200"
        @update:current-page="page => emit('page-change', page)"
      />
    </div>
  </div>
</template>
