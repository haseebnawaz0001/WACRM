<script setup lang="ts">
/**
 * The rule as a whole, when no card is selected.
 *
 * "What this does" reads the canvas back as a few sentences — "When a tag is
 * added, it will check whether… If yes, it will…" — the check that the
 * picture says what the person meant, in the words they would use to explain
 * it to a colleague. Each step's words open its card. Below it: what still
 * stops the rule from running, how it is doing, and how often one customer
 * can go through it.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Textarea } from '@/components/ui/textarea'
import DurationInput from '@/components/crmactions/fields/DurationInput.vue'
import SegmentedChoice from '@/components/crmactions/fields/SegmentedChoice.vue'
import { FIELD } from '@/components/crmactions/fields/styles'
import { AlertTriangle, CheckCircle2, ChevronRight } from 'lucide-vue-next'
import type { AutomationRunPolicy } from '@/services/api'
import type { Paragraph, ProseRun } from '../flow/sentences'
import InspectorSection from './InspectorSection.vue'

export interface ProblemItem { id: string; title: string; message: string }

const props = defineProps<{
  prose: Paragraph[]
  problems: ProblemItem[]
  enabled: boolean
  stats?: { runs_24h: number; failures_24h: number }
  waitingTotal: number
  policy: AutomationRunPolicy
  description: string
  disabled?: boolean
}>()
const emit = defineEmits<{
  select: [id: string]
  'update:policy': [value: AutomationRunPolicy]
  'update:description': [value: string]
}>()

const { t } = useI18n()

/** A paragraph's runs, with neighbouring runs of one step joined into one link. */
function segments(runs: ProseRun[]): { step?: string; runs: ProseRun[] }[] {
  const out: { step?: string; runs: ProseRun[] }[] = []
  for (const run of runs) {
    const last = out[out.length - 1]
    if (last && last.step === run.step) last.runs.push(run)
    else out.push({ step: run.step, runs: [run] })
  }
  return out
}

const repeat = computed(() => props.policy.once_per_contact ? 'once' : props.policy.cooldown_minutes > 0 ? 'cooldown' : 'every')

/** The cooldown, shown in the largest unit that divides it. */
const cooldown = computed(() => {
  const m = props.policy.cooldown_minutes || 0
  if (m && m % 1440 === 0) return { amount: m / 1440, unit: 'days' }
  if (m && m % 60 === 0) return { amount: m / 60, unit: 'hours' }
  return { amount: m || 1, unit: m ? 'minutes' : 'days' }
})

function setRepeat(value: string) {
  emit('update:policy', {
    ...props.policy,
    once_per_contact: value === 'once',
    cooldown_minutes: value === 'cooldown' ? (props.policy.cooldown_minutes || 1440) : 0
  })
}

function setCooldown(v: { amount: number; unit: string }) {
  const factor = v.unit === 'days' ? 1440 : v.unit === 'hours' ? 60 : 1
  emit('update:policy', { ...props.policy, cooldown_minutes: Math.max(1, Math.round(v.amount * factor)) })
}

const note = ref(props.description)
watch(() => props.description, v => { note.value = v })
</script>

