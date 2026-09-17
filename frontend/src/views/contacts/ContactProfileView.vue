<script setup lang="ts">
/**
 * The contact profile (plan 02).
 *
 * Everything a company knows about a customer lived in one chat thread, so
 * "what happened with this person" meant scrolling. The profile puts the record
 * and the story side by side: fields, deals and follow-ups on the left, and one
 * ordered feed of everything that happened on the right.
 *
 * Messages are collapsed into exchanges rather than listed one by one — thirty
 * rows for thirty replies buries everything else that happened.
 */
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { unwrapResponse, unwrapItemResponse, unwrapListResponse } from '@/lib/api-utils'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Separator } from '@/components/ui/separator'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import { PageHeader, ErrorState, ContactSidebar, type SidebarSection } from '@/components/shared'
import {
  contactsService, contactFieldsService, timelineService,
  dealsService, tasksService, duplicatesService,
  type TimelineItem, type Deal, type Task, type ContactField
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { wsService } from '@/services/websocket'
import { getInitials, getAvatarGradient, formatDateTime } from '@/lib/utils'
import {
  User, MessageSquare, ListChecks, KanbanSquare, Tag, ArrowLeft,
  RefreshCw, FileText, Phone, ArrowRightLeft
} from 'lucide-vue-next'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

const contact = ref<any>(null)
const items = ref<TimelineItem[]>([])
const nextBefore = ref<string | undefined>()
const deals = ref<Deal[]>([])
const tasks = ref<Task[]>([])
const merges = ref<any[]>([])
const fieldDefs = ref<ContactField[]>([])

const isLoading = ref(true)
const fetchError = ref(false)
const typeFilter = ref('')

const contactId = computed(() => String(route.params.id))
const canSeeDeals = computed(() => authStore.hasPermission('deals', 'read'))
const canSeeTasks = computed(() => authStore.hasPermission('tasks', 'read'))

/** The icon for each kind of timeline entry, so the feed can be skimmed. */
const iconFor: Record<string, any> = {
  message_burst: MessageSquare,
  note: FileText,
  call: Phone,
  task: ListChecks,
  deal: KanbanSquare,
  tag: Tag,
  transfer: ArrowRightLeft,
  assignment: User,
  conversation: MessageSquare,
  field_change: RefreshCw
}

async function load() {
  try {
    const [contactResult, timelineResult] = await Promise.all([
      contactsService.get(contactId.value),
      timelineService.forContact(contactId.value, { limit: 50 })
    ])
    // Every response is { status, data: … }. Reading `.data` off the axios
    // response yields that envelope, not the record — the page rendered an
    // empty header, no fields, no deals, no tasks and an empty timeline for
    // every contact until this was unwrapped.
    const loaded = unwrapItemResponse<any>(contactResult, 'contact')

    // A merged contact's URL resolves to the survivor (plan 06). The API has
    // already returned the surviving record; rewriting the address keeps a
    // bookmark or an old link from staying on an id that no longer holds the
    // history. replace(), not push(), so Back does not bounce between them.
    if (loaded?.merged_into_id && loaded.merged_into_id !== contactId.value) {
      router.replace({ name: 'contact-profile', params: { id: loaded.merged_into_id } })
      return
    }

    contact.value = loaded
    const timeline = unwrapResponse<any>(timelineResult)
    items.value = timeline?.items || []
    nextBefore.value = timeline?.next_before
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }

  // Side panels are best-effort: a profile that fails to open because the
  // deals list errored would be worse than a profile with one empty panel.
  if (canSeeDeals.value) {
    try {
      const response = await dealsService.forContact(contactId.value, 'all')
      deals.value = unwrapListResponse<any>(response, 'deals')
    } catch { deals.value = [] }
  }
  if (canSeeTasks.value) {
    try {
      const response = await tasksService.list({ contact_id: contactId.value, status: 'open' })
      tasks.value = unwrapListResponse<any>(response, 'tasks')
    } catch { tasks.value = [] }
  }
  try {
    const response = await duplicatesService.forContact(contactId.value)
    merges.value = unwrapListResponse<any>(response, 'merges')
  } catch { merges.value = [] }
  try {
    const response = await contactFieldsService.list()
    fieldDefs.value = unwrapListResponse<any>(response, 'fields')
  } catch { fieldDefs.value = [] }
}

async function loadMore() {
  if (!nextBefore.value) return
  try {
    const page = unwrapResponse<any>(await timelineService.forContact(contactId.value, {
      limit: 50, before: nextBefore.value, types: typeFilter.value || undefined
    }))
    items.value.push(...(page?.items || []))
    nextBefore.value = page?.next_before
  } catch {
    // Nothing more to say than "that did not load"; the button stays.
  }
}

async function applyFilter(value: string) {
  typeFilter.value = value
  try {
    const filtered = unwrapResponse<any>(await timelineService.forContact(contactId.value, {
      limit: 50, types: value || undefined
    }))
    items.value = filtered?.items || []
    nextBefore.value = filtered?.next_before
  } catch {
    items.value = []
  }
}

function money(deal: Deal): string {
  return new Intl.NumberFormat(locale.value, {
    style: 'currency', currency: deal.currency || 'USD', maximumFractionDigits: 0
  }).format(deal.value)
}

const name = computed(() => contact.value?.profile_name || contact.value?.phone_number || '')

/**
 * The record's fields in the organization's own order, labelled the way the
 * organization labelled them. Values come back keyed by slug; showing the slug
 * would make the panel read like a database dump.
 */
const fields = computed(() => {
  const values = contact.value?.fields || {}
  return fieldDefs.value
    .filter(def => values[def.key] !== undefined && values[def.key] !== null && values[def.key] !== '')
    .map(def => ({ key: def.key, label: def.label, value: values[def.key] }))
})

/**
 * Watch this contact's topic while the page is open (plan 10, S10).
 *
 * The page used to be a snapshot: a field edited from the chat panel, a note
 * added by a colleague or a task completed elsewhere left the profile showing
 * yesterday's record until somebody reloaded. Topic subscriptions are what make
 * this possible without also receiving every other contact's traffic — which is
 * what the old single-valued `set_contact` would have meant.
 */
/**
 * The sidebar's sections, in the order an agent reads them: who this is, then
 * what is open on them, then how the record came to look like this.
 *
 * Sections with nothing in them are hidden rather than shown empty — a column
 * of "no deals / no tasks / no merges" is noise on the majority of contacts.
 */
const sidebarSections = computed<SidebarSection[]>(() => [
  { id: 'header' },
  { id: 'deals', label: t('contactProfile.deals'), hidden: !canSeeDeals.value || !deals.value.length },
  { id: 'tasks', label: t('contactProfile.openTasks'), hidden: !canSeeTasks.value || !tasks.value.length },
  { id: 'merges', label: t('contactProfile.merges'), hidden: !merges.value.length }
])

const stopWatching: Array<() => void> = []

function watchContact() {
  const topic = `contact:${contactId.value}`
  stopWatching.push(wsService.subscribeTopics([topic]))

  // Ids only: refetch rather than trusting a payload assembled for somebody
  // else's permissions.
  const refresh = (payload: any) => {
    if (payload?.contact_id === contactId.value) void load()
  }
  for (const event of ['contact_updated', 'custom_fields_updated', 'conversation_updated', 'task_updated']) {
    stopWatching.push(wsService.subscribe(event, refresh))
  }
}

onMounted(() => {
  void load()
  watchContact()
})

onUnmounted(() => {
  stopWatching.forEach(stop => stop())
  stopWatching.length = 0
})
</script>

<template>
  <div class="space-y-4 p-4">
    <ErrorState v-if="fetchError" :message="t('contactProfile.loadFailed')" @retry="load" />
    <p v-else-if="isLoading" class="text-muted-foreground">{{ t('common.loading') }}</p>

    <template v-else-if="contact">
      <PageHeader :title="name" :icon="User">
        <template #actions>
          <Button variant="ghost" size="sm" @click="router.back()">
            <ArrowLeft class="mr-1.5 h-4 w-4" />
            {{ t('common.back') }}
          </Button>
          <Button size="sm" @click="router.push(`/chat?contact=${contactId}`)">
            <MessageSquare class="mr-1.5 h-4 w-4" />
            {{ t('contactProfile.openChat') }}
          </Button>
        </template>
      </PageHeader>

      <div class="grid gap-4 lg:grid-cols-3">
        <!-- The record, through the shared sidebar (plan 10, S12). The chat
             panel renders the same sections in the same order, so an agent
             does not have to relearn where tags live when they move between
             the two screens. -->
        <ContactSidebar :sections="sidebarSections" storage-key="contact-profile">
          <template #header>
          <Card>
            <CardContent class="space-y-3 p-4">
              <div class="flex items-center gap-3">
                <Avatar class="h-12 w-12">
                  <AvatarFallback :class="getAvatarGradient(name)" class="text-white">
                    {{ getInitials(name) }}
                  </AvatarFallback>
                </Avatar>
                <div class="min-w-0">
                  <p class="truncate font-medium">{{ name }}</p>
                  <p class="truncate text-sm text-muted-foreground">{{ contact.phone_number }}</p>
                </div>
              </div>

              <div v-if="contact.tags?.length" class="flex flex-wrap gap-1">
                <Badge v-for="tag in contact.tags" :key="tag" variant="outline" class="px-1.5 py-0 text-[11px]">
                  {{ tag }}
                </Badge>
              </div>

              <Separator />

              <dl v-if="fields.length" class="space-y-1.5 text-sm">
                <div v-for="field in fields" :key="field.key" class="flex justify-between gap-2">
                  <dt class="text-muted-foreground">{{ field.label }}</dt>
                  <dd class="truncate text-right">{{ field.value }}</dd>
                </div>
              </dl>
              <p v-else class="text-sm text-muted-foreground">{{ t('contactProfile.noFields') }}</p>
            </CardContent>
          </Card>
          </template>

          <template #deals>
            <div class="space-y-2">
              <button
                v-for="deal in deals"
                :key="deal.id"
                class="flex w-full items-center justify-between gap-2 rounded-md border p-2 text-left text-sm hover:bg-accent"
                @click="router.push('/pipeline')"
              >
                <span class="min-w-0">
                  <span class="block truncate">{{ deal.title }}</span>
                  <Badge variant="outline" class="mt-0.5 px-1.5 py-0 text-[11px]">
                    {{ deal.stage_name }}
                  </Badge>
                </span>
                <span class="shrink-0 tabular-nums">{{ money(deal) }}</span>
              </button>
            </div>
          </template>

          <template #tasks>
            <div class="space-y-1.5">
              <div v-for="task in tasks" :key="task.id" class="flex items-center justify-between gap-2 text-sm">
                <span class="min-w-0 truncate">{{ task.title }}</span>
                <Badge
                  :variant="task.overdue ? 'destructive' : 'outline'"
                  class="shrink-0 px-1.5 py-0 text-[11px]"
                >
                  {{ formatDateTime(task.due_at) }}
                </Badge>
              </div>
            </div>
          </template>

          <!-- A record that suddenly holds somebody else's history has to be
               able to explain itself. -->
          <template #merges>
            <div class="space-y-1 text-sm text-muted-foreground">
              <p v-for="merge in merges" :key="merge.id">
                {{ t('contactProfile.mergedOn', { when: formatDateTime(merge.created_at) }) }}
              </p>
            </div>
          </template>
        </ContactSidebar>

        <!-- The story -->
        <Card class="lg:col-span-2">
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('contactProfile.timeline') }}</CardTitle>
            <Select :model-value="typeFilter" @update:model-value="v => applyFilter(String(v ?? ''))">
              <SelectTrigger class="h-8 w-44">
                <SelectValue :placeholder="t('contactProfile.everything')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">{{ t('contactProfile.everything') }}</SelectItem>
                <SelectItem value="message_burst">{{ t('contactProfile.filterMessages') }}</SelectItem>
                <SelectItem value="note">{{ t('contactProfile.filterNotes') }}</SelectItem>
                <SelectItem value="task">{{ t('contactProfile.filterTasks') }}</SelectItem>
                <SelectItem value="deal">{{ t('contactProfile.filterDeals') }}</SelectItem>
                <SelectItem value="tag">{{ t('contactProfile.filterTags') }}</SelectItem>
              </SelectContent>
            </Select>
          </CardHeader>
          <CardContent>
            <p v-if="!items.length" class="text-sm text-muted-foreground">
              {{ t('contactProfile.noHistory') }}
            </p>

            <ol v-else class="relative space-y-4 border-l pl-6">
              <li v-for="item in items" :key="item.id" class="relative">
                <span class="absolute -left-[31px] flex h-5 w-5 items-center justify-center rounded-full bg-background ring-4 ring-background">
                  <component :is="iconFor[item.type] || RefreshCw" class="h-3.5 w-3.5 text-muted-foreground" />
                </span>

                <div class="flex flex-wrap items-baseline justify-between gap-2">
                  <p class="text-sm">{{ item.summary }}</p>
                  <time class="shrink-0 text-xs text-muted-foreground">
                    {{ formatDateTime(item.occurred_at) }}
                  </time>
                </div>

                <p v-if="item.group" class="text-xs text-muted-foreground">
                  {{ t('contactProfile.exchange', {
                    count: item.group.count, from: item.group.from_customer
                  }) }}
                </p>
                <p v-else-if="item.actor?.name" class="text-xs text-muted-foreground">
                  {{ item.actor.name }}
                </p>
              </li>
            </ol>

            <Button v-if="nextBefore" variant="ghost" size="sm" class="mt-3" @click="loadMore">
              {{ t('contactProfile.loadMore') }}
            </Button>
          </CardContent>
        </Card>
      </div>
    </template>
  </div>
</template>
