<script setup lang="ts">
/**
 * Which columns are on screen, and how tightly they are packed.
 *
 * A contact list grows a column every time somebody adds a custom field, and
 * nobody needs all of them at once. Hiding is per person and remembered, so the
 * table you left is the table you come back to.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from '@/components/ui/dropdown-menu'
import { SlidersHorizontal, RotateCcw, Check } from 'lucide-vue-next'

const { t } = useI18n()

const props = defineProps<{
  /** Columns the viewer may show or hide, in the order they appear. */
  columns: Array<{ id: string; label: string; canHide: boolean; visible: boolean }>
  density: 'comfortable' | 'compact'
}>()

const emit = defineEmits<{
  'toggle-column': [id: string, visible: boolean]
  'update:density': ['comfortable' | 'compact']
  reset: []
}>()

const hiddenCount = computed(() => props.columns.filter(c => c.canHide && !c.visible).length)
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="outline" size="sm" class="gap-2">
        <SlidersHorizontal class="h-4 w-4" />
        <span class="max-sm:sr-only">{{ t('dataTable.view') }}</span>
        <span
          v-if="hiddenCount"
          class="flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-white/[0.08] px-1 text-[11px] font-semibold tabular-nums light:bg-gray-100"
        >{{ hiddenCount }}</span>
      </Button>
    </DropdownMenuTrigger>

    <DropdownMenuContent align="end" class="w-56">
      <DropdownMenuLabel>{{ t('dataTable.density') }}</DropdownMenuLabel>
      <DropdownMenuItem
        v-for="option in (['comfortable', 'compact'] as const)"
        :key="option"
        :aria-checked="density === option"
        role="menuitemradio"
        @select="emit('update:density', option)"
      >
        <Check :class="['mr-2 h-3.5 w-3.5', density === option ? 'opacity-100' : 'opacity-0']" />
        {{ t(`dataTable.${option}`) }}
      </DropdownMenuItem>

      <DropdownMenuSeparator />
      <DropdownMenuLabel>{{ t('dataTable.columns') }}</DropdownMenuLabel>
      <!-- @select.prevent keeps the menu open: hiding four columns in a row
           should not mean opening this menu four times. -->
      <DropdownMenuItem
        v-for="column in columns.filter(c => c.canHide)"
        :key="column.id"
        role="menuitemcheckbox"
        :aria-checked="column.visible"
        @select.prevent="emit('toggle-column', column.id, !column.visible)"
      >
        <Check :class="['mr-2 h-3.5 w-3.5 shrink-0', column.visible ? 'opacity-100' : 'opacity-0']" />
        <span class="truncate">{{ column.label }}</span>
      </DropdownMenuItem>

      <DropdownMenuSeparator />
      <DropdownMenuItem @click="emit('reset')">
        <RotateCcw class="mr-2 h-3.5 w-3.5" />
        {{ t('dataTable.reset') }}
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
