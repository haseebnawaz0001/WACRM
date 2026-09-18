<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Progress } from '@/components/ui/progress'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { campaignsService } from '@/services/api'
import { wsService } from '@/services/websocket'
import { toast } from 'vue-sonner'
import { PageHeader, DataTable, DeleteConfirmDialog, SearchInput, IconButton, ErrorState, DateRangePicker, type Column } from '@/components/shared'
import { getErrorMessage } from '@/lib/api-utils'
import {
  Plus,
  Pencil,
  Trash2,
  Megaphone,
  Play,
  Pause,
  CheckCircle,
  Clock,
  AlertCircle,
} from 'lucide-vue-next'
import { formatDate } from '@/lib/utils'
import { useSearchPagination } from '@/composables/useSearchPagination'
import { useDateRange } from '@/composables/useDateRange'

const { t } = useI18n()

interface Campaign {
  id: string
  name: string
  template_name: string
  template_id?: string
  whatsapp_account?: string
  header_media_id?: string
  header_media_filename?: string
  header_media_mime_type?: string
  status: 'draft' | 'scheduled' | 'running' | 'paused' | 'completed' | 'failed' | 'queued' | 'processing' | 'cancelled'
  total_recipients: number
  sent_count: number
  delivered_count: number
  read_count: number
  failed_count: number
  scheduled_at?: string
  started_at?: string
  completed_at?: string
  created_at: string
}

const campaigns = ref<Campaign[]>([])
const isLoading = ref(true)

const columns = computed<Column<Campaign>[]>(() => [
  { key: 'name', label: t('campaigns.campaign'), sortable: true },
  { key: 'template', label: t('campaigns.template', 'Template') },
  { key: 'status', label: t('campaigns.status'), sortable: true },
  { key: 'stats', label: t('campaigns.progress') },
  { key: 'created_at', label: t('campaigns.created'), sortable: true },
  { key: 'actions', label: t('common.actions'), align: 'right' },
])

const sortKey = ref('created_at')
const sortDirection = ref<'asc' | 'desc'>('desc')
const { searchQuery, currentPage, totalItems, pageSize, handlePageChange, resetAndFetch } = useSearchPagination({
  fetchFn: () => fetchCampaigns(),
})

// Filter state
const filterStatus = ref<string>('all')
const {
  selectedRange,
  customDateRange,
  isDatePickerOpen,
  dateRange,
  formatDateRangeDisplay,
  applyCustomRange: applyCustomRangeBase,
} = useDateRange()

const statusOptions = computed(() => [
  { value: 'all', label: t('campaigns.allStatuses') },
  { value: 'draft', label: t('campaigns.draft') },
  { value: 'queued', label: t('campaigns.queued') },
  { value: 'processing', label: t('campaigns.processing') },
  { value: 'completed', label: t('campaigns.completed') },
  { value: 'failed', label: t('campaigns.failed') },
  { value: 'cancelled', label: t('campaigns.cancelled') },
  { value: 'paused', label: t('campaigns.paused') },
])

// AlertDialog state
const deleteDialogOpen = ref(false)
const campaignToDelete = ref<Campaign | null>(null)
const isDeletingCampaign = ref(false)

// Error state
const error = ref<string | null>(null)

// WebSocket subscription for real-time stats updates
let unsubscribeCampaignStats: (() => void) | null = null

onMounted(async () => {
  await fetchCampaigns()

  // Subscribe to campaign stats updates
  unsubscribeCampaignStats = wsService.onCampaignStatsUpdate((payload) => {
    const campaign = campaigns.value.find(c => c.id === payload.campaign_id)
    if (campaign) {
      campaign.sent_count = payload.sent_count
      campaign.delivered_count = payload.delivered_count
      campaign.read_count = payload.read_count
      campaign.failed_count = payload.failed_count
      if (payload.status) {
        campaign.status = payload.status
      }
    }
  })
})

onUnmounted(() => {
  if (unsubscribeCampaignStats) {
    unsubscribeCampaignStats()
  }
})

async function fetchCampaigns() {
  isLoading.value = true
  error.value = null
  try {
    const { from, to } = dateRange.value
    const params: Record<string, string | number> = {
      from,
      to,
      page: currentPage.value,
      limit: pageSize
    }
    if (filterStatus.value && filterStatus.value !== 'all') {
      params.status = filterStatus.value
    }
    if (searchQuery.value) {
      params.search = searchQuery.value
    }
    const response = await campaignsService.list(params)
    // API returns: { status: "success", data: { campaigns: [...], total: N } }
    const data = response.data.data || response.data
    campaigns.value = data.campaigns || []
    totalItems.value = data.total ?? campaigns.value.length
  } catch (err: any) {
    console.error('Failed to fetch campaigns:', err)
    error.value = getErrorMessage(err, t('campaigns.fetchFailed'))
    campaigns.value = []
    totalItems.value = 0
  } finally {
    isLoading.value = false
  }
}

