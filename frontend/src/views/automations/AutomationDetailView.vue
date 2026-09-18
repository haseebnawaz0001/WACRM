<script setup lang="ts">
/**
 * The automation builder (plan 08).
 *
 * A rule reads as a sentence — when this happens, only if that is true, then do
 * these things — so the form is laid out as those three steps rather than as a
 * flat list of fields. The Test button is next to Save on purpose: a rule that
 * can message customers should be checked before it is switched on, and the
 * dry run is what makes that cheap.
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import { PageHeader, FilterBuilder, ErrorState } from '@/components/shared'
import {
  automationsService, contactsService,
  type Automation, type AutomationCatalog, type AutomationRun,
  type FilterNode, type FilterFieldInfo
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { unwrapResponse, unwrapListResponse } from '@/lib/api-utils'
import CrmActionList from '@/components/crmactions/CrmActionList.vue'
import { toast } from 'vue-sonner'
import { formatDateTime } from '@/lib/utils'
import { Zap, FlaskConical, ArrowLeft } from 'lucide-vue-next'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

const rule = ref<Automation | null>(null)
const catalog = ref<AutomationCatalog | null>(null)
const filterFields = ref<FilterFieldInfo[]>([])
const runs = ref<AutomationRun[]>([])

const isLoading = ref(true)
const isSaving = ref(false)
const fetchError = ref(false)

const canWrite = computed(() => authStore.hasPermission('automations', 'write'))

const filter = ref<FilterNode>({ op: 'and', rules: [] })

// --- Loading ---

async function load() {
  try {
    const [ruleResult, catalogResult] = await Promise.all([
      automationsService.get(String(route.params.id)),
      automationsService.catalog()
    ])
    // Same envelope as everywhere else: {status, data}. Reading it directly
    // meant the rule never loaded and the action catalog came back empty, so
    // the picker had nothing in it.
    const loaded = unwrapResponse<{ automation: Automation }>(ruleResult).automation
    rule.value = loaded
    catalog.value = unwrapResponse<AutomationCatalog>(catalogResult)
    filter.value = loaded.contact_filter || { op: 'and', rules: [] }
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }

  try {
    filterFields.value = unwrapListResponse<FilterFieldInfo>(
      await contactsService.filterFields(), 'fields'
    )
  } catch {
    filterFields.value = []
  }
}

async function loadRuns() {
  if (!rule.value) return
  try {
    runs.value = unwrapListResponse<AutomationRun>(
      await automationsService.runs(rule.value.id, { limit: 50 }), 'runs'
    )
  } catch {
    runs.value = []
  }
}

// --- Trigger ---

/** Triggers are grouped so the select reads as a menu rather than a list. */
const triggerGroups = computed(() => {
  const groups: Record<string, string[]> = {}
  for (const trigger of catalog.value?.triggers || []) {
    ;(groups[trigger.group] ||= []).push(trigger.type)
  }
  return groups
})

const selectedTrigger = computed(() =>
  catalog.value?.triggers.find(trigger => trigger.type === rule.value?.trigger_type)
)

/** Which settings the chosen trigger accepts, so the form only shows those. */
function triggerAccepts(key: string): boolean {
  return selectedTrigger.value?.config_keys.includes(key) ?? false
}

/** Comma-separated lists are the fastest way to type a handful of tags. */
function listValue(key: string): string {
  const value = rule.value?.trigger_config?.[key]
  return Array.isArray(value) ? value.join(', ') : ''
}

function setListValue(key: string, raw: string) {
  if (!rule.value) return
  const items = raw.split(',').map(s => s.trim()).filter(Boolean)
  if (items.length) rule.value.trigger_config[key] = items
  else delete rule.value.trigger_config[key]
}

function durationValue(key: string, part: 'amount' | 'unit'): string {
  const value = rule.value?.trigger_config?.[key]
  if (!value) return part === 'unit' ? 'days' : ''
  return String(value[part] ?? (part === 'unit' ? 'days' : ''))
}

function setDuration(key: string, part: 'amount' | 'unit', raw: string) {
  if (!rule.value) return
  const current = rule.value.trigger_config[key] || { amount: 1, unit: 'days' }
  rule.value.trigger_config[key] = {
    ...current,
    [part]: part === 'amount' ? Number(raw) || 0 : raw
  }
}

// --- Actions ---

const actionTypes = computed(() => catalog.value?.actions || [])
const maxActions = computed(() => catalog.value?.limits.max_actions_per_rule ?? 10)

