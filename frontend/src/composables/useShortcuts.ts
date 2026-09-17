/**
 * One keyboard shortcut registry for the whole app (plan 10, S12).
 *
 * Each plan specified its own keys — 03 wanted j/k/e/s/p, 04 wanted n/x/r, 07
 * wanted Ctrl+←/→ — and nothing reconciled them. Registered centrally, a
 * collision is visible the moment it is introduced rather than the moment an
 * agent presses a key and the wrong view responds.
 *
 * The registry also owns the two rules every per-view handler kept
 * re-implementing and occasionally getting wrong: a shortcut must not fire
 * while somebody is typing, and the help sheet has to know what exists.
 */
import { onBeforeUnmount, onMounted, reactive, readonly } from 'vue'

export interface Shortcut {
  /** Lowercase key, as reported by KeyboardEvent.key: 'n', 'k', 'arrowleft'. */
  key: string
  /** Modifier. 'mod' means Ctrl on Windows/Linux and Cmd on Mac. */
  mod?: boolean
  shift?: boolean
  /** Short description for the help sheet. */
  description: string
  /** Section heading in the help sheet. */
  group: string
  handler: (event: KeyboardEvent) => void
  /**
   * Fire even while a text field has focus. Off by default: a shortcut that
   * steals a keystroke mid-sentence is worse than no shortcut. Only
   * modifier-bearing shortcuts should ever set this.
   */
  whileTyping?: boolean
}

/** Registered shortcuts, keyed by their normalised combination. */
const registry = reactive(new Map<string, Shortcut>())

function comboOf(s: Pick<Shortcut, 'key' | 'mod' | 'shift'>): string {
  return [s.mod ? 'mod' : '', s.shift ? 'shift' : '', s.key.toLowerCase()]
    .filter(Boolean)
    .join('+')
}

function comboOfEvent(event: KeyboardEvent): string {
  return [
    event.metaKey || event.ctrlKey ? 'mod' : '',
    event.shiftKey ? 'shift' : '',
    event.key.toLowerCase()
  ]
    .filter(Boolean)
    .join('+')
}

/** True while focus is somewhere a keystroke means text, not a command. */
function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  const tag = el.tagName
  return (
    tag === 'INPUT' ||
    tag === 'TEXTAREA' ||
    tag === 'SELECT' ||
    el.isContentEditable === true
  )
}

let listening = false

function onKeydown(event: KeyboardEvent) {
  const shortcut = registry.get(comboOfEvent(event))
  if (!shortcut) return
  if (isTyping(event.target) && !shortcut.whileTyping) return

  event.preventDefault()
  shortcut.handler(event)
}

function startListening() {
  if (listening) return
  listening = true
  window.addEventListener('keydown', onKeydown)
}

/**
 * Register shortcuts for as long as the calling component is mounted.
 *
 * Registering a combination that is already taken logs and skips rather than
 * silently replacing: two views fighting over one key is a bug to fix, not a
 * race to win.
 */
export function useShortcuts(shortcuts: Shortcut[]) {
  const registered: string[] = []

  onMounted(() => {
    startListening()
    for (const shortcut of shortcuts) {
      const combo = comboOf(shortcut)
      if (registry.has(combo)) {
        console.warn(`[shortcuts] "${combo}" is already registered; ignoring the second claim`)
        continue
      }
      registry.set(combo, shortcut)
      registered.push(combo)
    }
  })

  onBeforeUnmount(() => {
    registered.forEach(combo => registry.delete(combo))
    registered.length = 0
  })
}

/** Everything currently registered, grouped for the help sheet. */
export function useShortcutHelp() {
  return readonly(registry)
}

/** Human-readable form of a combination, for the help sheet. */
export function formatShortcut(shortcut: Pick<Shortcut, 'key' | 'mod' | 'shift'>): string {
  const isMac = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform)
  const parts: string[] = []
  if (shortcut.mod) parts.push(isMac ? '⌘' : 'Ctrl')
  if (shortcut.shift) parts.push('⇧')

  const key = shortcut.key.toLowerCase()
  const pretty: Record<string, string> = {
    arrowleft: '←',
    arrowright: '→',
    arrowup: '↑',
    arrowdown: '↓',
    escape: 'Esc',
    enter: '↵'
  }
  parts.push(pretty[key] ?? key.toUpperCase())
  return parts.join(' ')
}
