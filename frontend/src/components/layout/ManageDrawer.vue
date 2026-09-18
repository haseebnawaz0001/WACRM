<script setup lang="ts">
/**
 * The settings surface, as a panel over the page instead of a list inside the
 * sidebar.
 *
 * Sixteen settings pages used to expand inside a strip pinned to the bottom of
 * the rail and capped at 45% of its height. Opening it showed eight of them in
 * a second nested scrollbar and pushed the Messaging, Calling and Analytics
 * sections out of view — leaving their headings pointing at nothing. Given the
 * width of the page instead, all sixteen fit at once, grouped by what somebody
 * came to do, and nothing else has to move to make room.
 */
import { computed, ref, watch, nextTick } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { onKeyStroke } from '@vueuse/core'
import { X } from 'lucide-vue-next'
import { manageGroups, type NavItem } from './navigation'

const props = defineProps<{
  open: boolean
  /** The manage section's items, already filtered by permission. */
  items: Array<NavItem & { active: boolean }>
}>()

const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const route = useRoute()
const { t } = useI18n()
const panel = ref<HTMLElement | null>(null)

/** Every settings page the viewer may open, keyed by path. */
const pagesByPath = computed(() => {
  const out = new Map<string, NavItem>()
  for (const item of props.items) {
    for (const child of item.children ?? [item]) out.set(child.path, child)
  }
  return out
})

/**
 * The groups, filtered to what this viewer can actually open.
 *
 * A heading over an empty column tells an agent there is something here they
 * cannot have, which is worse than the heading not being there.
 */
const groups = computed(() =>
  manageGroups
    .map(group => ({
      label: group.label,
      pages: group.paths.map(path => pagesByPath.value.get(path)).filter((p): p is NavItem => !!p)
    }))
    .filter(group => group.pages.length > 0)
)

/** Anything a group forgot still has somewhere to appear. */
const ungrouped = computed(() => {
  const placed = new Set(manageGroups.flatMap(g => g.paths))
  return [...pagesByPath.value.values()].filter(page => !placed.has(page.path))
})

function close() {
  emit('update:open', false)
}

onKeyStroke('Escape', () => {
  if (props.open) close()
})

// Opening a settings page from the drawer is the end of what the drawer is
// for, so it closes behind you rather than staying over the page you asked
// for.
watch(() => route.path, () => close())

// Focus moves into the panel so the keyboard lands where the eye does.
watch(() => props.open, async isOpen => {
  if (!isOpen) return
  await nextTick()
  panel.value?.querySelector<HTMLElement>('a, button')?.focus()
})

function isCurrent(path: string) {
  return route.path === path || route.path.startsWith(path + '/')
}
</script>

<template>
  <Transition name="manage-fade">
    <div
      v-if="open"
      class="fixed inset-0 z-50 bg-black/50 backdrop-blur-[2px] light:bg-black/25"
      @click="close"
    />
  </Transition>

  <Transition name="manage-panel">
    <div
      v-if="open"
      ref="panel"
      class="fixed inset-y-0 left-0 z-50 flex w-full max-w-[min(38rem,100vw)] flex-col border-r border-white/[0.08] bg-[#0d0d0e] shadow-2xl shadow-black/50 light:border-gray-200 light:bg-white"
      role="dialog"
      aria-modal="true"
      :aria-label="t('nav.manage')"
    >
      <div class="flex h-16 shrink-0 items-center justify-between border-b border-white/[0.08] px-5 light:border-gray-200">
        <div>
          <h2 class="text-sm font-semibold text-white light:text-gray-900">{{ t('nav.manage') }}</h2>
          <p class="text-xs text-white/45 light:text-gray-500">{{ t('nav.manageDesc') }}</p>
        </div>
        <button
          type="button"
          class="sidebar-link rounded-lg p-1.5 text-white/50 transition-colors hover:bg-white/[0.06] hover:text-white light:text-gray-500 light:hover:bg-gray-100 light:hover:text-gray-900"
          :aria-label="t('common.close')"
          @click="close"
        >
          <X class="h-4 w-4" />
        </button>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto sidebar-scroll px-5 py-5">
        <div class="grid gap-x-8 gap-y-6 sm:grid-cols-2">
          <section v-for="group in groups" :key="group.label">
            <h3 class="mb-1.5 px-2 text-[10px] font-semibold uppercase tracking-wider text-white/40 light:text-gray-500">
              {{ t(group.label) }}
            </h3>
            <div class="space-y-px">
              <RouterLink
                v-for="page in group.pages"
                :key="page.path"
                :to="page.path"
                :class="[
                  'sidebar-link group/row flex items-center gap-2.5 rounded-lg px-2 py-[7px] text-[13px] transition-colors duration-150',
                  isCurrent(page.path)
                    ? 'bg-white/[0.07] font-medium text-white light:bg-gray-100 light:text-gray-900'
                    : 'text-white/65 hover:bg-white/[0.04] hover:text-white light:text-gray-600 light:hover:bg-gray-100/70 light:hover:text-gray-900'
                ]"
                :aria-current="isCurrent(page.path) ? 'page' : undefined"
              >
                <component
                  :is="page.icon"
                  :class="[
                    'h-4 w-4 shrink-0 transition-colors duration-150',
                    isCurrent(page.path)
                      ? 'text-emerald-400 light:text-emerald-600'
                      : 'text-white/40 group-hover/row:text-white/75 light:text-gray-400 light:group-hover/row:text-gray-600'
                  ]"
                  aria-hidden="true"
                />
                <span class="truncate">{{ t(page.name) }}</span>
              </RouterLink>
            </div>
          </section>

          <section v-if="ungrouped.length">
            <h3 class="mb-1.5 px-2 text-[10px] font-semibold uppercase tracking-wider text-white/40 light:text-gray-500">
              {{ t('nav.manageOther') }}
            </h3>
            <div class="space-y-px">
              <RouterLink
                v-for="page in ungrouped"
                :key="page.path"
                :to="page.path"
                class="sidebar-link flex items-center gap-2.5 rounded-lg px-2 py-[7px] text-[13px] text-white/65 transition-colors hover:bg-white/[0.04] hover:text-white light:text-gray-600 light:hover:bg-gray-100/70"
              >
                <component :is="page.icon" class="h-4 w-4 shrink-0 text-white/40 light:text-gray-400" aria-hidden="true" />
                <span class="truncate">{{ t(page.name) }}</span>
              </RouterLink>
            </div>
          </section>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
/* One authored moment: the panel arrives from the edge it belongs to, fast
   enough that somebody opening settings mid-task is not made to watch it. */
.manage-panel-enter-active {
  transition: transform 220ms cubic-bezier(0.16, 1, 0.3, 1), opacity 160ms ease-out;
}
.manage-panel-leave-active {
  transition: transform 160ms cubic-bezier(0.4, 0, 1, 1), opacity 120ms ease-in;
}
.manage-panel-enter-from,
.manage-panel-leave-to {
  transform: translateX(-16px);
  opacity: 0;
}

.manage-fade-enter-active,
.manage-fade-leave-active {
  transition: opacity 180ms ease;
}
.manage-fade-enter-from,
.manage-fade-leave-to {
  opacity: 0;
}

@media (prefers-reduced-motion: reduce) {
  .manage-panel-enter-active,
  .manage-panel-leave-active,
  .manage-fade-enter-active,
  .manage-fade-leave-active {
    transition-duration: 1ms;
  }
  .manage-panel-enter-from,
  .manage-panel-leave-to {
    transform: none;
  }
}
</style>
