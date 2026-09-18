<script setup lang="ts" generic="T extends Record<string, any>">
import { computed } from 'vue'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ArrowUpDown, ArrowUp, ArrowDown } from 'lucide-vue-next'
import { Skeleton } from '@/components/ui/skeleton'
import PaginationControls from './PaginationControls.vue'
import type { Component } from 'vue'
import type { Column } from './types'

const props = withDefaults(defineProps<{
  items: T[]
  columns: Column<T>[]
  isLoading?: boolean
  emptyIcon?: Component
  emptyTitle?: string
  emptyDescription?: string
  // Sorting
  sortKey?: string
  sortDirection?: 'asc' | 'desc'
  // Row key - defaults to 'id' but can be customized
  rowKey?: string
  // Server-side pagination (recommended)
  // When enabled, parent handles pagination and passes page/totalItems
  serverPagination?: boolean
  currentPage?: number
  totalItems?: number
  pageSize?: number
  itemName?: string
  // Optional max height for the table area (e.g., 'calc(100vh - 320px)')
  // When set, the table body becomes scrollable while header and pagination stay fixed
  maxHeight?: string

  // --- v2 (plan 10, S12) ---

  /**
   * Sorting is the server's.
   *
   * The table sorted in the browser even while emitting a sort event, so a
   * paginated list re-ordered the twenty rows on screen and called it sorted —
   * the row that should have come first was on page four. With serverSort the
   * table only asks; the parent answers.
   */
  serverSort?: boolean

  /** Rows respond to a click. Emits `row-click`. */
  rowClick?: boolean

  /** Show a checkbox column and a bulk action bar. */
  selectable?: boolean
  /** Selected row keys, for `v-model:selected`. */
  selected?: string[]

  /** Column keys currently hidden, for `v-model:hiddenColumns`. */
  hiddenColumns?: string[]

  /** When set, the table shows an error instead of rows. */
  error?: string | null
}>(), {
  rowKey: 'id',
  serverPagination: false,
  currentPage: 1,
  totalItems: 0,
  pageSize: 10,
  serverSort: false,
  rowClick: false,
  selectable: false,
  selected: () => [],
  hiddenColumns: () => []
})

const emit = defineEmits<{
  'update:sortKey': [key: string]
  'update:sortDirection': [direction: 'asc' | 'desc']
  'sort': [key: string, direction: 'asc' | 'desc']
  'update:currentPage': [page: number]
  'page-change': [page: number]
  'update:selected': [keys: string[]]
  'row-click': [item: T, index: number]
  'retry': []
}>()

defineSlots<{
  [key: `cell-${string}`]: (props: { item: T; index: number }) => any
  empty: () => any
  'empty-action': () => any
  /** Rendered above the table while rows are selected. */
  'bulk-bar': (props: { selected: string[]; clear: () => void }) => any
  error: (props: { message: string }) => any
}>()

const hasSortableColumns = computed(() => props.columns.some(col => col.sortable))

function handleSort(column: Column<T>) {
  if (!column.sortable) return

  const sortKey = column.sortKey || column.key
  let newDirection: 'asc' | 'desc' = 'desc'

  if (props.sortKey === sortKey) {
    newDirection = props.sortDirection === 'asc' ? 'desc' : 'asc'
  }

  emit('update:sortKey', sortKey)
  emit('update:sortDirection', newDirection)
  emit('sort', sortKey, newDirection)
}

// Helper to get nested property value (e.g., 'role.name' -> item.role.name)
function getNestedValue(obj: Record<string, any>, path: string): any {
  return path.split('.').reduce((acc, key) => acc?.[key], obj)
}

