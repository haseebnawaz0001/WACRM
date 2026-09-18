<script setup lang="ts">
/**
 * ⌘K: one way to reach anything (plan 10, S12).
 *
 * The product grew nine modules, each reached by a sidebar item and each with
 * its own search box. Finding one contact meant knowing which module owns
 * contacts, navigating there, and searching — three steps to answer a question
 * an agent asks fifty times a day.
 *
 * The palette searches contacts by name and number and jumps to any page the
 * viewer may open. Pages are filtered by permission, so it never offers a
 * destination that will bounce them.
 */
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList
} from '@/components/ui/command'
import { navigationSections } from './navigation'
import { useAuthStore } from '@/stores/auth'
import { contactsService } from '@/services/api'
import { useShortcuts } from '@/composables/useShortcuts'
import { useCommandPalette } from '@/composables/useCommandPalette'

// Shared, so the sidebar's search row opens the same palette ⌘K does.
const { open, toggle } = useCommandPalette()
const query = ref('')
const router = useRouter()
const auth = useAuthStore()
const { t } = useI18n()

useShortcuts([
  {
    key: 'k',
    mod: true,
    group: 'global',
    description: t('shortcuts.commandPalette'),
    // The one shortcut that must work mid-sentence: an agent halfway through a
    // reply still needs to look something up.
    whileTyping: true,
    handler: toggle
  }
])

/** Every navigable page the viewer may actually open, flattened. */
const pages = computed(() => {
  const out: { name: string; path: string }[] = []
  for (const section of navigationSections) {
    for (const item of section.items) {
      const children = item.children?.length ? item.children : [item]
      for (const child of children) {
        if (child.permission && !auth.hasPermission(child.permission)) continue
        if (child.module && !auth.moduleEnabled(child.module)) continue
        out.push({ name: t(child.name), path: child.path })
      }
    }
  }
  // The same page can be reached from a parent and a child entry; offering it
  // twice makes the list look broken.
  return out.filter((page, i) => out.findIndex(p => p.path === page.path) === i)
})

const matchingPages = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return pages.value.slice(0, 8)
  return pages.value.filter(p => p.name.toLowerCase().includes(q)).slice(0, 8)
})

const contacts = ref<{ id: string; name: string; phone: string }[]>([])
const searching = ref(false)
let searchToken = 0

watch(query, value => {
  const q = value.trim()
  // Two characters: one letter matches most of the address book, and the
  // request is wasted before the person has said anything.
  if (q.length < 2 || !auth.hasPermission('chat', 'read')) {
    contacts.value = []
    return
  }

  const token = ++searchToken
  searching.value = true
  contactsService
    .search({ search: q, limit: 6, include: [] })
    .then(({ data }) => {
      // A slower earlier request must not overwrite a newer answer.
      if (token !== searchToken) return
      const rows = (data as any)?.data?.contacts ?? (data as any)?.contacts ?? []
      contacts.value = rows.map((c: any) => ({
        id: c.id,
        name: c.profile_name || c.name || c.phone_number,
        phone: c.phone_number
      }))
    })
    .catch(() => {
      if (token === searchToken) contacts.value = []
    })
    .finally(() => {
      if (token === searchToken) searching.value = false
    })
})

function go(path: string) {
  open.value = false
  query.value = ''
  void router.push(path)
}

watch(open, isOpen => {
  if (!isOpen) {
    query.value = ''
    contacts.value = []
  }
})
</script>

<template>
  <CommandDialog v-model:open="open">
    <CommandInput v-model="query" :placeholder="t('shortcuts.commandPlaceholder')" />
    <CommandList>
      <CommandEmpty>{{ t('common.noResults') }}</CommandEmpty>

      <CommandGroup v-if="contacts.length" :heading="t('nav.contacts')">
        <CommandItem
          v-for="contact in contacts"
          :key="contact.id"
          :value="`contact-${contact.id}`"
          @select="go(`/chat/${contact.id}`)"
        >
          <span class="truncate">{{ contact.name }}</span>
          <span class="ml-2 truncate text-xs text-muted-foreground">{{ contact.phone }}</span>
        </CommandItem>
      </CommandGroup>

      <CommandGroup v-if="matchingPages.length" :heading="t('shortcuts.pages')">
        <CommandItem
          v-for="page in matchingPages"
          :key="page.path"
          :value="`page-${page.path}`"
          @select="go(page.path)"
        >
          {{ page.name }}
        </CommandItem>
      </CommandGroup>
    </CommandList>
  </CommandDialog>
</template>
