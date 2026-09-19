<script setup lang="ts">
/**
 * Text that can mention the contact: "Hi ‹Contact name›, thanks for…".
 *
 * The saved text carries placeholders — {{contact.name}} — but the person
 * never sees one. Each is drawn as a chip wearing its plain name, goes in at
 * the caret from "Insert detail", and is deleted like a single character.
 * The list comes from the backend catalog, the one the engine renders from,
 * so nothing offered here comes out blank.
 *
 * The field is a small contenteditable that only ever holds text, chips and
 * line breaks: typing is left to the browser, while anything that changes the
 * structure (a chip, Enter, a paste, deleting a chip) rewrites the model
 * string and redraws, with the caret kept at the same place in that string.
 * A browser cannot put the caret beside a chip with no text next to it, so a
 * chip at either end of a line, or next to another chip, gets an invisible
 * anchor character that the model never sees.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Braces } from 'lucide-vue-next'
import { useLookups } from '@/components/automations/useLookups'
import { variableLabel } from '@/components/automations/flow/sentences'
import { FIELD_SHELL } from './styles'

const props = withDefaults(defineProps<{
  modelValue?: string | null
  multiline?: boolean
  placeholder?: string
  disabled?: boolean
  ariaLabel?: string
  rows?: number
}>(), { multiline: false, rows: 3 })

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const { t } = useI18n()
const lookups = useLookups()
const editor = ref<HTMLElement | null>(null)
const open = ref(false)
const search = ref('')

interface VariableRow { path: string; label: string; group: string; example?: string }

/** Catalog variables, with "Custom field" expanded into the org's own fields. */
const variables = computed<VariableRow[]>(() => {
  const out: VariableRow[] = []
  for (const v of lookups.state.variables) {
    if (v.dynamic && v.path === 'contact.fields.') {
      for (const field of lookups.state.fields.filter(f => !f.archived_at)) {
        out.push({ path: `contact.fields.${field.key}`, label: field.label, group: v.group })
      }
      continue
    }
    if (v.dynamic) continue
    out.push({ path: v.path, label: v.label, group: v.group, example: v.example })
  }
  return out
})

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  const list = q ? variables.value.filter(v => v.label.toLowerCase().includes(q) || v.path.includes(q)) : variables.value
  const groups: Record<string, VariableRow[]> = {}
  for (const v of list) (groups[v.group] ||= []).push(v)
  return groups
})

// --- The model string and the drawn field ---

const PLACEHOLDER = /\{\{\s*([\w.]+)\s*\}\}/g

/** The placeholder as saved; built here because braces in a template are syntax. */
function token(path: string): string {
  return `${'{'}${'{'}${path}${'}'}${'}'}`
}

/** The caret anchor beside a chip; stripped from everything read back. */
const ANCHOR = '\u200b'
const unanchored = (text: string) => text.split(ANCHOR).join('')

/** The last value this field sent up, so its own echo is not redrawn. */
let sent: string | null = null
/** Where the caret was, in model offsets, for the picker to insert at. */
let caret = { start: -1, end: -1 }

const value = () => props.modelValue ?? ''
const isEmpty = computed(() => !value())

function chip(path: string): HTMLElement {
  const span = document.createElement('span')
  span.className = 'vt-chip'
  span.contentEditable = 'false'
  span.dataset.var = path
  span.textContent = variableLabel(path, lookups)
  return span
}

function draw(text: string) {
  const root = editor.value
  if (!root) return
  const out = document.createDocumentFragment()
  text.split('\n').forEach((line, i) => {
    if (i) out.appendChild(document.createElement('br'))
    let from = 0
    for (const match of line.matchAll(PLACEHOLDER)) {
      const at = match.index ?? 0
      // Nothing between this chip and the line's start or the last chip.
      out.appendChild(document.createTextNode(at > from ? line.slice(from, at) : ANCHOR))
      out.appendChild(chip(match[1]))
      from = at + match[0].length
    }
    if (from < line.length) out.appendChild(document.createTextNode(line.slice(from)))
    else if (from > 0) out.appendChild(document.createTextNode(ANCHOR))
  })
  // A line break at the very end only shows with something after it.
  if (text.endsWith('\n')) out.appendChild(document.createElement('br'))
  root.replaceChildren(out)
}

/** How long a node is in the model string. */
function lengthOf(node: Node): number {
  if (node.nodeType === Node.TEXT_NODE) return unanchored(node.textContent || '').length
  const el = node as HTMLElement
  if (el.dataset?.var) return token(el.dataset.var).length
  if (el.nodeName === 'BR') return 1
  let n = 0
  node.childNodes.forEach(child => { n += lengthOf(child) })
  return n
}

