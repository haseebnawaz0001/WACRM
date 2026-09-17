<script setup lang="ts">
/**
 * The inbox (plan 03).
 *
 * A chat list sorted by last message answers "who wrote most recently", which
 * is not the same as "what still needs an answer". A conversation has a state —
 * open, pending, snoozed, resolved — and the inbox is the list of the ones that
 * are still somebody's problem.
 *
 * Resolved conversations are hidden by default: a list that never empties is a
 * list nobody feels they are making progress against.
 */
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PageHeader, ErrorState } from '@/components/shared'
import { chatbotService, inboxService, type InboxRow, type InboxCounts } from '@/services/api'
import { unwrapResponse, unwrapListResponse } from '@/lib/api-utils'
import { useAuthStore } from '@/stores/auth'
import { wsService } from '@/services/websocket'
import { toast } from 'vue-sonner'
import { Inbox, Check, Clock, Bot } from 'lucide-vue-next'

const { t, locale } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

type InboxViewKey = 'mine' | 'unassigned' | 'bot' | 'all'

const rows = ref<InboxRow[]>([])
const counts = ref<InboxCounts | null>(null)

/**
 * The tab comes from the URL so a view is shareable and survives a reload —
 * and so `/chatbot/transfers` can redirect straight into the queue rather than
 * dropping a supervisor on somebody else's inbox (plan 03, plan 10 §4.2).
 */
const route = useRoute()
const initialView = (route.query.view as InboxViewKey) || 'mine'
const view = ref<InboxViewKey>(
  ['mine', 'unassigned', 'bot', 'all'].includes(initialView) ? initialView : 'mine'
)
const status = ref('')
const isPicking = ref(false)

const isLoading = ref(true)
const fetchError = ref(false)

const canAssign = computed(() => authStore.hasPermission('chat.assign', 'write'))

