<script setup lang="ts">
/**
 * CRM Reports (plan 09).
 *
 * The dashboard could already count messages and campaigns. It could not say
 * where new contacts come from, how many leads become customers, who is behind
 * on follow-ups, or how fast anyone actually replies — which are the questions
 * that decide what a team does next week.
 *
 * Every figure is read live rather than from a rollup, because these numbers
 * are looked at precisely when somebody is deciding something, and a rollup is
 * wrong between the change and the next job tick.
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PageHeader, ErrorState } from '@/components/shared'
import {
  reportsService,
  type ContactsBySourceReport, type LifecycleFunnelReport,
  type PipelineFunnelReport, type PipelineForecastReport,
  type AgentPerformanceReport, type TaskAgentRow,
  type CampaignRepliesReport
} from '@/services/api'
import { unwrapResponse, unwrapListResponse } from '@/lib/api-utils'
import { useAuthStore } from '@/stores/auth'
import { PieChart, Download } from 'lucide-vue-next'

const { t, locale } = useI18n()
const authStore = useAuthStore()

const from = ref(isoDaysAgo(30))
const to = ref(isoDaysAgo(0))

const contacts = ref<ContactsBySourceReport | null>(null)
const lifecycle = ref<LifecycleFunnelReport | null>(null)
const agents = ref<AgentPerformanceReport | null>(null)
const tasks = ref<TaskAgentRow[]>([])
const pipeline = ref<PipelineFunnelReport | null>(null)
const forecast = ref<PipelineForecastReport | null>(null)
const campaigns = ref<CampaignRepliesReport | null>(null)

const isLoading = ref(true)
const fetchError = ref(false)
const pipelineAvailable = ref(false)

const canExport = computed(() => authStore.hasPermission('reports', 'export'))
const canSeeDeals = computed(() => authStore.hasPermission('deals', 'read'))

const range = computed(() => ({ from: from.value, to: to.value }))

function isoDaysAgo(days: number): string {
  const date = new Date()
  date.setDate(date.getDate() - days)
  return date.toISOString().slice(0, 10)
}

async function load() {
  isLoading.value = true
  try {
    const [contactsResult, lifecycleResult, agentsResult, tasksResult, campaignsResult] = await Promise.all([
      reportsService.contactsBySource(range.value),
      reportsService.lifecycleFunnel(range.value),
      reportsService.agentPerformance(range.value),
      reportsService.tasksByAgent(range.value),
      reportsService.campaignReplies(range.value)
    ])
    // Every response is { status, data: … }; reading `.data` off the axios
    // response yields that envelope, not the report.
    contacts.value = unwrapResponse<ContactsBySourceReport>(contactsResult)
    lifecycle.value = unwrapResponse<LifecycleFunnelReport>(lifecycleResult)
    agents.value = unwrapResponse<AgentPerformanceReport>(agentsResult)
    tasks.value = unwrapListResponse<TaskAgentRow>(tasksResult, 'rows')
    campaigns.value = unwrapResponse<CampaignRepliesReport>(campaignsResult)
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }

  if (!canSeeDeals.value) return
  try {
    const [funnelResult, forecastResult] = await Promise.all([
      reportsService.pipelineFunnel(range.value),
      reportsService.pipelineForecast({ ...range.value, months: 6 })
    ])
    pipeline.value = unwrapResponse<PipelineFunnelReport>(funnelResult)
    forecast.value = unwrapResponse<PipelineForecastReport>(forecastResult)
    pipelineAvailable.value = true
  } catch {
    // No pipeline configured is not an error, just a tab worth hiding.
    pipelineAvailable.value = false
  }
}

watch([from, to], load)

function download(key: string) {
  window.open(reportsService.exportUrl(key, range.value), '_blank')
}

// --- Formatting ---

/** Seconds read badly on a dashboard; "4m 12s" is what people compare. */
function duration(seconds?: number): string {
  if (seconds === undefined || seconds === null) return '—'
  if (seconds < 60) return t('crmReports.seconds', { count: Math.round(seconds) })
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return t('crmReports.minutes', { count: minutes })
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return t('crmReports.hoursMinutes', { hours, minutes: minutes % 60 })
  return t('crmReports.days', { count: Math.round(hours / 24) })
}

function percent(value: number): string {
  return `${value.toFixed(1)}%`
}

function money(value: number): string {
  return new Intl.NumberFormat(locale.value, {
    style: 'currency', currency: 'USD', maximumFractionDigits: 0
  }).format(value)
}

