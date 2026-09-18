<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { onKeyStroke } from '@vueuse/core'
import { useAuthStore } from '@/stores/auth'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  MessageSquare,
  ChevronLeft,
  ChevronRight,
  Menu,
  X,
  Search,
  Settings
} from 'lucide-vue-next'
import { wsService } from '@/services/websocket'
import { authService, organizationsService } from '@/services/api'
import OrganizationSwitcher from './OrganizationSwitcher.vue'
import UserMenu from './UserMenu.vue'
import NotificationBell from './NotificationBell.vue'
import CommandPalette from './CommandPalette.vue'
import { useNavBadgesStore } from '@/stores/navBadges'
import ShortcutHelp from './ShortcutHelp.vue'
import SidebarNavItem from './SidebarNavItem.vue'
import SidebarGroup from './SidebarGroup.vue'
import ManageDrawer from './ManageDrawer.vue'
import ActiveCallPanel from '@/components/calling/ActiveCallPanel.vue'
import { ScrollToTop } from '@/components/shared'
import { navigationSections, type NavSection, type NavItem } from './navigation'
import { useCommandPalette } from '@/composables/useCommandPalette'
import { useNavPins } from '@/composables/useNavPins'

useI18n() // Enable $t() in template

const COLLAPSED_STORAGE_KEY = 'sidebar-collapsed'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const isCollapsed = ref(readCollapsedPreference())
const isMobileMenuOpen = ref(false)
const sidebarRef = ref<HTMLElement | null>(null)

function readCollapsedPreference(): boolean {
  try {
    return localStorage.getItem(COLLAPSED_STORAGE_KEY) === 'true'
  } catch {
    return false
  }
}

watch(isCollapsed, (collapsed) => {
  try {
    localStorage.setItem(COLLAPSED_STORAGE_KEY, String(collapsed))
  } catch {
    // Storage unavailable (private mode); the preference just won't persist.
  }
})

onKeyStroke('Escape', () => {
  isMobileMenuOpen.value = false
})

// Keep the current page's row visible when it lives below the fold of a long
// submenu (e.g. Settings → Audit Logs).
watch(() => route.path, async () => {
  await nextTick()
  sidebarRef.value?.querySelector('[aria-current="page"]')?.scrollIntoView({ block: 'nearest' })
}, { immediate: true })

const navBadges = useNavBadgesStore()
const badgeUnsubscribers: Array<() => void> = []

// Refresh user data and connect WebSocket on mount
onMounted(() => {
  if (authStore.isAuthenticated) {
    // Fetch fresh permissions in background (non-destructive — interceptor handles 401)
    authStore.refreshUserData()

    // Which optional modules this org uses, so the menu matches what the API
    // will actually serve (plan 07). A failure leaves every module enabled,
    // which is the behaviour of an org that has never changed the setting.
    organizationsService.current()
      .then(({ data }) => authStore.setModules((data as any)?.data?.modules ?? (data as any)?.modules))
      .catch(() => {})

    // Sidebar badges (plan 10, S12). Fetched once here and refreshed on the
    // realtime events that can change them, rather than polled: a timer in
    // every open tab is a lot of traffic to keep two numbers honest.
    void navBadges.refresh()

    wsService.connect(async () => {
      try {
        const resp = await authService.getWSToken()
        return resp.data.data.token
      } catch {
        return null
      }
    })

    for (const event of ['new_message', 'conversation_updated', 'task_updated', 'crm_event']) {
      badgeUnsubscribers.push(wsService.subscribe(event, () => void navBadges.refresh()))
    }
  }
})

onUnmounted(() => {
  badgeUnsubscribers.forEach(stop => stop())
  badgeUnsubscribers.length = 0
})

function filterItems(items: NavSection['items']) {
  return items
    .filter(item => {
      // A module the organization has switched off has no endpoints behind it,
      // so showing the item would only lead to a 404.
      if (item.module && !authStore.moduleEnabled(item.module)) {
        return false
      }
      if (item.childPermissions) {
        return item.childPermissions.some(p => authStore.hasPermission(p, 'read'))
      }
      return !item.permission || authStore.hasPermission(item.permission, 'read')
    })
    .map(item => {
      const filteredChildren = item.children?.filter(
        child => !child.permission || authStore.hasPermission(child.permission, 'read')
      )

      let effectivePath = item.path
      if (item.childPermissions && item.permission && !authStore.hasPermission(item.permission, 'read') && filteredChildren?.length) {
        effectivePath = filteredChildren[0].path
      }

      const originalPath = item.path
      const isActive = originalPath === '/'
        ? route.name === 'dashboard'
        : originalPath === '/inbox'
          ? route.name === 'inbox'
          : route.path.startsWith(originalPath)

      return {
        ...item,
        path: effectivePath,
        active: isActive,
        children: filteredChildren
      }
    })
}