function read(): string {
  const root = editor.value
  if (!root) return value()
  let out = ''
  const walk = (node: Node) => {
    node.childNodes.forEach(child => {
      if (child.nodeType === Node.TEXT_NODE) out += unanchored(child.textContent || '')
      else if ((child as HTMLElement).dataset?.var) out += token((child as HTMLElement).dataset.var!)
      else if (child.nodeName === 'BR') out += '\n'
      else {
        // A browser sometimes wraps a new line in a block of its own.
        if (/^(DIV|P)$/.test(child.nodeName) && out && !out.endsWith('\n')) out += '\n'
        walk(child)
      }
    })
  }
  walk(root)
  if (root.lastChild?.nodeName === 'BR' && out.endsWith('\n')) out = out.slice(0, -1)
  return props.multiline ? out : out.replace(/\n/g, ' ')
}

/** Anything other than text, chips and line breaks directly in the field. */
function untidy(): boolean {
  return Array.from(editor.value?.childNodes || []).some(n =>
    n.nodeType !== Node.TEXT_NODE && n.nodeName !== 'BR' && !(n as HTMLElement).dataset?.var)
}

/** A DOM position as an offset into the model string. */
function offsetOf(container: Node, offset: number): number {
  const root = editor.value!
  let count = 0
  let found = -1
  const walk = (node: Node): boolean => {
    for (let i = 0; i < node.childNodes.length; i++) {
      if (node === container && i === offset) { found = count; return true }
      const child = node.childNodes[i]
      if (child === container && child.nodeType === Node.TEXT_NODE) {
        found = count + unanchored((child.textContent || '').slice(0, offset)).length
        return true
      }
      if ((child as HTMLElement).dataset?.var && child.contains(container)) {
        found = count + lengthOf(child)
        return true
      }
      if (child.nodeType === Node.ELEMENT_NODE && child.nodeName !== 'BR' && !(child as HTMLElement).dataset?.var) {
        if (walk(child)) return true
      } else {
        count += lengthOf(child)
      }
    }
    if (node === container) { found = count; return true }
    return false
  }
  walk(root)
  // The model can be a tick behind the field while an edit is on its way up,
  // so the field's own text is what bounds an offset.
  return Math.min(found < 0 ? count : found, read().length)
}

function selection(): { start: number; end: number } | null {
  const sel = window.getSelection()
  const root = editor.value
  if (!sel?.rangeCount || !root) return null
  const range = sel.getRangeAt(0)
  if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) return null
  const a = offsetOf(range.startContainer, range.startOffset)
  const b = offsetOf(range.endContainer, range.endOffset)
  return { start: Math.min(a, b), end: Math.max(a, b) }
}

/** Puts the caret at an offset into `text`, the field's content. The field is flat after a draw. */
function place(at: number, text = read()) {
  const root = editor.value
  const sel = window.getSelection()
  if (!root || !sel) return
  const range = document.createRange()
  let left = at
  let placed = false
  const nodes = Array.from(root.childNodes)
  for (let i = 0; i < nodes.length && !placed; i++) {
    const node = nodes[i]
    const len = lengthOf(node)
    if (node.nodeType === Node.TEXT_NODE && left <= len) {
      range.setStart(node, domOffset(node.textContent || '', left))
      placed = true
    } else if (left === 0) {
      range.setStart(root, i)
      placed = true
    } else {
      left -= Math.min(left, len)
    }
  }
  if (!placed) {
    const trailing = root.lastChild?.nodeName === 'BR' && text.endsWith('\n') ? 1 : 0
    range.setStart(root, nodes.length - trailing)
  }
  range.collapse(true)
  sel.removeAllRanges()
  sel.addRange(range)
  caret = { start: at, end: at }
  reveal(range)
}

/** Scrolls the field so the caret is in sight — after a chip lands at the end, say. */
function reveal(range: Range) {
  const root = editor.value
  const rect = range.getClientRects()[0] || (range.startContainer as Element).getBoundingClientRect?.()
  if (!root || !rect) return
  const box = root.getBoundingClientRect()
  const margin = 12
  if (rect.right > box.right - margin) root.scrollLeft += rect.right - box.right + margin
  else if (rect.left < box.left + margin) root.scrollLeft -= box.left + margin - rect.left
  if (rect.bottom > box.bottom) root.scrollTop += rect.bottom - box.bottom + 4
  else if (rect.top < box.top) root.scrollTop -= box.top - rect.top + 4
}

