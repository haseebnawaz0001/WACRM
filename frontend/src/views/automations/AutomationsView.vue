<script setup lang="ts">
/**
 * The automations list (plan 08).
 *
 * Each row says what the rule does in a sentence and whether it can be
 * trusted right now: on and working, on and failing, or not finished. A rule
 * nobody can read is a rule nobody will leave switched on, and a draft nobody
 * can see is unfinished is a draft that stays that way.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger
} from '@/components/ui/dropdown-menu'
import { PageHeader, ErrorState, DeleteConfirmDialog } from '@/components/shared'
import StatusDot from '@/components/shared/StatusDot.vue'
import { automationsService, type Automation } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { Zap, Plus, MoreHorizontal, ArrowRight, Split, Hourglass } from 'lucide-vue-next'
import { unwrapResponse, unwrapListResponse } from '@/lib/api-utils'
import { useLookups } from '@/components/automations/useLookups'
import { triggerSentence, stepSentence } from '@/components/automations/flow/sentences'
import { triggerDef } from '@/components/automations/flow/triggers'
import { toneTile } from '@/components/automations/flow/steps'
import { recipes } from '@/components/automations/flow/recipes'
import { countSteps, CONDITION, WAIT, type FlowStep } from '@/components/automations/flow/tree'

const { t } = useI18n()
const router = useRouter()
const authStore = useAuthStore()
const lookups = useLookups()

const automations = ref<Automation[]>([])
const isLoading = ref(true)
const fetchError = ref(false)

const canWrite = computed(() => authStore.hasPermission('automations', 'write'))
const canDelete = computed(() => authStore.hasPermission('automations', 'delete'))

async function fetchAutomations() {
  try {
    automations.value = unwrapListResponse<Automation>(await automationsService.list(), 'automations')
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

onMounted(() => {
  fetchAutomations()
  lookups.ensure('tags', 'users', 'teams', 'templates', 'pipelines', 'taskTypes', 'fields', 'filterFields', 'variables')
})

function problemsOf(rule: Automation): number {
  return rule.problems?.length ?? 0
}

async function toggle(rule: Automation, enabled: boolean) {
  if (enabled && problemsOf(rule)) {
    // Not an error to shout about: it is unfinished, and the builder shows
    // exactly what is missing.
    router.push(`/automations/${rule.id}`)
    return
  }
  // Optimistic, then corrected: a switch that waits for a round trip feels broken.
  rule.enabled = enabled
  try {
    const response = enabled
      ? await automationsService.enable(rule.id)
      : await automationsService.disable(rule.id)
    Object.assign(rule, unwrapResponse<{ automation: Automation }>(response).automation)
  } catch (error: any) {
    rule.enabled = !enabled
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function duplicate(rule: Automation) {
  try {
    const copy = unwrapResponse<{ automation: Automation }>(
      await automationsService.create({
        ...rule,
        name: t('automations.copyOf', { name: rule.name }),
        enabled: false
      } as any)
    ).automation
    router.push(`/automations/${copy.id}`)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

const deleting = ref<Automation | null>(null)
async function confirmDelete() {
  const rule = deleting.value
  if (!rule) return
  try {
    await automationsService.delete(rule.id)
    automations.value = automations.value.filter(a => a.id !== rule.id)
    toast.success(t('common.deletedSuccess'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    deleting.value = null
  }
}

/** "When a tag is added (VIP) → Create a follow-up, and 2 more steps". */
function summary(rule: Automation) {
  const trigger = triggerSentence(t, lookups, rule.trigger_type, rule.trigger_config)
  const steps = (rule.actions || []) as FlowStep[]
  const first = steps[0] ? stepSentence(t, lookups, lookups.state.filterFields, steps[0]).title : ''
  const more = countSteps(steps) - (steps[0] ? 1 : 0)
  return {
    when: trigger.detail ? `${trigger.title} (${trigger.detail})` : trigger.title,
    then: first
      ? more > 0 ? t('automations.list.thenMore', { step: first, n: more }, more) : first
      : t('automations.list.noSteps')
  }
}

