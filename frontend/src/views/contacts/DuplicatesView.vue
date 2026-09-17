<script setup lang="ts">
/**
 * Duplicate review (plan 06).
 *
 * Two records for one person is a problem that grows quietly: history splits,
 * the agent answering sees half the conversation, and nobody goes looking for
 * it by hand. The scanner suggests pairs; a person decides, because merging is
 * not reversible and an automatic merge that is wrong is worse than a duplicate.
 *
 * Each pair shows why it was suggested and how much history each side holds,
 * which is usually what decides which record survives.
 */
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle
} from '@/components/ui/alert-dialog'
import { PageHeader, ErrorState } from '@/components/shared'
import { duplicatesService, type DuplicateCandidate, type DuplicateContactSummary } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { formatDate } from '@/lib/utils'
import { Copy, RefreshCw, X } from 'lucide-vue-next'

const { t } = useI18n()
const authStore = useAuthStore()

const candidates = ref<DuplicateCandidate[]>([])
const isLoading = ref(true)
const isScanning = ref(false)
const fetchError = ref(false)

const canMerge = computed(() => authStore.hasPermission('contacts', 'delete'))
const canReview = computed(() => authStore.hasPermission('contacts', 'write'))

/** The pair and which side would survive, held until the person confirms. */
const pending = ref<{ candidate: DuplicateCandidate; keep: DuplicateContactSummary; drop: DuplicateContactSummary } | null>(null)

async function fetchCandidates() {
  try {
    const { data: envelope } = await duplicatesService.list()
    const data = (envelope as any)?.data ?? envelope
    candidates.value = data.candidates || []
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

async function scan() {
  isScanning.value = true
  try {
    const { data: envelope } = await duplicatesService.scan()
    const data = (envelope as any)?.data ?? envelope
    toast.success(t('duplicates.scanned', { count: data.found }))
    await fetchCandidates()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    isScanning.value = false
  }
}

async function dismiss(candidate: DuplicateCandidate) {
  try {
    await duplicatesService.dismiss(candidate.id)
    candidates.value = candidates.value.filter(row => row.id !== candidate.id)
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

function ask(candidate: DuplicateCandidate, keep: DuplicateContactSummary, drop: DuplicateContactSummary) {
  pending.value = { candidate, keep, drop }
}

async function confirmMerge() {
  if (!pending.value) return
  const { candidate, keep, drop } = pending.value
  pending.value = null
  try {
    await duplicatesService.merge(keep.id, drop.id)
    candidates.value = candidates.value.filter(row => row.id !== candidate.id)
    toast.success(t('duplicates.merged'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

onMounted(fetchCandidates)
</script>

<template>
  <div class="space-y-4 p-4">
    <PageHeader
      :title="t('duplicates.title')"
      :description="t('duplicates.description')"
      :icon="Copy"
    >
      <template #actions>
        <Button v-if="canReview" size="sm" variant="outline" :disabled="isScanning" @click="scan">
          <RefreshCw class="mr-1.5 h-4 w-4" :class="isScanning ? 'animate-spin' : ''" />
          {{ t('duplicates.scan') }}
        </Button>
      </template>
    </PageHeader>

    <ErrorState v-if="fetchError" :message="t('duplicates.loadFailed')" @retry="fetchCandidates" />
    <p v-else-if="isLoading" class="text-muted-foreground">{{ t('common.loading') }}</p>

    <Card v-else-if="!candidates.length">
      <CardContent class="flex flex-col items-center gap-3 py-10 text-center">
        <Copy class="h-8 w-8 text-muted-foreground" />
        <p class="text-sm text-muted-foreground">{{ t('duplicates.empty') }}</p>
      </CardContent>
    </Card>

    <ul v-else class="space-y-3">
      <li v-for="candidate in candidates" :key="candidate.id">
        <Card>
          <CardContent class="space-y-3 p-4">
            <div class="flex flex-wrap items-center gap-2">
              <Badge variant="secondary" class="px-1.5 py-0 text-[11px]">
                {{ t('duplicates.score', { score: candidate.score }) }}
              </Badge>
              <!-- Why, not just how confident: somebody deciding should not be
                   asked to trust a number. -->
              <Badge
                v-for="reason in candidate.reasons"
                :key="reason"
                variant="outline"
                class="px-1.5 py-0 text-[11px]"
              >
                {{ t(`duplicates.reasons.${reason}`, reason) }}
              </Badge>
              <span class="ml-auto text-xs text-muted-foreground">
                {{ formatDate(candidate.detected_at) }}
              </span>
            </div>

            <div class="grid gap-3 sm:grid-cols-2">
              <div
                v-for="side in [candidate.contact_a, candidate.contact_b]"
                :key="side.id"
                class="space-y-1 rounded-md border p-3"
              >
                <p class="font-medium">{{ side.profile_name || side.phone_number }}</p>
                <p class="text-sm text-muted-foreground">{{ side.phone_number }}</p>
                <p class="text-xs text-muted-foreground">
                  {{ t('duplicates.messages', { count: side.message_count }) }}
                  · {{ t('duplicates.created', { when: formatDate(side.created_at) }) }}
                </p>
                <div v-if="side.tags.length" class="flex flex-wrap gap-1 pt-1">
                  <Badge v-for="tag in side.tags" :key="tag" variant="outline" class="px-1.5 py-0 text-[11px]">
                    {{ tag }}
                  </Badge>
                </div>

                <Button
                  v-if="canMerge"
                  class="mt-2 w-full"
                  size="sm"
                  variant="outline"
                  @click="ask(candidate, side, side.id === candidate.contact_a.id ? candidate.contact_b : candidate.contact_a)"
                >
                  {{ t('duplicates.keepThis') }}
                </Button>
              </div>
            </div>

            <Button v-if="canReview" variant="ghost" size="sm" @click="dismiss(candidate)">
              <X class="mr-1.5 h-4 w-4" />
              {{ t('duplicates.notTheSame') }}
            </Button>
          </CardContent>
        </Card>
      </li>
    </ul>

    <!-- Merging is not reversible, so it is confirmed in words rather than by
         a second click in the same place. -->
    <AlertDialog :open="!!pending" @update:open="open => !open && (pending = null)">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ t('duplicates.confirmTitle') }}</AlertDialogTitle>
          <AlertDialogDescription>
            {{ t('duplicates.confirmBody', {
              keep: pending?.keep.profile_name || pending?.keep.phone_number,
              drop: pending?.drop.profile_name || pending?.drop.phone_number
            }) }}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{{ t('common.cancel') }}</AlertDialogCancel>
          <AlertDialogAction @click="confirmMerge">{{ t('duplicates.merge') }}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
