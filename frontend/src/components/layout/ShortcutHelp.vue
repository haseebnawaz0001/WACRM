<script setup lang="ts">
/**
 * `?` shows what the keyboard can do (plan 10, S12).
 *
 * Shortcuts that nobody can discover are shortcuts nobody uses. The list is
 * built from the live registry rather than written out here, so a shortcut
 * cannot be added without appearing, and cannot be removed and left documented.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useShortcutHelp, useShortcuts, formatShortcut, type Shortcut } from '@/composables/useShortcuts'

const open = ref(false)
const { t } = useI18n()
const registry = useShortcutHelp()

useShortcuts([
  {
    key: '?',
    shift: true,
    group: 'global',
    description: t('shortcuts.showHelp'),
    handler: () => {
      open.value = !open.value
    }
  }
])

/** Grouped for display, in a stable order so the sheet does not reshuffle. */
const groups = computed(() => {
  const byGroup = new Map<string, Shortcut[]>()
  for (const shortcut of registry.values() as Iterable<Shortcut>) {
    const list = byGroup.get(shortcut.group) ?? []
    list.push(shortcut)
    byGroup.set(shortcut.group, list)
  }
  return [...byGroup.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, items]) => ({
      name,
      items: [...items].sort((a, b) => a.description.localeCompare(b.description))
    }))
})
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[80vh] overflow-y-auto sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{{ t('shortcuts.title') }}</DialogTitle>
      </DialogHeader>

      <p v-if="!groups.length" class="text-sm text-muted-foreground">
        {{ t('shortcuts.noneHere') }}
      </p>

      <div v-for="group in groups" :key="group.name" class="space-y-2">
        <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {{ group.name }}
        </h3>
        <div
          v-for="shortcut in group.items"
          :key="shortcut.description"
          class="flex items-center justify-between gap-4 text-sm"
        >
          <span class="min-w-0 truncate">{{ shortcut.description }}</span>
          <kbd class="shrink-0 rounded border bg-muted px-1.5 py-0.5 font-mono text-xs">
            {{ formatShortcut(shortcut) }}
          </kbd>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