// For client-side sorting (when server doesn't handle sorting)
const sortedItems = computed(() => {
  // serverSort means the order on screen is the order the server sent. Sorting
  // again here would re-order one page of a paginated list and call it sorted.
  if (props.serverSort || !props.sortKey || !hasSortableColumns.value) {
    return props.items
  }

  return [...props.items].sort((a, b) => {
    const aVal = getNestedValue(a, props.sortKey!)
    const bVal = getNestedValue(b, props.sortKey!)

    // Handle null/undefined
    if (aVal == null && bVal == null) return 0
    if (aVal == null) return props.sortDirection === 'asc' ? -1 : 1
    if (bVal == null) return props.sortDirection === 'asc' ? 1 : -1

    // Boolean comparison
    if (typeof aVal === 'boolean' && typeof bVal === 'boolean') {
      if (aVal === bVal) return 0
      return props.sortDirection === 'asc' ? (aVal ? 1 : -1) : (aVal ? -1 : 1)
    }

    // String comparison
    if (typeof aVal === 'string' && typeof bVal === 'string') {
      const comparison = aVal.localeCompare(bVal, undefined, { sensitivity: 'base' })
      return props.sortDirection === 'asc' ? comparison : -comparison
    }

    // Numeric comparison
    if (aVal < bVal) return props.sortDirection === 'asc' ? -1 : 1
    if (aVal > bVal) return props.sortDirection === 'asc' ? 1 : -1
    return 0
  })
})

// Pagination computed properties
// Use totalItems from props if server pagination, otherwise use items length
const effectiveTotalItems = computed(() => {
  if (props.serverPagination) {
    // If server returns total, use it; otherwise fallback to items length
    return props.totalItems > 0 ? props.totalItems : sortedItems.value.length
  }
  return sortedItems.value.length
})

const totalPages = computed(() => {
  return Math.ceil(effectiveTotalItems.value / props.pageSize) || 1
})

const needsPagination = computed(() => props.serverPagination && totalPages.value > 1)

// Display items - relies on server to handle pagination when serverPagination is enabled
const displayItems = computed(() => {
  return sortedItems.value
})

function handlePageChange(page: number) {
  emit('update:currentPage', page)
  emit('page-change', page)
}

function getRowKey(item: T, index: number): string {
  return item[props.rowKey] ?? `row-${index}`
}

// Columns the viewer has hidden. Hiding is a display choice, so it never
// changes what the parent fetched.
const visibleColumns = computed(() =>
  props.columns.filter(col => !props.hiddenColumns.includes(col.key))
)

const selectedSet = computed(() => new Set(props.selected))

const allVisibleSelected = computed(() =>
  displayItems.value.length > 0 &&
  displayItems.value.every((item, i) => selectedSet.value.has(getRowKey(item, i)))
)

function toggleRow(item: T, index: number) {
  const key = getRowKey(item, index)
  const next = new Set(props.selected)
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
  }
  emit('update:selected', [...next])
}

/**
 * Select-all covers the rows on screen, not every row matching the filter.
 *
 * A checkbox that silently selected forty thousand contacts because the
 * filter matched them is how a bulk action becomes an incident. A caller that
 * wants "everything matching" asks for it explicitly, in its own bulk bar.
 */
function toggleAllVisible() {
  const next = new Set(props.selected)
  const keys = displayItems.value.map((item, i) => getRowKey(item, i))
  if (allVisibleSelected.value) {
    keys.forEach(k => next.delete(k))
  } else {
    keys.forEach(k => next.add(k))
  }
  emit('update:selected', [...next])
}

function clearSelection() {
  emit('update:selected', [])
}

function handleRowClick(item: T, index: number, event: MouseEvent) {
  if (!props.rowClick) return
  // A click on a control inside the row belongs to that control. Without this
  // every menu button and link would also open the row.
  const target = event.target as HTMLElement | null
  if (target?.closest('button, a, input, [role="menuitem"], [data-no-row-click]')) return
  emit('row-click', item, index)
}

const columnCount = computed(() => visibleColumns.value.length + (props.selectable ? 1 : 0))
</script>

