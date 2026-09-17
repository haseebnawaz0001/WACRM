<script setup lang="ts">
/**
 * An agent's landing page (plan 04, plan 10 §4.9).
 *
 * The Dashboard requires `analytics`, which agents do not have, so signing in
 * put them on a page they could not open and bounced them somewhere arbitrary.
 * They also had nowhere that answered the two questions they actually start the
 * day with: what is waiting for me, and what did I promise?
 *
 * Deliberately not a smaller Dashboard. No org-wide totals, no charts — an
 * agent cannot act on either, and putting them here would only reproduce the
 * permission problem in a different shape.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { PageHeader } from '@/components/shared'
import { Inbox, ListChecks, AlertCircle } from 'lucide-vue-next'
import { inboxService, tasksService, type InboxRow, type Task } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useFormatters } from '@/composables/useFormatters'

const { t } = useI18n()
const auth = useAuthStore()
const { formatRelative } = useFormatters()

const conversations = ref<InboxRow[]>([])
const tasks = ref<Task[]>([])
const overdue = ref(0)
const dueToday = ref(0)
const isLoading = ref(true)

const canSeeInbox = computed(() => auth.hasPermission('chat', 'read'))
const canSeeTasks = computed(() => auth.hasPermission('tasks', 'read'))

async function load() {
  isLoading.value = true
  try {
    // The two sides load independently: an agent without tasks permission
    // still gets their conversations rather than an empty page.
    if (canSeeInbox.value) {
      try {
        const { data } = await inboxService.list({ view: 'mine', limit: 8 })
        conversations.value = ((data as any)?.data ?? data)?.conversations ?? []
      } catch {
        conversations.value = []
      }
    }

    if (canSeeTasks.value) {
      try {
        const { data } = await tasksService.list({ view: 'mine', status: 'open', limit: 8 })
        const payload = (data as any)?.data ?? data
        tasks.value = payload?.tasks ?? []
        overdue.value = Number(payload?.overdue ?? 0)
        dueToday.value = Number(payload?.due_today ?? 0)
      } catch {
        tasks.value = []
      }
    }
  } finally {
    isLoading.value = false
  }
}

onMounted(load)

const greeting = computed(() => {
  const name = auth.user?.full_name?.split(' ')[0]
  return name ? t('home.greetingNamed', { name }) : t('home.greeting')
})
</script>

<template>
  <div class="space-y-4 p-4">
    <PageHeader :title="greeting" :description="t('home.subtitle')" />

    <p v-if="isLoading" class="text-sm text-muted-foreground">{{ t('common.loading') }}</p>

    <div v-else class="grid gap-4 lg:grid-cols-2">
      <Card v-if="canSeeInbox">
        <CardHeader class="flex flex-row items-center justify-between gap-2 space-y-0">
          <CardTitle class="flex items-center gap-2 text-base">
            <Inbox class="h-4 w-4" />
            {{ t('home.myConversations') }}
          </CardTitle>
          <RouterLink to="/inbox" class="text-sm underline-offset-2 hover:underline">
            {{ t('common.viewAll') }}
          </RouterLink>
        </CardHeader>
        <CardContent class="space-y-1">
          <p v-if="!conversations.length" class="py-6 text-center text-sm text-muted-foreground">
            {{ t('home.inboxClear') }}
          </p>
          <RouterLink
            v-for="row in conversations"
            :key="row.id"
            :to="`/chat/${row.contact_id}`"
            class="flex items-center gap-2 rounded-md px-2 py-2 hover:bg-accent/40"
          >
            <span class="min-w-0 flex-1 truncate text-sm">
              {{ row.contact_name || row.contact_phone }}
            </span>
            <!-- Waiting time, not message count: how long somebody has been
                 waiting is what decides what to pick up next. -->
            <span v-if="row.waiting_since" class="shrink-0 text-xs text-amber-600 dark:text-amber-500">
              {{ formatRelative(row.waiting_since) }}
            </span>
          </RouterLink>
        </CardContent>
      </Card>

      <Card v-if="canSeeTasks">
        <CardHeader class="flex flex-row items-center justify-between gap-2 space-y-0">
          <CardTitle class="flex items-center gap-2 text-base">
            <ListChecks class="h-4 w-4" />
            {{ t('home.myTasks') }}
            <Badge v-if="overdue" variant="destructive" class="ml-1">
              {{ t('home.overdueCount', { count: overdue }) }}
            </Badge>
            <Badge v-else-if="dueToday" variant="outline" class="ml-1">
              {{ t('home.dueTodayCount', { count: dueToday }) }}
            </Badge>
          </CardTitle>
          <RouterLink to="/tasks" class="text-sm underline-offset-2 hover:underline">
            {{ t('common.viewAll') }}
          </RouterLink>
        </CardHeader>
        <CardContent class="space-y-1">
          <p v-if="!tasks.length" class="py-6 text-center text-sm text-muted-foreground">
            {{ t('home.tasksClear') }}
          </p>
          <RouterLink
            v-for="task in tasks"
            :key="task.id"
            :to="`/tasks?highlight=${task.id}`"
            class="flex items-center gap-2 rounded-md px-2 py-2 hover:bg-accent/40"
          >
            <AlertCircle
              v-if="task.overdue"
              class="h-3.5 w-3.5 shrink-0 text-destructive"
              :aria-label="t('home.overdue')"
            />
            <span class="min-w-0 flex-1 truncate text-sm">{{ task.title }}</span>
            <span class="shrink-0 text-xs text-muted-foreground">
              {{ formatRelative(task.due_at) }}
            </span>
          </RouterLink>
        </CardContent>
      </Card>
    </div>
  </div>
</template>
