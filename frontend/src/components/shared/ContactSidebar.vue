<script setup lang="ts">
/**
 * One contact panel, used by the chat and the profile (plan 10, S12).
 *
 * Plans 01, 02, 04 and 07 each planned their own column of contact detail, and
 * the chat already had one. Four panels means four answers to "where do tags
 * go?" — and, worse, four places to remember when a new kind of record needs a
 * home. The section a customer's deals appear in should not depend on which
 * screen the agent happens to be looking at.
 *
 * Sections are declared by the caller and rendered in order. The shell owns
 * collapsing, headings, the empty state and remembering what the viewer folded
 * away; the caller owns what goes inside.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronDown } from 'lucide-vue-next'

export interface SidebarSection {
  /** Stable id — used as the slot name and the collapse-memory key. */
  id: string
  /** Heading. Omitted renders the section without one (the header section). */
  label?: string
  /** Start collapsed. */
  collapsed?: boolean
  /** Hide entirely — for sections the viewer has no permission for. */
  hidden?: boolean
}

const props = withDefaults(
  defineProps<{
    sections: SidebarSection[]
    /** Namespace for the collapse memory, so chat and profile can differ. */
    storageKey?: string
  }>(),
  { storageKey: 'contact-sidebar' }
)

const { t } = useI18n()

const visibleSections = computed(() => props.sections.filter(s => !s.hidden))

/**
 * Which sections the viewer has folded away.
 *
 * Remembered per browser because it is a working preference, not a setting:
 * an agent who never uses deals should not have to collapse that section every
 * time they open a chat. A blocked or full localStorage must not break the
 * panel, so every access is guarded.
 */
const collapsed = ref<Record<string, boolean>>({})

function load() {
  const seeded: Record<string, boolean> = {}
  for (const section of props.sections) {
    if (section.collapsed) seeded[section.id] = true
  }
  try {
    const raw = localStorage.getItem(`${props.storageKey}:collapsed`)
    if (raw) Object.assign(seeded, JSON.parse(raw))
  } catch {
    // private window, cleared site data, quota — the panel still works
  }
  collapsed.value = seeded
}
load()

watch(
  () => props.storageKey,
  () => load()
)

function toggle(id: string) {
  collapsed.value = { ...collapsed.value, [id]: !collapsed.value[id] }
  try {
    localStorage.setItem(`${props.storageKey}:collapsed`, JSON.stringify(collapsed.value))
  } catch {
    // as above: the fold still applies for this session
  }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <template v-for="section in visibleSections" :key="section.id">
      <!-- An unlabelled section is the header: always open, no chrome. -->
      <section v-if="!section.label">
        <slot :name="section.id" />
      </section>

      <section v-else class="rounded-lg border">
        <button
          type="button"
          class="flex w-full items-center justify-between gap-2 px-3 py-2 text-left"
          :aria-expanded="!collapsed[section.id]"
          @click="toggle(section.id)"
        >
          <span class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {{ section.label }}
          </span>
          <ChevronDown
            class="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform"
            :class="collapsed[section.id] && '-rotate-90'"
          />
        </button>

        <div v-show="!collapsed[section.id]" class="border-t px-3 py-2">
          <slot :name="section.id">
            <p class="text-sm text-muted-foreground">{{ t('common.nothingHere') }}</p>
          </slot>
        </div>
      </section>
    </template>
  </div>
</template>
