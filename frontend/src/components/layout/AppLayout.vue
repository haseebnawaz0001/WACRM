<script setup lang="ts">
import { ref, computed, onMounted, watch, nextTick } from 'vue'
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
  X
} from 'lucide-vue-next'
import { wsService } from '@/services/websocket'
import { authService, organizationsService } from '@/services/api'
import OrganizationSwitcher from './OrganizationSwitcher.vue'
import UserMenu from './UserMenu.vue'
import NotificationBell from './NotificationBell.vue'
import SidebarNavItem from './SidebarNavItem.vue'
import ActiveCallPanel from '@/components/calling/ActiveCallPanel.vue'
import { ScrollToTop } from '@/components/shared'
import { navigationSections, type NavSection } from './navigation'

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

    wsService.connect(async () => {
      try {
        const resp = await authService.getWSToken()
        return resp.data.data.token
      } catch {
        return null
      }
    })
  }
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
        : originalPath === '/chat'
          ? route.name === 'chat' || route.name === 'chat-conversation'
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

const mainSections = computed(() => navSections.value.filter(s => !s.pinBottom))
const bottomSections = computed(() => navSections.value.filter(s => s.pinBottom))

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
        <div class="h-7 w-7 rounded-lg bg-gradient-to-br from-emerald-500 to-green-600 flex items-center justify-center shadow-lg shadow-emerald-500/20">
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
            class="sidebar-link flex items-center gap-2 rounded-lg p-0.5"
            :aria-label="isCollapsed ? 'WA CRM' : undefined"
          >
            <div class="h-7 w-7 rounded-lg bg-gradient-to-br from-emerald-500 to-green-600 flex items-center justify-center shadow-lg shadow-emerald-500/20">
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

      <nav :aria-label="$t('nav.mainNavigation')" class="flex min-h-0 flex-1 flex-col">
        <ScrollArea class="min-h-0 flex-1">
          <div class="px-2 py-2">
            <template v-for="(section, sIdx) in mainSections" :key="section.label">
              <div
                v-if="section.label && !isCollapsed"
                :class="['px-2.5 pb-1 text-[10px] font-semibold uppercase tracking-wider text-white/45 light:text-gray-500', sIdx === 0 ? 'pt-1' : 'pt-4']"
              >
                {{ $t(section.label) }}
              </div>
              <div v-else-if="sIdx > 0" class="my-2 mx-2 border-t border-white/[0.06] light:border-gray-200" />

              <div class="space-y-0.5">
                <SidebarNavItem
                  v-for="item in section.items"
                  :key="item.path"
                  :item="item"
                  :collapsed="isCollapsed"
                  :current-path="route.path"
                  @navigate="closeMobileMenu"
                />
              </div>
            </template>
          </div>
        </ScrollArea>

        <!-- Bottom-pinned navigation (Settings). Capped so an open submenu can't push the main list out of view. -->
        <div
          v-if="bottomSections.length > 0"
          class="max-h-[45%] shrink-0 overflow-y-auto border-t border-white/[0.06] px-2 py-2 light:border-gray-200"
        >
          <template v-for="section in bottomSections" :key="section.label">
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
      </nav>

      <div :class="['border-t border-white/[0.08] light:border-gray-200 px-2 py-1.5', isCollapsed && 'flex justify-center']">
        <NotificationBell :collapsed="isCollapsed" />
      </div>

      <UserMenu :collapsed="isCollapsed" @logout="handleLogout" />
    </aside>

    <!-- Main content -->
    <main id="main-content" class="flex-1 overflow-hidden pt-12 md:pt-0 bg-[#0a0a0b] light:bg-gray-50" role="main">
      <RouterView v-slot="{ Component, route: viewRoute }">
        <Transition name="page" mode="out-in">
          <component :is="Component" :key="viewRoute.meta.stableKey ? String(viewRoute.name) : viewRoute.path" />
        </Transition>
      </RouterView>
      <ActiveCallPanel />
      <ScrollToTop />
    </main>
  </div>
</template>
