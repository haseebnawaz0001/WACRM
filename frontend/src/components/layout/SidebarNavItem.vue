<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useNavBadgesStore } from '@/stores/navBadges'
import type { NavItem } from './navigation'

const props = defineProps<{
  item: NavItem & { active: boolean }
  collapsed: boolean
  currentPath: string
}>()

const emit = defineEmits<{
  navigate: []
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
          'sidebar-link nav-active-indicator btn-press relative flex items-center gap-2.5 rounded-lg px-2.5 py-2 max-md:py-3 text-[13px] font-medium transition-colors duration-150',
          parentMarked
            ? 'bg-white/[0.08] text-white light:bg-gray-100 light:text-gray-900'
            : item.active
              ? 'text-white hover:bg-white/[0.04] light:text-gray-900 light:hover:bg-gray-100/70'
              : 'text-white/55 hover:text-white hover:bg-white/[0.04] light:text-gray-600 light:hover:text-gray-900 light:hover:bg-gray-100/70',
          collapsed && 'md:justify-center md:px-2'
        ]"
        :data-active="parentMarked"
        :aria-current="parentMarked ? 'page' : undefined"
        @click="emit('navigate')"
      >
        <component :is="item.icon" class="h-4 w-4 shrink-0" aria-hidden="true" />
        <span :class="collapsed && 'md:sr-only'">{{ $t(item.name) }}</span>
        <span
          v-if="badgeCount > 0"
          :class="[
            'ml-auto shrink-0 rounded-full bg-emerald-500/15 px-1.5 py-0.5 text-[11px] font-medium text-emerald-400 light:bg-emerald-100 light:text-emerald-700',
            collapsed && 'md:absolute md:right-1 md:top-1 md:ml-0 md:px-1 md:py-0'
          ]"
        >
          {{ badgeCount > 99 ? '99+' : badgeCount }}
        </span>
      </RouterLink>
    </TooltipTrigger>
    <TooltipContent side="right" :side-offset="10">{{ $t(item.name) }}</TooltipContent>
  </Tooltip>

  <div
    v-if="showChildren"
    class="ml-[1.125rem] mt-0.5 mb-1 space-y-0.5 border-l border-white/[0.08] pl-2 light:border-gray-200"
  >
    <RouterLink
      v-for="child in item.children"
      :key="child.path"
      :to="child.path"
      :class="[
        'sidebar-link nav-active-indicator nav-child flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 max-md:py-2.5 text-[13px] transition-colors duration-150',
        activeChildPath === child.path
          ? 'bg-white/[0.08] font-medium text-white light:bg-gray-100 light:text-gray-900'
          : 'text-white/50 hover:text-white hover:bg-white/[0.04] light:text-gray-500 light:hover:text-gray-900 light:hover:bg-gray-100/70'
      ]"
      :data-active="activeChildPath === child.path"
      :aria-current="activeChildPath === child.path ? 'page' : undefined"
      @click="emit('navigate')"
    >
      <component :is="child.icon" class="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <span class="truncate">{{ $t(child.name) }}</span>
    </RouterLink>
  </div>
</template>
