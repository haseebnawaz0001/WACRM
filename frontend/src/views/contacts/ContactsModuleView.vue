<script setup lang="ts">
/**
 * The Contacts module (plan 01).
 *
 * Contacts used to live under Settings, which framed them as configuration
 * rather than the core record of a CRM. This is the main-menu list: it filters
 * through the shared FilterBuilder, sorts server-side, and renders whichever
 * custom fields an organization has marked "show in list".
 */
import { ref, onMounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { Card, CardContent } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  PageHeader, SearchInput, DataTable, ErrorState, FilterBuilder,
  CreateContactDialog, ImportExportDialog, type Column
} from '@/components/shared'
import {
  contactsService, contactFieldsService, segmentsService, campaignsService, campaignAudienceService,
  type ContactSearchRow, type ContactField, type FilterNode, type FilterFieldInfo,
  type Segment
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useOrganizationsStore } from '@/stores/organizations'
import { toast } from 'vue-sonner'
import {
  Contact as ContactIcon, SlidersHorizontal, X, ListChecks, Plus, ArrowDownUp,
  Users, Bookmark, Send, Download, RefreshCw, Pencil
} from 'lucide-vue-next'
import { useDebounceFn } from '@vueuse/core'
import { useListViewState, jsonFilterCodec } from '@/composables/useListViewState'
import { unwrapListResponse, unwrapItemResponse, getErrorMessage } from '@/lib/api-utils'
import { formatDate } from '@/lib/utils'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const organizationsStore = useOrganizationsStore()

const contacts = ref<ContactSearchRow[]>([])
const listFields = ref<ContactField[]>([])
const filterFields = ref<FilterFieldInfo[]>([])

const isLoading = ref(true)
const fetchError = ref(false)

/**
 * The list's state lives in the URL (plan 10, S12).
 *
 * A filtered contacts list was previously un-shareable — "the ones tagged VIP
 * with no owner" could only be described in words — and was lost on every
 * reload, which is the moment somebody most wants it back.
 */
const {
  search: searchQuery,
  sortKey,
  sortDir: sortDirection,
  page: currentPage,
  filter: urlFilter
} = useListViewState<FilterNode>({
  defaultSortKey: 'last_message_at',
  defaultSortDir: 'desc',
  ...jsonFilterCodec<FilterNode>()
})

const totalItems = ref(0)
const pageSize = 50


const showFilters = ref(false)
// The FilterBuilder wants a concrete node; the URL carries null when there is
// no filter, so the two are bridged rather than conflated.
const filter = computed<FilterNode>({
  get: () => urlFilter.value ?? { op: 'and', rules: [] },
  set: value => {
    urlFilter.value = value?.rules?.length ? value : null
  }
})

const canManageFields = computed(() => authStore.hasPermission('contact_fields', 'write'))
const canWrite = computed(() => authStore.hasPermission('contacts', 'write'))
const canImportExport = computed(() => authStore.hasPermission('contacts', 'read'))

const createOpen = ref(false)
const importExportOpen = ref(false)

/**
 * Saved segments as views of this list (plan 05).
 *
 * A segment is a question an organization has already answered — "customers in
 * Lahore who have not replied in 30 days" — and it lived on a page of its own,
 * so using one meant leaving the contacts list and rebuilding the same filter
 * by hand. Selecting one here loads its filter, and the list becomes that
 * audience with somewhere to send it.
 */
const segments = ref<Segment[]>([])
const activeSegmentId = ref<string>(String(route.query.segment ?? ''))
const activeSegment = computed(() =>
  segments.value.find((s) => s.id === activeSegmentId.value) ?? null
)
const canUseSegments = computed(() => authStore.hasPermission('segments', 'read'))
const canSendCampaigns = computed(() => authStore.hasPermission('campaigns', 'write'))

async function loadSegments() {
  if (!canUseSegments.value) return
  try {
    segments.value = unwrapListResponse<Segment>(await segmentsService.list(), 'segments')
  } catch {
    // The contacts list works without them; a failed segment load should not
    // take the page with it.
    segments.value = []
  }
}

