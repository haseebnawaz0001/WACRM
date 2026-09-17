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
import { useRouter } from 'vue-router'
import { Card, CardContent } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  PageHeader, SearchInput, DataTable, ErrorState, FilterBuilder,
  CreateContactDialog, ImportExportDialog, type Column
} from '@/components/shared'
import {
  contactsService, contactFieldsService,
  type ContactSearchRow, type ContactField, type FilterNode, type FilterFieldInfo
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useOrganizationsStore } from '@/stores/organizations'
import { toast } from 'vue-sonner'
import { Contact as ContactIcon, SlidersHorizontal, X, ListChecks, Plus, ArrowDownUp } from 'lucide-vue-next'
import { useDebounceFn } from '@vueuse/core'
import { useListViewState, jsonFilterCodec } from '@/composables/useListViewState'
import { formatDate } from '@/lib/utils'

const { t } = useI18n()
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