// Filter navigation sections based on user permissions
const navSections = computed(() => {
  return navigationSections
    .map(section => ({
      ...section,
      items: filterItems(section.items)
    }))
    .filter(section => section.items.length > 0)
})

/**
 * The rail's three tiers (see NavSectionKind).
 *
 * Splitting by kind is what stops nineteen rows competing as equals: the
 * places work happens stay visible, the weekly groups fold away, and the
 * sixteen settings pages leave the rail entirely for a panel with room for
 * them.
 */
const workspaceSections = computed(() => navSections.value.filter(s => (s.kind ?? 'group') === 'workspace'))
const groupSections = computed(() => navSections.value.filter(s => (s.kind ?? 'group') === 'group'))
const manageSections = computed(() => navSections.value.filter(s => s.kind === 'manage'))

const isManageOpen = ref(false)
const manageItems = computed(() => manageSections.value.flatMap(s => s.items))
// The Manage row marks itself while any settings page is open, so the sidebar
// still answers "where am I?" for a page that is not in it.
const manageActive = computed(() => route.path.startsWith('/settings'))

const { show: openCommandPalette } = useCommandPalette()
const { pins, toggle: togglePin, isPinned } = useNavPins()

/** Every navigable item, flattened, so a pinned path can be resolved to one. */
const allItems = computed(() => {
  const out: Array<NavItem & { active: boolean }> = []
  for (const section of navSections.value) {
    for (const item of section.items) {
      out.push(item as NavItem & { active: boolean })
      for (const child of item.children ?? []) {
        out.push({
          ...child,
          active: route.path === child.path || route.path.startsWith(child.path + '/')
        } as NavItem & { active: boolean })
      }
    }
  }
  return out
})

/**
 * Pinned rows, in the order they were pinned.
 *
 * A pin whose page the viewer can no longer open resolves to nothing and is
 * skipped rather than rendered as a dead row: permissions change, and a
 * sidebar that keeps offering a 403 is worse than one that quietly forgets.
 */
const pinnedItems = computed(() =>
  pins.value
    .map(path => allItems.value.find(item => item.path === path))
    .filter((item): item is NavItem & { active: boolean } => !!item)
)

const toggleSidebar = () => {
  isCollapsed.value = !isCollapsed.value
}

const closeMobileMenu = () => {
  isMobileMenuOpen.value = false
}

const handleLogout = async () => {
  await authStore.logout()
  router.push('/login')
}
</script>

