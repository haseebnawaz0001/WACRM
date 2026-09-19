<script setup lang="ts">
/**
 * The start card's questions: what starts it, narrowed how, and for whom.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { FilterBuilder } from '@/components/shared'
import type { FilterNode } from '@/services/api'
import SegmentedChoice from '@/components/crmactions/fields/SegmentedChoice.vue'
import { toneTile } from '../flow/steps'
import { triggerDef, missingTriggerFields } from '../flow/triggers'
import { useLookups } from '../useLookups'
import TriggerPicker from './TriggerPicker.vue'
import TriggerFields from './TriggerFields.vue'
import InspectorSection from './InspectorSection.vue'

const props = defineProps<{
  type: string
  config: Record<string, any>
  filter: FilterNode
  available: string[]
  disabled?: boolean
  serverProblem?: string
}>()
const emit = defineEmits<{
  'update:type': [value: string]
  'update:config': [value: Record<string, any>]
  'update:filter': [value: FilterNode]
}>()

const { t } = useI18n()
const lookups = useLookups()
lookups.ensure('filterFields')

const choosing = ref(false)
const def = computed(() => triggerDef(props.type))
const missing = computed(() => missingTriggerFields(props.type, props.config))
const hasSettings = computed(() => (def.value?.fields.length ?? 0) > 0)

const audience = computed(() => props.filter.rules?.length ? 'some' : 'everyone')
const showFilter = ref(audience.value === 'some')

function setAudience(value: string) {
  showFilter.value = value === 'some'
  if (value === 'everyone') emit('update:filter', { op: 'and', rules: [] })
}

function pick(type: string) {
  if (type !== props.type) {
    emit('update:type', type)
    emit('update:config', {})
  }
  choosing.value = false
}
</script>

<template>
  <div>
    <InspectorSection :title="t('automations.inspector.whatStarts')">
      <template v-if="!choosing">
        <div class="flex items-start gap-3 rounded-md border border-emerald-500/25 bg-emerald-500/[0.06] p-3 light:border-emerald-200 light:bg-emerald-50/60">
          <span :class="['mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md', toneTile.trigger]">
            <component :is="def?.icon" v-if="def" class="h-4 w-4" />
          </span>
          <div class="min-w-0 flex-1">
            <p class="text-sm font-medium">{{ t(`automations.triggers.${type}`, type) }}</p>
            <p class="text-xs leading-4 text-muted-foreground">{{ t(`automations.triggerDescriptions.${type}`, '') }}</p>
          </div>
          <Button v-if="!disabled" variant="outline" size="sm" class="h-7 shrink-0 px-2 text-xs" @click="choosing = true">
            {{ t('automations.inspector.change') }}
          </Button>
        </div>
        <p v-if="serverProblem && !missing.length" class="mt-2 text-xs text-amber-300 light:text-amber-700">{{ serverProblem }}</p>
      </template>
      <template v-else>
        <TriggerPicker :model-value="type" :available="available" dense @pick="pick" />
        <Button variant="ghost" size="sm" class="mt-2" @click="choosing = false">{{ t('common.cancel') }}</Button>
      </template>
    </InspectorSection>

    <InspectorSection v-if="!choosing && hasSettings" :title="t('automations.inspector.narrowIt')" :hint="t('automations.inspector.narrowItHint')">
      <TriggerFields
        :type="type"
        :config="config"
        :disabled="disabled"
        :missing="missing"
        @update:config="v => emit('update:config', v)"
      />
    </InspectorSection>

    <InspectorSection v-if="!choosing" :title="t('automations.inspector.whoFor')">
      <SegmentedChoice
        :model-value="showFilter ? 'some' : 'everyone'"
        :options="[
          { value: 'everyone', label: t('automations.inspector.everyone') },
          { value: 'some', label: t('automations.inspector.onlySome') }
        ]"
        :disabled="disabled"
        :aria-label="t('automations.inspector.whoFor')"
        @update:model-value="setAudience"
      />
      <div v-if="showFilter" class="mt-3 space-y-2">
        <p class="text-xs leading-relaxed text-muted-foreground">{{ t('automations.inspector.whoForHint') }}</p>
        <FilterBuilder
          :model-value="filter"
          :fields="lookups.state.filterFields"
          @update:model-value="v => emit('update:filter', v)"
        />
      </div>
    </InspectorSection>
  </div>
</template>