function hasKind(rule: Automation, kind: string): boolean {
  const walk = (steps: FlowStep[] = []): boolean =>
    steps.some(s => s.type === kind || walk(s.then) || walk(s.else))
  return walk(rule.actions as FlowStep[])
}

function status(rule: Automation) {
  if (rule.enabled) {
    const failures = rule.stats?.failures_24h
    if (failures) return { label: t('automations.builder.statusOnFailing', { n: failures }, failures), tone: 'bad' as const }
    return { label: t('automations.builder.statusOn'), tone: 'good' as const }
  }
  const n = problemsOf(rule)
  if (n) return { label: t('automations.builder.statusDraft', { n }, n), tone: 'warn' as const }
  return { label: t('automations.list.off'), tone: 'neutral' as const }
}

function relative(when?: string | null): string {
  if (!when) return t('automations.neverRun')
  const minutes = Math.round((Date.now() - new Date(when).getTime()) / 60000)
  if (minutes < 1) return t('automations.justNow')
  if (minutes < 60) return t('automations.minutesAgo', { count: minutes })
  const hours = Math.round(minutes / 60)
  if (hours < 24) return t('automations.hoursAgo', { count: hours })
  return t('automations.daysAgo', { count: Math.round(hours / 24) })
}

const suggestions = computed(() => recipes.slice(0, 3))

