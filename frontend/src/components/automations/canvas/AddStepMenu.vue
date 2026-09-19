<script setup lang="ts">
/**
 * "What should happen here?" — the menu behind every + on the path.
 *
 * Each choice is named for what it does to the customer or the team, with a
 * line saying what that means, because "set_conversation_status" and "call a
 * webhook" are the product's words, not the person's. Questions and waits
 * come first: they are what turn a list of chores into a process.
 */
import { computed, inject, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { stepIcons, toneOf, toneTile, paletteGroups } from '../flow/steps'
import type { InsertPoint } from '../flow/tree'
import { flowContextKey } from './context'

const props = defineProps<{ point: InsertPoint }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const flow = inject(flowContextKey)!
const search = ref('')
const active = ref(0)
const inputEl = ref<HTMLInputElement | null>(null)
const listEl = ref<HTMLElement | null>(null)

/** Whether the list runs on below what is showing. */
const more = ref(false)
function onScroll() {
  const el = listEl.value
  more.value = !!el && el.scrollTop + el.clientHeight < el.scrollHeight - 4
}

onMounted(async () => {
  await nextTick()
  inputEl.value?.focus()
  onScroll()
})

interface Item { type: string; title: string; description: string; blocked: string | null }

const groups = computed(() => {
  const q = search.value.trim().toLowerCase()
  return paletteGroups(flow.available.value).map(group => ({
    key: group.key,
    items: group.types.map<Item>(type => ({
      type,
      title: t(`automations.steps.${type}.title`, type),
      description: t(`automations.steps.${type}.description`, ''),
      blocked: flow.blockedReason(props.point, type)
    })).filter(item => !q || item.title.toLowerCase().includes(q) || item.description.toLowerCase().includes(q))
  })).filter(group => group.items.length)
})

const flat = computed(() => groups.value.flatMap(g => g.items))

function pick(item: Item) {
  if (item.blocked) return
  flow.insert(props.point, item.type)
  emit('close')
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    active.value = Math.min(active.value + 1, flat.value.length - 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    active.value = Math.max(active.value - 1, 0)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    const item = flat.value[active.value]
    if (item) pick(item)
    return
  } else {
    return
  }
  nextTick(() => listEl.value?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' }))
}

function indexOf(item: Item) {
  return flat.value.indexOf(item)
}
</script>

<template>
  <div class="flex max-h-[min(34rem,var(--reka-popover-content-available-height,70vh))] flex-col">
    <div class="border-b border-border px-3">
      <input
        ref="inputEl"
        v-model="search"
        class="h-10 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
        :placeholder="t('automations.canvas.addSearch')"
        :aria-label="t('automations.canvas.addSearch')"
        @keydown="onKeydown"
        @input="active = 0; nextTick(onScroll)"
      >
    </div>
    <div ref="listEl" class="relative min-h-0 flex-1 overflow-y-auto overscroll-contain p-1.5" role="listbox" @scroll="onScroll">
      <section v-for="group in groups" :key="group.key" class="pb-1">
        <p class="px-2 pb-1 pt-2 text-[11px] font-medium text-muted-foreground">
          {{ t(`automations.stepGroups.${group.key}`) }}
        </p>
        <button
          v-for="item in group.items"
          :key="item.type"
          type="button"
          role="option"
          :aria-disabled="!!item.blocked"
          :data-active="indexOf(item) === active"
          :title="item.blocked || undefined"
          :class="[
            'flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-left data-[active=true]:bg-accent',
            item.blocked ? 'cursor-not-allowed opacity-45' : ''
          ]"
          @mouseenter="active = indexOf(item)"
          @click="pick(item)"
        >
          <span :class="['mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md', toneTile[toneOf(item.type)]]">
            <component :is="stepIcons[item.type]" v-if="stepIcons[item.type]" class="h-3.5 w-3.5" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block text-sm font-medium leading-5">{{ item.title }}</span>
            <span class="block text-xs leading-4 text-muted-foreground">{{ item.blocked || item.description }}</span>
          </span>
        </button>
      </section>
      <p v-if="!groups.length" class="px-2 py-6 text-center text-sm text-muted-foreground">
        {{ t('automations.picker.noMatch') }}
      </p>
      <!-- More below: a fade over the last row says the list goes on. -->
      <div
        v-if="more"
        class="pointer-events-none sticky -bottom-1.5 -mx-1.5 -mb-1.5 h-10 bg-gradient-to-t from-popover to-transparent"
        aria-hidden="true"
      />
    </div>
  </div>
</template>