function applyCustomRange() {
  applyCustomRangeBase()
  fetchCampaigns()
}

// Watch for filter changes
watch([filterStatus, selectedRange], () => {
  if (selectedRange.value !== 'custom') {
    resetAndFetch()
  }
})

function openDeleteDialog(campaign: Campaign) {
  campaignToDelete.value = campaign
  deleteDialogOpen.value = true
}

async function confirmDeleteCampaign() {
  if (!campaignToDelete.value) return

  isDeletingCampaign.value = true
  try {
    await campaignsService.delete(campaignToDelete.value.id)
    toast.success(t('common.deletedSuccess', { resource: t('resources.Campaign') }))
    deleteDialogOpen.value = false
    campaignToDelete.value = null
    await fetchCampaigns()
  } catch (error: any) {
    toast.error(getErrorMessage(error, t('common.failedDelete', { resource: t('resources.campaign') })))
  } finally {
    isDeletingCampaign.value = false
  }
}

function getStatusIcon(status: string) {
  switch (status) {
    case 'completed':
      return CheckCircle
    case 'running':
    case 'processing':
    case 'queued':
      return Play
    case 'paused':
      return Pause
    case 'scheduled':
      return Clock
    case 'failed':
    case 'cancelled':
      return AlertCircle
    default:
      return Megaphone
  }
}

/**
 * The badge variant for a status.
 *
 * This used to hand-paint `border-green-600 text-green-600` onto an outline
 * badge — reinventing variants the component already has, in colours that
 * cleared neither mode's contrast floor: green-600 sits near 4:1 on the dark
 * background and on white, under the 4.5:1 that 12px text needs. The variants
 * carry a tuned pair for each mode.
 */
type StatusVariant = 'success' | 'info' | 'warning' | 'destructive' | 'secondary' | 'outline'

function getStatusVariant(status: string): StatusVariant {
  switch (status) {
    case 'completed':
      return 'success'
    case 'running':
    case 'processing':
    case 'queued':
      return 'info'
    case 'paused':
      return 'warning'
    case 'failed':
    case 'cancelled':
      return 'destructive'
    case 'scheduled':
      return 'secondary'
    default:
      return 'outline'
  }
}

/**
 * The word for a status, not the value the column stores.
 *
 * The badge printed `campaign.status` straight from the API, so an English
 * reader got "processing" in lower case and everybody else got English.
 * The filter beside it has had the translated labels all along.
 */
function statusLabel(status: string): string {
  const key = `campaigns.${status}`
  const label = t(key)
  return label === key ? status : label
}

function getProgressPercentage(campaign: Campaign): number {
  if (campaign.total_recipients === 0) return 0
  return Math.round((campaign.sent_count / campaign.total_recipients) * 100)
}