<template>
  <!-- Bulk bar: only while something is selected, so it never takes space it
       has no use for. -->
  <div
    v-if="selectable && selected.length"
    class="flex flex-wrap items-center gap-2 border-b bg-muted/40 px-4 py-2"
  >
    <span class="text-sm font-medium">{{ selected.length }}</span>
    <slot name="bulk-bar" :selected="selected" :clear="clearSelection" />
    <button
      type="button"
      class="ml-auto text-sm text-muted-foreground underline-offset-2 hover:underline"
      @click="clearSelection"
    >
      &times;
    </button>
  </div>

  <div :class="maxHeight ? 'overflow-auto' : ''" :style="maxHeight ? { maxHeight } : {}">
  <Table>
    <TableHeader>
      <TableRow>
        <TableHead v-if="selectable" class="w-10">
          <input
            type="checkbox"
            class="h-4 w-4 cursor-pointer rounded border-input"
            :checked="allVisibleSelected"
            :aria-label="String(selected.length)"
            @change="toggleAllVisible"
          />
        </TableHead>
        <TableHead
          v-for="col in visibleColumns"
          :key="col.key"
          :class="[
            col.width,
            col.align === 'right' && 'text-right',
            col.align === 'center' && 'text-center',
            col.sortable && 'cursor-pointer select-none hover:text-foreground transition-colors',
          ]"
          @click="handleSort(col)"
        >
          <div
            :class="[
              'flex items-center gap-1',
              col.align === 'right' && 'justify-end',
              col.align === 'center' && 'justify-center',
            ]"
          >
            {{ col.label }}
            <template v-if="col.sortable">
              <ArrowUp
                v-if="sortKey === (col.sortKey || col.key) && sortDirection === 'asc'"
                class="h-3 w-3"
              />
              <ArrowDown
                v-else-if="sortKey === (col.sortKey || col.key) && sortDirection === 'desc'"
                class="h-3 w-3"
              />
              <ArrowUpDown v-else class="h-3 w-3 opacity-30" />
            </template>
          </div>
        </TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <!-- Loading State - Skeleton Rows -->
      <template v-if="isLoading">
        <TableRow v-for="row in 5" :key="`skeleton-${row}`">
          <TableCell v-if="selectable" />
          <TableCell v-for="col in visibleColumns" :key="`skeleton-${row}-${col.key}`">
            <Skeleton
              :class="[
                'h-4 skeleton-shimmer',
                col.key === 'actions' ? 'w-16' : row % 3 === 0 ? 'w-3/4' : row % 3 === 1 ? 'w-1/2' : 'w-2/3',
              ]"
            />
          </TableCell>
        </TableRow>
      </template>

      <!-- Error state: a failed load is not an empty list, and telling the
           viewer "no results" when the request failed sends them hunting for a
           filter that is not the problem. -->
      <TableRow v-else-if="error">
        <TableCell :colspan="columnCount" class="h-24 text-center">
          <slot name="error" :message="error">
            <p class="text-sm text-destructive">{{ error }}</p>
            <button
              type="button"
              class="mt-2 text-sm underline underline-offset-2"
              @click="emit('retry')"
            >
              &#8635;
            </button>
          </slot>
        </TableCell>
      </TableRow>

      <!-- Empty State -->
      <TableRow v-else-if="sortedItems.length === 0">
        <TableCell :colspan="columnCount" class="h-24 text-center text-muted-foreground">
          <slot name="empty">
            <div v-if="emptyIcon" class="mb-3 mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-primary/10 ring-1 ring-primary/15">
              <component :is="emptyIcon" class="h-7 w-7 text-primary/60" />
            </div>
            <p v-if="emptyTitle">{{ emptyTitle }}</p>
            <p v-if="emptyDescription" class="text-sm">{{ emptyDescription }}</p>
            <div class="mt-3">
              <slot name="empty-action" />
            </div>
          </slot>
        </TableCell>
      </TableRow>

      <!-- Data Rows -->
      <TableRow
        v-else
        v-for="(item, index) in displayItems"
        :key="getRowKey(item, index)"
        :class="rowClick && 'cursor-pointer hover:bg-accent/40'"
        @click="handleRowClick(item, index, $event)"
      >
        <TableCell v-if="selectable" class="w-10">
          <input
            type="checkbox"
            class="h-4 w-4 cursor-pointer rounded border-input"
            :checked="selectedSet.has(getRowKey(item, index))"
            data-no-row-click
            @change="toggleRow(item, index)"
          />
        </TableCell>
        <TableCell
          v-for="col in visibleColumns"
          :key="col.key"
          :class="[
            col.align === 'right' && 'text-right',
            col.align === 'center' && 'text-center',
          ]"
        >
          <slot :name="`cell-${col.key}`" :item="item" :index="index">
            {{ (item as any)[col.key] }}
          </slot>
        </TableCell>
      </TableRow>
    </TableBody>
  </Table>
  </div>

  <!-- Server-side Pagination -->
  <div v-if="needsPagination && !isLoading" class="border-t px-4 py-3">
    <PaginationControls
      :current-page="currentPage"
      :total-pages="totalPages"
      :total-items="totalItems"
      :page-size="pageSize"
      :item-name="itemName"
      @update:current-page="handlePageChange"
    />
  </div>
</template>