async function useRecipe(key: string) {
  const recipe = recipes.find(r => r.key === key)
  if (!recipe) return
  try {
    const created = unwrapResponse<{ automation: Automation }>(await automationsService.create(recipe.build(t) as any)).automation
    router.push(`/automations/${created.id}`)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}
</script>

<template>
  <div class="flex h-full flex-col">
    <PageHeader :title="t('automations.title')" :description="t('automations.description')" :icon="Zap">
      <template #actions>
        <Button v-if="canWrite" size="sm" @click="router.push('/automations/new')">
          <Plus class="mr-1.5 h-4 w-4" />
          {{ t('automations.new') }}
        </Button>
      </template>
    </PageHeader>

    <div class="flex-1 overflow-y-auto">
      <div class="mx-auto max-w-5xl px-4 py-6 sm:px-6">
        <ErrorState v-if="fetchError" :message="t('automations.loadFailed')" @retry="fetchAutomations" />

        <div v-else-if="isLoading" class="space-y-2">
          <div v-for="i in 5" :key="i" class="h-[72px] animate-pulse rounded-md bg-white/[0.03] light:bg-gray-100" />
        </div>

        <!-- First run: explain by example, not with a paragraph. -->
        <div v-else-if="!automations.length" class="py-6">
          <h2 class="text-lg font-semibold text-white light:text-gray-900">{{ t('automations.empty.title') }}</h2>
          <p class="mt-1.5 max-w-2xl text-sm leading-relaxed text-muted-foreground">{{ t('automations.empty.body') }}</p>
          <ul v-if="canWrite" class="mt-6 divide-y divide-white/[0.06] rounded-lg border border-white/[0.08] light:divide-gray-100 light:border-gray-200">
            <li v-for="recipe in suggestions" :key="recipe.key">
              <button
                type="button"
                class="group flex w-full items-center gap-4 px-4 py-3.5 text-left hover:bg-white/[0.03] light:hover:bg-gray-50"
                @click="useRecipe(recipe.key)"
              >
                <span :class="['flex h-8 w-8 shrink-0 items-center justify-center rounded-md', toneTile.trigger]">
                  <component :is="recipe.icon" class="h-4 w-4" />
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block text-sm font-medium">{{ t(`automations.recipes.${recipe.key}.name`) }}</span>
                  <span class="block text-xs text-muted-foreground">{{ t(`automations.recipes.${recipe.key}.summary`) }}</span>
                </span>
                <ArrowRight class="h-4 w-4 shrink-0 text-white/25 group-hover:text-white/70 light:text-gray-300 light:group-hover:text-gray-600" />
              </button>
            </li>
          </ul>
          <Button v-if="canWrite" variant="outline" size="sm" class="mt-4" @click="router.push('/automations/new')">
            {{ t('automations.empty.browse') }}
          </Button>
        </div>

        <ul v-else class="divide-y divide-white/[0.06] rounded-lg border border-white/[0.08] light:divide-gray-100 light:border-gray-200">
          <li
            v-for="rule in automations"
            :key="rule.id"
            class="group flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3.5 transition-colors hover:bg-white/[0.02] light:hover:bg-gray-50/70"
          >
            <span :class="['flex h-9 w-9 shrink-0 items-center justify-center rounded-md', toneTile.trigger]">
              <component :is="triggerDef(rule.trigger_type)?.icon || Zap" class="h-4 w-4" />
            </span>
            <button type="button" class="min-w-0 flex-1 basis-64 text-left" @click="router.push(`/automations/${rule.id}`)">
              <span class="flex items-center gap-2">
                <span class="truncate text-sm font-medium">{{ rule.name }}</span>
                <Split v-if="hasKind(rule, CONDITION)" class="h-3.5 w-3.5 shrink-0 text-amber-400/80 light:text-amber-600" :aria-label="t('automations.list.hasQuestion')" />
                <Hourglass v-if="hasKind(rule, WAIT)" class="h-3.5 w-3.5 shrink-0 text-sky-400/80 light:text-sky-600" :aria-label="t('automations.list.hasWait')" />
              </span>
              <span class="mt-0.5 flex min-w-0 items-center gap-1.5 text-[13px] text-muted-foreground">
                <span class="truncate">{{ summary(rule).when }}</span>
                <ArrowRight class="h-3 w-3 shrink-0 opacity-60" />
                <span class="truncate">{{ summary(rule).then }}</span>
              </span>
            </button>

            <div class="flex items-center gap-4">
              <div class="hidden w-40 text-right sm:block">
                <StatusDot :label="status(rule).label" :tone="status(rule).tone" class="justify-end text-xs" />
                <p class="mt-0.5 text-[11px] tabular-nums text-muted-foreground">
                  {{ relative(rule.last_run_at) }}<template v-if="rule.stats?.runs_24h"> · {{ t('automations.runs24h', { count: rule.stats.runs_24h }) }}</template>
                </p>
              </div>
              <Switch
                :model-value="rule.enabled"
                :disabled="!canWrite"
                :aria-label="t('automations.enabledLabel', { name: rule.name })"
                @update:model-value="(value: boolean) => toggle(rule, value)"
              />
              <DropdownMenu v-if="canWrite">
                <DropdownMenuTrigger as-child>
                  <Button variant="ghost" size="icon" class="h-8 w-8" :aria-label="t('common.actions')">
                    <MoreHorizontal class="h-4 w-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem @click="router.push(`/automations/${rule.id}`)">{{ t('automations.list.open') }}</DropdownMenuItem>
                  <DropdownMenuItem @click="duplicate(rule)">{{ t('automations.duplicate') }}</DropdownMenuItem>
                  <template v-if="canDelete">
                    <DropdownMenuSeparator />
                    <DropdownMenuItem class="text-destructive focus:text-destructive" @click="deleting = rule">
                      {{ t('common.delete') }}
                    </DropdownMenuItem>
                  </template>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </li>
        </ul>
      </div>
    </div>

    <DeleteConfirmDialog
      :open="!!deleting"
      :title="t('automations.builder.deleteTitle')"
      :description="t('automations.builder.deleteDescription', { name: deleting?.name || '' })"
      :confirm-label="t('common.delete')"
      @update:open="v => { if (!v) deleting = null }"
      @confirm="confirmDelete"
    />
  </div>
</template>
