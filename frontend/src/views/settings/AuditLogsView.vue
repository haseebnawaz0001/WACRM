<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { PageHeader, DataTable, DateRangePicker, type Column } from '@/components/shared'
import {
  auditLogsService,
  type AuditLogEntry,
  type AuditActionOption,
  type AuditResourceType,
} from '@/services/api'
import { useUsersStore } from '@/stores/users'
import { useDateRange } from '@/composables/useDateRange'
import { ScrollText } from 'lucide-vue-next'
import { formatDate, formatLabel } from '@/lib/utils'
import StatusDot from '@/components/shared/StatusDot.vue'

const { t } = useI18n()
const usersStore = useUsersStore()

const logs = ref<AuditLogEntry[]>([])
const isLoading = ref(true)

// Pagination
const currentPage = ref(1)
const totalItems = ref(0)
const pageSize = 25

// Sort
const sortKey = ref('created_at')
const sortDirection = ref<'asc' | 'desc'>('desc')

// Filters
const filterUser = ref('all')
const filterAction = ref('all')
const filterResourceType = ref('all')

// Filter options come from the server: the hand-written list here offered ten
// resource types against the twenty-nine the audit log actually records, so
// most changes were logged and then unfindable (plan 10, S9).
const resourceTypes = ref<AuditResourceType[]>([])
const actionOptions = ref<AuditActionOption[]>([])

const groupedResourceTypes = computed(() => {
  const groups = new Map<string, AuditResourceType[]>()
  for (const rt of resourceTypes.value) {
    const list = groups.get(rt.group) ?? []
    list.push(rt)
    groups.set(rt.group, list)
  }
  return [...groups.entries()].sort((a, b) => a[0].localeCompare(b[0]))
})

async function fetchCatalog() {
  try {
    const response = await auditLogsService.catalog()
    const data = (response.data as any).data || response.data
    resourceTypes.value = data.resource_types || []
    actionOptions.value = data.actions || []
  } catch {
    resourceTypes.value = []
    actionOptions.value = []
  }
}

// Date range
const {
  selectedRange, customDateRange, isDatePickerOpen,
  dateRange, formatDateRangeDisplay, applyCustomRange: baseApplyCustomRange,
} = useDateRange()

function applyCustomRange() {
  baseApplyCustomRange()
  applyFilter()
}

watch(selectedRange, (val) => {
  if (val !== 'custom') applyFilter()
})

const columns = computed<Column<AuditLogEntry>[]>(() => [
  { key: 'user_name', label: t('auditLogs.user'), sortable: true },
  { key: 'action', label: t('auditLogs.action') },
  { key: 'resource_type', label: t('auditLogs.resource') },
  { key: 'changes', label: t('auditLogs.changes') },
  { key: 'created_at', label: t('auditLogs.date'), sortable: true },
])

function handlePageChange(page: number) {
  currentPage.value = page
  fetchLogs()
}

async function fetchLogs() {
  isLoading.value = true
  try {
    const { from, to } = dateRange.value
    const params: Record<string, any> = { page: currentPage.value, limit: pageSize, from, to }
    if (filterUser.value && filterUser.value !== 'all') params.user_id = filterUser.value
    if (filterAction.value && filterAction.value !== 'all') params.action = filterAction.value
    if (filterResourceType.value && filterResourceType.value !== 'all') params.resource_type = filterResourceType.value

    const response = await auditLogsService.list(params)
    const data = (response.data as any).data || response.data
    logs.value = data.audit_logs || []
    totalItems.value = data.total || 0
  } catch {
    logs.value = []
    totalItems.value = 0
  } finally {
    isLoading.value = false
  }
}

function applyFilter() {
  currentPage.value = 1
  fetchLogs()
}

/**
 * Every row in this table records an action, so every row wore a filled badge
 * and the page was a column of colour with nothing standing out. Deletion is
 * the one people scan an audit log for; it keeps the loud colour, and the
 * routine two settle down.
 */
function actionTone(action: string): 'good' | 'warn' | 'bad' | 'neutral' {
  switch (action) {
    case 'created': return 'good'
    case 'deleted': return 'bad'
    default: return 'neutral'
  }
}

function changeSummary(log: AuditLogEntry): string {
  if (!log.changes || log.changes.length === 0) return '—'
  // "10 fields set" next to a bare "7 fields" read as two different kinds of
  // fact. Both are a count of what the action touched, so both say so.
  if (log.action === 'created') return `${log.changes.length} fields set`
  if (log.action === 'deleted') return `${log.changes.length} fields removed`
  return log.changes.map(c => formatLabel(c.field)).join(', ')
}

