<script setup lang="ts">
/**
 * Where a new automation starts: from a goal, or from what should set it off.
 *
 * Most people arrive knowing the problem ("customers go quiet and nobody
 * notices"), not the mechanism, so goals come first. Each one shows the shape
 * of its path in miniature — a question here, a wait there — so the step from
 * "that is what I want" to "that is how it works" is visible before the
 * builder opens. People who already know what should start it pick that
 * directly and build the rest themselves.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { PageHeader, ErrorState } from '@/components/shared'
import { automationsService, type Automation, type AutomationCatalog } from '@/services/api'
import { unwrapResponse } from '@/lib/api-utils'
import { toast } from 'vue-sonner'
import { Zap, ArrowRight, Loader2 } from 'lucide-vue-next'
import TriggerPicker from '@/components/automations/inspector/TriggerPicker.vue'
import { recipes, recipeGoals, type Recipe } from '@/components/automations/flow/recipes'
import { CONDITION, WAIT, type FlowStep } from '@/components/automations/flow/tree'
import { toneTile } from '@/components/automations/flow/steps'

const { t } = useI18n()
const router = useRouter()

const catalog = ref<AutomationCatalog | null>(null)
const loadError = ref(false)
const creating = ref<string | null>(null)

onMounted(async () => {
  try {
    catalog.value = unwrapResponse<AutomationCatalog>(await automationsService.catalog())
  } catch {
    loadError.value = true
  }
})

const offered = computed(() => new Set((catalog.value?.triggers || []).map(tr => tr.type)))

/** Only recipes whose trigger this installation can fire. */
const byGoal = computed(() => recipeGoals.map(goal => ({
  goal,
  items: recipes.filter(r => r.goal === goal && (!catalog.value || offered.value.has(r.build(t).trigger_type)))
})).filter(g => g.items.length))

/** The path's shape as marks: a dot per action, a diamond per question, a bar per wait. */
function shape(steps: FlowStep[]): ('action' | 'condition' | 'wait')[] {
  return steps.flatMap(s => [
    s.type === CONDITION ? 'condition' as const : s.type === WAIT ? 'wait' as const : 'action' as const,
    ...shape([...(s.then || []), ...(s.else || [])])
  ])
}

async function create(key: string, body: Partial<Automation>) {
  if (creating.value) return
  creating.value = key
  try {
    const created = unwrapResponse<{ automation: Automation }>(await automationsService.create(body)).automation
    router.push(`/automations/${created.id}`)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
    creating.value = null
  }
}

function useRecipe(recipe: Recipe) {
  create(recipe.key, recipe.build(t) as any)
}

function startFrom(type: string) {
  create(`trigger:${type}`, {
    name: t(`automations.triggers.${type}`, type).replace(/^./, c => c.toUpperCase()),
    trigger_type: type,
    trigger_config: {},
    actions: []
  })
}
</script>

<template>
  <div class="flex h-full flex-col">
    <PageHeader
      :title="t('automations.gallery.title')"
      :description="t('automations.gallery.description')"
      :icon="Zap"
      back-link="/automations"
    />
    <div class="flex-1 overflow-y-auto">
      <ErrorState v-if="loadError" :message="t('automations.loadFailed')" />
      <div v-else class="mx-auto grid max-w-6xl gap-10 px-4 py-8 sm:px-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <!-- Goals -->
        <div class="space-y-8">
          <section v-for="group in byGoal" :key="group.goal">
            <h2 class="text-sm font-semibold text-white light:text-gray-900">{{ t(`automations.gallery.goals.${group.goal}`) }}</h2>
            <ul class="mt-3 divide-y divide-white/[0.06] rounded-lg border border-white/[0.08] light:divide-gray-100 light:border-gray-200">
              <li v-for="recipe in group.items" :key="recipe.key">
                <button
                  type="button"
                  class="group flex w-full items-start gap-4 px-4 py-4 text-left transition-colors duration-150 hover:bg-white/[0.03] focus-visible:bg-white/[0.03] focus-visible:outline-none light:hover:bg-gray-50 light:focus-visible:bg-gray-50"
                  :disabled="!!creating"
                  @click="useRecipe(recipe)"
                >
                  <span :class="['mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-md', toneTile.trigger]">
                    <component :is="recipe.icon" class="h-4 w-4" />
                  </span>
                  <span class="min-w-0 flex-1">
                    <span class="block text-sm font-medium">{{ t(`automations.recipes.${recipe.key}.name`) }}</span>
                    <span class="mt-0.5 block text-[13px] leading-5 text-muted-foreground">{{ t(`automations.recipes.${recipe.key}.summary`) }}</span>
                    <!-- The path, in miniature. -->
                    <span class="mt-2.5 flex items-center gap-1" aria-hidden="true">
                      <span class="h-2 w-2 rounded-full bg-emerald-400/80 light:bg-emerald-500" />
                      <template v-for="(mark, i) in shape(recipe.build(t).actions)" :key="i">
                        <span class="h-px w-3 bg-white/15 light:bg-gray-300" />
                        <span v-if="mark === 'condition'" class="h-2 w-2 rotate-45 bg-violet-400/80 light:bg-violet-500" />
                        <span v-else-if="mark === 'wait'" class="h-1.5 w-3 rounded-full bg-sky-400/70 light:bg-sky-500" />
                        <span v-else class="h-2 w-2 rounded-full border border-white/40 light:border-gray-400" />
                      </template>
                    </span>
                  </span>
                  <Loader2 v-if="creating === recipe.key" class="mt-1 h-4 w-4 shrink-0 animate-spin text-muted-foreground" />
                  <ArrowRight v-else class="mt-1 h-4 w-4 shrink-0 text-white/25 transition-transform duration-150 group-hover:translate-x-0.5 group-hover:text-white/70 light:text-gray-300 light:group-hover:text-gray-600" />
                </button>
              </li>
            </ul>
          </section>
        </div>

        <!-- Or: what should start it -->
        <aside class="lg:sticky lg:top-6 lg:self-start">
          <h2 class="text-sm font-semibold text-white light:text-gray-900">{{ t('automations.gallery.fromTrigger') }}</h2>
          <p class="mt-1 text-[13px] leading-5 text-muted-foreground">{{ t('automations.gallery.fromTriggerHint') }}</p>
          <div class="mt-3 rounded-lg border border-white/[0.08] p-3 light:border-gray-200">
            <TriggerPicker
              v-if="catalog"
              :available="catalog.triggers.map(tr => tr.type)"
              dense
              @pick="startFrom"
            />
            <div v-else class="space-y-2">
              <div v-for="i in 6" :key="i" class="h-10 animate-pulse rounded-md bg-white/[0.03] light:bg-gray-100" />
            </div>
          </div>
        </aside>
      </div>
    </div>
  </div>
</template>
