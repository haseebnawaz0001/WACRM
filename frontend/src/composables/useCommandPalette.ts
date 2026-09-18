import { ref } from 'vue'

/**
 * Shared open state for the command palette.
 *
 * The palette owned its own local ref, so ⌘K was the only way in and the
 * feature was invisible to anyone who had not been told about it. The sidebar
 * now shows a search row that opens the same palette, which means the open
 * state has to live somewhere both can reach.
 *
 * Module-level rather than provide/inject: there is exactly one palette in the
 * app shell, and threading a provider through the layout to say so would be
 * ceremony around a single boolean.
 */
const open = ref(false)

export function useCommandPalette() {
  return {
    open,
    show: () => {
      open.value = true
    },
    toggle: () => {
      open.value = !open.value
    }
  }
}