/** Where the `at`-th real character of a text node sits, past any anchors there. */
function domOffset(text: string, at: number): number {
  let seen = 0
  let i = 0
  while (i < text.length && (seen < at || text[i] === ANCHOR)) {
    if (text[i] !== ANCHOR) seen++
    i++
  }
  return i
}

function send(text: string) {
  sent = text
  emit('update:modelValue', text)
}

/** Replaces part of the text and redraws, leaving the caret after the new part. */
function replace(start: number, end: number, insert: string) {
  const current = read()
  const next = current.slice(0, start) + insert + current.slice(end)
  send(next)
  draw(next)
  editor.value?.focus()
  place(start + insert.length, next)
}

function onInput() {
  const text = read()
  if (untidy()) {
    const at = selection()?.start ?? text.length
    draw(text)
    place(at, text)
  }
  if (text !== (sent ?? value())) send(text)
}

const TOKEN_BEFORE = /\{\{\s*[\w.]+\s*\}\}$/
const TOKEN_AFTER = /^\{\{\s*[\w.]+\s*\}\}/

function onKeydown(event: KeyboardEvent) {
  if (event.isComposing) return
  if (event.key === 'Enter') {
    event.preventDefault()
    const range = props.multiline ? selection() : null
    if (range) replace(range.start, range.end, '\n')
    return
  }
  // A chip goes in one keypress, and so does the anchor beside it, which
  // would otherwise take a press that seems to do nothing.
  if ((event.key !== 'Backspace' && event.key !== 'Delete') || event.altKey || event.ctrlKey || event.metaKey) return
  const range = selection()
  if (!range || range.start !== range.end) return
  const text = read()
  const at = range.start
  if (event.key === 'Backspace') {
    const token = text.slice(0, at).match(TOKEN_BEFORE)?.[0]
    if (token) {
      event.preventDefault()
      replace(at - token.length, at, '')
    } else if (besideAnchor(-1)) {
      event.preventDefault()
      if (at > 0) replace(at - 1, at, '')
    }
  } else {
    const token = text.slice(at).match(TOKEN_AFTER)?.[0]
    if (token) {
      event.preventDefault()
      replace(at, at + token.length, '')
    } else if (besideAnchor(1)) {
      event.preventDefault()
      if (at < text.length) replace(at, at + 1, '')
    }
  }
}

/** Whether the character just before (-1) or after (1) the caret is an anchor. */
function besideAnchor(side: -1 | 1): boolean {
  const sel = window.getSelection()
  const node = sel?.rangeCount ? sel.getRangeAt(0).startContainer : null
  if (!node || node.nodeType !== Node.TEXT_NODE) return false
  const offset = sel!.getRangeAt(0).startOffset
  return (node.textContent || '')[side < 0 ? offset - 1 : offset] === ANCHOR
}

function onPaste(event: ClipboardEvent) {
  event.preventDefault()
  let text = event.clipboardData?.getData('text/plain') || ''
  if (!props.multiline) text = text.replace(/\s*\r?\n\s*/g, ' ')
  text = text.replace(/\r\n?/g, '\n')
  const length = read().length
  const range = selection() || { start: length, end: length }
  replace(range.start, range.end, text)
}

function remember() {
  const range = selection()
  if (range) caret = range
}

function insert(path: string) {
  const text = read()
  const start = caret.start >= 0 ? Math.min(caret.start, text.length) : text.length
  const end = caret.end >= 0 ? Math.min(caret.end, text.length) : text.length
  open.value = false
  // "today{{contact.phone}}" would send as "today0300…": a chip dropped
  // against a word gets the space a person would have typed.
  const before = /\S$/.test(text.slice(0, start)) ? ' ' : ''
  const after = /^[\p{L}\p{N}]/u.test(text.slice(end)) ? ' ' : ''
  replace(start, end, before + token(path) + after)
}

// Redraw when the value changes from outside (undo, a recipe, another field),
// or when a chip's name becomes known.
watch(() => props.modelValue, now => {
  if ((now ?? '') === sent) return
  sent = null
  draw(now ?? '')
})
watch(() => [lookups.state.variables.length, lookups.state.fields.length], () => {
  if (document.activeElement !== editor.value) draw(value())
})

onMounted(() => {
  lookups.ensure('variables', 'fields')
  draw(value())
  document.addEventListener('selectionchange', onSelection)
})
onBeforeUnmount(() => document.removeEventListener('selectionchange', onSelection))