async function fetchInbox() {
  try {
    const [listResult, countsResult] = await Promise.all([
      inboxService.list({ view: view.value, status: status.value, limit: 100 }),
      inboxService.counts()
    ])
    // Every response is { status, data: … }: reading `.data` off the axios
    // response yields the envelope, so the list was always empty and every
    // badge always zero.
    rows.value = unwrapListResponse<InboxRow>(listResult, 'conversations')
    counts.value = unwrapResponse<InboxCounts>(countsResult)
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

watch([view, status], fetchInbox)

// replace, not push: switching tabs is refining one view, and pushing would
// make Back walk through every tab the agent glanced at.
watch(view, value => {
  void router.replace({ query: { ...route.query, view: value } })
})

/**
 * Take the next conversation from the queue (plan 10 §4.2).
 *
 * The Transfers page had this and the inbox did not, so an agent who wanted
 * work had to go to a different screen to ask for it. The pick itself is the
 * transfer service's, which serialises two agents pressing this at once.
 */
async function pickNext() {
  isPicking.value = true
  try {
    const { data } = await chatbotService.pickNextTransfer()
    const payload = (data as any)?.data ?? data
    if (!payload?.transfer) {
      toast.info(t('inbox.queueEmpty'))
      return
    }
    await fetchInbox()
    void router.push(`/chat/${payload.transfer.contact_id}`)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    isPicking.value = false
  }
}

async function resolve(row: InboxRow) {
  // Optimistic: the row leaves the list immediately, because the point of
  // resolving is watching the list get shorter.
  const previous = rows.value
  rows.value = rows.value.filter(item => item.id !== row.id)
  try {
    await inboxService.resolve(row.contact_id)
    fetchInbox()
  } catch (error: any) {
    rows.value = previous
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

const snoozing = ref<InboxRow | null>(null)
const snoozeUntil = ref('')

async function confirmSnooze() {
  if (!snoozing.value || !snoozeUntil.value) return
  const row = snoozing.value
  snoozing.value = null
  try {
    await inboxService.snooze(row.contact_id, new Date(snoozeUntil.value).toISOString())
    snoozeUntil.value = ''
    fetchInbox()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function takeIt(row: InboxRow) {
  try {
    await inboxService.assign(row.contact_id, authStore.user?.id)
    fetchInbox()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

/** How long a customer has been waiting, which is the inbox's real sort key. */
function waiting(row: InboxRow): string | null {
  if (!row.waiting_since) return null
  const minutes = Math.round((Date.now() - new Date(row.waiting_since).getTime()) / 60000)
  if (minutes < 60) return t('inbox.waitingMinutes', { count: minutes })
  const hours = Math.round(minutes / 60)
  if (hours < 24) return t('inbox.waitingHours', { count: hours })
  return t('inbox.waitingDays', { count: Math.round(hours / 24) })
}

function lastMessage(row: InboxRow): string {
  if (!row.last_message_at) return ''
  return new Intl.DateTimeFormat(locale.value, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
  }).format(new Date(row.last_message_at))
}

let unsubscribe: (() => void) | null = null

const stopWatching: Array<() => void> = []

onMounted(async () => {
  await fetchInbox()
  // A shared inbox that only updates on reload has two people answering the
  // same customer.
  unsubscribe = wsService.subscribe('crm_event', fetchInbox)

  // The typed events too (plan 10, S10). crm_event is a firehose the list has
  // to match on by string; these say plainly that a conversation or a contact
  // in this list changed.
  for (const event of ['conversation_updated', 'contact_updated', 'contact_merged']) {
    stopWatching.push(wsService.subscribe(event, () => void fetchInbox()))
  }
})

onUnmounted(() => {
  unsubscribe?.()
  stopWatching.forEach(stop => stop())
  stopWatching.length = 0
})
</script>

<template>
  <div class="flex h-full flex-col">
    <PageHeader :title="t('inbox.title')" :description="t('inbox.description')" :icon="Inbox" />

    <div class="flex flex-wrap items-center gap-3 border-b px-4 pb-3">
      <Tabs :model-value="view" @update:model-value="v => view = v as InboxViewKey">
        <TabsList>
          <TabsTrigger value="mine">
            {{ t('inbox.viewMine') }}
            <Badge v-if="counts?.mine" variant="secondary" class="ml-1.5 px-1.5 py-0 text-[11px]">
              {{ counts.mine }}
            </Badge>
          </TabsTrigger>
          <TabsTrigger value="unassigned">
            {{ t('inbox.viewUnassigned') }}
            <Badge v-if="counts?.unassigned" variant="secondary" class="ml-1.5 px-1.5 py-0 text-[11px]">
              {{ counts.unassigned }}
            </Badge>
          </TabsTrigger>
          <TabsTrigger value="bot">{{ t('inbox.viewBot') }}</TabsTrigger>
          <TabsTrigger value="all">{{ t('inbox.viewAll') }}</TabsTrigger>
        </TabsList>
      </Tabs>

      <Select v-model="status">
        <SelectTrigger class="h-8 w-40"><SelectValue :placeholder="t('inbox.statusActive')" /></SelectTrigger>
        <SelectContent>
          <SelectItem value="">{{ t('inbox.statusActive') }}</SelectItem>
          <SelectItem value="open">{{ t('inbox.statusOpen') }}</SelectItem>
          <SelectItem value="pending">{{ t('inbox.statusPending') }}</SelectItem>
          <SelectItem value="snoozed">{{ t('inbox.statusSnoozed') }}</SelectItem>
          <SelectItem value="resolved">{{ t('inbox.statusResolved') }}</SelectItem>
        </SelectContent>
      </Select>

      <!-- The queue's own control, on the queue (plan 10 §4.2). It lived on a
           separate Transfers page, so an agent who wanted work had to go to a
           different screen to ask for it. -->
      <Button
        v-if="view === 'unassigned' && canAssign"
        variant="outline"
        size="sm"
        class="ml-auto"
        :disabled="isPicking"
        @click="pickNext"
      >
        {{ t('inbox.pickNext') }}
      </Button>
    </div>

    <ErrorState v-if="fetchError" :message="t('inbox.loadFailed')" @retry="fetchInbox" />
    <p v-else-if="isLoading" class="p-4 text-muted-foreground">{{ t('common.loading') }}</p>

    <Card v-else-if="!rows.length" class="m-4">
      <CardContent class="flex flex-col items-center gap-3 py-10 text-center">
        <Check class="h-8 w-8 text-muted-foreground" />
        <p class="text-sm text-muted-foreground">{{ t('inbox.empty') }}</p>
      </CardContent>
    </Card>

    <ul v-else class="flex-1 divide-y overflow-y-auto">
      <li
        v-for="row in rows"
        :key="row.id"
        class="flex flex-wrap items-center gap-3 px-4 py-3 hover:bg-accent/40"
      >
        <button class="min-w-0 flex-1 text-left" @click="router.push(`/chat?contact=${row.contact_id}`)">
          <div class="flex items-center gap-2">
            <span class="truncate font-medium">{{ row.contact_name || row.contact_phone }}</span>
            <Bot v-if="row.handling === 'bot'" class="h-3.5 w-3.5 text-muted-foreground" :aria-label="t('inbox.botHandled')" />
            <Badge
              v-else-if="row.handling === 'handoff_pending'"
              variant="outline"
              class="px-1.5 py-0 text-[11px] text-amber-600 dark:text-amber-500"
            >
              {{ t('inbox.handoffPending') }}
            </Badge>
            <Badge v-if="row.reopened_count" variant="outline" class="px-1.5 py-0 text-[11px]">
              {{ t('inbox.reopened', { count: row.reopened_count }) }}
            </Badge>
          </div>
          <p class="truncate text-xs text-muted-foreground">
            <span v-if="row.last_message_preview">{{ row.last_message_preview }} · </span>
            {{ lastMessage(row) }}
            <span v-if="row.snoozed_until"> · {{ t('inbox.snoozedUntil') }}</span>
          </p>
        </button>

        <!-- How long they have waited, not when they wrote: the inbox is a
             list of unanswered questions. -->
        <Badge v-if="waiting(row)" variant="outline" class="shrink-0 gap-1 px-1.5 py-0 text-[11px]">
          <Clock class="h-3 w-3" />
          {{ waiting(row) }}
        </Badge>

        <div class="flex shrink-0 items-center gap-1">
          <Button
            v-if="canAssign && !row.assignee_id"
            size="sm" variant="outline"
            @click="takeIt(row)"
          >
            {{ t('inbox.takeIt') }}
          </Button>
          <Button size="sm" variant="ghost" @click="snoozing = row">
            {{ t('inbox.snooze') }}
          </Button>
          <Button size="sm" variant="ghost" @click="resolve(row)">
            {{ t('inbox.resolve') }}
          </Button>
        </div>
      </li>
    </ul>

    <Dialog :open="!!snoozing" @update:open="open => !open && (snoozing = null)">
      <DialogContent>
        <DialogHeader><DialogTitle>{{ t('inbox.snoozeTitle') }}</DialogTitle></DialogHeader>
        <div class="space-y-1.5">
          <Label>{{ t('inbox.snoozeUntil') }}</Label>
          <Input v-model="snoozeUntil" type="datetime-local" />
          <p class="text-xs text-muted-foreground">{{ t('inbox.snoozeHint') }}</p>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="snoozing = null">{{ t('common.cancel') }}</Button>
          <Button :disabled="!snoozeUntil" @click="confirmSnooze">{{ t('inbox.snooze') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
