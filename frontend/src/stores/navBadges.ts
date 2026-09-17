/**
 * The counts the sidebar shows (plan 10, S12).
 *
 * Both numbers already existed on the server and neither reached the menu: the
 * unread count lived inside the chat view, and the overdue task count nowhere
 * at all — an agent found out they were late by opening Tasks. A badge is the
 * only part of the product that tells someone to go somewhere they were not
 * already going, so it is worth one small store.
 *
 * Counts are refreshed on demand and on the realtime events that can change
 * them, never on a timer: a poll every thirty seconds across every open tab is
 * a lot of traffic to keep one number honest.
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { inboxService, tasksService } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import type { NavBadgeKey } from '@/components/layout/navigation'

export const useNavBadgesStore = defineStore('navBadges', () => {
  const inboxUnread = ref(0)
  const tasksDue = ref(0)

  function countFor(key: NavBadgeKey): number {
    return key === 'inboxUnread' ? inboxUnread.value : tasksDue.value
  }

  /**
   * Refresh both counts, skipping the ones the viewer cannot see.
   *
   * Failures are swallowed: a badge is a convenience, and an error toast
   * because a count could not be fetched would be noise about something the
   * person did not ask for.
   */
  async function refresh() {
    const auth = useAuthStore()

    if (auth.hasPermission('chat', 'read')) {
      try {
        const { data } = await inboxService.counts()
        const counts = (data as any)?.data ?? data
        inboxUnread.value = Number(counts?.mine ?? 0)
      } catch {
        // leave the last known value rather than flashing zero
      }
    }

    if (auth.hasPermission('tasks', 'read')) {
      try {
        const { data } = await tasksService.list({ view: 'mine', status: 'open', limit: 1 })
        const payload = (data as any)?.data ?? data
        // Overdue plus due today is what a person can act on now; the rest is
        // a plan, not a prompt.
        tasksDue.value = Number(payload?.due_count ?? payload?.total ?? 0)
      } catch {
        // as above
      }
    }
  }

  function clear() {
    inboxUnread.value = 0
    tasksDue.value = 0
  }

  return { inboxUnread, tasksDue, countFor, refresh, clear }
})
