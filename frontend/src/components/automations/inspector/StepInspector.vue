<script setup lang="ts">
/**
 * The selected step's questions.
 *
 * An action asks for what it needs to do its job; a question asks what to
 * check; a wait asks how long. Each explains itself in one line at the top,
 * because the person choosing "Check the contact" for the first time should
 * not have to guess what Yes and No will mean.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { FilterBuilder } from '@/components/shared'
import CrmActionFields from '@/components/crmactions/CrmActionFields.vue'
import DurationInput from '@/components/crmactions/fields/DurationInput.vue'
import SegmentedChoice from '@/components/crmactions/fields/SegmentedChoice.vue'
import { missingFields } from '@/components/crmactions/schema'
import { ArrowUp, ArrowDown, Copy, Trash2 } from 'lucide-vue-next'
import { stepIcons, toneOf, toneTile } from '../flow/steps'
import { CONDITION, WAIT, type FlowStep } from '../flow/tree'
import { durationText } from '../flow/sentences'
import { useLookups } from '../useLookups'
import { formatDateTime } from '@/lib/utils'
import InspectorSection from './InspectorSection.vue'

const props = defineProps<{
  step: FlowStep
  disabled?: boolean
  serverProblem?: string
  waiting?: number
  canUp: boolean
  canDown: boolean
}>()
const emit = defineEmits<{
  update: [patch: Partial<FlowStep>]
  remove: []
  duplicate: []
  move: [direction: -1 | 1]
}>()

const { t } = useI18n()
const lookups = useLookups()
lookups.ensure('filterFields')

const kind = computed(() => props.step.type === CONDITION ? 'condition' : props.step.type === WAIT ? 'wait' : 'action')
const missing = computed(() => kind.value === 'action' ? missingFields(props.step.type, props.step.config || {}) : [])
const icon = computed(() => stepIcons[props.step.type])

/** When someone reaching this wait right now would move on. */
const resumesAt = computed(() => {
  const w = props.step.config?.for
  const amount = Number(w?.amount)
  if (!(amount > 0)) return ''
  const ms = { minutes: 60e3, hours: 3600e3, days: 86400e3 }[w.unit as 'minutes' | 'hours' | 'days'] || 86400e3
  return formatDateTime(new Date(Date.now() + amount * ms))
})
</script>

<template>
  <div>
    <InspectorSection>
      <div class="flex items-start gap-3">
        <span :class="['flex h-9 w-9 shrink-0 items-center justify-center rounded-md', toneTile[toneOf(step.type)]]">
          <component :is="icon" v-if="icon" class="h-4 w-4" />
        </span>
        <div class="min-w-0">
          <h2 class="text-sm font-semibold text-white light:text-gray-900">{{ t(`automations.steps.${step.type}.title`, step.type) }}</h2>
          <p class="mt-0.5 text-xs leading-relaxed text-muted-foreground">{{ t(`automations.steps.${step.type}.description`, '') }}</p>
        </div>
      </div>
      <p
        v-if="serverProblem && !missing.length"
        class="mt-3 rounded-sm border border-amber-500/30 bg-amber-500/[0.07] px-3 py-2 text-xs text-amber-200 light:border-amber-200 light:bg-amber-50 light:text-amber-800"
      >{{ serverProblem }}</p>
    </InspectorSection>

    <!-- An action: what it needs, and what happens if it cannot be done. -->
    <template v-if="kind === 'action'">
      <InspectorSection>
        <CrmActionFields
          :type="step.type"
          :config="step.config || {}"
          :disabled="disabled"
          :missing="missing"
          @update:config="config => emit('update', { config })"
        />
      </InspectorSection>
      <InspectorSection :title="t('automations.inspector.ifFails')" :hint="t('automations.inspector.ifFailsHint')">
        <SegmentedChoice
          :model-value="step.continue_on_error ? 'carry_on' : 'stop'"
          :options="[
            { value: 'stop', label: t('automations.inspector.stopHere') },
            { value: 'carry_on', label: t('automations.inspector.carryOn') }
          ]"
          :disabled="disabled"
          :aria-label="t('automations.inspector.ifFails')"
          @update:model-value="v => emit('update', { continue_on_error: v === 'carry_on' })"
        />
      </InspectorSection>
    </template>

    <!-- A question: what to check. -->
    <InspectorSection
      v-else-if="kind === 'condition'"
      :title="t('automations.inspector.whatToCheck')"
    >
      <FilterBuilder
        :model-value="step.config?.filter || { op: 'and', rules: [] }"
        :fields="lookups.state.filterFields"
        @update:model-value="filter => emit('update', { config: { ...step.config, filter } })"
      />
    </InspectorSection>

    <!-- A wait: how long. -->
    <InspectorSection v-else :title="t('automations.inspector.howLong')">
      <DurationInput
        :model-value="step.config?.for"
        :disabled="disabled"
        :aria-label="t('automations.inspector.howLong')"
        @update:model-value="v => emit('update', { config: { ...step.config, for: v } })"
      />
      <p v-if="resumesAt" class="mt-2 text-xs text-muted-foreground">
        {{ t('automations.inspector.resumesAt', { when: resumesAt, duration: durationText(t, step.config?.for) }) }}
      </p>
      <p class="mt-3 text-xs leading-relaxed text-muted-foreground">{{ t('automations.inspector.waitHint') }}</p>
      <p v-if="waiting" class="mt-3 text-xs font-medium text-sky-300 light:text-sky-700">
        {{ t('automations.canvas.waitingHere', { n: waiting }, waiting) }}
      </p>
    </InspectorSection>

    <InspectorSection v-if="!disabled">
      <div class="flex items-center gap-0.5">
        <Button variant="ghost" size="sm" class="h-8 px-2 text-xs" :disabled="!canUp" @click="emit('move', -1)">
          <ArrowUp class="mr-1 h-3.5 w-3.5" />{{ t('automations.inspector.moveUp') }}
        </Button>
        <Button variant="ghost" size="sm" class="h-8 px-2 text-xs" :disabled="!canDown" @click="emit('move', 1)">
          <ArrowDown class="mr-1 h-3.5 w-3.5" />{{ t('automations.inspector.moveDown') }}
        </Button>
        <Button variant="ghost" size="sm" class="h-8 px-2 text-xs" @click="emit('duplicate')">
          <Copy class="mr-1 h-3.5 w-3.5" />{{ t('automations.canvas.duplicate') }}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          class="ml-auto h-8 px-2 text-xs text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          @click="emit('remove')"
        >
          <Trash2 class="mr-1 h-3.5 w-3.5" />{{ t('automations.canvas.remove') }}
        </Button>
      </div>
    </InspectorSection>
  </div>
</template>
