<script setup lang="ts">
/** Extra request headers, as name and value rows. */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import { Plus, X } from 'lucide-vue-next'
import { FIELD } from './styles'

const props = defineProps<{ modelValue?: Record<string, string> | null; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: Record<string, string>] }>()
const { t } = useI18n()

const rows = computed(() => Object.entries(props.modelValue || {}))

function write(entries: [string, string][]) {
  emit('update:modelValue', Object.fromEntries(entries))
}
function setKey(index: number, key: string) {
  const next = rows.value.map(r => [...r] as [string, string])
  next[index][0] = key
  write(next)
}
function setValue(index: number, value: string) {
  const next = rows.value.map(r => [...r] as [string, string])
  next[index][1] = value
  write(next)
}
function add() {
  write([...rows.value, ['', '']])
}
function remove(index: number) {
  write(rows.value.filter((_, i) => i !== index))
}
</script>

<template>
  <div class="space-y-2">
    <div v-for="([key, value], index) in rows" :key="index" class="flex gap-2">
      <input
        :value="key"
        :class="[FIELD, 'h-9 w-2/5']"
        :placeholder="t('automations.fields.headerName')"
        :disabled="disabled"
        @input="e => setKey(index, (e.target as HTMLInputElement).value)"
      >
      <input
        :value="value"
        :class="[FIELD, 'h-9 flex-1']"
        :placeholder="t('automations.fields.headerValue')"
        :disabled="disabled"
        @input="e => setValue(index, (e.target as HTMLInputElement).value)"
      >
      <Button
        v-if="!disabled"
        variant="ghost" size="icon" class="h-9 w-9 shrink-0"
        :aria-label="t('common.delete')"
        @click="remove(index)"
      >
        <X class="h-4 w-4" />
      </Button>
    </div>
    <Button v-if="!disabled" variant="outline" size="sm" class="h-8" @click="add">
      <Plus class="mr-1.5 h-3.5 w-3.5" />
      {{ t('automations.fields.addHeader') }}
    </Button>
  </div>
</template>