function onSelection() {
  if (editor.value && document.activeElement === editor.value) remember()
}
</script>

<template>
  <div
    :class="[
      FIELD_SHELL,
      'relative',
      multiline ? 'flex flex-col' : 'flex h-10 items-center',
      disabled ? 'cursor-not-allowed text-white/70 light:text-gray-500' : ''
    ]"
  >
    <div
      ref="editor"
      role="textbox"
      :aria-multiline="multiline"
      :aria-label="ariaLabel"
      :aria-placeholder="placeholder"
      :aria-disabled="disabled || undefined"
      :contenteditable="!disabled"
      spellcheck="true"
      :class="[
        'variable-text min-w-0 flex-1 px-3 outline-none',
        disabled ? 'cursor-not-allowed' : 'cursor-text',
        multiline
          ? 'max-h-72 overflow-y-auto whitespace-pre-wrap break-words pb-1 pt-2 leading-relaxed'
          : 'variable-text-single h-full overflow-x-auto overflow-y-hidden whitespace-pre py-2 leading-6'
      ]"
      :style="multiline ? { minHeight: `${rows * 1.625 + 0.75}rem` } : undefined"
      @input="onInput"
      @keydown="onKeydown"
      @paste="onPaste"
      @drop.prevent
      @keyup="remember"
      @mouseup="remember"
    />
    <span
      v-if="isEmpty && placeholder"
      :class="[
        'pointer-events-none absolute left-3 top-2 max-w-[calc(100%-3.5rem)] truncate text-white/40 light:text-gray-400',
        multiline ? 'leading-relaxed' : 'leading-6'
      ]"
      aria-hidden="true"
    >{{ placeholder }}</span>
    <div v-if="!disabled" :class="multiline ? 'px-1.5 pb-1.5' : 'mr-1.5 shrink-0'">
      <Popover v-model:open="open">
        <PopoverTrigger as-child>
          <button
            type="button"
            class="flex items-center gap-1 rounded-sm px-1.5 py-1 text-xs text-white/55 transition-colors hover:bg-white/[0.08] hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40 light:text-gray-500 light:hover:bg-gray-100 light:hover:text-gray-900"
            :aria-label="t('automations.variables.insert')"
            :title="t('automations.variables.insert')"
            @mousedown.prevent
          >
            <Braces class="h-3.5 w-3.5" />
            <span v-if="multiline">{{ t('automations.variables.insert') }}</span>
          </button>
        </PopoverTrigger>
        <PopoverContent class="w-72 p-0" align="start">
          <div class="border-b border-border px-3">
            <input
              v-model="search"
              class="h-9 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
              :placeholder="t('automations.variables.search')"
            >
          </div>
          <div class="max-h-72 overflow-y-auto p-1">
            <p class="px-2 pb-1 pt-1.5 text-xs text-muted-foreground">{{ t('automations.variables.hint') }}</p>
            <template v-for="(rows, group) in filtered" :key="group">
              <p class="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">
                {{ t(`automations.variables.groups.${group}`, String(group)) }}
              </p>
              <button
                v-for="v in rows"
                :key="v.path"
                type="button"
                class="flex w-full items-baseline gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                @click="insert(v.path)"
              >
                <span class="min-w-0 flex-1 truncate">{{ v.label }}</span>
                <span v-if="v.example" class="shrink-0 truncate text-xs text-muted-foreground">{{ v.example }}</span>
              </button>
            </template>
            <p v-if="!Object.keys(filtered).length" class="px-2 py-4 text-center text-sm text-muted-foreground">
              {{ t('automations.picker.noMatch') }}
            </p>
          </div>
        </PopoverContent>
      </Popover>
    </div>
  </div>
</template>

<style>
/* Chips are drawn into the field by hand, so they are styled globally. */
.variable-text .vt-chip {
  display: inline-block;
  margin: 0;
  padding: 0 6px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.1);
  box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.12);
  color: #fff;
  font-size: 0.923em;
  font-weight: 500;
  line-height: 1.5;
  white-space: nowrap;
  vertical-align: baseline;
  cursor: default;
  user-select: all;
}
.light .variable-text .vt-chip {
  background: #eef2f6;
  box-shadow: inset 0 0 0 1px #dbe2ea;
  color: #0f172a;
}
.variable-text-single {
  scrollbar-width: none;
}
.variable-text-single::-webkit-scrollbar {
  display: none;
}
</style>
