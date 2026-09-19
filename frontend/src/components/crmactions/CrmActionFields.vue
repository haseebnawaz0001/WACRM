<script setup lang="ts">
/**
 * One action's settings, each with the input it deserves (plan 10, S7).
 *
 * Shared by the automation builder's inspector, the chatbot CRM-action node
 * and keyword rules, so an action asks for the same things the same way
 * everywhere. What each action needs lives in schema.ts; this renders it.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ChevronDown } from 'lucide-vue-next'
import { useLookups } from '@/components/automations/useLookups'
import { visibleFields, type ActionField } from './schema'
import OptionPicker, { type PickerOption } from './fields/OptionPicker.vue'
import DurationInput from './fields/DurationInput.vue'
import SegmentedChoice from './fields/SegmentedChoice.vue'
import VariableText from './fields/VariableText.vue'
import RecipientsField from './fields/RecipientsField.vue'
import HeadersField from './fields/HeadersField.vue'
import TemplateParamsField from './fields/TemplateParamsField.vue'
import { FIELD } from './fields/styles'

const props = withDefaults(defineProps<{
  type: string
  config: Record<string, any>
  disabled?: boolean
  /** Field keys to mark as still needed. */
  missing?: string[]
}>(), { disabled: false, missing: () => [] })

const emit = defineEmits<{ 'update:config': [value: Record<string, any>] }>()

const { t } = useI18n()
const lookups = useLookups()
const showAdvanced = ref(false)

const fields = computed(() => visibleFields(props.type, props.config))
const basic = computed(() => fields.value.filter(f => !f.advanced))
const advanced = computed(() => fields.value.filter(f => f.advanced))

const needs: Record<string, Parameters<typeof lookups.ensure>> = {
  tags: ['tags'], user: ['users'], team: ['teams'], template: ['templates'],
  pipeline: ['pipelines'], stage: ['pipelines'], taskType: ['taskTypes'],
  contactField: ['fields'], fieldValue: ['fields'], owner: ['users']
}

watch(() => props.type, () => {
  const kinds = new Set<string>()
  for (const field of visibleFields(props.type, props.config)) {
    for (const kind of needs[field.kind] || []) kinds.add(kind)
  }
  if (kinds.size) lookups.ensure(...([...kinds] as any))
  // The sentence on the canvas names what a step points at, so a template
  // step also needs templates even while the inspector shows something else.
}, { immediate: true })

function set(key: string, value: unknown) {
  const next = { ...props.config, [key]: value }
  // Changing what a value belongs to invalidates the value.
  if (key === 'field' && props.type === 'set_field') delete next.value
  if (key === 'pipeline_id') delete next.stage_id
  if (key === 'template_id') delete next.param_mappings
  emit('update:config', next)
}

function choiceLabel(value: string): string {
  return t(`automations.choices.${value}`, value)
}

function options(field: ActionField): PickerOption[] {
  switch (field.kind) {
    case 'tags':
      return lookups.state.tags.map(tag => ({ value: tag.name, label: tag.name, color: tag.color }))
    case 'user':
      return lookups.state.users.filter(u => u.is_active !== false)
        .map(u => ({ value: u.id, label: u.full_name, hint: u.email }))
    case 'team':
      return lookups.state.teams.filter(team => team.is_active !== false)
        .map(team => ({ value: team.id, label: team.name }))
    case 'template':
      return lookups.state.templates.map(tpl => ({
        value: tpl.id,
        label: tpl.name,
        hint: [tpl.language, tpl.category?.toLowerCase(), tpl.status && tpl.status !== 'APPROVED' ? t('automations.hints.notApproved') : '']
          .filter(Boolean).join(' · '),
        group: tpl.status === 'APPROVED' ? t('automations.picker.approved') : t('automations.picker.notApproved')
      })).sort((a, b) => (a.group === b.group ? 0 : a.group === t('automations.picker.approved') ? -1 : 1))
    case 'pipeline':
      return lookups.state.pipelines.map(p => ({ value: p.id, label: p.name }))
    case 'stage': {
      const pipelines = props.config.pipeline_id
        ? lookups.state.pipelines.filter(p => p.id === props.config.pipeline_id)
        : lookups.state.pipelines
      return pipelines.flatMap(p => (p.stages || []).map(s => ({
        value: s.id, label: s.name, group: pipelines.length > 1 ? p.name : undefined
      })))
    }
    case 'taskType':
      return lookups.state.taskTypes.filter(tt => !tt.archived_at).map(tt => ({ value: tt.key, label: tt.label }))
    case 'contactField':
      return lookups.state.fields.filter(f => !f.archived_at).map(f => ({ value: f.key, label: f.label }))
  }
  return []
}

