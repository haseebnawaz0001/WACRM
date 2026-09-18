<script setup lang="ts">
/**
 * A collapsible sidebar section.
 *
 * Messaging, Calling and Analytics are used weekly, not hourly. Held open
 * permanently they pushed Analytics below the fold on a 900px screen; hidden
 * in a drawer they would be two clicks from work people genuinely do. Open on
 * demand, remembered per person, and opened automatically when the current
 * page lives inside — so the sidebar always shows where you are.
 */
import { computed, ref, watch } from 'vue'
import { ChevronRight } from 'lucide-vue-next'
import SidebarNavItem from './SidebarNavItem.vue'
import type { NavItem, NavSection } from './navigation'

type ActiveNavItem = NavItem & { active: boolean }

const props = defineProps<{
  section: Omit<NavSection, 'items'> & { items: ActiveNavItem[] }
  collapsed: boolean
  currentPath: string
  /** Passed through so a group's rows can be pinned like any other. */
  isPinned?: (path: string) => boolean
}>()

const emit = defineEmits<{ navigate: []; togglePin: [path: string] }>()

const STORAGE_KEY = 'sidebar-groups-open'

/** Which groups the viewer left open, by label. */
function readOpenGroups(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
  } catch {
    return {}
  }
}

function persist(label: string, open: boolean) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...readOpenGroups(), [label]: open }))
  } catch {
    // Storage unavailable (private mode); the group just won't be remembered.
  }
}

/** True while any page in this group is the current one. */
const holdsCurrentPage = computed(() => props.section.items.some(item => item.active))

const isOpen = ref(holdsCurrentPage.value || readOpenGroups()[props.section.label] === true)

// Navigating into a group opens it, so the sidebar never shows a collapsed
// header as the only trace of the page you are looking at.
watch(holdsCurrentPage, holds => {
  if (holds) isOpen.value = true
})

function toggle() {
  isOpen.value = !isOpen.value
  persist(props.section.label, isOpen.value)
}

/** Shown on a closed group so its contents are still countable. */
const itemCount = computed(() => props.section.items.length)
</script>

<template>
  <!-- Collapsed rail: the header would be a chevron with nothing to label, so
       the items stand on their own and their tooltips carry the names. -->
  <div v-if="collapsed" class="space-y-px">
    <SidebarNavItem
      v-for="item in section.items"
      :key="item.path"
      :item="item"
      :collapsed="true"
      :current-path="currentPath"
      @navigate="emit('navigate')"
    />
  </div>

  <div v-else>
    <button
      type="button"
      class="sidebar-link group/head flex w-full items-center gap-2.5 rounded-sm px-2.5 py-[7px] max-md:py-3.5 text-[13px] font-medium text-white/60 transition-colors duration-150 hover:bg-white/[0.04] hover:text-white light:font-normal light:text-gray-600 light:hover:bg-gray-100/70 light:hover:text-gray-900"
      :aria-expanded="isOpen"
      @click="toggle"
    >
      <component
        :is="section.icon"
        v-if="section.icon"
        :class="[
          'h-4 w-4 shrink-0 transition-colors duration-150',
          holdsCurrentPage
            ? 'text-emerald-400 light:text-emerald-600'
            : 'text-white/50 group-hover/head:text-white/80 light:text-gray-400 light:group-hover/head:text-gray-600'
        ]"
        aria-hidden="true"
      />
      <span class="truncate">{{ $t(section.label) }}</span>

      <!-- A closed group says how much is behind it; an open one does not need
           to, because you can see. -->
      <span
        v-if="!isOpen"
        class="ml-auto shrink-0 text-[11px] tabular-nums text-white/55 light:text-gray-500"
      >{{ itemCount }}</span>
      <ChevronRight
        :class="[
          'h-3.5 w-3.5 shrink-0 text-white/50 transition-transform duration-200 light:text-gray-400',
          // The count takes ml-auto when closed, so the chevron only needs a
          // gap from it; open, there is no count and the chevron does the
          // pushing itself.
          isOpen ? 'ml-auto rotate-90' : 'ml-1.5'
        ]"
        aria-hidden="true"
      />
    </button>

    <div v-show="isOpen" class="mt-px space-y-px pl-3">
      <SidebarNavItem
        v-for="item in section.items"
        :key="item.path"
        :item="item"
        :collapsed="false"
        :current-path="currentPath"
        :pinned="isPinned ? isPinned(item.path) : false"
        pinnable
        @navigate="emit('navigate')"
        @toggle-pin="emit('togglePin', item.path)"
      />
    </div>
  </div>
</template>