// --- Saving and testing ---

async function save() {
  if (!rule.value) return
  isSaving.value = true
  try {
    const { data: envelope } = await automationsService.update(rule.value.id, {
      ...rule.value,
      contact_filter: filter.value.rules?.length ? filter.value : null
    })
    const data = (envelope as any)?.data ?? envelope
    rule.value = data.automation
    toast.success(t('common.savedSuccess'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    isSaving.value = false
  }
}

async function toggleEnabled(enabled: boolean) {
  if (!rule.value) return
  try {
    const { data } = enabled
      ? await automationsService.enable(rule.value.id)
      : await automationsService.disable(rule.value.id)
    rule.value = data.automation
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

const showTest = ref(false)
const testQuery = ref('')
const testResults = ref<Array<{ id: string; profile_name: string; phone_number: string }>>([])
const testRun = ref<AutomationRun | null>(null)

async function searchTestContacts() {
  if (testQuery.value.length < 2) {
    testResults.value = []
    return
  }
  try {
    const { data: envelope } = await contactsService.list({ search: testQuery.value, limit: 10 })
    const data = (envelope as any)?.data ?? envelope
    testResults.value = data.contacts || data || []
  } catch {
    testResults.value = []
  }
}
watch(testQuery, searchTestContacts)

async function runTest(contactId: string) {
  if (!rule.value) return
  try {
    const { data: envelope } = await automationsService.test(rule.value.id, contactId)
    const data = (envelope as any)?.data ?? envelope
    testRun.value = data.run
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

function statusVariant(status: string) {
  if (status === 'succeeded') return 'default'
  if (status === 'skipped') return 'secondary'
  return 'destructive'
}

onMounted(async () => {
  await load()
  await loadRuns()
})
</script>

<template>
  <!--
    AppLayout's <main> is overflow-hidden, so a view that does not bring its own
    scroller simply clips. This page is a form several cards long: on a 500px
    viewport its content ran to 1226px and 726px of it — including Save — could
    not be reached, with no scrollbar to hint that it was there.
  -->
  <div class="flex h-full flex-col">
    <ErrorState v-if="fetchError" :message="t('automations.loadFailed')" @retry="load" />
    <p v-else-if="isLoading" class="p-4 text-muted-foreground">{{ t('common.loading') }}</p>

    <template v-else-if="rule">
      <PageHeader :title="rule.name" :icon="Zap">
        <template #actions>
          <Button variant="ghost" size="sm" @click="router.push('/automations')">
            <ArrowLeft class="mr-1.5 h-4 w-4" />
            {{ t('common.back') }}
          </Button>
          <Button variant="outline" size="sm" @click="showTest = true">
            <FlaskConical class="mr-1.5 h-4 w-4" />
            {{ t('automations.test') }}
          </Button>
          <div class="flex items-center gap-2 px-2">
            <Switch
              :model-value="rule.enabled"
              :disabled="!canWrite"
              :aria-label="t('automations.enabledLabel', { name: rule.name })"
              @update:model-value="toggleEnabled"
            />
            <span class="text-sm text-muted-foreground">
              {{ rule.enabled ? t('automations.on') : t('automations.off') }}
            </span>
          </div>
          <Button v-if="canWrite" size="sm" :disabled="isSaving" @click="save">
            {{ t('common.save') }}
          </Button>
        </template>
      </PageHeader>

      <ScrollArea class="flex-1">
        <div class="space-y-4 p-4">
      <Tabs default-value="build">
        <TabsList>
          <TabsTrigger value="build">{{ t('automations.tabBuild') }}</TabsTrigger>
          <TabsTrigger value="runs" @click="loadRuns">{{ t('automations.tabRuns') }}</TabsTrigger>
        </TabsList>

        <TabsContent value="build" class="space-y-4">
          <Card>
            <CardContent class="space-y-3 p-4">
              <div class="space-y-1.5">
                <Label>{{ t('automations.name') }}</Label>
                <Input v-model="rule.name" :disabled="!canWrite" />
              </div>
              <div class="space-y-1.5">
                <Label>{{ t('automations.descriptionLabel') }}</Label>
                <Input v-model="rule.description" :disabled="!canWrite" />
              </div>
            </CardContent>
          </Card>

          <!-- 1. When -->
          <Card>
            <CardHeader><CardTitle class="text-base">{{ t('automations.when') }}</CardTitle></CardHeader>
            <CardContent class="space-y-3">
              <Select v-model="rule.trigger_type" :disabled="!canWrite">
                <SelectTrigger :aria-label="t('automations.when')"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup v-for="(types, group) in triggerGroups" :key="group">
                    <SelectLabel>{{ t(`automations.groups.${group}`, group) }}</SelectLabel>
                    <SelectItem v-for="type in types" :key="type" :value="type">
                      {{ t(`automations.triggers.${type}`, type) }}
                    </SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>

              <div v-if="triggerAccepts('tags')" class="space-y-1.5">
                <Label>{{ t('automations.config.tags') }}</Label>
                <Input
                  :model-value="listValue('tags')"
                  :placeholder="t('automations.config.tagsPlaceholder')"
                  :disabled="!canWrite"
                  @update:model-value="v => setListValue('tags', String(v))"
                />
              </div>

              <div v-if="triggerAccepts('to')" class="space-y-1.5">
                <Label>{{ t('automations.config.to') }}</Label>
                <Input
                  :model-value="listValue('to')"
                  :disabled="!canWrite"
                  @update:model-value="v => setListValue('to', String(v))"
                />
              </div>

              <div v-if="triggerAccepts('type_keys')" class="space-y-1.5">
                <Label>{{ t('automations.config.typeKeys') }}</Label>
                <Input
                  :model-value="listValue('type_keys')"
                  :disabled="!canWrite"
                  @update:model-value="v => setListValue('type_keys', String(v))"
                />
              </div>

              <div v-if="triggerAccepts('field')" class="space-y-1.5">
                <Label>{{ t('automations.config.field') }}</Label>
                <Input v-model="rule.trigger_config.field" :disabled="!canWrite" />
              </div>

              <div v-if="triggerAccepts('after')" class="space-y-1.5">
                <Label>{{ t('automations.config.after') }}</Label>
                <div class="flex gap-2">
                  <Input
                    class="w-24"
                    type="number"
                    min="1"
                    :model-value="durationValue('after', 'amount')"
                    :disabled="!canWrite"
                    @update:model-value="v => setDuration('after', 'amount', String(v))"
                  />
                  <Select
                    :model-value="durationValue('after', 'unit')"
                    :disabled="!canWrite"
                    @update:model-value="v => setDuration('after', 'unit', String(v))"
                  >
                    <SelectTrigger class="w-32" aria-label="Unit"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="minutes">{{ t('automations.units.minutes') }}</SelectItem>
                      <SelectItem value="hours">{{ t('automations.units.hours') }}</SelectItem>
                      <SelectItem value="days">{{ t('automations.units.days') }}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>

              <div v-if="triggerAccepts('offset_days')" class="grid gap-3 sm:grid-cols-2">
                <div class="space-y-1.5">
                  <Label>{{ t('automations.config.offsetDays') }}</Label>
                  <Input v-model.number="rule.trigger_config.offset_days" type="number" :disabled="!canWrite" />
                  <p class="text-xs text-muted-foreground">{{ t('automations.config.offsetHint') }}</p>
                </div>
                <div class="space-y-1.5">
                  <Label>{{ t('automations.config.atLocalHour') }}</Label>
                  <Input
                    v-model.number="rule.trigger_config.at_local_hour"
                    type="number" min="0" max="23" :disabled="!canWrite"
                  />
                </div>
              </div>
            </CardContent>
          </Card>

          <!-- 2. Only if -->
          <Card>
            <CardHeader><CardTitle class="text-base">{{ t('automations.onlyIf') }}</CardTitle></CardHeader>
            <CardContent>
              <FilterBuilder v-model="filter" :fields="filterFields" :disabled="!canWrite" />
              <p class="mt-2 text-xs text-muted-foreground">{{ t('automations.onlyIfHint') }}</p>
            </CardContent>
          </Card>

          <!-- 3. Then -->
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('automations.then') }}</CardTitle>
            </CardHeader>
            <CardContent>
              <!-- Shared with the chatbot CRM-action node and keyword rules
                   (plan 10, S7), so the three cannot disagree about what an
                   action needs. -->
              <CrmActionList
                v-model="rule.actions"
                :available-types="actionTypes"
                :editable="canWrite"
                :max-actions="maxActions"
              />
            </CardContent>
          </Card>

          <!-- 4. Settings -->
          <Card>
            <CardHeader><CardTitle class="text-base">{{ t('automations.settings') }}</CardTitle></CardHeader>
            <CardContent class="space-y-3">
              <label class="flex items-center gap-2 text-sm">
                <Switch
                  :model-value="rule.run_policy.once_per_contact"
                  :disabled="!canWrite"
                  @update:model-value="(v: boolean) => rule!.run_policy.once_per_contact = v"
                />
                {{ t('automations.oncePerContact') }}
              </label>
              <div class="grid gap-3 sm:grid-cols-2">
                <div class="space-y-1.5">
                  <Label>{{ t('automations.cooldown') }}</Label>
                  <Input v-model.number="rule.run_policy.cooldown_minutes" type="number" min="0" :disabled="!canWrite" />
                </div>
                <div class="space-y-1.5">
                  <Label>{{ t('automations.maxRunsPerHour') }}</Label>
                  <Input v-model.number="rule.run_policy.max_runs_per_hour" type="number" min="1" :disabled="!canWrite" />
                  <p class="text-xs text-muted-foreground">{{ t('automations.maxRunsHint') }}</p>
                </div>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <!-- Runs -->
        <TabsContent value="runs">
          <Card>
            <CardContent class="p-4">
              <p v-if="!runs.length" class="text-sm text-muted-foreground">
                {{ t('automations.noRuns') }}
              </p>
              <ul v-else class="divide-y">
                <li v-for="run in runs" :key="run.id" class="py-2">
                  <div class="flex flex-wrap items-center gap-2">
                    <Badge :variant="statusVariant(run.status)" class="px-1.5 py-0 text-[11px]">
                      {{ t(`automations.status.${run.status}`, run.status) }}
                    </Badge>
                    <span v-if="run.skip_reason" class="text-xs text-muted-foreground">
                      {{ t(`automations.skip.${run.skip_reason}`, run.skip_reason) }}
                    </span>
                    <span class="ml-auto text-xs text-muted-foreground">
                      {{ formatDateTime(run.started_at) }}
                    </span>
                  </div>
                  <ul v-if="run.action_results?.list?.length" class="mt-1 space-y-0.5 pl-1">
                    <li
                      v-for="result in run.action_results.list"
                      :key="result.id"
                      class="text-xs text-muted-foreground"
                    >
                      {{ t(`automations.actions.${result.type}`, result.type) }} —
                      {{ t(`automations.status.${result.status}`, result.status) }}
                      <span v-if="result.error" class="text-destructive">: {{ result.error }}</span>
                    </li>
                  </ul>
                </li>
              </ul>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
        </div>
      </ScrollArea>
    </template>

    <!-- Test -->
    <Dialog v-model:open="showTest">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('automations.test') }}</DialogTitle>
          <DialogDescription>{{ t('automations.testHint') }}</DialogDescription>
        </DialogHeader>

        <Input v-model="testQuery" :placeholder="t('automations.testContactPlaceholder')" />
        <ul v-if="testResults.length" class="max-h-40 overflow-y-auto rounded-md border">
          <li
            v-for="contact in testResults"
            :key="contact.id"
            class="cursor-pointer px-3 py-1.5 text-sm hover:bg-accent"
            @click="runTest(contact.id)"
          >
            {{ contact.profile_name || contact.phone_number }}
          </li>
        </ul>

        <template v-if="testRun">
          <Separator />
          <div class="space-y-2">
            <div class="flex items-center gap-2">
              <Badge :variant="statusVariant(testRun.status)">
                {{ t(`automations.status.${testRun.status}`, testRun.status) }}
              </Badge>
              <span v-if="testRun.skip_reason" class="text-sm text-muted-foreground">
                {{ t(`automations.skip.${testRun.skip_reason}`, testRun.skip_reason) }}
              </span>
            </div>
            <ul class="space-y-1">
              <li
                v-for="result in testRun.action_results?.list || []"
                :key="result.id"
                class="text-sm"
              >
                <span class="font-medium">
                  {{ t(`automations.actions.${result.type}`, result.type) }}
                </span>
                — {{ t(`automations.status.${result.status}`, result.status) }}
                <span v-if="result.error" class="text-destructive">: {{ result.error }}</span>
                <pre
                  v-if="result.output"
                  class="mt-0.5 overflow-x-auto rounded bg-muted p-2 text-xs"
                >{{ JSON.stringify(result.output, null, 2) }}</pre>
              </li>
            </ul>
          </div>
        </template>

        <DialogFooter>
          <Button variant="outline" @click="showTest = false; testRun = null">
            {{ t('common.close') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
