<script setup lang="ts">
/**
 * What this automation has done, one contact at a time.
 *
 * Each run is the answer to "why did Amara get that message?" — who it was
 * for, how it ended, and a way to see the path they took drawn on the canvas
 * rather than reconstructed from a list of step ids.
 */
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import StatusDot from '@/components/shared/StatusDot.vue'
import { Route, RefreshCw } from 'lucide-vue-next'
import { automationsService, type AutomationRun } from '@/services/api'
import { unwrapListResponse } from '@/lib/api-utils'
import { formatDateTime } from '@/lib/utils'

const props = defineProps<{ ruleId: string }>()
const emit = defineEmits<{ show: [run: AutomationRun] }>()

const { t } = useI18n()
const runs = ref<AutomationRun[]>([])
const loading = ref(true)
const filter = ref<'all' | 'problems' | 'waiting'>('all')

async function load() {
  loading.value = true
  try {
    const status = filter.value === 'waiting' ? 'waiting' : undefined
    const list = unwrapListResponse<AutomationRun>(await automationsService.runs(props.ruleId, { limit: 100, status }), 'runs')
    runs.value = filter.value === 'problems'
      ? list.filter(r => r.status === 'failed' || r.status === 'partially_failed' || r.status === 'cancelled')
      : list.filter(r => !r.dry_run || filter.value === 'all')
  } catch {
    runs.value = []
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(filter, load)
defineExpose({ load })

function tone(status: string): 'good' | 'warn' | 'bad' | 'neutral' {
  if (status === 'succeeded') return 'good'
  if (status === 'failed' || status === 'partially_failed') return 'bad'
  if (status === 'waiting' || status === 'cancelled') return 'warn'
  return 'neutral'
}

function outcome(run: AutomationRun): string {
  if (run.status === 'skipped' || run.status === 'cancelled') {
    return t(`automations.skip.${run.skip_reason}`, run.skip_reason || '')
  }
  const results = run.action_results?.list || []
  const failed = results.find(r => r.status === 'failed')
  if (failed) return t('automations.history.stoppedAt', { step: t(`automations.steps.${failed.type}.title`, failed.type) })
  if (run.status === 'waiting') {
    const until = [...results].reverse().find(r => r.status === 'waiting')?.output?.until
    return until ? t('automations.history.waitingUntil', { when: formatDateTime(until) }) : ''
  }
  return t('automations.history.steps', { n: results.length }, results.length)
}
</script>

<template>
  <div class="mx-auto w-full max-w-3xl px-4 py-6 sm:px-6">
    <div class="mb-4 flex flex-wrap items-center gap-2">
      <div class="flex gap-1 rounded-sm border border-white/[0.1] p-1 light:border-gray-200" role="tablist">
        <button
          v-for="f in (['all', 'problems', 'waiting'] as const)"
          :key="f"
          type="button"
          role="tab"
          :aria-selected="filter === f"
          :class="[
            'rounded-sm px-3 py-1 text-sm transition-colors',
            filter === f ? 'bg-white/[0.1] font-medium text-white light:bg-gray-100 light:text-gray-900' : 'text-white/60 hover:text-white light:text-gray-500 light:hover:text-gray-900'
          ]"
          @click="filter = f"
        >{{ t(`automations.history.filters.${f}`) }}</button>
      </div>
      <Button variant="ghost" size="sm" class="ml-auto" :aria-label="t('common.refresh')" @click="load">
        <RefreshCw class="mr-1.5 h-3.5 w-3.5" />{{ t('common.refresh') }}
      </Button>
    </div>

    <div v-if="loading" class="space-y-2">
      <div v-for="i in 4" :key="i" class="h-14 animate-pulse rounded-md bg-white/[0.03] light:bg-gray-100" />
    </div>

    <div v-else-if="!runs.length" class="rounded-md border border-dashed border-white/10 px-6 py-12 text-center light:border-gray-200">
      <p class="text-sm font-medium">{{ t(`automations.history.empty.${filter}`) }}</p>
      <p class="mt-1 text-xs text-muted-foreground">{{ t('automations.history.emptyHint') }}</p>
    </div>

    <ul v-else class="divide-y divide-white/[0.06] rounded-md border border-white/[0.08] light:divide-gray-100 light:border-gray-200">
      <li v-for="run in runs" :key="run.id" class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
        <div class="min-w-0 flex-1">
          <p class="flex items-center gap-2 text-sm">
            <span class="truncate font-medium">{{ run.contact_name || t('automations.history.noContact') }}</span>
            <span v-if="run.dry_run" class="rounded-full bg-white/[0.06] px-1.5 py-0.5 text-[10px] font-medium text-white/60 light:bg-gray-100 light:text-gray-500">
              {{ t('automations.history.test') }}
            </span>
          </p>
          <p
            class="mt-0.5 truncate text-xs text-muted-foreground"
            :title="run.action_results?.list?.find(r => r.status === 'failed')?.error || undefined"
          >{{ outcome(run) }}</p>
        </div>
        <StatusDot :label="t(`automations.status.${run.status}`, run.status)" :tone="tone(run.status)" />
        <span class="w-36 text-right text-xs tabular-nums text-muted-foreground max-sm:w-auto">{{ formatDateTime(run.started_at) }}</span>
        <Button
          v-if="run.action_results?.list?.length"
          variant="ghost"
          size="sm"
          class="h-8"
          @click="emit('show', run)"
        >
          <Route class="mr-1.5 h-3.5 w-3.5" />{{ t('automations.history.showPath') }}
        </Button>
      </li>
    </ul>
  </div>
</template>
