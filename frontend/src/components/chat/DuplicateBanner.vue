<script setup lang="ts">
/**
 * "This looks like the same person" — in the chat, where it matters
 * (plan 06, plan 10 §4.10).
 *
 * Duplicates had a settings page of their own, which is the wrong place for
 * them: nobody opens a data-quality screen. The moment an agent needs to know
 * two records are the same person is the moment they are reading one of them
 * and the history looks oddly short — so the banner belongs in the chat header.
 *
 * It offers the merge and the dismissal, because a banner that only reports a
 * problem is a banner people learn to scroll past.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Users } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/shared'
import { duplicatesService, type DuplicateCandidate } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'

const props = defineProps<{ contactId?: string | null }>()
const emit = defineEmits<{ merged: [survivorId: string] }>()

const { t } = useI18n()
const auth = useAuthStore()

const candidate = ref<DuplicateCandidate | null>(null)
const confirmOpen = ref(false)
const isMerging = ref(false)

// Merging is irreversible and moves one customer's whole history onto another.
// It is gated on the same permission as deleting a contact.
const canMerge = computed(() => auth.hasPermission('contacts', 'delete'))

/** The other contact in the pair — whichever side this chat is not. */
const other = computed(() => {
  if (!candidate.value || !props.contactId) return null
  return candidate.value.contact_a.id === props.contactId
    ? candidate.value.contact_b
    : candidate.value.contact_a
})

async function load() {
  candidate.value = null
  // contacts:write, not read. The endpoint behind this requires write — it
  // exists to offer a merge — so a read-only agent asking for it got a 403 on
  // every conversation they opened. The banner swallowed it, so nobody saw
  // anything except a failed request per thread.
  if (!props.contactId || !auth.hasPermission('contacts', 'write')) return

  try {
    const { data } = await duplicatesService.list(50)
    const rows = (data as any)?.data?.candidates ?? (data as any)?.candidates ?? []
    candidate.value =
      rows.find(
        (c: DuplicateCandidate) =>
          c.contact_a.id === props.contactId || c.contact_b.id === props.contactId
      ) ?? null
  } catch {
    // A banner is a convenience. Failing to fetch it must not put an error in
    // front of somebody who is trying to answer a customer.
    candidate.value = null
  }
}

watch(() => props.contactId, load, { immediate: true })

async function merge() {
  if (!candidate.value || !props.contactId || !other.value) return

  isMerging.value = true
  try {
    // This chat's contact survives: the agent is looking at it, and moving
    // them to a different record mid-conversation would be the surprise.
    await duplicatesService.merge(props.contactId, other.value.id)
    toast.success(t('duplicates.merged'))
    confirmOpen.value = false
    emit('merged', props.contactId)
    candidate.value = null
  } catch {
    toast.error(t('duplicates.mergeFailed'))
  } finally {
    isMerging.value = false
  }
}

async function dismiss() {
  if (!candidate.value) return
  const id = candidate.value.id
  candidate.value = null
  try {
    await duplicatesService.dismiss(id)
  } catch {
    // Dismissing is a preference, not a transaction; if it did not stick the
    // banner comes back, which is a smaller problem than an error toast.
  }
}
</script>

<template>
  <div
    v-if="candidate && other"
    class="flex flex-wrap items-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm"
  >
    <Users class="h-4 w-4 shrink-0 text-amber-600 dark:text-amber-500" />
    <span class="min-w-0 flex-1">
      {{ t('duplicates.bannerText', { name: other.profile_name || other.phone_number }) }}
    </span>

    <Button v-if="canMerge" size="sm" variant="outline" @click="confirmOpen = true">
      {{ t('duplicates.merge') }}
    </Button>
    <Button size="sm" variant="ghost" @click="dismiss">
      {{ t('duplicates.notADuplicate') }}
    </Button>

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="t('duplicates.confirmTitle')"
      :description="t('duplicates.confirmBody', { name: other.profile_name || other.phone_number })"
      :confirm-label="t('duplicates.merge')"
      :is-submitting="isMerging"
      variant="destructive"
      @confirm="merge"
    />
  </div>
</template>
