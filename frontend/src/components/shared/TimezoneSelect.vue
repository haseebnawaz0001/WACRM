<script setup lang="ts">
/**
 * TimezoneSelect is a searchable IANA timezone picker (plan 10, S11).
 *
 * The settings page offered five zones: UTC, New York, Los Angeles, London and
 * Tokyo. Every organization outside those had to pick the wrong one, which then
 * decided its business hours, its SLA windows and every date range in its
 * reports — so the setting that existed to make the product local made it
 * quietly wrong instead.
 *
 * The list comes from the browser's own IANA database, so it needs no
 * maintenance, and each entry shows its current UTC offset because "Asia/Thimphu"
 * means nothing to most people and "UTC+06:00" does.
 */
import { computed, ref } from 'vue'
import { Check, ChevronsUpDown } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'

const props = withDefaults(defineProps<{
  modelValue: string
  placeholder?: string
  searchPlaceholder?: string
  emptyText?: string
  /** Label for the "use the organization's zone" entry; omitted when absent. */
  inheritLabel?: string
  disabled?: boolean
}>(), {
  placeholder: 'Select a timezone',
  searchPlaceholder: 'Search timezones',
  emptyText: 'No timezone found',
})

const emit = defineEmits<{ 'update:modelValue': [string] }>()

const open = ref(false)

/** offsetFor renders a zone's current UTC offset, e.g. UTC+05:30. */
function offsetFor(zone: string): string {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      timeZoneName: 'longOffset',
    }).formatToParts(new Date())
    return parts.find(p => p.type === 'timeZoneName')?.value || ''
  } catch {
    return ''
  }
}

const zones = computed(() => {
  let names: string[] = []
  try {
    // supportedValuesOf is the browser's IANA list; older engines lack it, and
    // a short fallback beats an empty picker.
    names = (Intl as any).supportedValuesOf?.('timeZone') ?? []
  } catch {
    names = []
  }
  if (names.length === 0) {
    names = ['UTC', 'America/New_York', 'America/Los_Angeles', 'Europe/London', 'Asia/Kolkata', 'Asia/Tokyo']
  }
  if (!names.includes('UTC')) names = ['UTC', ...names]

  return names.map(name => ({
    name,
    label: `${name.replace(/_/g, ' ')} (${offsetFor(name)})`,
  }))
})

const selectedLabel = computed(() => {
  if (!props.modelValue) return props.inheritLabel || props.placeholder
  return zones.value.find(z => z.name === props.modelValue)?.label || props.modelValue
})

function select(zone: string) {
  emit('update:modelValue', zone)
  open.value = false
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button
        variant="outline"
        role="combobox"
        :aria-expanded="open"
        :disabled="disabled"
        class="w-full justify-between bg-white/[0.04] border-white/[0.1] text-white/70 font-normal light:bg-white light:border-gray-200 light:text-gray-700"
      >
        <span class="truncate">{{ selectedLabel }}</span>
        <ChevronsUpDown class="ml-2 h-4 w-4 shrink-0 opacity-50" />
      </Button>
    </PopoverTrigger>
    <PopoverContent class="w-[--reka-popover-trigger-width] p-0" align="start">
      <Command>
        <CommandInput :placeholder="searchPlaceholder" />
        <CommandList class="max-h-72">
          <CommandEmpty>{{ emptyText }}</CommandEmpty>
          <CommandGroup>
            <CommandItem
              v-if="inheritLabel"
              value=""
              @select="() => select('')"
            >
              <Check :class="['mr-2 h-4 w-4', modelValue === '' ? 'opacity-100' : 'opacity-0']" />
              {{ inheritLabel }}
            </CommandItem>
            <CommandItem
              v-for="zone in zones"
              :key="zone.name"
              :value="zone.label"
              @select="() => select(zone.name)"
            >
              <Check :class="['mr-2 h-4 w-4', modelValue === zone.name ? 'opacity-100' : 'opacity-0']" />
              {{ zone.label }}
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>
</template>