</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <PageHeader
      :title="$t('campaigns.title')"
      :description="$t('campaigns.subtitle')"
      :icon="Megaphone"
    >
      <template #actions>
        <RouterLink to="/campaigns/new">
          <Button variant="outline" size="sm">
            <Plus class="h-4 w-4 mr-2" />
            {{ $t('campaigns.createCampaign') }}
          </Button>
        </RouterLink>
      </template>
    </PageHeader>

    <!-- Campaigns List -->
    <ScrollArea class="flex-1">
      <div class="p-6">
        <div>
          <Card>
            <!--
              The card used to open with "Your Campaigns / Bulk messaging
              campaigns for your customers", directly under a page header
              reading "Campaigns / Manage bulk messaging campaigns". One thing,
              named twice, pushing the filters and the first row down the page.
              The header names it; the card gets on with showing it.
            -->
            <CardHeader class="pb-4">
              <div class="flex items-center justify-end flex-wrap gap-4">
                <div class="flex items-center gap-2 flex-wrap">
                  <Select v-model="filterStatus">
                    <SelectTrigger class="w-[140px]">
                      <SelectValue :placeholder="$t('campaigns.allStatuses')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="opt in statusOptions" :key="opt.value" :value="opt.value">
                        {{ opt.label }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <DateRangePicker
                    v-model:selected-range="selectedRange"
                    v-model:custom-date-range="customDateRange"
                    v-model:is-date-picker-open="isDatePickerOpen"
                    :format-date-range-display="formatDateRangeDisplay"
                    @apply-custom="applyCustomRange"
                  />
                  <SearchInput v-model="searchQuery" :placeholder="$t('campaigns.searchCampaigns') + '...'" class="w-48" />
                </div>
              </div>
            </CardHeader>
            <CardContent>
              <ErrorState
                v-if="error && !isLoading"
                :title="$t('campaigns.fetchFailedTitle')"
                :description="error"
                :retry-label="$t('common.retry')"
                @retry="fetchCampaigns"
              />
              <DataTable
                v-else
                :items="campaigns"
                :columns="columns"
                :is-loading="isLoading"
                :empty-icon="Megaphone"
                :empty-title="searchQuery ? $t('campaigns.noMatchingCampaigns') : $t('campaigns.noCampaignsYet')"
                :empty-description="searchQuery ? $t('campaigns.noMatchingCampaignsDesc') : $t('campaigns.noCampaignsYetDesc')"
                v-model:sort-key="sortKey"
                v-model:sort-direction="sortDirection"
                server-pagination
                :current-page="currentPage"
                :total-items="totalItems"
                :page-size="pageSize"
                item-name="campaigns"
                @page-change="handlePageChange"
              >
                <template #cell-name="{ item: campaign }">
                  <RouterLink :to="`/campaigns/${campaign.id}`" class="font-medium hover:opacity-80">{{ campaign.name }}</RouterLink>
                </template>
                <template #cell-template="{ item: campaign }">
                  <span class="text-sm text-muted-foreground">{{ campaign.template_name || '—' }}</span>
                </template>
                <template #cell-status="{ item: campaign }">
                  <Badge :variant="getStatusVariant(campaign.status)" class="text-xs">
                    <component :is="getStatusIcon(campaign.status)" class="h-3 w-3 mr-1" />
                    {{ statusLabel(campaign.status) }}
                  </Badge>
                </template>
                <!--
                  Four bare numbers used to sit here, told apart only by being
                  green or blue, with the word that named each one hidden in a
                  `title` nobody hovers and no touch device shows. A draft read
                  "8 0 0": two of those numbers could not be anything but zero
                  yet. Every number now carries its noun, zeros that cannot mean
                  anything are left out, and only failure is coloured.
                -->
                <template #cell-stats="{ item: campaign }">
                  <div class="space-y-1">
                    <div v-if="campaign.status === 'running' || campaign.status === 'processing'" class="w-32 space-y-1">
                      <Progress :model-value="getProgressPercentage(campaign)" class="h-1.5" />
                      <span class="text-xs text-muted-foreground">
                        {{ campaign.sent_count }} / {{ campaign.total_recipients }} {{ $t('campaigns.sent').toLowerCase() }}
                      </span>
                    </div>
                    <div class="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5 text-xs">
                      <span class="whitespace-nowrap">
                        <span class="font-medium tabular-nums">{{ campaign.total_recipients }}</span>
                        <span class="ml-1 text-muted-foreground">{{ $t('campaigns.recipients').toLowerCase() }}</span>
                      </span>
                      <span v-if="campaign.delivered_count > 0" class="whitespace-nowrap">
                        <span class="font-medium tabular-nums">{{ campaign.delivered_count }}</span>
                        <span class="ml-1 text-muted-foreground">{{ $t('campaigns.delivered').toLowerCase() }}</span>
                      </span>
                      <span v-if="campaign.read_count > 0" class="whitespace-nowrap">
                        <span class="font-medium tabular-nums">{{ campaign.read_count }}</span>
                        <span class="ml-1 text-muted-foreground">{{ $t('campaigns.read').toLowerCase() }}</span>
                      </span>
                      <span v-if="campaign.failed_count > 0" class="whitespace-nowrap text-destructive">
                        <span class="font-medium tabular-nums">{{ campaign.failed_count }}</span>
                        <span class="ml-1">{{ $t('campaigns.failed').toLowerCase() }}</span>
                      </span>
                    </div>
                  </div>
                </template>
                <template #cell-created_at="{ item: campaign }">
                  <span class="text-muted-foreground text-sm">{{ formatDate(campaign.created_at) }}</span>
                </template>
                <template #cell-actions="{ item: campaign }">
                  <div class="flex items-center justify-end gap-1">
                    <RouterLink :to="`/campaigns/${campaign.id}`"><IconButton :icon="Pencil" :label="$t('campaigns.editCampaign')" class="h-8 w-8" /></RouterLink>
                    <IconButton
                      :icon="Trash2"
                      :label="$t('campaigns.deleteCampaign')"
                      class="h-8 w-8 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                      :disabled="campaign.status === 'running' || campaign.status === 'processing'"
                      @click="openDeleteDialog(campaign)"
                    />
                  </div>
                </template>
                <template #empty-action>
                  <RouterLink v-if="!searchQuery" to="/campaigns/new">
                    <Button variant="outline" size="sm">
                      <Plus class="h-4 w-4 mr-2" />
                      {{ $t('campaigns.createCampaign') }}
                    </Button>
                  </RouterLink>
                </template>
              </DataTable>
            </CardContent>
          </Card>
        </div>
      </div>
    </ScrollArea>

    <DeleteConfirmDialog
      v-model:open="deleteDialogOpen"
      :title="$t('campaigns.deleteCampaign')"
      :item-name="campaignToDelete?.name"
      :is-submitting="isDeletingCampaign"
      @confirm="confirmDeleteCampaign"
    />

    <!-- Media Preview Dialog -->
  </div>
</template>
