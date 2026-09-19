<script setup lang="ts">
/**
 * The bar across the top of every page.
 *
 * Pages used to draw this themselves, and it showed: titles at three sizes,
 * a back arrow on half the settings pages, breadcrumbs on some and a
 * sentence on others, the page's main action filled on one page and outlined
 * on the next, and editors with a toolbar all of their own. One bar now, with
 * one anatomy:
 *
 *   [←] [icon] Title  [status]                  [view controls] [secondary] [Primary]
 *              one line: what this page is for, or the trail up to it
 *
 * - It is 56px tall on a desktop — the height of the sidebar's logo row and
 *   the inbox's pane headers — so every top edge in the app is one line.
 * - The back arrow is for pages you drill into (a record, an editor, a "new"
 *   page). A page the navigation reaches has no back arrow; the sidebar is
 *   the way there and away.
 * - The line under the title is the trail to a record (its title is the
 *   record's name, so the trail says what kind of thing it is), and on every
 *   other page one sentence saying what the page is for.
 * - Status sits beside the title, not among the buttons.
 * - Actions run from quiet to loud: view controls (a date range, an account),
 *   then outlined secondary actions, then at most one filled primary action,
 *   always last.
 */
import { computed, type Component } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeft, ChevronRight } from 'lucide-vue-next'

const props = defineProps<{
  title?: string
  description?: string
  icon?: Component
  /** Where the back arrow leads when there is no page to go back to. */
  backLink?: string
  /** The way up to this page. A last crumb without a link is this page itself and is not repeated. */
  breadcrumbs?: Array<{ label: string; href?: string }>
  /**
   * Takes over the back arrow, for an editor that has to ask before
   * unsaved work is thrown away. Bound with @back.
   */
  onBack?: () => void
}>()

const { t } = useI18n()
const router = useRouter()

const trail = computed(() => {
  const crumbs = props.breadcrumbs || []
  return crumbs.length && !crumbs[crumbs.length - 1].href ? crumbs.slice(0, -1) : crumbs
})

/**
 * Back goes back — to the filtered list, the conversation, wherever the
 * person came from — and only falls back to the parent page when they
 * arrived here directly.
 */
function goBack(event: MouseEvent) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
  if (props.onBack) {
    event.preventDefault()
    props.onBack()
    return
  }
  if (window.history.state?.back) {
    event.preventDefault()
    router.back()
  }
}
</script>

<template>
  <header class="shrink-0 border-b border-white/[0.08] bg-[#0a0a0b]/95 backdrop-blur light:border-gray-200 light:bg-white/95">
    <!--
      One row on a desktop. On a phone the actions take a line of their own
      rather than running off the edge.
    -->
    <div class="flex min-h-16 flex-wrap items-center gap-x-3 gap-y-2 px-6 py-2 max-md:px-4 md:h-16 md:flex-nowrap md:py-0">
      <div class="flex min-w-0 flex-1 basis-48 items-center gap-3">
        <RouterLink
          v-if="backLink"
          :to="backLink"
          :aria-label="t('common.back')"
          :title="t('common.back')"
          class="-ml-2 flex h-8 w-8 shrink-0 items-center justify-center rounded-sm text-white/60 transition-colors hover:bg-white/[0.06] hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring light:text-gray-500 light:hover:bg-gray-100 light:hover:text-gray-900"
          @click="goBack"
        >
          <ArrowLeft class="h-4 w-4" />
        </RouterLink>
        <component
          :is="icon"
          v-if="icon"
          class="h-5 w-5 shrink-0 text-white/50 light:text-gray-400"
          aria-hidden="true"
        />
        <div class="min-w-0 flex-1">
          <div class="flex min-w-0 items-center gap-2">
            <slot name="title">
              <h1 class="truncate text-lg font-semibold leading-6 text-white light:text-gray-900">{{ title }}</h1>
            </slot>
            <slot name="status" />
          </div>
          <nav
            v-if="trail.length"
            :aria-label="t('common.breadcrumb', 'Breadcrumb')"
            class="flex min-w-0 items-center gap-1 text-sm leading-5 text-white/50 light:text-gray-500"
          >
            <template v-for="(crumb, index) in trail" :key="index">
              <ChevronRight v-if="index" class="h-3.5 w-3.5 shrink-0 opacity-60" aria-hidden="true" />
              <RouterLink
                v-if="crumb.href"
                :to="crumb.href"
                class="truncate transition-colors hover:text-white light:hover:text-gray-900"
              >{{ crumb.label }}</RouterLink>
              <span v-else class="truncate">{{ crumb.label }}</span>
            </template>
          </nav>
          <p
            v-else-if="description"
            class="truncate text-sm leading-5 text-white/50 max-md:line-clamp-2 max-md:whitespace-normal light:text-gray-500"
            :title="description"
          >{{ description }}</p>
        </div>
      </div>
      <!-- Every control here is one height: a date range beside a button used
           to stand a few pixels taller than it. -->
      <div
        v-if="$slots.actions"
        class="flex shrink-0 flex-wrap items-center gap-2 max-md:w-full [&_[role=combobox]]:h-8 [&_button:not([role=switch])]:h-8"
      >
        <slot name="actions" />
      </div>
    </div>
    <!-- A page's own views (Build / History), when they belong to the bar. -->
    <div v-if="$slots.tabs" class="px-4 max-md:px-2">
      <slot name="tabs" />
    </div>
  </header>
</template>
