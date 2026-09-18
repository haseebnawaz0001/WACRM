<script setup lang="ts">
/**
 * Every dropdown menu in the product.
 *
 * This wrapper used to declare its own `open` prop and bind it unconditionally:
 *
 *   <DropdownMenuRoot :open="open" @update:open="emit('update:open', $event)">
 *
 * Binding `open` is what tells reka-ui the menu is controlled, so it stopped
 * keeping its own state and emitted every change upward instead. Callers write
 * plain `<DropdownMenu>` with nothing listening, so the event went nowhere and
 * the menu never opened — the trigger reported `data-state="closed"` however it
 * was activated, by mouse, by keyboard, or forced. Every dropdown in the app
 * was dead, and silently: no error, just a button that did nothing.
 *
 * Forwarding props and emits is what the other primitives here already do
 * (Popover, Collapsible, Accordion). It keeps the uncontrolled case working and
 * still lets a caller drive `v-model:open` when it wants to.
 */
import type { DropdownMenuRootEmits, DropdownMenuRootProps } from 'reka-ui'
import { DropdownMenuRoot, useForwardPropsEmits } from 'reka-ui'

const props = defineProps<DropdownMenuRootProps>()
const emits = defineEmits<DropdownMenuRootEmits>()

const forwarded = useForwardPropsEmits(props, emits)
</script>

<template>
  <DropdownMenuRoot v-bind="forwarded">
    <slot />
  </DropdownMenuRoot>
</template>
