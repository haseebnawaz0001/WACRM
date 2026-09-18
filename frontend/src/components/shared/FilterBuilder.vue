<script setup lang="ts">
/**
 * FilterBuilder renders a contact filter tree from the backend's field
 * registry (plan 00, F6).
 *
 * The field list, the operators each type allows and the dropdown options all
 * come from `/contacts/filter-fields`, so a field an organization adds appears
 * here without a frontend change. Segments, automation conditions and campaign
 * audiences reuse this same component and the same filter shape.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import { Plus, Trash2, FolderPlus } from 'lucide-vue-next'
import type { FilterNode, FilterFieldInfo } from '@/services/api'

const props = defineProps<{
  modelValue: FilterNode
  fields: FilterFieldInfo[]
  /** Nesting depth of this group; the backend rejects anything deeper than 3. */
  depth?: number
}>()

const emit = defineEmits<{ 'update:modelValue': [FilterNode] }>()

const { t } = useI18n()

const MAX_DEPTH = 3
const depth = computed(() => props.depth ?? 1)
const canNest = computed(() => depth.value < MAX_DEPTH)

const group = computed<FilterNode>(() => props.modelValue)
const rules = computed<FilterNode[]>(() => props.modelValue.rules ?? [])

/** Operators that take no value, so the value input is hidden for them. */
const valuelessOperators = new Set([
  'is_empty', 'is_not_empty', 'is_true', 'is_false', 'is_me', 'is_unassigned'
])

/** Operators whose value is a list of choices. */
const listOperators = new Set(['in', 'not_in', 'contains_any', 'contains_all', 'contains_none'])

function update(next: Partial<FilterNode>) {
  emit('update:modelValue', { ...group.value, ...next })
}

function setRule(index: number, rule: FilterNode) {
  const next = [...rules.value]
  next[index] = rule
  update({ rules: next })
}

function removeRule(index: number) {
  update({ rules: rules.value.filter((_, i) => i !== index) })
}

function addRule() {
  const first = props.fields[0]
  if (!first) return
  update({
    rules: [...rules.value, { field: first.key, operator: first.operators[0], value: '' }]
  })
}

function addGroup() {
  update({ rules: [...rules.value, { op: 'and', rules: [] }] })
}

function fieldFor(key?: string): FilterFieldInfo | undefined {
  return props.fields.find((f) => f.key === key)
}

/** Changing the field resets the operator and value: the old ones rarely apply. */
function onFieldChange(index: number, key: string) {
  const field = fieldFor(key)
  setRule(index, {
    field: key,
    operator: field?.operators[0] ?? 'equals',
    value: ''
  })
}

function onOperatorChange(index: number, operator: string) {
  const rule = rules.value[index]
  setRule(index, {
    ...rule,
    operator,
    // Switching between a single value and a list would otherwise send the
    // wrong shape and be rejected by the server.
    value: listOperators.has(operator) ? [] : ''
  })
}

function onValueChange(index: number, value: unknown) {
  setRule(index, { ...rules.value[index], value })
}

/** Turns the comma-separated input of a list operator into an array. */
function onListValueChange(index: number, raw: string) {
  onValueChange(index, raw.split(',').map((s) => s.trim()).filter(Boolean))
}

function listValueText(rule: FilterNode): string {
  return Array.isArray(rule.value) ? (rule.value as string[]).join(', ') : ''
}

function inputTypeFor(field?: FilterFieldInfo): string {
  if (!field) return 'text'
  if (field.type === 'number') return 'number'
  if (field.type === 'date') return 'date'
  return 'text'
}

function operatorLabel(op: string): string {
  // Falls back to the raw operator when a translation is missing, so a new
  // backend operator is still usable rather than showing an empty menu item.
  const key = `filters.op.${op}`
  const translated = t(key)
  return translated === key ? op.replace(/_/g, ' ') : translated
}

const toggleLabel = computed(() =>
  group.value.op === 'or' ? t('filters.matchAny') : t('filters.matchAll')
)

function toggleOp() {
  update({ op: group.value.op === 'or' ? 'and' : 'or' })
}

// Selected dropdown values are tracked as text so one input serves every type.
const listDrafts = ref<Record<number, string>>({})
</script>

