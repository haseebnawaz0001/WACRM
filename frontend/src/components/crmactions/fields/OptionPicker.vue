<script setup lang="ts">
/**
 * Pick one thing, or several, from the organization's own list.
 *
 * Every id a rule stores — a team, a person, a template, a stage — is chosen
 * here by name. Typing an id was the old way, and it meant looking the id up
 * somewhere else first, which nobody at a front desk should have to know how
 * to do. Searchable because lists grow; creatable where the product can make
 * the thing on the spot (a new tag).
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Check, ChevronsUpDown, Plus, X } from 'lucide-vue-next'
import { getTagColorClass } from '@/lib/constants'
import { FIELD_BUTTON, PLACEHOLDER } from './styles'

export interface PickerOption {
  value: string
  label: string
  hint?: string
  /** A tag colour; renders the chip in it. */
  color?: string
  group?: string
}

const props = withDefaults(defineProps<{
  modelValue?: string | string[] | null
  options: PickerOption[]
  multiple?: boolean
  placeholder?: string
  /** Offer "Add “…”" for a value that is not in the list. */
  creatable?: boolean
  disabled?: boolean
  ariaLabel?: string
  /** A single picker can be cleared back to "not set". */
  clearable?: boolean
}>(), { multiple: false, creatable: false, disabled: false, clearable: false })

const emit = defineEmits<{ 'update:modelValue': [value: any] }>()

const { t } = useI18n()
const open = ref(false)
const search = ref('')
const active = ref(0)
const inputEl = ref<HTMLInputElement | null>(null)
const listEl = ref<HTMLElement | null>(null)

const selected = computed<string[]>(() => {
  const v = props.modelValue
  if (Array.isArray(v)) return v
  return v ? [v] : []
})

function labelOf(value: string): string {
  return props.options.find(o => o.value === value)?.label ?? value
}
function colorOf(value: string): string | undefined {
  return props.options.find(o => o.value === value)?.color
}

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return props.options
  return props.options.filter(o =>
    o.label.toLowerCase().includes(q) || o.hint?.toLowerCase().includes(q))
})

const canCreate = computed(() => {
  const q = search.value.trim()
  if (!props.creatable || !q) return false
  return !props.options.some(o => o.label.toLowerCase() === q.toLowerCase())
    && !selected.value.some(v => v.toLowerCase() === q.toLowerCase())
})

/** Rows in display order, with group headers interleaved. */
const rows = computed(() => {
  const out: Array<{ kind: 'group'; label: string } | { kind: 'option'; option: PickerOption; index: number }> = []
  let lastGroup: string | undefined
  let index = canCreate.value ? 1 : 0
  for (const option of filtered.value) {
    if (option.group && option.group !== lastGroup) {
      out.push({ kind: 'group', label: option.group })
      lastGroup = option.group
    }
    out.push({ kind: 'option', option, index: index++ })
  }
  return out
})

const total = computed(() => filtered.value.length + (canCreate.value ? 1 : 0))

watch(open, async isOpen => {
  if (!isOpen) return
  search.value = ''
  active.value = 0
  await nextTick()
  inputEl.value?.focus()
})
watch(search, () => { active.value = 0 })

function choose(value: string) {
  if (props.multiple) {
    const has = selected.value.includes(value)
    emit('update:modelValue', has ? selected.value.filter(v => v !== value) : [...selected.value, value])
    search.value = ''
    return
  }
  emit('update:modelValue', value)
  open.value = false
}

function create() {
  const value = search.value.trim()
  if (!value) return
  choose(value)
}

function remove(value: string) {
  if (props.multiple) emit('update:modelValue', selected.value.filter(v => v !== value))
  else emit('update:modelValue', null)
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    active.value = Math.min(active.value + 1, Math.max(total.value - 1, 0))
    scrollActive()
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    active.value = Math.max(active.value - 1, 0)
    scrollActive()
  } else if (event.key === 'Enter') {
    event.preventDefault()
    if (canCreate.value && active.value === 0) return create()
    const offset = canCreate.value ? 1 : 0
    const option = filtered.value[active.value - offset]
    if (option) choose(option.value)
  } else if (event.key === 'Backspace' && !search.value && props.multiple && selected.value.length) {
    remove(selected.value[selected.value.length - 1])
  }
}