const valueField = computed(() => lookups.field(props.config.field))
const ownerModes = ['contact_owner', 'conversation_assignee', 'rule_creator', 'user']

function isMissing(field: ActionField) {
  return props.missing.includes(field.key)
}
</script>

<template>
  <div class="space-y-4">
    <template v-for="group in [basic, showAdvanced ? advanced : []]" :key="group === basic ? 'basic' : 'advanced'">
      <div v-for="field in group" :key="field.key" class="space-y-1.5">
        <div v-if="field.kind !== 'boolean'" class="flex items-baseline justify-between gap-2">
          <label class="text-[13px] font-medium text-white/85 light:text-gray-800">
            {{ t(`automations.fields.${field.label}`) }}
          </label>
          <span
            v-if="isMissing(field)"
            class="text-xs text-amber-300 light:text-amber-700"
          >{{ t('automations.needed') }}</span>
        </div>

        <VariableText
          v-if="field.kind === 'text' && field.variables"
          :model-value="config[field.key]"
          :disabled="disabled"
          :placeholder="field.placeholder ? t(`automations.placeholders.${field.placeholder}`) : ''"
          :aria-label="t(`automations.fields.${field.label}`)"
          @update:model-value="v => set(field.key, v)"
        />
        <Input
          v-else-if="field.kind === 'text'"
          :model-value="config[field.key] ?? ''"
          :disabled="disabled"
          :placeholder="field.placeholder ? t(`automations.placeholders.${field.placeholder}`) : ''"
          @update:model-value="v => set(field.key, v)"
        />
        <VariableText
          v-else-if="field.kind === 'longtext'"
          multiline
          :model-value="config[field.key]"
          :disabled="disabled"
          :aria-label="t(`automations.fields.${field.label}`)"
          @update:model-value="v => set(field.key, v)"
        />
        <input
          v-else-if="field.kind === 'number'"
          type="number"
          :class="[FIELD, 'h-10 tabular-nums']"
          :value="config[field.key] ?? ''"
          :disabled="disabled"
          :aria-label="t(`automations.fields.${field.label}`)"
          @input="e => set(field.key, (e.target as HTMLInputElement).value === '' ? undefined : Number((e.target as HTMLInputElement).value))"
        >
        <label v-else-if="field.kind === 'boolean'" class="flex items-center justify-between gap-3 text-[13px] font-medium">
          {{ t(`automations.fields.${field.label}`) }}
          <Switch
            :model-value="!!config[field.key]"
            :disabled="disabled"
            @update:model-value="(v: boolean) => set(field.key, v)"
          />
        </label>
        <template v-else-if="field.kind === 'choice'">
          <SegmentedChoice
            v-if="(field.options || []).length <= 3"
            :model-value="config[field.key]"
            :options="(field.options || []).map(o => ({ value: o, label: choiceLabel(o) }))"
            :disabled="disabled"
            :aria-label="t(`automations.fields.${field.label}`)"
            @update:model-value="v => set(field.key, v)"
          />
          <Select
            v-else
            :model-value="config[field.key] ?? (field.options || [])[0]"
            :disabled="disabled"
            @update:model-value="v => set(field.key, v)"
          >
            <SelectTrigger :aria-label="t(`automations.fields.${field.label}`)"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="o in field.options" :key="o" :value="o">{{ choiceLabel(o) }}</SelectItem>
            </SelectContent>
          </Select>
        </template>
        <OptionPicker
          v-else-if="field.kind === 'tags'"
          :model-value="config[field.key] || []"
          :options="options(field)"
          multiple
          creatable
          :disabled="disabled"
          :placeholder="t('automations.placeholders.tags')"
          :aria-label="t(`automations.fields.${field.label}`)"
          @update:model-value="v => set(field.key, v)"
        />
        <OptionPicker
          v-else-if="['user', 'team', 'template', 'pipeline', 'stage', 'taskType', 'contactField'].includes(field.kind)"
          :model-value="config[field.key]"
          :options="options(field)"
          :disabled="disabled"
          :clearable="!field.required"
          :aria-label="t(`automations.fields.${field.label}`)"
          @update:model-value="v => set(field.key, v ?? undefined)"
        />
        <template v-else-if="field.kind === 'fieldValue'">
          <p v-if="!config.field" class="text-xs text-muted-foreground">{{ t('automations.hints.pickFieldFirst') }}</p>
          <OptionPicker
            v-else-if="valueField?.type === 'dropdown'"
            :model-value="config[field.key]"
            :options="(valueField.options || []).map(o => ({ value: o.value, label: o.label ?? o.value }))"
            :disabled="disabled"
            :aria-label="t(`automations.fields.${field.label}`)"
            @update:model-value="v => set(field.key, v)"
          />
          <input
            v-else-if="valueField?.type === 'date'"
            type="date"
            :class="[FIELD, 'h-10']"
            :value="config[field.key] ?? ''"
            :disabled="disabled"
            @input="e => set(field.key, (e.target as HTMLInputElement).value)"
          >
          <input
            v-else-if="valueField?.type === 'number'"
            type="number"
            :class="[FIELD, 'h-10 tabular-nums']"
            :value="config[field.key] ?? ''"
            :disabled="disabled"
            @input="e => set(field.key, Number((e.target as HTMLInputElement).value))"
          >
          <VariableText
            v-else
            :model-value="config[field.key]"
            :disabled="disabled"
            @update:model-value="v => set(field.key, v)"
          />
        </template>
        <DurationInput
          v-else-if="field.kind === 'duration'"
          :model-value="config[field.key]"
          :disabled="disabled"
          :aria-label="t(`automations.fields.${field.label}`)"
          @update:model-value="v => set(field.key, v)"
        />
        <RecipientsField
          v-else-if="field.kind === 'recipients'"
          :model-value="config[field.key]"
          :disabled="disabled"
          @update:model-value="v => set(field.key, v)"
        />
        <div v-else-if="field.kind === 'owner'" class="space-y-2">
          <Select
            :model-value="config.owner?.mode || 'contact_owner'"
            :disabled="disabled"
            @update:model-value="v => set('owner', { ...(config.owner || {}), mode: String(v) })"
          >
            <SelectTrigger :aria-label="t(`automations.fields.${field.label}`)"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="mode in ownerModes" :key="mode" :value="mode">
                {{ t(`automations.choices.owner_${mode}`) }}
              </SelectItem>
            </SelectContent>
          </Select>
          <OptionPicker
            v-if="config.owner?.mode === 'user'"
            :model-value="config.owner?.user_id"
            :options="options({ key: 'user_id', kind: 'user', label: 'person' })"
            :disabled="disabled"
            :aria-label="t('automations.fields.person')"
            @update:model-value="v => set('owner', { ...(config.owner || {}), user_id: v })"
          />
        </div>
        <HeadersField
          v-else-if="field.kind === 'headers'"
          :model-value="config[field.key]"
          :disabled="disabled"
          @update:model-value="v => set(field.key, v)"
        />
        <TemplateParamsField
          v-else-if="field.kind === 'templateParams'"
          :model-value="config[field.key]"
          :template-id="config.template_id"
          :disabled="disabled"
          @update:model-value="v => set(field.key, v)"
        />

        <p v-if="field.hint" class="text-xs leading-relaxed text-muted-foreground">
          {{ t(`automations.hints.${field.hint}`) }}
        </p>
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

    <p v-if="!fields.length" class="text-sm text-muted-foreground">{{ t('automations.noSettings') }}</p>
  </div>
</template>