<template>
  <div
    class="rounded-lg border border-border/60 p-3 space-y-2"
    :class="depth > 1 ? 'bg-muted/30' : ''"
  >
    <div class="flex items-center gap-2">
      <Button variant="outline" size="sm" class="h-7 px-2 text-xs" @click="toggleOp">
        {{ toggleLabel }}
      </Button>
      <span class="text-xs text-muted-foreground">{{ $t('filters.ofTheFollowing') }}</span>
    </div>

    <div v-for="(rule, index) in rules" :key="index" class="flex items-start gap-2">
      <!-- Nested group -->
      <div v-if="rule.rules || rule.op" class="flex-1">
        <FilterBuilder
          :model-value="rule"
          :fields="fields"
          :depth="depth + 1"
          @update:model-value="(v) => setRule(index, v)"
        />
      </div>

      <!-- Leaf condition -->
      <template v-else>
        <Select
          :model-value="rule.field"
          @update:model-value="(v) => onFieldChange(index, String(v))"
        >
          <SelectTrigger class="h-8 w-[180px] text-xs" :aria-label="$t('dashboard.field')"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="f in fields" :key="f.key" :value="f.key">{{ f.label }}</SelectItem>
          </SelectContent>
        </Select>

        <Select
          :model-value="rule.operator"
          @update:model-value="(v) => onOperatorChange(index, String(v))"
        >
          <SelectTrigger class="h-8 w-[150px] text-xs" :aria-label="$t('dashboard.operator')"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem
              v-for="op in fieldFor(rule.field)?.operators ?? []"
              :key="op"
              :value="op"
            >
              {{ operatorLabel(op) }}
            </SelectItem>
          </SelectContent>
        </Select>

        <!-- Value input, shaped by the field type and operator -->
        <template v-if="!valuelessOperators.has(rule.operator ?? '')">
          <!-- A dropdown field with a list operator picks from its own options -->
          <Select
            v-if="fieldFor(rule.field)?.options?.length && listOperators.has(rule.operator ?? '')"
            :model-value="Array.isArray(rule.value) ? String(rule.value[0] ?? '') : ''"
            @update:model-value="(v) => onValueChange(index, [String(v)])"
          >
            <SelectTrigger class="h-8 flex-1 text-xs" :aria-label="$t('filters.selectValue')"><SelectValue :placeholder="$t('filters.selectValue')" /></SelectTrigger>
            <SelectContent>
              <SelectItem
                v-for="opt in fieldFor(rule.field)?.options ?? []"
                :key="opt.value"
                :value="opt.value"
              >
                {{ opt.label || opt.value }}
              </SelectItem>
            </SelectContent>
          </Select>

          <Input
            v-else-if="listOperators.has(rule.operator ?? '')"
            :model-value="listDrafts[index] ?? listValueText(rule)"
            class="h-8 flex-1 text-xs"
            :placeholder="$t('filters.commaSeparated')"
            @update:model-value="(v) => { listDrafts[index] = String(v); onListValueChange(index, String(v)) }"
          />

          <Input
            v-else
            :model-value="(rule.value as string) ?? ''"
            :type="inputTypeFor(fieldFor(rule.field))"
            class="h-8 flex-1 text-xs"
            @update:model-value="(v) => onValueChange(index, v)"
          />
        </template>
        <div v-else class="flex-1" />
      </template>

      <Button variant="ghost" size="icon" class="h-8 w-8 shrink-0" @click="removeRule(index)">
        <Trash2 class="h-3.5 w-3.5 text-muted-foreground" />
      </Button>
    </div>

    <div class="flex items-center gap-2 pt-1">
      <Button variant="ghost" size="sm" class="h-7 px-2 text-xs" :disabled="!fields.length" @click="addRule">
        <Plus class="h-3.5 w-3.5 mr-1" />{{ $t('filters.addCondition') }}
      </Button>
      <!-- Nesting stops at the depth the backend accepts, so a filter cannot be
           built here that the server would reject. -->
      <Button
        v-if="canNest"
        variant="ghost"
        size="sm"
        class="h-7 px-2 text-xs"
        @click="addGroup"
      >
        <FolderPlus class="h-3.5 w-3.5 mr-1" />{{ $t('filters.addGroup') }}
      </Button>
      <Badge v-if="!rules.length" variant="outline" class="text-xs font-normal">
        {{ $t('filters.noConditions') }}
      </Badge>
    </div>
  </div>
</template>
