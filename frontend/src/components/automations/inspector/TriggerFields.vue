<script setup lang="ts">
/**
 * The settings that narrow a trigger: which tag, which stage, how long.
 *
 * Empty means "any" for every one of them, and each says so, so a person can
 * see that leaving "Tags" blank means every tag rather than wondering whether
 * the rule is broken.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ChevronDown } from 'lucide-vue-next'
import OptionPicker, { type PickerOption } from '@/components/crmactions/fields/OptionPicker.vue'
import DurationInput from '@/components/crmactions/fields/DurationInput.vue'
import SegmentedChoice from '@/components/crmactions/fields/SegmentedChoice.vue'
import { FIELD } from '@/components/crmactions/fields/styles'
import { triggerDef, type TriggerField } from '../flow/triggers'
import { useLookups } from '../useLookups'

const props = defineProps<{
  type: string
  config: Record<string, any>
  disabled?: boolean
  missing?: string[]
}>()
const emit = defineEmits<{ 'update:config': [value: Record<string, any>] }>()

const { t } = useI18n()
const lookups = useLookups()
const showAdvanced = ref(false)

const def = computed(() => triggerDef(props.type))
const basic = computed(() => (def.value?.fields || []).filter(f => !f.advanced))
const advanced = computed(() => (def.value?.fields || []).filter(f => f.advanced))

const needs: Record<string, any[]> = {
  tags: ['tags'], users: ['users'], teams: ['teams'], contactField: ['fields'], fieldValues: ['fields'],
  lifecycleStages: ['fields'], accounts: ['accounts'], flows: ['flows'], campaigns: ['campaigns'],
  taskTypes: ['taskTypes'], pipeline: ['pipelines'], stages: ['pipelines']
}
watch(() => props.type, () => {
  const kinds = new Set<string>()
  for (const f of def.value?.fields || []) for (const k of needs[f.kind] || []) kinds.add(k)
  if (kinds.size) lookups.ensure(...([...kinds] as any))
}, { immediate: true })

function set(key: string, value: unknown) {
  const next = { ...props.config }
  const empty = value === undefined || value === null || value === '' || (Array.isArray(value) && !value.length)
  if (empty) delete next[key]
  else next[key] = value
  if (key === 'field') delete next.to
  if (key === 'pipeline_id') delete next.to_stage_ids
  emit('update:config', next)
}

function options(field: TriggerField): PickerOption[] {
  switch (field.kind) {
    case 'tags': return lookups.state.tags.map(tag => ({ value: tag.name, label: tag.name, color: tag.color }))
    case 'users': return lookups.state.users.map(u => ({ value: u.id, label: u.full_name, hint: u.email }))
    case 'teams': return lookups.state.teams.map(team => ({ value: team.id, label: team.name }))
    case 'multiChoice':
    case 'choice': return (field.options || []).map(o => ({ value: o, label: t(`automations.choices.${o}`, o) }))
    case 'contactField': {
      const dateOnly = props.type === 'time.date_field'
      return lookups.state.fields
        .filter(f => !f.archived_at && (!dateOnly || f.type === 'date'))
        .map(f => ({ value: f.key, label: f.label }))
    }
    case 'fieldValues': {
      const f = lookups.field(props.config.field)
      return (f?.options || []).map(o => ({ value: o.value, label: o.label ?? o.value }))
    }
    case 'lifecycleStages': {
      const f = lookups.field('lifecycle_stage')
      return (f?.options || []).map(o => ({ value: o.value, label: o.label ?? o.value }))
    }
    case 'accounts': return lookups.state.accounts.map(a => ({ value: a.name, label: a.name }))
    case 'flows': return lookups.state.flows.map(f => ({ value: f.id, label: f.name }))
    case 'campaigns': return lookups.state.campaigns.map(c => ({ value: c.id, label: c.name }))
    case 'taskTypes': return lookups.state.taskTypes.filter(tt => !tt.archived_at).map(tt => ({ value: tt.key, label: tt.label }))
    case 'pipeline': return lookups.state.pipelines.map(p => ({ value: p.id, label: p.name }))
    case 'stages': {
      const pipelines = props.config.pipeline_id
        ? lookups.state.pipelines.filter(p => p.id === props.config.pipeline_id)
        : lookups.state.pipelines
      return pipelines.flatMap(p => (p.stages || []).map(s => ({
        value: s.id, label: s.name, group: pipelines.length > 1 ? p.name : undefined
      })))
    }
  }
  return []
}

const offset = computed(() => Number(props.config.offset_days ?? 0))
const offsetDirection = computed(() => offset.value < 0 ? 'before' : offset.value > 0 ? 'after' : 'on')
function setOffset(direction: string, days: number) {
  const n = Math.max(0, Math.round(days))
  set('offset_days', direction === 'on' ? 0 : direction === 'before' ? -Math.max(n, 1) : Math.max(n, 1))
}

const hours = Array.from({ length: 24 }, (_, h) => h)
const fieldValueIsFree = computed(() => {
  const f = lookups.field(props.config.field)
  return !!props.config.field && f?.type !== 'dropdown'
})
</script>

<template>
  <div class="space-y-4">
    <template v-for="group in [basic, showAdvanced ? advanced : []]" :key="group === basic ? 'b' : 'a'">
      <div v-for="field in group" :key="field.key" class="space-y-1.5">
        <div class="flex items-baseline justify-between gap-2">
          <label class="text-[13px] font-medium text-white/85 light:text-gray-800">
            {{ t(`automations.triggerFields.${field.label}`) }}
          </label>
          <span v-if="missing?.includes(field.key)" class="text-xs text-amber-300 light:text-amber-700">{{ t('automations.needed') }}</span>
          <span v-else-if="!field.required" class="text-xs text-muted-foreground">{{ t('automations.optional') }}</span>
        </div>

        <DurationInput
          v-if="field.kind === 'duration'"
          :model-value="config[field.key]"
          :disabled="disabled"
          :aria-label="t(`automations.triggerFields.${field.label}`)"
          @update:model-value="v => set(field.key, v)"
        />
        <SegmentedChoice
          v-else-if="field.kind === 'choice'"
          :model-value="config[field.key] || ''"
          :options="[{ value: '', label: t('automations.choices.either') }, ...options(field)]"
          :disabled="disabled"
          @update:model-value="v => set(field.key, v)"
        />
        <template v-else-if="field.kind === 'dateOffset'">
          <SegmentedChoice
            :model-value="offsetDirection"
            :options="['before', 'on', 'after'].map(v => ({ value: v, label: t(`automations.choices.date_${v}`) }))"
            :disabled="disabled"
            @update:model-value="v => setOffset(v, Math.abs(offset) || 7)"
          />
          <div v-if="offsetDirection !== 'on'" class="flex items-center gap-2 text-sm">
            <input
              type="number"
              min="1"
              :class="[FIELD, 'h-10 w-24 tabular-nums']"
              :value="Math.abs(offset)"
              :disabled="disabled"
              :aria-label="t('automations.triggerFields.days')"
              @input="e => setOffset(offsetDirection, Number((e.target as HTMLInputElement).value) || 1)"
            >
            <span class="text-muted-foreground">
              {{ t(offsetDirection === 'before' ? 'automations.triggerFields.daysBefore' : 'automations.triggerFields.daysAfter', Math.abs(offset) || 2) }}
            </span>
          </div>
        </template>
        <Select
          v-else-if="field.kind === 'hour'"
          :model-value="config.at_local_hour === undefined ? 'any' : String(config.at_local_hour)"
          :disabled="disabled"
          @update:model-value="v => set('at_local_hour', v === 'any' ? undefined : Number(v))"
        >
          <SelectTrigger :aria-label="t(`automations.triggerFields.${field.label}`)"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="any">{{ t('automations.choices.anyTime') }}</SelectItem>
            <SelectItem v-for="h in hours" :key="h" :value="String(h)">{{ String(h).padStart(2, '0') }}:00</SelectItem>
          </SelectContent>
        </Select>
        <OptionPicker
          v-else-if="field.kind === 'contactField' || field.kind === 'pipeline'"
          :model-value="config[field.key]"
          :options="options(field)"
          :disabled="disabled"
          :clearable="!field.required"
          :placeholder="field.required ? t('automations.picker.choose') : t('automations.any')"
          :aria-label="t(`automations.triggerFields.${field.label}`)"
          @update:model-value="v => set(field.key, v)"
        />
        <template v-else-if="field.kind === 'fieldValues'">
          <p v-if="!config.field" class="text-xs text-muted-foreground">{{ t('automations.hints.pickFieldFirst') }}</p>
          <OptionPicker
            v-else
            :model-value="config[field.key] || []"
            :options="options(field)"
            multiple
            :creatable="fieldValueIsFree"
            :disabled="disabled"
            :placeholder="t('automations.anyValue')"
            :aria-label="t(`automations.triggerFields.${field.label}`)"
            @update:model-value="v => set(field.key, v)"
          />
        </template>
        <OptionPicker
          v-else
          :model-value="config[field.key] || []"
          :options="options(field)"
          multiple
          :creatable="field.kind === 'tags'"
          :disabled="disabled"
          :placeholder="t('automations.any')"
          :aria-label="t(`automations.triggerFields.${field.label}`)"
          @update:model-value="v => set(field.key, v)"
        />
      </div>
    </template>

    <button
      v-if="advanced.length"
      type="button"
      class="flex items-center gap-1 text-xs font-medium text-white/60 hover:text-white light:text-gray-500 light:hover:text-gray-900"
      :aria-expanded="showAdvanced"
      @click="showAdvanced = !showAdvanced"
    >
      <ChevronDown :class="['h-3.5 w-3.5 transition-transform duration-200', showAdvanced && 'rotate-180']" />
      {{ showAdvanced ? t('automations.fewerOptions') : t('automations.moreOptions') }}
    </button>
  </div>
</template>
