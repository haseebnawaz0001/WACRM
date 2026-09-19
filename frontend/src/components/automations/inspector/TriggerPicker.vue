<script setup lang="ts">
/**
 * "What should start it?" — every trigger, in the words of the person asking.
 *
 * Grouped by what it is about (a contact, a conversation, time passing) and
 * searchable, with a line under each saying when exactly it fires. The time
 * triggers are the ones people do not think to look for — nothing happens
 * when a customer goes quiet — so their group says what they are for.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Search, Check } from 'lucide-vue-next'
import { toneTile } from '../flow/steps'
import { triggerDef, triggerGroupOrder } from '../flow/triggers'

const props = defineProps<{
  modelValue?: string
  /** Trigger types the backend offers. */
  available: string[]
  dense?: boolean
}>()
const emit = defineEmits<{ pick: [type: string] }>()

const { t } = useI18n()
const search = ref('')

const groups = computed(() => {
  const q = search.value.trim().toLowerCase()
  const byGroup: Record<string, string[]> = {}
  for (const type of props.available) {
    const def = triggerDef(type)
    const group = def?.group || 'other'
    const title = t(`automations.triggers.${type}`, type).toLowerCase()
    const description = t(`automations.triggerDescriptions.${type}`, '').toLowerCase()
    if (q && !title.includes(q) && !description.includes(q)) continue
    ;(byGroup[group] ||= []).push(type)
  }
  const order = [...triggerGroupOrder, ...Object.keys(byGroup).filter(g => !triggerGroupOrder.includes(g))]
  return order.filter(g => byGroup[g]?.length).map(g => ({ key: g, types: byGroup[g] }))
})
</script>

<template>
  <div class="space-y-3">
    <div class="relative">
      <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
      <input
        v-model="search"
        class="h-10 w-full rounded-sm border border-white/[0.1] bg-white/[0.04] pl-9 pr-3 text-sm outline-none placeholder:text-white/40 focus-visible:border-emerald-500/60 focus-visible:ring-2 focus-visible:ring-emerald-500/30 light:border-gray-200 light:bg-white light:placeholder:text-gray-400"
        :placeholder="t('automations.triggerPicker.search')"
        :aria-label="t('automations.triggerPicker.search')"
      >
    </div>
    <section v-for="group in groups" :key="group.key">
      <h3 class="px-1 pb-1.5 pt-2 text-xs font-medium text-muted-foreground">
        {{ t(`automations.groups.${group.key}`, group.key) }}
      </h3>
      <div :class="dense ? 'space-y-0.5' : 'grid gap-1'">
        <button
          v-for="type in group.types"
          :key="type"
          type="button"
          :aria-pressed="modelValue === type"
          :class="[
            'flex w-full items-start gap-3 rounded-md px-2.5 py-2 text-left transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40',
            modelValue === type
              ? 'bg-emerald-500/10 light:bg-emerald-50'
              : 'hover:bg-white/[0.05] light:hover:bg-gray-50'
          ]"
          @click="emit('pick', type)"
        >
          <span :class="['mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md', toneTile.trigger]">
            <component :is="triggerDef(type)?.icon" v-if="triggerDef(type)" class="h-3.5 w-3.5" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block text-sm font-medium leading-5">{{ t(`automations.triggers.${type}`, type) }}</span>
            <span class="block text-xs leading-4 text-muted-foreground">{{ t(`automations.triggerDescriptions.${type}`, '') }}</span>
          </span>
          <Check v-if="modelValue === type" class="mt-1 h-4 w-4 shrink-0 text-emerald-400 light:text-emerald-600" />
        </button>
      </div>
    </section>
    <p v-if="!groups.length" class="py-6 text-center text-sm text-muted-foreground">{{ t('automations.picker.noMatch') }}</p>
  </div>
</template>
