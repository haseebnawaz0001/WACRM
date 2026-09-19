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
import { PageHeader, ErrorState, ContactSidebar, DateRangePicker, type SidebarSection } from '@/components/shared'
import { useDateRange } from '@/composables/useDateRange'
import {
  contactsService, contactFieldsService, timelineService,
  dealsService, tasksService, duplicatesService,
  type TimelineItem, type Deal, type Task, type ContactField
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { wsService } from '@/services/websocket'
import { getInitials, getAvatarColor, formatDateTime } from '@/lib/utils'
import {
  User, MessageSquare, ListChecks, KanbanSquare, Tag,
  RefreshCw, FileText, Phone, ArrowRightLeft, Megaphone, Bot, UserCheck,
  CircleDot, AlertTriangle
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

/**
 * The timeline opens on everything and narrows on request (plan 02).
 *
 * A default period would be the wrong question here: the reason to open a
 * customer's profile is usually something that happened months ago, and a feed
 * silently cut to "this month" reads as a customer with no history.
 */
const {
  selectedRange, customDateRange, isDatePickerOpen,
  dateRange, formatDateRangeDisplay, applyCustomRange
} = useDateRange({ defaultPreset: 'all' })

/** The bounds the API is asked for, omitted entirely while the range is "all". */
function rangeParams() {
  const { from, to } = dateRange.value
  return from && to ? { from, to } : {}
}

const contactId = computed(() => String(route.params.id))
const canSeeDeals = computed(() => authStore.hasPermission('deals', 'read'))
const canSeeTasks = computed(() => authStore.hasPermission('tasks', 'read'))

/**
 * The icon for each kind of entry, so the feed can be skimmed.
 *
 * These keys have to be the ones the API sends. Three of them were not: the map
 * looked for `deal`, `conversation` and nothing at all for lifecycle changes,
 * campaign sends or chatbot sessions, while the server sends `activity`,
 * `conversation_status`, `lifecycle_stage`, `campaign_send` and
 * `chatbot_session`. Five of the thirteen kinds therefore fell through to the
 * fallback, and since those five cover deals, conversations and lifecycle
 * moves, most of the column was the same grey circular arrow — an icon per row
 * that told you nothing.
 */
const iconForType: Record<string, any> = {
  message_burst: MessageSquare,
  note: FileText,
  call: Phone,
  task: ListChecks,
  tag: Tag,
  transfer: ArrowRightLeft,
  assignment: UserCheck,
  conversation_status: CircleDot,
  field_change: RefreshCw,
  lifecycle_stage: RefreshCw,
  campaign_send: Megaphone,
  chatbot_session: Bot,
  deal: KanbanSquare,
  activity: CircleDot
}

/**
 * An `activity` covers deals, tasks and the contact record itself, so the row's
 * subject is a better answer than its type where the server gives us one.
 */
const iconForSubject: Record<string, any> = {
  deal: KanbanSquare,
  task: ListChecks,
  contact: User,
  conversation: MessageSquare
}

function iconFor(item: TimelineItem) {
  if (item.type === 'task' && /overdue/i.test(item.summary)) return AlertTriangle
  const subject = item.data?.subject_type as string | undefined
  if (item.type === 'activity' && subject && iconForSubject[subject]) {
    return iconForSubject[subject]
  }
  return iconForType[item.type] ?? RefreshCw
}

/**
 * Entries in the order they happened, under the day they happened on.
 *
 * The list repeated the full date on every row — "Sep 17, 2026 11:42 PM" three
 * times in a row — which is the part the eye already knows and the time is the
 * part it wants. The day is said once, at the top of its own group.
 */
const groupedItems = computed(() => {
  const groups: Array<{ key: string; label: string; items: TimelineItem[] }> = []
  for (const item of items.value) {
    const date = new Date(item.occurred_at)
    const key = date.toDateString()
    const existing = groups.find(g => g.key === key)
    if (existing) {
      existing.items.push(item)
      continue
    }
    groups.push({ key, label: dayLabel(date), items: [item] })
  }
  return groups
})

function dayLabel(date: Date) {
  const today = new Date()
  const yesterday = new Date(today)
  yesterday.setDate(today.getDate() - 1)
  if (date.toDateString() === today.toDateString()) return t('contactProfile.today')
  if (date.toDateString() === yesterday.toDateString()) return t('contactProfile.yesterday')
  return new Intl.DateTimeFormat(locale.value, {
    day: 'numeric', month: 'long', year: 'numeric'
  }).format(date)
}

function timeOf(value: string) {
  return new Intl.DateTimeFormat(locale.value, { hour: 'numeric', minute: '2-digit' })
    .format(new Date(value))
}

/**
 * The actor, but only when the sentence above has not already said it.
 *
 * Every summary that has an actor ends "... by Omar Haddad", and the line
 * underneath repeated "Omar Haddad" on its own. Same for a message burst: "9
 * messages · 4 from the customer" sat above "9 messages, 4 from them", which
 * also said "1 messages" when there was one.
 */
/**
 * The entries worth noticing in a wall of them.
 *
 * A task going overdue and an SLA being breached are the two things on this
 * feed somebody has to act on, and they were rendered exactly like a tag being
 * added. Nothing else is coloured, so these are the only rows that catch.
 */
function isAlert(item: TimelineItem) {
  return /overdue|breached|failed|lost/i.test(item.summary)
}

function actorLine(item: TimelineItem) {
  const name = item.actor?.name
  if (!name) return null
  return item.summary.includes(name) ? null : name
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
      limit: 50, before: nextBefore.value, types: typeFilter.value || undefined, ...rangeParams()
    }))
    items.value.push(...(page?.items || []))
    nextBefore.value = page?.next_before
  } catch {
    // Nothing more to say than "that did not load"; the button stays.
  }
}

