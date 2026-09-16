import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { notificationsService, type AppNotification } from '@/services/api'
import { wsService } from '@/services/websocket'

/**
 * Notifications store (plan 00, F5).
 *
 * The unread count is kept separately from the loaded list rather than derived
 * from it: the bell shows a badge before the panel has ever been opened, and
 * the list is only the most recent page, so counting what is loaded would
 * under-report as soon as there are more than a page of them.
 */
export const useNotificationsStore = defineStore('notifications', () => {
  const items = ref<AppNotification[]>([])
  const unreadCount = ref(0)
  const isLoading = ref(false)
  const isLoadingMore = ref(false)
  const nextCursor = ref<string | null>(null)
  const hasLoaded = ref(false)

  let unsubscribe: (() => void) | null = null

  const hasMore = computed(() => nextCursor.value !== null)
  const unread = computed(() => items.value.filter(n => !n.read_at))

  function unwrap(response: any) {
    return response.data?.data ?? response.data
  }

  async function fetchUnreadCount() {
    try {
      const data = unwrap(await notificationsService.unreadCount())
      unreadCount.value = data.count ?? 0
    } catch {
      // A failed count must not break the shell it is rendered in.
    }
  }

  async function fetchNotifications() {
    isLoading.value = true
    try {
      const data = unwrap(await notificationsService.list({ limit: 20 }))
      items.value = data.notifications || []
      nextCursor.value = data.next_cursor ?? null
      hasLoaded.value = true
    } catch {
      items.value = []
      nextCursor.value = null
    } finally {
      isLoading.value = false
    }
  }

  async function fetchMore() {
    if (isLoadingMore.value || !nextCursor.value) return
    isLoadingMore.value = true
    try {
      const data = unwrap(await notificationsService.list({ limit: 20, cursor: nextCursor.value }))
      items.value = [...items.value, ...(data.notifications || [])]
      nextCursor.value = data.next_cursor ?? null
    } catch {
      nextCursor.value = null
    } finally {
      isLoadingMore.value = false
    }
  }

  async function markRead(id: string) {
    const item = items.value.find(n => n.id === id)
    if (!item || item.read_at) return
    // Updated locally first: the badge should drop the moment it is clicked,
    // and the request is not something the reader should wait for.
    item.read_at = new Date().toISOString()
    unreadCount.value = Math.max(0, unreadCount.value - 1)
    try {
      await notificationsService.markRead(id)
    } catch {
      item.read_at = null
      unreadCount.value += 1
    }
  }

  async function markAllRead() {
    const previous = items.value.map(n => n.read_at)
    const previousCount = unreadCount.value
    const now = new Date().toISOString()
    items.value.forEach(n => { if (!n.read_at) n.read_at = now })
    unreadCount.value = 0
    try {
      await notificationsService.markAllRead()
    } catch {
      items.value.forEach((n, i) => { n.read_at = previous[i] })
      unreadCount.value = previousCount
    }
  }

  /** Handle a notification pushed over the socket while the app is open. */
  function receive(payload: any) {
    const incoming: AppNotification | undefined = payload?.notification ?? payload
    if (!incoming?.id) return
    if (items.value.some(n => n.id === incoming.id)) return
    items.value = [incoming, ...items.value]
    if (!incoming.read_at) unreadCount.value += 1
  }

  /** Start listening. Safe to call more than once. */
  function connect() {
    if (unsubscribe) return
    unsubscribe = wsService.subscribe('notification_created', receive)
    fetchUnreadCount()
  }

  function disconnect() {
    unsubscribe?.()
    unsubscribe = null
  }

  function reset() {
    disconnect()
    items.value = []
    unreadCount.value = 0
    nextCursor.value = null
    hasLoaded.value = false
  }

  return {
    items,
    unread,
    unreadCount,
    isLoading,
    isLoadingMore,
    hasMore,
    hasLoaded,
    fetchNotifications,
    fetchMore,
    fetchUnreadCount,
    markRead,
    markAllRead,
    receive,
    connect,
    disconnect,
    reset
  }
})
