<script setup lang="ts">
/**
 * "Try it on a contact" — the step that makes a rule safe to switch on.
 *
 * Pick a real customer and see, on the canvas and in words, exactly what
 * would happen to them: which way each question goes, what each message would
 * say with their name filled in, where it would stop. Nothing is sent and
 * nothing is changed. It tests what is on screen, saved or not.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Search, Check, X, CircleSlash, Hourglass, FlaskConical, Loader2 } from 'lucide-vue-next'
import { automationsService, contactsService, type Automation, type AutomationRun } from '@/services/api'
import { unwrapResponse } from '@/lib/api-utils'
import { toast } from 'vue-sonner'
import { findStep, CONDITION, WAIT, type FlowStep } from '../flow/tree'
import { stepSentence } from '../flow/sentences'
import { useLookups } from '../useLookups'
import InspectorSection from './InspectorSection.vue'

const props = defineProps<{
  ruleId: string
  /** The rule as it is on screen. */
  draft: () => Partial<Automation>
  steps: FlowStep[]
  /** Step id → what it still needs, in the builder's own words. */
  problems?: Record<string, string>
}>()
const emit = defineEmits<{ result: [run: AutomationRun | null, name: string]; select: [id: string] }>()

const { t } = useI18n()
const lookups = useLookups()

interface ContactHit { id: string; profile_name?: string; phone_number?: string }

const query = ref('')
const hits = ref<ContactHit[]>([])
const contact = ref<ContactHit | null>(null)
const run = ref<AutomationRun | null>(null)
const running = ref(false)

let timer: ReturnType<typeof setTimeout> | undefined
watch(query, q => {
  clearTimeout(timer)
  if (q.trim().length < 2) {
    hits.value = []
    return
  }
  timer = setTimeout(async () => {
    try {
      const response = await contactsService.list({ search: q.trim(), limit: 8 })
      const data = (response.data as any)?.data ?? response.data
      hits.value = data?.contacts || []
    } catch {
      hits.value = []
    }
  }, 220)
})

async function tryOn(target: ContactHit) {
  contact.value = target
  hits.value = []
  query.value = ''
  running.value = true
  try {
    const response = await automationsService.test(props.ruleId, target.id, props.draft())
    run.value = unwrapResponse<{ run: AutomationRun }>(response).run
    emit('result', run.value, target.profile_name || target.phone_number || '')
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    running.value = false
  }
}

function clear() {
  run.value = null
  contact.value = null
  emit('result', null, '')
}

const name = computed(() => contact.value?.profile_name || contact.value?.phone_number || '')

interface Line { id: string; icon: any; tone: string; text: string; preview?: string; error?: string; fixable?: boolean }

/** What the output of a step would put in front of someone. */
function previewOf(output?: Record<string, any>): string | undefined {
  if (!output) return undefined
  const text = output.text ?? output.content ?? output.body ?? output.title
  if (typeof text === 'string' && text.trim()) return text
  if (output.params && typeof output.params === 'object') {
    const values = Object.values(output.params).filter(v => typeof v === 'string' && v)
    if (values.length) return (values as string[]).join(' · ')
  }
  return undefined
}

const lines = computed<Line[]>(() => (run.value?.action_results?.list || []).map(result => {
  const step = findStep(props.steps, result.id)
  const title = step
    ? stepSentence(t, lookups, lookups.state.filterFields, step).title
    : t(`automations.steps.${result.type}.title`, result.type)
  if (result.type === CONDITION && result.status === 'succeeded') {
    const yes = result.output?.branch === 'then'
    return { id: result.id, icon: Check, tone: 'good', text: yes ? t('automations.test.wentYes', { step: title }) : t('automations.test.wentNo', { step: title }) }
  }
  if (result.type === WAIT && result.status === 'succeeded') {
    return { id: result.id, icon: Hourglass, tone: 'wait', text: t('automations.test.wouldWait', { step: title }) }
  }
  if (result.status === 'failed') {
    // The engine's reason is written for developers ("…needs a team_id").
    // A step that is not finished says what it needs, as its card does;
    // anything else is described, not quoted.
    const problem = props.problems?.[result.id]
    return {
      id: result.id,
      icon: X,
      tone: 'bad',
      text: t('automations.test.couldNot', { step: title }),
      error: problem || t('automations.test.failedHere'),
      fixable: !!step
    }
  }
  if (result.status === 'skipped') {
    return { id: result.id, icon: CircleSlash, tone: 'muted', text: t('automations.test.skipped', { step: title }) }
  }
  return { id: result.id, icon: Check, tone: 'good', text: t('automations.test.would', { step: title }), preview: previewOf(result.output) }
}))

