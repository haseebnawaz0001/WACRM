<script setup lang="ts">
/**
 * Notification bell (plan 00, F5).
 *
 * The backend has stored notifications, a WebSocket push and an unread count,
 * but nothing in the UI consumed them, so every task reminder, SLA escalation
 * and rotting-deal alert the system wrote was invisible. This is the surface
 * that makes them reachable.
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useNotificationsStore } from '@/stores/notifications'
import type { AppNotification } from '@/services/api'
import {
  Bell,
  CalendarClock,
  AlertTriangle,
  UserPlus,
  CircleDollarSign,
  Zap,
  Users,
  CheckCheck,
  Loader2
} from 'lucide-vue-next'

const props = defineProps<{ collapsed?: boolean }>()

const store = useNotificationsStore()
const router = useRouter()
const { t } = useI18n()

const isOpen = ref(false)

// Anything past 99 is "a lot"; the exact number stops being useful and the
// badge stops fitting.
const badge = computed(() => (store.unreadCount > 99 ? '99+' : String(store.unreadCount)))

const iconFor: Record<string, any> = {
  task_due: CalendarClock,
  task_overdue: AlertTriangle,
  task_assigned: CalendarClock,
  conversation_assigned: UserPlus,
  conversation_snooze_ended: CalendarClock,
  sla_escalation: AlertTriangle,
  automation: Zap,
  merge_suggestions: Users,
  deal_rotting: CircleDollarSign
}

// Overdue and breached SLAs are the ones worth colouring; making every row
// shout would mean none of them do.
const urgent = new Set(['task_overdue', 'sla_escalation', 'deal_rotting'])

function relativeTime(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const seconds = Math.round((Date.now() - then) / 1000)
  if (seconds < 60) return t('notifications.justNow')
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return t('notifications.minutesAgo', { n: minutes })
  const hours = Math.round(minutes / 60)
  if (hours < 24) return t('notifications.hoursAgo', { n: hours })
  const days = Math.round(hours / 24)
  return t('notifications.daysAgo', { n: days })
}

async function open(notification: AppNotification) {
  await store.markRead(notification.id)
  isOpen.value = false
  if (notification.link) {
    // Links are produced by the server as app routes, e.g.
    // /contacts/<id>?tab=tasks.
    router.push(notification.link).catch(() => {})
  }
}

watch(isOpen, (nowOpen) => {
  if (nowOpen) store.fetchNotifications()
})

onMounted(() => store.connect())
onUnmounted(() => store.disconnect())
</script>

<template>
  <Popover v-model:open="isOpen">
    <PopoverTrigger as-child>
      <Button
        variant="ghost"
        :class="[
          'relative text-white/70 hover:text-white hover:bg-white/[0.08] light:text-gray-600 light:hover:text-gray-900 light:hover:bg-gray-100',
          props.collapsed ? 'h-9 w-9 p-0 justify-center' : 'h-9 w-full justify-start gap-2 px-2'
        ]"
        :aria-label="t('notifications.title')"
      >
        <span class="relative inline-flex">
          <Bell class="h-4 w-4" aria-hidden="true" />
          <!-- Collapsed there is no room for a number, so the rail gets a dot:
               the count itself is unreadable at that size anyway, and the badge
               that used to hold it was 14px square sitting on a 14px bell,
               covering nearly half the icon it was meant to annotate. -->
          <span
            v-if="props.collapsed && store.unreadCount > 0"
            class="absolute -right-1 -top-1 h-2 w-2 rounded-full bg-emerald-500 ring-2 ring-[#0a0a0b] light:ring-white"
          />
        </span>
        <span v-if="!props.collapsed" class="text-[13px]">{{ t('notifications.title') }}</span>
        <!-- Expanded, the count goes where every other count in this sidebar
             goes: the end of the row, in the same pill. -->
        <span
          v-if="!props.collapsed && store.unreadCount > 0"
          class="ml-auto flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-full bg-emerald-500/15 px-1 text-[11px] font-semibold tabular-nums text-emerald-400 light:bg-emerald-100 light:text-emerald-700"
        >{{ badge }}</span>
      </Button>
    </PopoverTrigger>

    <PopoverContent side="right" align="end" class="w-96 p-0">
      <div class="flex items-center justify-between border-b px-3 py-2">
        <span class="text-sm font-medium">{{ t('notifications.title') }}</span>
        <Button
          v-if="store.unreadCount > 0"
          variant="ghost"
          size="sm"
          class="h-7 gap-1 text-xs"
          @click="store.markAllRead()"
        >
          <CheckCheck class="h-3.5 w-3.5" />
          {{ t('notifications.markAllRead') }}
        </Button>
      </div>

      <div v-if="store.isLoading" class="flex items-center justify-center py-10">
        <Loader2 class="h-5 w-5 animate-spin text-muted-foreground" />
      </div>

      <div
        v-else-if="store.items.length === 0"
        class="px-4 py-10 text-center"
      >
        <Bell class="mx-auto h-7 w-7 text-muted-foreground/40" aria-hidden="true" />
        <p class="mt-2 text-sm text-muted-foreground">{{ t('notifications.empty') }}</p>
        <p class="mt-1 text-xs text-muted-foreground/70">{{ t('notifications.emptyHint') }}</p>
      </div>

      <ScrollArea v-else class="max-h-96">
        <ul class="divide-y">
          <li v-for="n in store.items" :key="n.id">
            <button
              type="button"
              class="flex w-full items-start gap-3 px-3 py-2.5 text-left transition-colors hover:bg-muted/60"
              :class="!n.read_at && 'bg-emerald-500/[0.06]'"
              @click="open(n)"
            >
              <component
                :is="iconFor[n.type] || Bell"
                class="mt-0.5 h-4 w-4 shrink-0"
                :class="urgent.has(n.type) ? 'text-amber-500' : 'text-muted-foreground'"
                aria-hidden="true"
              />
              <span class="min-w-0 flex-1">
                <span class="flex items-start justify-between gap-2">
                  <span class="text-[13px] font-medium leading-snug">{{ n.title }}</span>
                  <span
                    v-if="!n.read_at"
                    class="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500"
                    :aria-label="t('notifications.unread')"
                  />
                </span>
                <span v-if="n.body" class="mt-0.5 block text-xs text-muted-foreground line-clamp-2">{{ n.body }}</span>
                <span class="mt-1 block text-[11px] text-muted-foreground/70">{{ relativeTime(n.created_at) }}</span>
              </span>
            </button>
          </li>
        </ul>

        <div v-if="store.hasMore" class="p-2">
          <Button
            variant="ghost"
            size="sm"
            class="w-full text-xs"
            :disabled="store.isLoadingMore"
            @click="store.fetchMore()"
          >
            <Loader2 v-if="store.isLoadingMore" class="mr-1 h-3.5 w-3.5 animate-spin" />
            {{ t('notifications.loadMore') }}
          </Button>
        </div>
      </ScrollArea>
    </PopoverContent>
  </Popover>
</template>