async function scrollActive() {
  await nextTick()
  listEl.value?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child :disabled="disabled">
      <button
        type="button"
        :class="[FIELD_BUTTON, 'flex-wrap']"
        :aria-label="ariaLabel"
        :disabled="disabled"
      >
        <template v-if="selected.length && multiple">
          <span
            v-for="value in selected"
            :key="value"
            :class="[
              'inline-flex max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
              colorOf(value) ? getTagColorClass(colorOf(value)!) : 'bg-white/[0.08] text-white/85 light:bg-gray-100 light:text-gray-800'
            ]"
          >
            <span class="truncate">{{ labelOf(value) }}</span>
            <span
              v-if="!disabled"
              role="button"
              tabindex="-1"
              class="-mr-0.5 rounded-full p-0.5 opacity-60 hover:opacity-100"
              :aria-label="t('automations.picker.remove', { name: labelOf(value) })"
              @click.stop="remove(value)"
            >
              <X class="h-3 w-3" />
            </span>
          </span>
        </template>
        <span v-else-if="selected.length" class="min-w-0 flex-1 truncate">{{ labelOf(selected[0]) }}</span>
        <span v-else :class="['min-w-0 flex-1 truncate', PLACEHOLDER]">{{ placeholder || t('automations.picker.choose') }}</span>
        <span class="ml-auto flex shrink-0 items-center gap-1">
          <span
            v-if="clearable && !multiple && selected.length && !disabled"
            role="button"
            tabindex="-1"
            class="rounded-sm p-0.5 text-white/40 hover:text-white light:text-gray-400 light:hover:text-gray-900"
            :aria-label="t('automations.picker.clear')"
            @click.stop="remove(selected[0])"
          >
            <X class="h-3.5 w-3.5" />
          </span>
          <ChevronsUpDown class="h-4 w-4 opacity-40" />
        </span>
      </button>
    </PopoverTrigger>
    <PopoverContent class="w-[var(--reka-popover-trigger-width)] min-w-64 p-0" align="start">
      <div class="border-b border-border px-3">
        <input
          ref="inputEl"
          v-model="search"
          class="h-10 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          :placeholder="creatable ? t('automations.picker.searchOrCreate') : t('automations.picker.search')"
          role="combobox"
          :aria-expanded="open"
          aria-autocomplete="list"
          @keydown="onKeydown"
        >
      </div>
      <div ref="listEl" class="max-h-64 overflow-y-auto p-1" role="listbox" :aria-multiselectable="multiple">
        <button
          v-if="canCreate"
          type="button"
          role="option"
          :data-active="active === 0"
          :aria-selected="false"
          class="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm data-[active=true]:bg-accent"
          @mouseenter="active = 0"
          @click="create"
        >
          <Plus class="h-4 w-4 text-emerald-400 light:text-emerald-600" />
          {{ t('automations.picker.create', { value: search.trim() }) }}
        </button>
        <template v-for="row in rows" :key="row.kind === 'group' ? `g:${row.label}` : row.option.value">
          <p v-if="row.kind === 'group'" class="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">
            {{ row.label }}
          </p>
          <button
            v-else
            type="button"
            role="option"
            :data-active="active === row.index"
            :aria-selected="selected.includes(row.option.value)"
            class="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm data-[active=true]:bg-accent"
            @mouseenter="active = row.index"
            @click="choose(row.option.value)"
          >
            <span
              v-if="row.option.color"
              :class="['h-2 w-2 shrink-0 rounded-full', getTagColorClass(row.option.color).split(' ')[0]]"
            />
            <span class="min-w-0 flex-1">
              <span class="block truncate">{{ row.option.label }}</span>
              <span v-if="row.option.hint" class="block truncate text-xs text-muted-foreground">{{ row.option.hint }}</span>
            </span>
            <Check v-if="selected.includes(row.option.value)" class="h-4 w-4 shrink-0 text-emerald-400 light:text-emerald-600" />
          </button>
        </template>
        <p v-if="!total" class="px-2 py-6 text-center text-sm text-muted-foreground">
          {{ options.length ? t('automations.picker.noMatch') : t('automations.picker.empty') }}
        </p>
      </div>
    </PopoverContent>
  </Popover>
</template>
