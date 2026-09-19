<script setup lang="ts">
/**
 * One action's editor, as a card in a list (plan 10, S7).
 *
 * Used where actions are a plain list — the chatbot's CRM-action node and
 * keyword rules. The automation builder puts the same fields in its step
 * inspector instead; both render CrmActionFields, so an action asks for the
 * same things in all three places.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Trash2, ArrowUp, ArrowDown } from 'lucide-vue-next'
import { stepIcons } from '@/components/automations/flow/steps'
import CrmActionFields from './CrmActionFields.vue'
import { missingFields, type CrmActionSpec } from './schema'

const props = withDefaults(
  defineProps<{
    action: CrmActionSpec
    index: number
    total: number
    editable?: boolean
    /** Hidden where the surface has no per-action error branch to take. */
    showContinueOnError?: boolean
  }>(),
  { editable: true, showContinueOnError: true }
)

const emit = defineEmits<{
  (e: 'remove'): void
  (e: 'move', direction: -1 | 1): void
  (e: 'update', action: CrmActionSpec): void
}>()

const { t } = useI18n()

const isFirst = computed(() => props.index === 0)
const isLast = computed(() => props.index === props.total - 1)
const missing = computed(() => missingFields(props.action.type, props.action.config || {}))
const icon = computed(() => stepIcons[props.action.type])

function setConfig(config: Record<string, any>) {
  emit('update', { ...props.action, config })
}
</script>

<template>
  <div
    :class="[
      'space-y-3 rounded-md border p-3 transition-colors',
      missing.length ? 'border-amber-500/40 light:border-amber-300' : 'border-border'
    ]"
  >
    <div class="flex items-center justify-between gap-2">
      <div class="flex min-w-0 items-center gap-2">
        <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-sm bg-white/[0.06] text-white/70 light:bg-gray-100 light:text-gray-600">
          <component :is="icon" v-if="icon" class="h-3.5 w-3.5" />
          <span v-else class="text-[11px] tabular-nums">{{ index + 1 }}</span>
        </span>
        <span class="truncate text-sm font-medium">
          {{ t(`automations.steps.${action.type}.title`, action.type) }}
        </span>
      </div>
      <div v-if="editable" class="flex items-center gap-0.5">
        <Button
          variant="ghost" size="icon" class="h-8 w-8" :disabled="isFirst"
          :aria-label="t('common.moveUp')"
          @click="emit('move', -1)"
        >
          <ArrowUp class="h-4 w-4" />
        </Button>
        <Button
          variant="ghost" size="icon" class="h-8 w-8" :disabled="isLast"
          :aria-label="t('common.moveDown')"
          @click="emit('move', 1)"
        >
          <ArrowDown class="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          class="h-8 w-8 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          :aria-label="t('common.delete')"
          @click="emit('remove')"
        >
          <Trash2 class="h-4 w-4" />
        </Button>
      </div>
    </div>

    <CrmActionFields
      :type="action.type"
      :config="action.config || {}"
      :disabled="!editable"
      :missing="missing"
      @update:config="setConfig"
    />

    <label v-if="showContinueOnError" class="flex items-center justify-between gap-3 text-xs text-muted-foreground">
      {{ t('automations.carryOnIfFails') }}
      <Switch
        :model-value="!!action.continue_on_error"
        :disabled="!editable"
        @update:model-value="(v: boolean) => emit('update', { ...action, continue_on_error: v })"
      />
    </label>
  </div>
</template>