async function applyFilter(value: string) {
  typeFilter.value = value
  await reloadTimeline()
}

/** Reloads the first page under the current type filter and date range. */
async function reloadTimeline() {
  try {
    const filtered = unwrapResponse<any>(await timelineService.forContact(contactId.value, {
      limit: 50, types: typeFilter.value || undefined, ...rangeParams()
    }))
    items.value = filtered?.items || []
    nextBefore.value = filtered?.next_before
  } catch {
    items.value = []
  }
}

/**
 * Where a timeline entry leads.
 *
 * An entry that names something — an exchange, a campaign, a deal — and cannot
 * open it makes the reader go and find it by hand, which is the search the
 * timeline exists to replace. A burst carries the message it is anchored on,
 * so the chat can open on that exchange rather than at the newest message.
 */
function linkFor(item: TimelineItem): string | null {
  const data = item.data || {}
  switch (item.type) {
    case 'message_burst':
      return data.message_id
        ? `/inbox/${contactId.value}?around=${data.message_id}`
        : `/inbox/${contactId.value}`
    case 'campaign_send':
      return data.campaign_id ? `/campaigns/${data.campaign_id}` : null
    case 'call':
      return '/calling/logs'
    case 'task':
      return canSeeTasks.value ? '/tasks' : null
    case 'deal':
      return canSeeDeals.value && data.subject_id ? `/pipeline?deal=${data.subject_id}` : null
    default:
      return null
  }
}