/** Opens a segment as the current view, or returns to all contacts. */
async function selectSegment(id: string) {
  activeSegmentId.value = id
  currentPage.value = 1
  void router.replace({ query: { ...route.query, segment: id || undefined } })

  if (!id) {
    await fetchContacts()
    return
  }

  // The segment's own filter becomes the list's filter, so the builder shows
  // what the audience actually is and can be adjusted from there.
  const segment = segments.value.find((s) => s.id === id)
  if (segment?.filter) filter.value = segment.filter as FilterNode
  await fetchContacts()
}

async function refreshSegmentCount() {
  const segment = activeSegment.value
  if (!segment) return
  try {
    const { data: envelope } = await segmentsService.count(segment.id)
    const data = (envelope as any)?.data ?? envelope
    segment.contact_count = data.count
    segment.counted_at = new Date().toISOString()
  } catch {
    toast.error(t('segments.countFailed'))
  }
}

/** Exports exactly this audience, rather than whatever filters get rebuilt. */
async function exportSegment() {
  const segment = activeSegment.value
  if (!segment) return
  try {
    const response = await segmentsService.export(segment.id)
    downloadCSV(response.data as unknown as string, `${segment.name}.csv`)
  } catch {
    toast.error(t('common.failedLoad', { resource: t('resources.contacts') }))
  }
}

function downloadCSV(body: string, filename: string) {
  const url = URL.createObjectURL(new Blob([body], { type: 'text/csv;charset=utf-8;' }))
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}

/**
 * Starts a campaign aimed at this segment.
 *
 * The draft is created and aimed in one step so the audience is already set
 * when the campaign opens — picking the segment again on the next screen is
 * the step that makes people target the wrong list.
 */
async function sendCampaign() {
  const segment = activeSegment.value
  if (!segment) return
  try {
    const created = unwrapItemResponse<any>(
      await campaignsService.create({ name: segment.name }), 'campaign'
    )
    await campaignAudienceService.set(created.id, segment.id)
    void router.push(`/campaigns/${created.id}`)
  } catch (e) {
    toast.error(getErrorMessage(e, t('segments.campaignFailed')))
  }
}

/** A new contact goes straight to its profile: creating one is usually the
 *  first step of working on it, not an end in itself. */
function onContactCreated(contact: any) {
  createOpen.value = false
  void fetchContacts()
  if (contact?.id) void router.push(`/contacts/${contact.id}`)
}

/** How many leaf conditions are set, for the badge on the Filters button. */
const activeFilterCount = computed(() => countRules(filter.value))
function countRules(node: FilterNode): number {
  if (!node.rules) return node.field ? 1 : 0
  return node.rules.reduce((sum, r) => sum + countRules(r), 0)
}

// Columns are the fixed ones plus whichever fields the org shows in the list.
const columns = computed<Column<ContactSearchRow>[]>(() => {
  const base: Column<ContactSearchRow>[] = [
    { key: 'contact', label: t('contacts.contact'), sortable: true, sortKey: 'profile_name' },
    { key: 'tags', label: t('contacts.tags') }
  ]
  for (const field of listFields.value) {
    base.push({ key: `field:${field.key}`, label: field.label })
  }
  base.push({ key: 'last_message', label: t('contacts.lastMessageAt'), sortable: true, sortKey: 'last_message_at' })
  return base
})

const debouncedSearch = useDebounceFn(() => {
  currentPage.value = 1
  fetchContacts()
}, 300)

watch(searchQuery, () => debouncedSearch())
watch([sortKey, sortDirection], () => fetchContacts())
watch(() => organizationsStore.selectedOrgId, () => {
  void loadFieldMetadata()
  fetchContacts()
})

function handlePageChange(page: number) {
  currentPage.value = page
  fetchContacts()
}

function applyFilters() {
  currentPage.value = 1
  fetchContacts()
}

function clearFilters() {
  filter.value = { op: 'and', rules: [] }
  currentPage.value = 1
  fetchContacts()
}

function openContact(row: ContactSearchRow) {
  router.push(`/settings/contacts/${row.id}`)
}

/** Renders one custom field value for the table cell. */
function fieldValue(row: ContactSearchRow, field: ContactField): string {
  const raw = row.fields?.[field.key]
  if (raw === undefined || raw === null || raw === '') return '—'
  if (field.type === 'dropdown') {
    const option = field.options?.find((o) => o.value === raw)
    return option?.label || String(raw)
  }
  return String(raw)
}