/** A bar's width as a share of the biggest value in its list. */
function widthOf(value: number, max: number): string {
  if (max <= 0) return '0%'
  return `${Math.max(2, (value / max) * 100)}%`
}

const maxLifecycle = computed(() =>
  Math.max(1, ...(lifecycle.value?.steps || []).map(step => step.reached))
)
const maxPipeline = computed(() =>
  Math.max(1, ...(pipeline.value?.steps || []).map(step => step.reached))
)

onMounted(load)
</script>

<template>
  <!-- <main> is overflow-hidden; a view without its own scroller clips
       everything past the fold, with no scrollbar to say so. -->
  <div class="flex h-full flex-col">
    <PageHeader
      :title="t('crmReports.title')"
      :description="t('crmReports.description')"
      :icon="PieChart"
    />

    <ScrollArea class="flex-1">
      <div class="space-y-4 p-4">

    <!-- Range -->
    <div class="flex flex-wrap items-end gap-3">
      <div class="space-y-1.5">
        <Label class="text-xs">{{ t('crmReports.from') }}</Label>
        <Input v-model="from" type="date" class="w-40" />
      </div>
      <div class="space-y-1.5">
        <Label class="text-xs">{{ t('crmReports.to') }}</Label>
        <Input v-model="to" type="date" class="w-40" />
      </div>
      <p class="pb-2 text-xs text-muted-foreground">{{ t('crmReports.timezoneNote') }}</p>
    </div>

    <ErrorState v-if="fetchError" :message="t('crmReports.loadFailed')" @retry="load" />
    <p v-else-if="isLoading" class="text-muted-foreground">{{ t('common.loading') }}</p>

    <Tabs v-else default-value="overview">
      <TabsList>
        <TabsTrigger value="overview">{{ t('crmReports.tabOverview') }}</TabsTrigger>
        <TabsTrigger value="agents">{{ t('crmReports.tabAgents') }}</TabsTrigger>
        <TabsTrigger v-if="pipelineAvailable" value="pipeline">
          {{ t('crmReports.tabPipeline') }}
        </TabsTrigger>
      </TabsList>

      <!-- Overview -->
      <TabsContent value="overview" class="space-y-4">
        <!-- R6: did the campaign work, as opposed to arrive (plan 09)? -->
        <Card>
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('crmReports.campaignReplies') }}</CardTitle>
            <Button v-if="canExport" variant="ghost" size="sm" @click="download('campaign-replies')">
              <Download class="mr-1.5 h-4 w-4" />
              {{ t('crmReports.export') }}
            </Button>
          </CardHeader>
          <CardContent>
            <p v-if="!campaigns?.rows?.length" class="text-sm text-muted-foreground">
              {{ t('crmReports.noCampaigns') }}
            </p>
            <template v-else>
              <table class="w-full text-sm">
                <thead class="text-left text-xs text-muted-foreground">
                  <tr>
                    <th class="pb-2 font-medium">{{ t('crmReports.campaign') }}</th>
                    <th class="pb-2 text-right font-medium">{{ t('crmReports.delivered') }}</th>
                    <th class="pb-2 text-right font-medium">{{ t('crmReports.replied') }}</th>
                    <th class="pb-2 text-right font-medium">{{ t('crmReports.replyRate') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="row in campaigns.rows" :key="row.campaign_id" class="border-t">
                    <td class="py-2">{{ row.name }}</td>
                    <td class="py-2 text-right">{{ row.delivered }}</td>
                    <td class="py-2 text-right">{{ row.replied }}</td>
                    <td class="py-2 text-right">{{ row.reply_rate.toFixed(1) }}%</td>
                  </tr>
                </tbody>
              </table>
              <p class="mt-3 text-xs text-muted-foreground">{{ campaigns.counting_rule }}</p>
            </template>
          </CardContent>
        </Card>

        <Card>
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('crmReports.contactsBySource') }}</CardTitle>
            <Button v-if="canExport" variant="ghost" size="sm" @click="download('contacts-by-source')">
              <Download class="mr-1.5 h-4 w-4" />
              {{ t('crmReports.export') }}
            </Button>
          </CardHeader>
          <CardContent>
            <p v-if="!contacts?.totals.length" class="text-sm text-muted-foreground">
              {{ t('crmReports.noContacts') }}
            </p>
            <table v-else class="w-full text-sm">
              <thead class="text-left text-xs text-muted-foreground">
                <tr>
                  <th class="pb-2 font-medium">{{ t('crmReports.source') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.contacts') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.share') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.becameCustomer') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y">
                <tr v-for="row in contacts.totals" :key="row.source">
                  <td class="py-1.5">{{ row.source }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ row.contacts }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ percent(row.share) }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ row.became_customer }}</td>
                </tr>
              </tbody>
            </table>
          </CardContent>
        </Card>

        <Card>
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('crmReports.lifecycleFunnel') }}</CardTitle>
            <Button v-if="canExport" variant="ghost" size="sm" @click="download('lifecycle-funnel')">
              <Download class="mr-1.5 h-4 w-4" />
              {{ t('crmReports.export') }}
            </Button>
          </CardHeader>
          <CardContent class="space-y-2">
            <div v-for="(step, index) in lifecycle?.steps || []" :key="step.key" class="space-y-1">
              <div class="flex items-baseline justify-between text-sm">
                <span>{{ step.label }}</span>
                <span class="tabular-nums">
                  {{ step.reached }}
                  <span v-if="index > 0" class="ml-2 text-xs text-muted-foreground">
                    {{ percent(step.conversion) }}
                  </span>
                </span>
              </div>
              <!-- A horizontal bar rather than a chart library: the value is
                   already written next to it, so the bar only has to make the
                   shape of the drop-off visible. -->
              <div class="h-2 rounded bg-muted">
                <div
                  class="h-2 rounded bg-primary"
                  :style="{ width: widthOf(step.reached, maxLifecycle) }"
                />
              </div>
            </div>
            <p v-if="lifecycle?.note" class="pt-1 text-xs text-muted-foreground">
              {{ lifecycle.note }}
            </p>
          </CardContent>
        </Card>
      </TabsContent>

      <!-- Agents -->
      <TabsContent value="agents" class="space-y-4">
        <Card>
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('crmReports.agentPerformance') }}</CardTitle>
            <Button v-if="canExport" variant="ghost" size="sm" @click="download('agent-performance')">
              <Download class="mr-1.5 h-4 w-4" />
              {{ t('crmReports.export') }}
            </Button>
          </CardHeader>
          <CardContent>
            <p v-if="!agents?.rows.length" class="text-sm text-muted-foreground">
              {{ t('crmReports.noAgentData') }}
            </p>
            <template v-else>
              <div class="overflow-x-auto">
                <table class="w-full text-sm">
                  <thead class="text-left text-xs text-muted-foreground">
                    <tr>
                      <th class="pb-2 font-medium">{{ t('crmReports.agent') }}</th>
                      <th class="pb-2 text-right font-medium">{{ t('crmReports.handled') }}</th>
                      <th class="pb-2 text-right font-medium">{{ t('crmReports.firstResponseMedian') }}</th>
                      <th class="pb-2 text-right font-medium">{{ t('crmReports.firstResponseP90') }}</th>
                      <th class="pb-2 text-right font-medium">{{ t('crmReports.resolutionMedian') }}</th>
                      <th class="pb-2 text-right font-medium">{{ t('crmReports.reopened') }}</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y">
                    <tr v-for="row in agents.rows" :key="row.user_id">
                      <td class="py-1.5">{{ row.name }}</td>
                      <td class="py-1.5 text-right tabular-nums">{{ row.handled }}</td>
                      <td class="py-1.5 text-right tabular-nums">
                        {{ duration(row.first_response_median_seconds) }}
                      </td>
                      <td class="py-1.5 text-right tabular-nums">
                        {{ duration(row.first_response_p90_seconds) }}
                      </td>
                      <td class="py-1.5 text-right tabular-nums">
                        {{ duration(row.resolution_median_seconds) }}
                      </td>
                      <td class="py-1.5 text-right tabular-nums">{{ percent(row.reopened_rate) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p class="pt-2 text-xs text-muted-foreground">{{ agents.note }}</p>
            </template>
          </CardContent>
        </Card>

        <Card>
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('crmReports.tasksByAgent') }}</CardTitle>
            <Button v-if="canExport" variant="ghost" size="sm" @click="download('tasks-by-agent')">
              <Download class="mr-1.5 h-4 w-4" />
              {{ t('crmReports.export') }}
            </Button>
          </CardHeader>
          <CardContent>
            <p v-if="!tasks.length" class="text-sm text-muted-foreground">
              {{ t('crmReports.noTaskData') }}
            </p>
            <table v-else class="w-full text-sm">
              <thead class="text-left text-xs text-muted-foreground">
                <tr>
                  <th class="pb-2 font-medium">{{ t('crmReports.agent') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.open') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.overdue') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.dueToday') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.completed') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.onTime') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y">
                <tr v-for="row in tasks" :key="row.user_id">
                  <td class="py-1.5">{{ row.name }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ row.open }}</td>
                  <td class="py-1.5 text-right tabular-nums">
                    <Badge v-if="row.overdue > 0" variant="destructive" class="px-1.5 py-0">
                      {{ row.overdue }}
                    </Badge>
                    <span v-else>0</span>
                  </td>
                  <td class="py-1.5 text-right tabular-nums">{{ row.due_today }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ row.completed }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ percent(row.on_time_rate) }}</td>
                </tr>
              </tbody>
            </table>
          </CardContent>
        </Card>
      </TabsContent>

      <!-- Pipeline -->
      <TabsContent v-if="pipelineAvailable" value="pipeline" class="space-y-4">
        <Card>
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('crmReports.pipelineFunnel') }}</CardTitle>
            <Button v-if="canExport" variant="ghost" size="sm" @click="download('pipeline-funnel')">
              <Download class="mr-1.5 h-4 w-4" />
              {{ t('crmReports.export') }}
            </Button>
          </CardHeader>
          <CardContent class="space-y-3">
            <div class="flex flex-wrap items-center gap-4 text-sm">
              <span>
                <span class="text-muted-foreground">{{ t('crmReports.winRate') }}:</span>
                <strong class="ml-1 tabular-nums">{{ percent(pipeline?.win_rate || 0) }}</strong>
              </span>
              <span class="text-muted-foreground">
                {{ t('crmReports.wonLost', { won: pipeline?.won || 0, lost: pipeline?.lost || 0 }) }}
              </span>
              <span class="text-xs text-muted-foreground">{{ t('crmReports.winRateNote') }}</span>
            </div>

            <div v-for="(step, index) in pipeline?.steps || []" :key="step.key" class="space-y-1">
              <div class="flex items-baseline justify-between text-sm">
                <span>{{ step.label }}</span>
                <span class="tabular-nums">
                  {{ step.reached }}
                  <span v-if="index > 0" class="ml-2 text-xs text-muted-foreground">
                    {{ percent(step.conversion) }}
                  </span>
                  <span v-if="step.median_days_from_previous" class="ml-2 text-xs text-muted-foreground">
                    {{ t('crmReports.medianDays', { days: step.median_days_from_previous.toFixed(1) }) }}
                  </span>
                </span>
              </div>
              <div class="h-2 rounded bg-muted">
                <div class="h-2 rounded bg-primary" :style="{ width: widthOf(step.reached, maxPipeline) }" />
              </div>
            </div>
          </CardContent>
        </Card>

        <Card v-if="pipeline?.lost_reasons?.length">
          <CardHeader><CardTitle class="text-base">{{ t('crmReports.lostReasons') }}</CardTitle></CardHeader>
          <CardContent>
            <table class="w-full text-sm">
              <tbody class="divide-y">
                <tr v-for="row in pipeline.lost_reasons" :key="row.reason">
                  <td class="py-1.5">{{ row.reason }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ row.count }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ money(row.value) }}</td>
                </tr>
              </tbody>
            </table>
          </CardContent>
        </Card>

        <Card v-if="forecast">
          <CardHeader><CardTitle class="text-base">{{ t('crmReports.forecast') }}</CardTitle></CardHeader>
          <CardContent class="space-y-3">
            <table class="w-full text-sm">
              <thead class="text-left text-xs text-muted-foreground">
                <tr>
                  <th class="pb-2 font-medium">{{ t('crmReports.month') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.deals') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.value') }}</th>
                  <th class="pb-2 text-right font-medium">{{ t('crmReports.weighted') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y">
                <tr v-for="row in forecast.months" :key="row.month">
                  <td class="py-1.5">{{ row.month }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ row.deals }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ money(row.value) }}</td>
                  <td class="py-1.5 text-right tabular-nums">{{ money(row.weighted_value) }}</td>
                </tr>
              </tbody>
            </table>

            <!-- Named rather than hidden: a forecast that quietly omits them
                 looks complete when it is not. -->
            <p v-if="forecast.overdue" class="text-sm text-destructive">
              {{ t('crmReports.overdueDeals', {
                count: forecast.overdue, value: money(forecast.overdue_value)
              }) }}
            </p>
            <p v-if="forecast.undated" class="text-sm text-muted-foreground">
              {{ t('crmReports.undatedDeals', {
                count: forecast.undated, value: money(forecast.undated_value)
              }) }}
            </p>
          </CardContent>
        </Card>
      </TabsContent>
    </Tabs>
      </div>
    </ScrollArea>
  </div>
</template>
