<script setup lang="ts">
/**
 * One card on the pipeline board (plan 07).
 *
 * A card has to answer "what is this, who is it with, what is it worth and is
 * it stuck" at a glance — a board you have to click through to read is a list
 * with extra steps.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Badge } from '@/components/ui/badge'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { getInitials, getAvatarGradient } from '@/lib/utils'
import { AlertTriangle, CalendarDays } from 'lucide-vue-next'
import type { Deal } from '@/services/api'

const props = defineProps<{ deal: Deal; currency: string }>()
const { t, locale } = useI18n()

const money = computed(() =>
  new Intl.NumberFormat(locale.value, {
    style: 'currency',
    currency: props.deal.currency || props.currency || 'USD',
    maximumFractionDigits: 0
  }).format(props.deal.value)
)

const closeDate = computed(() => {
  if (!props.deal.expected_close_date) return null
  const date = new Date(props.deal.expected_close_date)
  return {
    label: new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric' }).format(date),
    // A close date in the past is the single most useful warning on a board:
    // it means the forecast is already wrong.
    overdue: date.getTime() < Date.now() && props.deal.status === 'open'
  }
})

const name = computed(() => props.deal.contact_name || props.deal.contact_phone || '')
</script>

<template>
  <div
    class="group cursor-grab rounded-lg border bg-card p-3 shadow-sm transition hover:shadow-md active:cursor-grabbing"
    :class="deal.rotting ? 'border-l-4 border-l-amber-500' : ''"
    tabindex="0"
    role="button"
    :aria-label="deal.title"
  >
    <div class="flex items-start justify-between gap-2">
      <p class="text-sm font-medium leading-snug line-clamp-2">{{ deal.title }}</p>
      <AlertTriangle
        v-if="deal.rotting"
        class="mt-0.5 h-4 w-4 shrink-0 text-amber-500"
        :aria-label="t('pipeline.rotting')"
      />
    </div>

    <p class="mt-1 text-sm font-semibold tabular-nums">{{ money }}</p>

    <div class="mt-2 flex items-center justify-between gap-2">
      <div class="flex min-w-0 items-center gap-1.5">
        <Avatar class="h-5 w-5">
          <AvatarFallback :class="getAvatarGradient(name)" class="text-[10px] text-white">
            {{ getInitials(name) }}
          </AvatarFallback>
        </Avatar>
        <span class="truncate text-xs text-muted-foreground">{{ name }}</span>
      </div>

      <Badge
        v-if="closeDate"
        variant="outline"
        class="shrink-0 gap-1 px-1.5 py-0 text-[11px]"
        :class="closeDate.overdue ? 'border-destructive text-destructive' : ''"
      >
        <CalendarDays class="h-3 w-3" />
        {{ closeDate.label }}
      </Badge>
    </div>
  </div>
</template>
