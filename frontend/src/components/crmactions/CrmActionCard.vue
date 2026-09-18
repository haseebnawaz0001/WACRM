<script setup lang="ts">
/**
 * One action's editor (plan 10, S7).
 *
 * Shared by the automation builder, the chatbot CRM-action node and keyword
 * rules, so the three cannot drift on what an action needs.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'
import { Trash2, ArrowUp, ArrowDown } from 'lucide-vue-next'
import { fieldsFor, configList, setConfigList, type CrmActionSpec } from './schema'

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
}>()

const { t } = useI18n()

const fields = computed(() => fieldsFor(props.action.type))
const isFirst = computed(() => props.index === 0)
const isLast = computed(() => props.index === props.total - 1)
</script>

<template>
  <div class="space-y-3 rounded-md border p-3">
    <div class="flex items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <Badge variant="secondary" class="px-1.5 py-0">{{ index + 1 }}</Badge>
        <span class="font-medium">
          {{ t(`automations.actions.${action.type}`, action.type) }}
        </span>
      </div>
      <!-- Drawn icons, not typed ones. These two were the literal characters
           ↑ and ↓ set in the body font, so they sat at a different weight and
           optical size from every other control in the app and shifted with
           whatever font the system resolved. -->
      <div v-if="editable" class="flex items-center gap-1">
        <Button
          variant="ghost" size="icon" :disabled="isFirst"
          :aria-label="t('common.moveUp')"
          @click="emit('move', -1)"
        >
          <ArrowUp class="h-4 w-4" />
        </Button>
        <Button
          variant="ghost" size="icon" :disabled="isLast"
          :aria-label="t('common.moveDown')"
          @click="emit('move', 1)"
        >
          <ArrowDown class="h-4 w-4" />
        </Button>
        <!-- Destructive on hover, like every other delete in the product. Red
             at rest made it the loudest thing in a group of three peers. -->
        <Button
          variant="ghost"
          size="icon"
          class="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          :aria-label="t('common.delete')"
          @click="emit('remove')"
        >
          <Trash2 class="h-4 w-4" />
        </Button>
      </div>
    </div>

    <div v-for="field in fields" :key="field.key" class="space-y-1.5">
      <Label class="text-xs">{{ t(`automations.config.${field.label}`, field.label) }}</Label>
      <Textarea
        v-if="field.kind === 'textarea'"
        v-model="action.config[field.key]"
        :rows="2"
        :disabled="!editable"
      />
      <Input
        v-else-if="field.kind === 'list'"
        :model-value="configList(action, field.key)"
        :disabled="!editable"
        @update:model-value="v => setConfigList(action, field.key, String(v))"
      />
      <Input
        v-else
        v-model="action.config[field.key]"
        :type="field.kind === 'number' ? 'number' : 'text'"
        :disabled="!editable"
      />
    </div>

    <p v-if="fields.length === 0" class="text-xs text-muted-foreground">
      {{ t('automations.noConfigurableFields') }}
    </p>

    <label v-if="showContinueOnError" class="flex items-center gap-2 text-xs text-muted-foreground">
      <Switch
        :model-value="!!action.continue_on_error"
        :disabled="!editable"
        @update:model-value="(v: boolean) => action.continue_on_error = v"
      />
      {{ t('automations.continueOnError') }}
    </label>
  </div>
</template>
