<script setup lang="ts">
/**
 * The automations list (plan 08).
 *
 * Automation in this product used to mean answering messages. Nothing reacted
 * to the record itself changing — a tag added, a task going overdue, a customer
 * going quiet for a week — so the follow-up that mattered most was the one
 * somebody had to remember.
 *
 * The list leads with what each rule does in a sentence, and with whether it
 * has been failing: a rule nobody can read is a rule nobody will trust enough
 * to leave switched on.
 */
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger
} from '@/components/ui/dropdown-menu'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import { PageHeader, ErrorState } from '@/components/shared'
import { automationsService, type Automation } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { Zap, Plus, MoreVertical, AlertTriangle } from 'lucide-vue-next'

const { t } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

const automations = ref<Automation[]>([])
const isLoading = ref(true)
const fetchError = ref(false)

const showRecipes = ref(false)
const canWrite = computed(() => authStore.hasPermission('automations', 'write'))
const canDelete = computed(() => authStore.hasPermission('automations', 'delete'))

/**
 * Recipes exist because a blank rule builder asks people to invent a process
 * before they know what the product can do. These are the four rules teams
 * write first.
 */
const recipes = computed(() => [
  {
    key: 'blank',
    name: t('automations.recipes.blank'),
    body: { name: t('automations.untitled'), trigger_type: 'contact.tag_added', trigger_config: {}, actions: [] }
  },
  {
    key: 'tagToTask',
    name: t('automations.recipes.tagToTask'),
    body: {
      name: t('automations.recipes.tagToTask'),
      trigger_type: 'contact.tag_added',
      trigger_config: { tags: ['VIP'] },
      actions: [{
        id: 'a1', type: 'create_task',
        config: {
          title: t('automations.recipes.tagToTaskTitle'),
          type_key: 'call_back',
          due_in: { amount: 1, unit: 'days' },
          owner: { mode: 'contact_owner' }
        }
      }]
    }
  },
  {
    key: 'quietCustomer',
    name: t('automations.recipes.quietCustomer'),
    body: {
      name: t('automations.recipes.quietCustomer'),
      trigger_type: 'time.no_customer_reply',
      trigger_config: { after: { amount: 3, unit: 'days' } },
      actions: [{
        id: 'a1', type: 'add_tags',
        config: { tags: [t('automations.recipes.quietTag')] }
      }]
    }
  },
  {
    key: 'resolvedTag',
    name: t('automations.recipes.resolvedTag'),
    body: {
      name: t('automations.recipes.resolvedTag'),
      trigger_type: 'conversation.status_changed',
      trigger_config: { to: ['resolved'] },
      actions: [{
        id: 'a1', type: 'add_tags',
        config: { tags: [t('automations.recipes.servedTag')] }
      }]
    }
  }
])

