<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Pin } from 'lucide-vue-next'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useNavBadgesStore } from '@/stores/navBadges'
import type { NavItem } from './navigation'

const props = withDefaults(defineProps<{
  item: NavItem & { active: boolean }
  collapsed: boolean
  currentPath: string
  /** Renders at child scale, for the rows inside an expandable item. */
  nested?: boolean
  /** Offers the pin control on hover. */
  pinnable?: boolean
  /** Whether this destination is currently pinned. */
  pinned?: boolean
}>(), { nested: false, pinnable: false, pinned: false })

const emit = defineEmits<{
  navigate: []
  togglePin: []
}>()

const badges = useNavBadgesStore()

/**
 * The live count for this item, or zero when it has none (plan 10, S12).
 *
 * Zero renders nothing rather than a "0": a badge saying there is nothing to do
 * is a badge asking to be ignored.
 */
const badgeCount = computed(() => (props.item.badgeKey ? badges.countFor(props.item.badgeKey) : 0))

const showChildren = computed(() => !!props.item.children?.length && props.item.active && !props.collapsed)

// With the submenu open the child row marks the current page, so the parent
// keeps only its text emphasis instead of a second highlighted row.
const parentMarked = computed(() => props.item.active && !showChildren.value)

// Longest matching prefix wins, so /settings/teams/:id highlights Teams rather
// than General (/settings).
const activeChildPath = computed(() => {
  let match = ''
  for (const child of props.item.children ?? []) {
    const isMatch = props.currentPath === child.path || props.currentPath.startsWith(child.path + '/')
    if (isMatch && child.path.length > match.length) match = child.path
  }
  return match
})
</script>

<template>
  <Tooltip :disabled="!collapsed" :delay-duration="0">
    <TooltipTrigger as-child>
      <RouterLink
        :to="item.path"
        :class="[
          'sidebar-link group/nav relative flex items-center gap-2.5 rounded-sm text-[13px] transition-colors duration-150',
          nested ? 'px-2.5 py-1.5 max-md:py-3' : 'px-2.5 py-[7px] max-md:py-3.5',
          // The current page is a filled row with an accent icon rather than a
          // rule down its edge: at this density a 3px bar reads as a seam
          // between the sidebar and the page, and it competed with the guide
          // line that submenus already hang from.
          parentMarked
            ? 'bg-white/[0.07] font-medium text-white light:bg-gray-100 light:text-gray-900'
            : item.active
              ? 'font-medium text-white hover:bg-white/[0.04] light:text-gray-900 light:hover:bg-gray-100/70'
              : 'font-medium text-white/60 hover:text-white hover:bg-white/[0.04] light:font-normal light:text-gray-600 light:hover:text-gray-900 light:hover:bg-gray-100/70',
          collapsed && 'md:justify-center md:px-2'
        ]"
        :data-active="parentMarked"
        :aria-current="parentMarked ? 'page' : undefined"
        @click="emit('navigate')"
      >
        <component
          :is="item.icon"
          :class="[
            'shrink-0 transition-colors duration-150',
            nested ? 'h-3.5 w-3.5' : 'h-4 w-4',
            item.active
              ? 'text-emerald-400 light:text-emerald-600'
              : 'text-white/45 group-hover/nav:text-white/80 light:text-gray-400 light:group-hover/nav:text-gray-600'
          ]"
          aria-hidden="true"
        />
        <span :class="['truncate', collapsed && 'md:sr-only']">{{ $t(item.name) }}</span>
        <span
          v-if="badgeCount > 0"
          :class="[
            'ml-auto flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-full px-1 text-[11px] font-semibold tabular-nums',
            'bg-emerald-500/15 text-emerald-400 light:bg-emerald-100 light:text-emerald-700',
            collapsed && 'md:absolute md:right-0.5 md:top-0.5 md:ml-0 md:px-1 md:py-0 md:text-[10px]'
          ]"
        >
          {{ badgeCount > 99 ? '99+' : badgeCount }}
        </span>

        <!-- Pinning, where the thing being pinned is. Hidden until the row is
             hovered or the control itself has focus, so nineteen rows do not
             each carry a permanent button; a pinned row keeps it visible,
             because that is how you unpin. Keyboard users reach it by tabbing,
             which is what makes hiding it acceptable. -->
        <button
          v-if="pinnable && !collapsed"
          type="button"
          :class="[
            'shrink-0 rounded p-0.5 transition-opacity duration-150',
            'focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-white/30',
            badgeCount > 0 ? 'ml-1' : 'ml-auto',
            pinned
              ? 'text-emerald-400 opacity-100 light:text-emerald-600'
              : 'text-white/40 opacity-0 hover:text-white group-hover/nav:opacity-100 light:text-gray-400 light:hover:text-gray-700'
          ]"
          :aria-label="pinned ? $t('nav.unpinThis') : $t('nav.pinThis')"
          :aria-pressed="pinned"
          @click.prevent.stop="emit('togglePin')"
        >
          <Pin :class="['h-3 w-3', pinned && 'fill-current']" aria-hidden="true" />
        </button>
      </RouterLink>
    </TooltipTrigger>
    <TooltipContent side="right" :side-offset="10">
      {{ $t(item.name) }}
      <span v-if="badgeCount > 0" class="ml-1 text-emerald-400">{{ badgeCount > 99 ? '99+' : badgeCount }}</span>
    </TooltipContent>
  </Tooltip>

  <div
    v-if="showChildren"
    class="ml-[1.125rem] mt-0.5 mb-1 space-y-px border-l border-white/[0.08] pl-2 light:border-gray-200"
  >
    <RouterLink
      v-for="child in item.children"
      :key="child.path"
      :to="child.path"
      :class="[
        'sidebar-link nav-active-indicator nav-child flex items-center gap-2.5 rounded-sm px-2.5 py-1.5 max-md:py-3 text-[13px] transition-colors duration-150',
        activeChildPath === child.path
          ? 'bg-white/[0.07] font-medium text-white light:bg-gray-100 light:text-gray-900'
          : 'text-white/55 hover:text-white hover:bg-white/[0.04] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100/70'
      ]"
      :data-active="activeChildPath === child.path"
      :aria-current="activeChildPath === child.path ? 'page' : undefined"
      @click="emit('navigate')"
    >
      <component
        :is="child.icon"
        :class="[
          'h-3.5 w-3.5 shrink-0',
          activeChildPath === child.path ? 'text-emerald-400 light:text-emerald-600' : 'text-white/40 light:text-gray-400'
        ]"
        aria-hidden="true"
      />
      <span class="truncate">{{ $t(child.name) }}</span>
    </RouterLink>
  </div>
</template>