<template>
  <div>
    <InspectorSection :title="t('automations.overview.whatItDoes')">
      <div class="space-y-2 text-[13.5px] leading-6 text-white/60 light:text-gray-600">
        <p
          v-for="(paragraph, i) in prose"
          :key="i"
          :style="paragraph.depth ? { marginInlineStart: `${(paragraph.depth - 1) * 14}px` } : undefined"
          :class="[
            paragraph.depth ? 'border-l-2 border-white/10 pl-3 light:border-gray-200' : '',
            paragraph.muted ? 'text-xs leading-5 text-muted-foreground' : ''
          ]"
        >
          <template v-for="(segment, j) in segments(paragraph.runs)" :key="j">
            <!-- A span, not a button: a button never wraps across lines, and a
                 step's words have to flow with the sentence they are in. -->
            <span
              v-if="segment.step"
              role="button"
              tabindex="0"
              class="cursor-pointer rounded-sm text-white underline decoration-white/20 decoration-dotted underline-offset-4 transition-colors hover:decoration-white/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40 light:text-gray-900 light:decoration-gray-300 light:hover:decoration-gray-500"
              @click="emit('select', segment.step)"
              @keydown.enter.prevent="emit('select', segment.step)"
              @keydown.space.prevent="emit('select', segment.step)"
            ><span
              v-for="(run, k) in segment.runs"
              :key="k"
              :class="run.value ? 'font-semibold' : ''"
            >{{ run.text }}</span></span>
            <template v-else>
              <span
                v-for="(run, k) in segment.runs"
                :key="k"
                :class="run.value ? 'font-medium text-white light:text-gray-900' : ''"
              >{{ run.text }}</span>
            </template>
          </template>
        </p>
      </div>
    </InspectorSection>

    <InspectorSection :title="problems.length ? t('automations.overview.stillNeeds') : t('automations.overview.readiness')">
      <div v-if="!problems.length" class="flex items-center gap-2 text-sm text-emerald-300 light:text-emerald-700">
        <CheckCircle2 class="h-4 w-4" />
        {{ enabled ? t('automations.overview.running') : t('automations.overview.ready') }}
      </div>
      <ul v-else class="space-y-1">
        <li v-for="p in problems" :key="p.id + p.message">
          <button
            type="button"
            class="flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-left hover:bg-white/[0.05] light:hover:bg-gray-50"
            @click="emit('select', p.id)"
          >
            <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0 text-amber-400 light:text-amber-600" />
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-medium">{{ p.title }}</span>
              <span class="block text-xs text-muted-foreground">{{ p.message }}</span>
            </span>
            <ChevronRight class="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
          </button>
        </li>
      </ul>
      <dl v-if="enabled || stats?.runs_24h || waitingTotal" class="mt-4 grid grid-cols-3 gap-3 text-sm">
        <div>
          <dt class="text-xs text-muted-foreground">{{ t('automations.overview.today') }}</dt>
          <dd class="mt-0.5 text-lg font-semibold tabular-nums">{{ stats?.runs_24h ?? 0 }}</dd>
        </div>
        <div>
          <dt class="text-xs text-muted-foreground">{{ t('automations.overview.failed') }}</dt>
          <dd :class="['mt-0.5 text-lg font-semibold tabular-nums', stats?.failures_24h ? 'text-red-300 light:text-red-600' : '']">
            {{ stats?.failures_24h ?? 0 }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-muted-foreground">{{ t('automations.overview.waitingNow') }}</dt>
          <dd class="mt-0.5 text-lg font-semibold tabular-nums">{{ waitingTotal }}</dd>
        </div>
      </dl>
    </InspectorSection>

    <InspectorSection :title="t('automations.overview.sameContact')" :hint="t('automations.overview.sameContactHint')">
      <SegmentedChoice
        :model-value="repeat"
        :options="[
          { value: 'every', label: t('automations.overview.everyTime') },
          { value: 'once', label: t('automations.overview.onlyOnce') },
          { value: 'cooldown', label: t('automations.overview.notTooOften') }
        ]"
        :disabled="disabled"
        :aria-label="t('automations.overview.sameContact')"
        @update:model-value="setRepeat"
      />
      <div v-if="repeat === 'cooldown'" class="mt-3 space-y-1.5">
        <p class="text-xs text-muted-foreground">{{ t('automations.overview.atMostOnceEvery') }}</p>
        <DurationInput :model-value="cooldown" :disabled="disabled" @update:model-value="setCooldown" />
      </div>
      <div class="mt-5 space-y-1.5">
        <label class="text-[13px] font-medium" for="automation-hourly-cap">{{ t('automations.overview.safetyLimit') }}</label>
        <div class="flex items-center gap-2 text-sm text-muted-foreground">
          <input
            id="automation-hourly-cap"
            type="number"
            min="1"
            :class="[FIELD, 'h-9 w-24 tabular-nums']"
            :value="policy.max_runs_per_hour || 500"
            :disabled="disabled"
            @input="e => emit('update:policy', { ...policy, max_runs_per_hour: Math.max(1, Number((e.target as HTMLInputElement).value) || 1) })"
          >
          {{ t('automations.overview.perHour') }}
        </div>
        <p class="text-xs leading-relaxed text-muted-foreground">{{ t('automations.overview.safetyLimitHint') }}</p>
      </div>
    </InspectorSection>

    <InspectorSection :title="t('automations.overview.note')">
      <Textarea
        v-model="note"
        :rows="2"
        :disabled="disabled"
        :placeholder="t('automations.overview.notePlaceholder')"
        @blur="emit('update:description', note)"
      />
    </InspectorSection>
  </div>
</template>
