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
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Separator } from '@/components/ui/separator'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import { PageHeader, ErrorState } from '@/components/shared'
import {
  contactsService, contactFieldsService, timelineService,
  dealsService, tasksService, duplicatesService,
  type TimelineItem, type Deal, type Task, type ContactField
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
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
    const loaded = contactResult.data.contact || contactResult.data

    // A merged contact's URL resolves to the survivor (plan 06). The API has
    // already returned the surviving record; rewriting the address keeps a
    // bookmark or an old link from staying on an id that no longer holds the
    // history. replace(), not push(), so Back does not bounce between them.
    if (loaded?.merged_into_id && loaded.merged_into_id !== contactId.value) {
      router.replace({ name: 'contact-profile', params: { id: loaded.merged_into_id } })
      return
    }

    contact.value = loaded
    items.value = timelineResult.data.items || []
    nextBefore.value = timelineResult.data.next_before
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
      const { data } = await dealsService.forContact(contactId.value, 'all')
      deals.value = data.deals || []
    } catch { deals.value = [] }
  }
  if (canSeeTasks.value) {
    try {
      const { data } = await tasksService.list({ contact_id: contactId.value, status: 'open' })
      tasks.value = data.tasks || []
    } catch { tasks.value = [] }
  }
  try {
    const { data } = await duplicatesService.forContact(contactId.value)
    merges.value = data.merges || []
  } catch { merges.value = [] }
  try {
    const { data } = await contactFieldsService.list()
    fieldDefs.value = data.fields || []
  } catch { fieldDefs.value = [] }
}

async function loadMore() {
  if (!nextBefore.value) return
  try {
    const { data } = await timelineService.forContact(contactId.value, {
      limit: 50, before: nextBefore.value, types: typeFilter.value || undefined
    })
    items.value.push(...(data.items || []))
    nextBefore.value = data.next_before
  } catch {
    // Nothing more to say than "that did not load"; the button stays.
  }
}

async function applyFilter(value: string) {
  typeFilter.value = value
  try {
    const { data } = await timelineService.forContact(contactId.value, {
      limit: 50, types: value || undefined
    })
    items.value = data.items || []
    nextBefore.value = data.next_before
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

onMounted(load)
</script>

<template>
  <div class="space-y-4 p-4">
    <ErrorState v-if="fetchError" :message="t('profile.loadFailed')" @retry="load" />
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
            {{ t('profile.openChat') }}
          </Button>
        </template>
      </PageHeader>

      <div class="grid gap-4 lg:grid-cols-3">
        <!-- The record -->
        <div class="space-y-4">
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
              <p v-else class="text-sm text-muted-foreground">{{ t('profile.noFields') }}</p>
            </CardContent>
          </Card>

          <Card v-if="canSeeDeals && deals.length">
            <CardHeader><CardTitle class="text-base">{{ t('profile.deals') }}</CardTitle></CardHeader>
            <CardContent class="space-y-2">
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
            </CardContent>
          </Card>

          <Card v-if="canSeeTasks && tasks.length">
            <CardHeader><CardTitle class="text-base">{{ t('profile.openTasks') }}</CardTitle></CardHeader>
            <CardContent class="space-y-1.5">
              <div v-for="task in tasks" :key="task.id" class="flex items-center justify-between gap-2 text-sm">
                <span class="min-w-0 truncate">{{ task.title }}</span>
                <Badge
                  :variant="task.overdue ? 'destructive' : 'outline'"
                  class="shrink-0 px-1.5 py-0 text-[11px]"
                >
                  {{ formatDateTime(task.due_at) }}
                </Badge>
              </div>
            </CardContent>
          </Card>

          <!-- A record that suddenly holds somebody else's history has to be
               able to explain itself. -->
          <Card v-if="merges.length">
            <CardHeader><CardTitle class="text-base">{{ t('profile.merges') }}</CardTitle></CardHeader>
            <CardContent class="space-y-1 text-sm text-muted-foreground">
              <p v-for="merge in merges" :key="merge.id">
                {{ t('profile.mergedOn', { when: formatDateTime(merge.created_at) }) }}
              </p>
            </CardContent>
          </Card>
        </div>

        <!-- The story -->
        <Card class="lg:col-span-2">
          <CardHeader class="flex-row items-center justify-between space-y-0">
            <CardTitle class="text-base">{{ t('profile.timeline') }}</CardTitle>
            <Select :model-value="typeFilter" @update:model-value="v => applyFilter(String(v ?? ''))">
              <SelectTrigger class="h-8 w-44">
                <SelectValue :placeholder="t('profile.everything')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">{{ t('profile.everything') }}</SelectItem>
                <SelectItem value="message_burst">{{ t('profile.filterMessages') }}</SelectItem>
                <SelectItem value="note">{{ t('profile.filterNotes') }}</SelectItem>
                <SelectItem value="task">{{ t('profile.filterTasks') }}</SelectItem>
                <SelectItem value="deal">{{ t('profile.filterDeals') }}</SelectItem>
                <SelectItem value="tag">{{ t('profile.filterTags') }}</SelectItem>
              </SelectContent>
            </Select>
          </CardHeader>
          <CardContent>
            <p v-if="!items.length" class="text-sm text-muted-foreground">
              {{ t('profile.noHistory') }}
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
                  {{ t('profile.exchange', {
                    count: item.group.count, from: item.group.from_customer
                  }) }}
                </p>
                <p v-else-if="item.actor?.name" class="text-xs text-muted-foreground">
                  {{ item.actor.name }}
                </p>
              </li>
            </ol>

            <Button v-if="nextBefore" variant="ghost" size="sm" class="mt-3" @click="loadMore">
              {{ t('profile.loadMore') }}
            </Button>
          </CardContent>
        </Card>
      </div>
    </template>
  </div>
</template>