async function loadFieldMetadata() {
  try {
    const [fieldsRes, filtersRes] = await Promise.all([
      contactFieldsService.list(),
      contactsService.filterFields()
    ])
    const all: ContactField[] = fieldsRes.data?.data?.fields ?? []
    listFields.value = all.filter((f) => f.show_in_list && !f.archived_at)
    filterFields.value = filtersRes.data?.data?.fields ?? []
  } catch {
    // The list is still usable without field columns or a filter builder, so
    // this degrades rather than blocking the page.
    listFields.value = []
    filterFields.value = []
  }
}

async function fetchContacts() {
  isLoading.value = true
  fetchError.value = false
  try {
    const response = await contactsService.search({
      filter: activeFilterCount.value > 0 ? filter.value : undefined,
      search: searchQuery.value || undefined,
      sort: [{ field: sortKey.value, dir: sortDirection.value }],
      page: currentPage.value,
      limit: pageSize,
      include: ['fields', 'unread']
    })
    contacts.value = response.data?.data?.contacts ?? []
    totalItems.value = response.data?.data?.total ?? 0
  } catch {
    fetchError.value = true
    toast.error(t('common.failedLoad', { resource: t('resources.contacts') }))
  } finally {
    isLoading.value = false
  }
}

onMounted(async () => {
  await loadFieldMetadata()
  await loadSegments()
  if (activeSegmentId.value) {
    await selectSegment(activeSegmentId.value)
    return
  }
  await fetchContacts()
})
</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <PageHeader
      :title="$t('contacts.title')"
      :icon="ContactIcon"
      icon-gradient="bg-gradient-to-br from-emerald-500 to-teal-600 shadow-emerald-500/20"
    >
      <template #actions>
        <Button variant="outline" size="sm" @click="showFilters = !showFilters">
          <SlidersHorizontal class="h-4 w-4 mr-2" />
          {{ $t('filters.title') }}
          <Badge v-if="activeFilterCount" variant="secondary" class="ml-2 h-5 px-1.5 text-xs">
            {{ activeFilterCount }}
          </Badge>
        </Button>
        <RouterLink v-if="canManageFields" to="/settings/contact-fields">
          <Button variant="ghost" size="sm">
            <ListChecks class="h-4 w-4 mr-2" />{{ $t('contactFields.title') }}
          </Button>
        </RouterLink>

        <!-- Creating and importing moved here with the module (plan 01). They
             lived on the Settings page that this replaced, and a Contacts
             module you cannot add a contact to is not the Contacts module. -->
        <Button v-if="canImportExport" variant="outline" size="sm" @click="importExportOpen = true">
          <ArrowDownUp class="h-4 w-4 mr-2" />{{ $t('importExport.title') }}
        </Button>
        <Button v-if="canWrite" size="sm" @click="createOpen = true">
          <Plus class="h-4 w-4 mr-2" />{{ $t('contacts.addContact') }}
        </Button>
      </template>
    </PageHeader>

    <CreateContactDialog v-model:open="createOpen" @created="onContactCreated" />
    <ImportExportDialog
      v-model:open="importExportOpen"
      table="contacts"
      :table-label="$t('resources.contacts')"
      @imported="fetchContacts"
    />

    <ErrorState
      v-if="fetchError && !isLoading"
      :title="$t('common.somethingWentWrong')"
      :description="$t('common.failedLoad', { resource: $t('resources.contacts') })"
      class="flex-1"
    >
      <template #action><Button size="sm" @click="fetchContacts">{{ $t('common.retry') }}</Button></template>
    </ErrorState>

    <ScrollArea v-else class="flex-1">
      <div class="p-6 space-y-4">
        <!-- Saved segments as views of this list (plan 05). A segment used to
             live on a page of its own, so using one meant leaving the contacts
             list and rebuilding the same filter by hand. -->
        <div v-if="canUseSegments && segments.length" class="flex flex-wrap items-center gap-2">
          <Button
            :variant="activeSegmentId ? 'ghost' : 'secondary'"
            size="sm"
            @click="selectSegment('')"
          >
            <Users class="h-3.5 w-3.5 mr-1.5" />{{ $t('contacts.allContacts') }}
          </Button>
          <Button
            v-for="segment in segments"
            :key="segment.id"
            :variant="activeSegmentId === segment.id ? 'secondary' : 'ghost'"
            size="sm"
            @click="selectSegment(segment.id)"
          >
            <Bookmark class="h-3.5 w-3.5 mr-1.5" />
            {{ segment.name }}
            <Badge variant="outline" class="ml-2 h-5 px-1.5 text-xs">
              {{ segment.contact_count ?? '—' }}
            </Badge>
          </Button>
        </div>

        <!-- What can be done with the audience now that it is on screen. -->
        <Card v-if="activeSegment" class="border-dashed">
          <CardContent class="flex flex-wrap items-center justify-between gap-3 py-3">
            <div>
              <p class="font-medium">{{ activeSegment.name }}</p>
              <p v-if="activeSegment.description" class="text-xs text-muted-foreground">
                {{ activeSegment.description }}
              </p>
              <p class="text-xs text-muted-foreground">
                {{ $t('segments.matches', { count: activeSegment.contact_count ?? 0 }) }}
              </p>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <Button variant="ghost" size="sm" @click="refreshSegmentCount">
                <RefreshCw class="h-3.5 w-3.5 mr-1.5" />{{ $t('segments.recount') }}
              </Button>
              <Button variant="outline" size="sm" @click="exportSegment">
                <Download class="h-3.5 w-3.5 mr-1.5" />{{ $t('common.export') }}
              </Button>
              <Button v-if="canSendCampaigns" size="sm" @click="sendCampaign">
                <Send class="h-3.5 w-3.5 mr-1.5" />{{ $t('segments.sendCampaign') }}
              </Button>
              <RouterLink to="/segments">
                <Button variant="ghost" size="sm">
                  <Pencil class="h-3.5 w-3.5 mr-1.5" />{{ $t('common.edit') }}
                </Button>
              </RouterLink>
            </div>
          </CardContent>
        </Card>

        <div v-if="showFilters" class="space-y-2">
          <FilterBuilder v-model="filter" :fields="filterFields" />
          <div class="flex items-center gap-2">
            <Button size="sm" @click="applyFilters">{{ $t('filters.apply') }}</Button>
            <Button v-if="activeFilterCount" variant="ghost" size="sm" @click="clearFilters">
              <X class="h-3.5 w-3.5 mr-1" />{{ $t('filters.clear') }}
            </Button>
          </div>
        </div>

        <Card>
          <CardContent class="pt-6">
            <div class="flex items-center justify-end mb-4">
              <SearchInput
                v-model="searchQuery"
                :placeholder="$t('contacts.searchContacts') + '...'"
                class="w-72"
              />
            </div>

            <DataTable
              :items="contacts"
              :columns="columns"
              :is-loading="isLoading"
              :empty-icon="ContactIcon"
              :empty-title="$t('contacts.noContactsYet')"
              :empty-description="$t('contacts.noContactsYetDesc')"
              v-model:sort-key="sortKey"
              v-model:sort-direction="sortDirection"
              server-pagination
              server-sort
              :current-page="currentPage"
              :total-items="totalItems"
              :page-size="pageSize"
              item-name="contacts"
              @page-change="handlePageChange"
            >
              <template #cell-contact="{ item }">
                <button class="text-left hover:underline" @click="openContact(item)">
                  <div class="font-medium">{{ item.profile_name || item.phone_number }}</div>
                  <div class="text-xs text-muted-foreground">{{ item.phone_number }}</div>
                </button>
              </template>

              <template #cell-tags="{ item }">
                <div class="flex flex-wrap gap-1">
                  <Badge v-for="tag in item.tags" :key="tag" variant="secondary" class="text-xs">
                    {{ tag }}
                  </Badge>
                  <span v-if="!item.tags?.length" class="text-xs text-muted-foreground">—</span>
                </div>
              </template>

              <template
                v-for="field in listFields"
                :key="field.key"
                #[`cell-field:${field.key}`]="{ item }"
              >
                <span class="text-sm">{{ fieldValue(item, field) }}</span>
              </template>

              <template #cell-last_message="{ item }">
                <span class="text-sm text-muted-foreground">
                  {{ item.last_message_at ? formatDate(item.last_message_at) : '—' }}
                </span>
              </template>
            </DataTable>
          </CardContent>
        </Card>
      </div>
    </ScrollArea>
  </div>
</template>
