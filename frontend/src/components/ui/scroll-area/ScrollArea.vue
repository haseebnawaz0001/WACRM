<script setup lang="ts">
import {
  ScrollAreaRoot,
  ScrollAreaViewport,
  ScrollAreaScrollbar,
  ScrollAreaThumb,
  ScrollAreaCorner
} from 'reka-ui'
import { cn } from '@/lib/utils'

/**
 * A scroll area, vertical unless asked otherwise.
 *
 * It used to render both scrollbars whenever `orientation` was left out, which
 * was fifty-four of the fifty-six places it is used. Enabling the horizontal
 * one makes reka-ui put `min-width: fit-content` on the viewport's content, and
 * that is a floor the page cannot go below: on a phone the contacts table's
 * 678px of columns became the width of the whole page, so the header ran off
 * the right edge, the saved views were cut in half, and reading a column meant
 * scrolling the entire app sideways.
 *
 * A page body scrolls down. Anything that is genuinely wider than its container
 * — a board, a wide table — says so with `orientation="horizontal"` or
 * `"both"`, and handles its own overflow.
 */
const props = withDefaults(
  defineProps<{
    class?: string
    orientation?: 'vertical' | 'horizontal' | 'both'
  }>(),
  { orientation: 'vertical' }
)
</script>

<template>
  <ScrollAreaRoot :class="cn('relative overflow-hidden', props.class)">
    <ScrollAreaViewport class="h-full w-full rounded-[inherit]">
      <slot />
    </ScrollAreaViewport>
    <ScrollAreaScrollbar
      v-if="props.orientation !== 'horizontal'"
      orientation="vertical"
      class="flex touch-none select-none transition-colors h-full w-2.5 border-l border-l-transparent p-[1px]"
    >
      <ScrollAreaThumb class="relative flex-1 rounded-full bg-border" />
    </ScrollAreaScrollbar>
    <ScrollAreaScrollbar
      v-if="props.orientation !== 'vertical'"
      orientation="horizontal"
      class="flex touch-none select-none transition-colors flex-col h-2.5 border-t border-t-transparent p-[1px]"
    >
      <ScrollAreaThumb class="relative flex-1 rounded-full bg-border" />
    </ScrollAreaScrollbar>
    <ScrollAreaCorner />
  </ScrollAreaRoot>
</template>