const toneClass: Record<string, string> = {
  good: 'text-emerald-400 light:text-emerald-600',
  bad: 'text-red-400 light:text-red-600',
  wait: 'text-sky-400 light:text-sky-600',
  muted: 'text-white/40 light:text-gray-400'
}
</script>

<template>
  <div>
    <InspectorSection :title="t('automations.test.title')" :hint="t('automations.test.hint')">
      <div class="relative">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          v-model="query"
          class="h-10 w-full rounded-sm border border-white/[0.1] bg-white/[0.04] pl-9 pr-3 text-sm outline-none placeholder:text-white/40 focus-visible:border-emerald-500/60 focus-visible:ring-2 focus-visible:ring-emerald-500/30 light:border-gray-200 light:bg-white light:placeholder:text-gray-400"
          :placeholder="t('automations.test.searchPlaceholder')"
          :aria-label="t('automations.test.searchPlaceholder')"
        >
      </div>
      <ul v-if="hits.length" class="mt-2 max-h-56 overflow-y-auto rounded-md border border-white/[0.08] p-1 light:border-gray-200">
        <li v-for="hit in hits" :key="hit.id">
          <button
            type="button"
            class="flex w-full items-baseline justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
            @click="tryOn(hit)"
          >
            <span class="truncate">{{ hit.profile_name || hit.phone_number }}</span>
            <span v-if="hit.profile_name" class="shrink-0 text-xs tabular-nums text-muted-foreground">{{ hit.phone_number }}</span>
          </button>
        </li>
      </ul>
    </InspectorSection>

    <InspectorSection v-if="running">
      <p class="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 class="h-4 w-4 animate-spin" />{{ t('automations.test.running', { name }) }}
      </p>
    </InspectorSection>

    <InspectorSection v-else-if="run" :title="t('automations.test.resultFor', { name })">
      <p v-if="run.status === 'skipped'" class="text-sm leading-relaxed">
        {{ t(`automations.test.skip.${run.skip_reason}`, { name }, t('automations.test.skip.other', { name })) }}
      </p>
      <template v-else>
        <ol class="space-y-2.5">
          <li v-for="line in lines" :key="line.id" class="flex gap-2.5 text-sm">
            <component :is="line.icon" :class="['mt-0.5 h-4 w-4 shrink-0', toneClass[line.tone]]" />
            <div class="min-w-0 flex-1">
              <p class="leading-5">{{ line.text }}</p>
              <p
                v-if="line.preview"
                class="mt-1 whitespace-pre-line rounded-sm border border-white/[0.08] bg-white/[0.03] px-2.5 py-1.5 text-xs leading-relaxed text-white/75 light:border-gray-200 light:bg-gray-50 light:text-gray-700"
              >{{ line.preview }}</p>
              <p v-if="line.error" class="mt-0.5 text-xs text-red-300 light:text-red-600">
                {{ line.error }}
                <button
                  v-if="line.fixable"
                  type="button"
                  class="ml-1 font-medium text-white underline decoration-white/30 underline-offset-2 hover:decoration-white light:text-gray-900 light:decoration-gray-400 light:hover:decoration-gray-900"
                  @click="emit('select', line.id)"
                >{{ t('automations.test.openStep') }}</button>
              </p>
            </div>
          </li>
        </ol>
        <p v-if="!lines.length" class="text-sm text-muted-foreground">{{ t('automations.test.nothing') }}</p>
      </template>
      <p class="mt-4 flex items-center gap-1.5 text-xs text-muted-foreground">
        <FlaskConical class="h-3.5 w-3.5" />{{ t('automations.test.nothingChanged') }}
      </p>
      <div class="mt-3 flex gap-2">
        <Button variant="outline" size="sm" @click="tryOn(contact!)">{{ t('automations.test.again') }}</Button>
        <Button variant="ghost" size="sm" @click="clear">{{ t('automations.test.clear') }}</Button>
      </div>
    </InspectorSection>
  </div>
</template>