onMounted(async () => {
  await Promise.all([usersStore.fetchUsers(), fetchCatalog()])
  fetchLogs()
})
</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <PageHeader
      :title="t('auditLogs.title')"
      :description="t('auditLogs.description')"
      :icon="ScrollText"
    />

    <ScrollArea class="flex-1">
      <div class="p-6">
        <div>
          <Card>
            <!-- The header above says "Track all changes made by users across
                 the platform"; this said "Changes made across the platform by
                 all users". The same sentence, turned around, one line below
                 itself. -->
            <CardHeader class="pb-4">
              <div class="flex items-center justify-end flex-wrap gap-4">
                <div class="flex items-center gap-2 flex-wrap">
                  <Select v-model="filterUser" @update:model-value="applyFilter">
                    <SelectTrigger class="w-[180px]" :aria-label="$t('auditLogs.allUsers')">
                      <SelectValue :placeholder="t('auditLogs.allUsers')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">{{ t('auditLogs.allUsers') }}</SelectItem>
                      <SelectItem v-for="user in usersStore.users" :key="user.id" :value="user.id">
                        {{ user.full_name || user.email }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <Select v-model="filterAction" @update:model-value="applyFilter">
                    <SelectTrigger class="w-[140px]" :aria-label="$t('auditLogs.allActions')">
                      <SelectValue :placeholder="t('auditLogs.allActions')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">{{ t('auditLogs.allActions') }}</SelectItem>
                      <SelectItem
                        v-for="option in actionOptions"
                        :key="option.value"
                        :value="option.value"
                      >
                        {{ t(`auditLogs.${option.value}`, option.label) }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <Select v-model="filterResourceType" @update:model-value="applyFilter">
                    <SelectTrigger class="w-[180px]" :aria-label="$t('auditLogs.allResources')">
                      <SelectValue :placeholder="t('auditLogs.allResources')" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">{{ t('auditLogs.allResources') }}</SelectItem>
                      <SelectGroup v-for="[group, items] in groupedResourceTypes" :key="group">
                        <SelectLabel>{{ group }}</SelectLabel>
                        <SelectItem v-for="rt in items" :key="rt.value" :value="rt.value">
                          {{ rt.label }}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <DateRangePicker
                    v-model:selected-range="selectedRange"
                    v-model:custom-date-range="customDateRange"
                    v-model:is-date-picker-open="isDatePickerOpen"
                    :format-date-range-display="formatDateRangeDisplay"
                    @apply-custom="applyCustomRange"
                  />
                </div>
              </div>
            </CardHeader>
            <CardContent>
              <DataTable
                :items="logs"
                :columns="columns"
                :is-loading="isLoading"
                :empty-icon="ScrollText"
                :empty-title="t('auditLogs.noLogs')"
                :empty-description="t('auditLogs.noLogsDesc')"
                v-model:sort-key="sortKey"
                v-model:sort-direction="sortDirection"
                server-pagination
                :current-page="currentPage"
                :total-items="totalItems"
                :page-size="pageSize"
                item-name="audit logs"
                @page-change="handlePageChange"
              >
                <template #cell-user_name="{ item: log }">
                  <div class="py-1">
                    <RouterLink
                      :to="`/settings/audit-logs/${log.id}`"
                      class="font-medium hover:opacity-80"
                    >
                      {{ log.user_name }}
                    </RouterLink>
                  </div>
                </template>

                <template #cell-action="{ item: log }">
                  <div class="py-1">
                    <StatusDot :label="t(`auditLogs.${log.action}`)" :tone="actionTone(log.action)" />
                  </div>
                </template>

                <template #cell-resource_type="{ item: log }">
                  <div class="py-1">
                    <span class="text-sm text-muted-foreground">{{ formatLabel(log.resource_type) }}</span>
                  </div>
                </template>

                <template #cell-changes="{ item: log }">
                  <div class="py-1">
                    <span class="text-sm text-muted-foreground">{{ changeSummary(log) }}</span>
                  </div>
                </template>

                <template #cell-created_at="{ item: log }">
                  <div class="py-1">
                    <span class="text-muted-foreground text-sm">{{ formatDate(log.created_at) }}</span>
                  </div>
                </template>
              </DataTable>
            </CardContent>
          </Card>
        </div>
      </div>
    </ScrollArea>
  </div>
</template>
