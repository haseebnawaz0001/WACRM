<script setup lang="ts">
/**
 * The shell every record detail uses (plan 10, S12).
 *
 * Tasks and deals each planned their own sheet. Two sheets means two answers to
 * the same questions — where the title goes, where related records go, whether
 * there is a way to reach the full page — and the second one is always the one
 * that forgets the "Open full page" link, which is what an agent needs when the
 * sheet turns out not to be enough.
 *
 * Sections are slots rather than props so a caller composes what its record
 * actually has, and the shell stays responsible only for the frame.
 */
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ExternalLink } from 'lucide-vue-next'

withDefaults(
  defineProps<{
    open: boolean
    title: string
    subtitle?: string | null
    /** Route to the record's own page. Omitted hides the link. */
    fullPageTo?: string | null
    side?: 'right' | 'bottom'
  }>(),
  { side: 'right' }
)

const emit = defineEmits<{ 'update:open': [value: boolean] }>()

defineSlots<{
  /** Extra controls beside the title. */
  actions: () => any
  /** The record's own fields. */
  default: () => any
  /** Related records — tasks, deals, notes. */
  related: () => any
  /** History for this record. */
  activity: () => any
  footer: () => any
}>()

const { t } = useI18n()
</script>

<template>
  <Sheet :open="open" @update:open="emit('update:open', $event)">
    <SheetContent :side="side" class="flex w-full flex-col gap-0 overflow-y-auto sm:max-w-xl">
      <SheetHeader class="space-y-1 pb-4">
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <SheetTitle class="truncate">{{ title }}</SheetTitle>
            <SheetDescription v-if="subtitle" class="truncate">{{ subtitle }}</SheetDescription>
          </div>
          <div class="flex shrink-0 items-center gap-1">
            <slot name="actions" />
            <!-- A sheet is a preview. When it is not enough, the way out has to
                 be visible without hunting for it. -->
            <Button v-if="fullPageTo" as-child variant="ghost" size="sm">
              <RouterLink :to="fullPageTo">
                <ExternalLink class="mr-1.5 h-3.5 w-3.5" />
                {{ t('common.openFullPage') }}
              </RouterLink>
            </Button>
          </div>
        </div>
      </SheetHeader>

      <div class="flex-1 space-y-6 pb-6">
        <section>
          <slot />
        </section>

        <section v-if="$slots.related" class="space-y-2">
          <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {{ t('common.related') }}
          </h3>
          <slot name="related" />
        </section>

        <section v-if="$slots.activity" class="space-y-2">
          <h3 class="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {{ t('common.activity') }}
          </h3>
          <slot name="activity" />
        </section>
      </div>

      <div v-if="$slots.footer" class="sticky bottom-0 border-t bg-background py-3">
        <slot name="footer" />
      </div>
    </SheetContent>
  </Sheet>
</template>