async function fetchAutomations() {
  try {
    const { data } = await automationsService.list()
    automations.value = data.automations || []
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

async function toggle(rule: Automation, enabled: boolean) {
  // Optimistic, then corrected: a switch that waits for a round trip feels
  // broken even when it works.
  rule.enabled = enabled
  try {
    const { data } = enabled
      ? await automationsService.enable(rule.id)
      : await automationsService.disable(rule.id)
    Object.assign(rule, data.automation)
  } catch (error: any) {
    rule.enabled = !enabled
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function createFromRecipe(recipe: { body: Record<string, any> }) {
  try {
    const { data } = await automationsService.create(recipe.body as any)
    showRecipes.value = false
    router.push(`/automations/${data.automation.id}`)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function duplicate(rule: Automation) {
  try {
    const { data } = await automationsService.create({
      ...rule,
      name: t('automations.copyOf', { name: rule.name }),
      enabled: false
    } as any)
    await fetchAutomations()
    router.push(`/automations/${data.automation.id}`)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function remove(rule: Automation) {
  try {
    await automationsService.delete(rule.id)
    automations.value = automations.value.filter(a => a.id !== rule.id)
    toast.success(t('common.deletedSuccess'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

/** One sentence saying what the rule reacts to and what it then does. */
function summarise(rule: Automation): string {
  const trigger = t(`automations.triggers.${rule.trigger_type}`, rule.trigger_type)
  if (!rule.actions?.length) return t('automations.summaryNoActions', { trigger })
  const first = t(`automations.actions.${rule.actions[0].type}`, rule.actions[0].type)
  const rest = rule.actions.length - 1
  return rest > 0
    ? t('automations.summaryMore', { trigger, action: first, count: rest })
    : t('automations.summary', { trigger, action: first })
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

onMounted(fetchAutomations)
</script>

<template>
  <div class="space-y-4 p-4">
    <PageHeader
      :title="t('automations.title')"
      :description="t('automations.description')"
      :icon="Zap"
    >
      <template #actions>
        <Button v-if="canWrite" size="sm" @click="showRecipes = true">
          <Plus class="mr-1.5 h-4 w-4" />
          {{ t('automations.new') }}
        </Button>
      </template>
    </PageHeader>

    <ErrorState v-if="fetchError" :message="t('automations.loadFailed')" @retry="fetchAutomations" />
    <p v-else-if="isLoading" class="text-muted-foreground">{{ t('common.loading') }}</p>

    <Card v-else-if="!automations.length">
      <CardContent class="flex flex-col items-center gap-3 py-10 text-center">
        <Zap class="h-8 w-8 text-muted-foreground" />
        <p class="text-sm text-muted-foreground">{{ t('automations.empty') }}</p>
        <Button v-if="canWrite" size="sm" @click="showRecipes = true">
          {{ t('automations.new') }}
        </Button>
      </CardContent>
    </Card>

    <ul v-else class="space-y-2">
      <li v-for="rule in automations" :key="rule.id">
        <Card class="transition hover:shadow-sm">
          <CardContent class="flex flex-wrap items-center gap-3 p-4">
            <button
              class="min-w-0 flex-1 text-left"
              @click="router.push(`/automations/${rule.id}`)"
            >
              <div class="flex items-center gap-2">
                <span class="truncate font-medium">{{ rule.name }}</span>
                <!-- Failures get a badge rather than a log line: a rule
                     failing quietly is the whole problem. -->
                <Badge
                  v-if="rule.stats?.failures_24h"
                  variant="destructive"
                  class="gap-1 px-1.5 py-0 text-[11px]"
                >
                  <AlertTriangle class="h-3 w-3" />
                  {{ t('automations.failures', { count: rule.stats.failures_24h }) }}
                </Badge>
              </div>
              <p class="mt-0.5 truncate text-sm text-muted-foreground">{{ summarise(rule) }}</p>
            </button>

            <div class="flex items-center gap-3 text-xs text-muted-foreground">
              <span>{{ relative(rule.last_run_at) }}</span>
              <span v-if="rule.stats">{{ t('automations.runs24h', { count: rule.stats.runs_24h }) }}</span>
            </div>

            <Switch
              :model-value="rule.enabled"
              :disabled="!canWrite"
              :aria-label="t('automations.enabledLabel', { name: rule.name })"
              @update:model-value="(value: boolean) => toggle(rule, value)"
            />

            <DropdownMenu v-if="canWrite">
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="icon" :aria-label="t('common.actions')">
                  <MoreVertical class="h-4 w-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem @click="duplicate(rule)">
                  {{ t('automations.duplicate') }}
                </DropdownMenuItem>
                <DropdownMenuItem v-if="canDelete" class="text-destructive" @click="remove(rule)">
                  {{ t('common.delete') }}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </CardContent>
        </Card>
      </li>
    </ul>

    <!-- Recipes -->
    <Dialog v-model:open="showRecipes">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('automations.new') }}</DialogTitle>
          <DialogDescription>{{ t('automations.recipesHint') }}</DialogDescription>
        </DialogHeader>
        <ul class="space-y-2">
          <li v-for="recipe in recipes" :key="recipe.key">
            <button
              class="w-full rounded-md border p-3 text-left text-sm transition hover:bg-accent"
              @click="createFromRecipe(recipe)"
            >
              {{ recipe.name }}
            </button>
          </li>
        </ul>
        <DialogFooter>
          <Button variant="outline" @click="showRecipes = false">{{ t('common.cancel') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