function openItem(item: TimelineItem) {
  const target = linkFor(item)
  if (target) router.push(target)
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
  <div class="flex h-full flex-col">
    <!-- Back returns to wherever the person came from — the conversation,
         the contacts list — and to the list when they arrived directly. -->
    <PageHeader
      :title="contact ? name : ''"
      :icon="User"
      back-link="/contacts"
      :breadcrumbs="[{ label: t('contacts.title'), href: '/contacts' }]"
    >
      <template v-if="contact" #actions>
        <Button size="sm" @click="router.push(`/inbox/${contactId}`)">
          <MessageSquare class="mr-1.5 h-4 w-4" />
          {{ t('contactProfile.openChat') }}
        </Button>
      </template>
    </PageHeader>
    <div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 md:p-6">
      <ErrorState v-if="fetchError" :message="t('contactProfile.loadFailed')" @retry="load" />
      <p v-else-if="isLoading" class="text-muted-foreground">{{ t('common.loading') }}</p>

      <template v-else-if="contact">
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
                    <AvatarFallback :class="getAvatarColor(name)" class="text-white">
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
            <CardHeader class="flex-row flex-wrap items-center justify-between gap-2 space-y-0">
              <CardTitle class="text-base">{{ t('contactProfile.timeline') }}</CardTitle>
              <div class="flex flex-wrap items-center gap-2">
              <DateRangePicker
                v-model:selected-range="selectedRange"
                v-model:custom-date-range="customDateRange"
                v-model:is-date-picker-open="isDatePickerOpen"
                :format-date-range-display="formatDateRangeDisplay"
                allow-all-time
                @update:selected-range="reloadTimeline"
                @apply-custom="() => { applyCustomRange(); reloadTimeline() }"
              />
              <Select :model-value="typeFilter" @update:model-value="v => applyFilter(String(v ?? ''))">
                <SelectTrigger class="h-8 w-44" :aria-label="$t('contactProfile.everything')">
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
              </div>
            </CardHeader>
            <CardContent>
              <p v-if="!items.length" class="text-sm text-muted-foreground">
                {{ t('contactProfile.noHistory') }}
              </p>

              <div v-else class="space-y-5">
                <section v-for="group in groupedItems" :key="group.key">
                  <!-- The day, said once. -->
                  <h3 class="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    {{ group.label }}
                  </h3>

                  <!--
                    A rail with a node per entry. The line is drawn behind the
                    nodes rather than as a border on the list, so it stops at the
                    last entry of a day instead of running past it.
                  -->
                  <ol class="relative space-y-px">
                    <li
                      v-for="(item, index) in group.items"
                      :key="item.id"
                      class="group/entry relative flex gap-3 rounded-sm px-2 py-2 -mx-2 transition-colors"
                      :class="linkFor(item) ? 'cursor-pointer hover:bg-muted/50' : ''"
                      @click="openItem(item)"
                    >
                      <span
                        v-if="index < group.items.length - 1"
                        class="absolute left-[15px] top-[30px] h-[calc(100%-22px)] w-px bg-white/[0.09] light:bg-gray-200"
                        aria-hidden="true"
                      />
                      <span
                        class="relative z-[1] mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-sm border border-border bg-background"
                        :class="isAlert(item) && 'border-destructive/40 text-destructive'"
                      >
                        <component :is="iconFor(item)" class="h-3.5 w-3.5" :class="!isAlert(item) && 'text-muted-foreground'" />
                      </span>

                      <div class="flex min-w-0 flex-1 items-baseline gap-3">
                        <!-- A real link where there is somewhere to go, so the
                             entry can be opened in a new tab, reached by keyboard
                             and read by a screen reader as the action it is. -->
                        <component
                          :is="linkFor(item) ? 'a' : 'p'"
                          :href="linkFor(item) ?? undefined"
                          class="min-w-0 flex-1 text-sm"
                          :class="[
                            linkFor(item) ? 'group-hover/entry:underline' : '',
                            isAlert(item) ? 'font-medium text-destructive' : 'text-foreground'
                          ]"
                          @click.prevent="openItem(item)"
                        >{{ item.summary }}<span
                          v-if="actorLine(item)"
                          class="text-muted-foreground"
                        > · {{ actorLine(item) }}</span></component>

                        <time
                          class="shrink-0 text-xs tabular-nums text-muted-foreground"
                          :datetime="item.occurred_at"
                          :title="formatDateTime(item.occurred_at)"
                        >{{ timeOf(item.occurred_at) }}</time>
                      </div>
                    </li>
                  </ol>
                </section>
              </div>

              <div v-if="nextBefore" class="mt-4 flex justify-center border-t pt-4">
                <Button variant="outline" size="sm" @click="loadMore">
                  {{ t('contactProfile.loadMore') }}
                </Button>
              </div>
            </CardContent>
          </Card>
        </div>
      </template>
    </div>
  </div>
</template>