<template>
  <div class="flex h-screen bg-[#0a0a0b] light:bg-gray-50">
    <!-- Skip link for accessibility -->
    <a href="#main-content" class="skip-link">{{ $t('nav.skipToMain') }}</a>

    <!-- Mobile header -->
    <header class="fixed top-0 left-0 right-0 z-50 flex h-12 items-center justify-between border-b border-white/[0.08] light:border-gray-200 bg-[#0a0a0b]/95 light:bg-white/95 backdrop-blur-sm px-3 md:hidden">
      <RouterLink to="/" class="flex items-center gap-2">
        <div class="h-7 w-7 rounded-lg bg-primary flex items-center justify-center">
          <MessageSquare class="h-4 w-4 text-white" />
        </div>
        <span class="font-semibold text-sm text-white light:text-gray-900">WA CRM</span>
      </RouterLink>
      <Button
        variant="ghost"
        size="icon"
        class="h-8 w-8 text-white/70 hover:text-white hover:bg-white/[0.08] light:text-gray-600 light:hover:text-gray-900 light:hover:bg-gray-100"
        :aria-label="$t('nav.toggleMenu')"
        :aria-expanded="isMobileMenuOpen"
        aria-controls="app-sidebar"
        @click="isMobileMenuOpen = !isMobileMenuOpen"
      >
        <X v-if="isMobileMenuOpen" class="h-5 w-5" />
        <Menu v-else class="h-5 w-5" />
      </Button>
    </header>

    <!-- Mobile menu overlay -->
    <div
      v-if="isMobileMenuOpen"
      class="fixed inset-0 z-40 bg-black/60 light:bg-black/30 backdrop-blur-sm md:hidden"
      aria-hidden="true"
      @click="closeMobileMenu"
    />

    <!-- Sidebar -->
    <aside
      id="app-sidebar"
      ref="sidebarRef"
      :class="[
        'flex flex-col border-r border-white/[0.08] light:border-gray-200 bg-[#0a0a0b] light:bg-white transition-[width,transform,visibility] duration-300 ease-out',
        'fixed inset-y-0 left-0 z-40 md:relative',
        'transform md:transform-none',
        // invisible while slid off-screen so keyboard focus can't land in a hidden menu
        isMobileMenuOpen ? 'translate-x-0' : 'max-md:invisible -translate-x-full md:translate-x-0',
        isCollapsed ? 'w-64 md:w-16' : 'w-64'
      ]"
    >
      <!-- Logo row: same height as page headers so the dividers line up. Hidden on mobile (the top bar carries it). -->
      <div class="hidden md:block border-b border-white/[0.08] light:border-gray-200">
        <div :class="['flex h-16 items-center', isCollapsed ? 'justify-center' : 'justify-between pl-3 pr-2']">
          <RouterLink
            to="/"
            class="sidebar-link flex items-center gap-2 rounded-sm p-0.5"
            :aria-label="isCollapsed ? 'WA CRM' : undefined"
          >
            <div class="h-7 w-7 rounded-lg bg-primary flex items-center justify-center">
              <MessageSquare class="h-4 w-4 text-white" aria-hidden="true" />
            </div>
            <span v-if="!isCollapsed" class="font-semibold text-sm text-white light:text-gray-900">
              WA CRM
            </span>
          </RouterLink>
          <Button
            v-if="!isCollapsed"
            variant="ghost"
            size="icon"
            class="h-7 w-7 text-white/50 hover:text-white hover:bg-white/[0.08] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100"
            :aria-label="$t('nav.collapseSidebar')"
            aria-controls="app-sidebar"
            aria-expanded="true"
            @click="toggleSidebar"
          >
            <ChevronLeft class="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      <!-- Collapsed: the expand control sits on the sidebar edge so the logo keeps the whole rail width -->
      <Button
        v-if="isCollapsed"
        variant="outline"
        size="icon"
        class="hidden md:flex absolute -right-3 top-5 z-10 h-6 w-6 rounded-full border-white/[0.12] bg-[#0a0a0b] text-white/60 shadow-md hover:bg-[#161617] hover:text-white light:border-gray-200 light:bg-white light:text-gray-500 light:hover:bg-gray-50 light:hover:text-gray-900"
        :aria-label="$t('nav.expandSidebar')"
        aria-controls="app-sidebar"
        aria-expanded="false"
        @click="toggleSidebar"
      >
        <ChevronRight class="h-3 w-3" />
      </Button>

      <!-- Mobile logo spacer -->
      <div class="h-12 md:hidden" />

      <OrganizationSwitcher :collapsed="isCollapsed" @expand="isCollapsed = false" />

      <!-- The palette has existed app-wide behind Cmd-K and nothing on screen
           said so. A row that looks like what it does makes the fastest way
           through nineteen destinations discoverable. -->
      <div class="px-2 pt-2">
        <button
          type="button"
          :class="[
            'sidebar-link group/search flex w-full items-center gap-2.5 rounded-sm border border-white/[0.07] bg-white/[0.02] px-2.5 py-[7px] max-md:py-3.5 text-[13px] text-white/45 transition-colors duration-150 hover:border-white/[0.12] hover:bg-white/[0.05] hover:text-white/80 light:border-gray-200 light:bg-gray-50 light:text-gray-500 light:hover:bg-gray-100 light:hover:text-gray-700',
            isCollapsed && 'md:justify-center md:px-2'
          ]"
          :aria-label="$t('nav.searchLabel')"
          @click="openCommandPalette"
        >
          <Search class="h-4 w-4 shrink-0" aria-hidden="true" />
          <span :class="isCollapsed && 'md:sr-only'">{{ $t('nav.search') }}</span>
          <kbd
            v-if="!isCollapsed"
            class="ml-auto hidden shrink-0 rounded border border-white/[0.1] bg-white/[0.04] px-1.5 py-0.5 font-sans text-[10px] font-medium text-white/40 md:block light:border-gray-200 light:bg-white light:text-gray-400"
          >&#8984;K</kbd>
        </button>
      </div>

      <nav :aria-label="$t('nav.mainNavigation')" class="flex min-h-0 flex-1 flex-col">
        <ScrollArea class="min-h-0 flex-1 sidebar-scroll">
          <div class="space-y-1 px-2 py-2">
            <!-- Pinned: only when somebody has made it theirs. An empty
                 "Pinned" heading is a chore the product hands the user. -->
            <div v-if="pinnedItems.length && !isCollapsed" class="space-y-px">
              <div class="px-2.5 pb-0.5 text-[10px] font-semibold uppercase tracking-wider text-white/35 light:text-gray-400">
                {{ $t('nav.pinned') }}
              </div>
              <SidebarNavItem
                v-for="item in pinnedItems"
                :key="`pin-${item.path}`"
                :item="item"
                :collapsed="isCollapsed"
                :current-path="route.path"
                :pinned="true"
                pinnable
                @navigate="closeMobileMenu"
                @toggle-pin="togglePin(item.path)"
              />
              <div class="!mt-2 mx-2.5 border-t border-white/[0.06] light:border-gray-200" />
            </div>

            <!-- Where work happens: always visible, no heading. "MAIN" over a
                 list of the product's main pages was a row spent saying
                 nothing. -->
            <div class="space-y-px">
              <template v-for="section in workspaceSections" :key="section.label">
                <SidebarNavItem
                  v-for="item in section.items"
                  :key="item.path"
                  :item="item"
                  :collapsed="isCollapsed"
                  :current-path="route.path"
                  @navigate="closeMobileMenu"
                />
              </template>
            </div>

            <!-- Weekly rather than hourly: folded away, opened on demand, and
                 opened automatically for the page you are on. -->
            <div v-if="groupSections.length" class="space-y-px pt-1">
              <div v-if="!isCollapsed" class="mx-2.5 mb-1 border-t border-white/[0.06] light:border-gray-200" />
              <SidebarGroup
                v-for="section in groupSections"
                :key="section.label"
                :section="section"
                :collapsed="isCollapsed"
                :current-path="route.path"
                :is-pinned="isPinned"
                @navigate="closeMobileMenu"
                @toggle-pin="togglePin"
              />
            </div>
          </div>
        </ScrollArea>
      </nav>

      <!-- One bottom bar instead of three stacked bordered strips. -->
      <div class="shrink-0 border-t border-white/[0.08] light:border-gray-200">
        <div :class="['px-2 pt-2', isCollapsed && 'md:flex md:justify-center']">
          <button
            v-if="manageItems.length"
            type="button"
            :class="[
              'sidebar-link group/manage flex w-full items-center gap-2.5 rounded-sm px-2.5 py-[7px] max-md:py-3.5 text-[13px] font-medium transition-colors duration-150',
              manageActive
                ? 'bg-white/[0.07] text-white light:bg-gray-100 light:text-gray-900'
                : 'text-white/60 hover:bg-white/[0.04] hover:text-white light:font-normal light:text-gray-600 light:hover:bg-gray-100/70 light:hover:text-gray-900',
              isCollapsed && 'md:justify-center md:px-2'
            ]"
            :aria-label="$t('nav.manage')"
            :aria-expanded="isManageOpen"
            @click="isManageOpen = true"
          >
            <Settings
              :class="[
                'h-4 w-4 shrink-0 transition-colors duration-150',
                manageActive
                  ? 'text-emerald-400 light:text-emerald-600'
                  : 'text-white/45 group-hover/manage:text-white/80 light:text-gray-400 light:group-hover/manage:text-gray-600'
              ]"
              aria-hidden="true"
            />
            <span :class="isCollapsed && 'md:sr-only'">{{ $t('nav.manage') }}</span>
          </button>
        </div>


        <div :class="['px-2 pt-1', isCollapsed && 'flex justify-center']">
          <NotificationBell :collapsed="isCollapsed" />
        </div>

        <UserMenu :collapsed="isCollapsed" @logout="handleLogout" />
      </div>
    </aside>

    <!-- Main content -->
    <!--
      min-w-0 is what stops a wide table dragging the whole page sideways.

      A flex item defaults to min-width:auto, so `flex-1` alone could not shrink
      this below the widest thing inside it. On a phone the contacts table is
      678px of columns, so the page became 678px wide: the header ran off the
      right edge, the saved views were cut in half, and the only way to read a
      column was to scroll the entire app. With the floor removed, the page is
      the width of the screen and the table scrolls inside its own card.
    -->
    <main id="main-content" class="min-w-0 flex-1 overflow-hidden pt-12 md:pt-0 bg-[#0a0a0b] light:bg-gray-50" role="main">
      <RouterView v-slot="{ Component, route: viewRoute }">
        <Transition name="page" mode="out-in">
          <component :is="Component" :key="viewRoute.meta.stableKey ? String(viewRoute.name) : viewRoute.path" />
        </Transition>
      </RouterView>
      <ActiveCallPanel />
      <ScrollToTop />
    </main>

    <!-- App-wide, so ⌘K and ? work from any page rather than only the ones
         that remembered to mount them (plan 10, S12). -->
    <CommandPalette />
    <ShortcutHelp />
    <ManageDrawer v-model:open="isManageOpen" :items="manageItems" />
  </div>
</template>
