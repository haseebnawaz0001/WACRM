<script setup lang="ts">
/**
 * The deal detail sheet (plan 07).
 *
 * A card on the board is a summary. This is where the rest lives: the value,
 * the owner, the close date, why it was lost, and the stage history that
 * answers "how long did this actually take".
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  dealsService, usersService,
  type Deal, type Pipeline, type DealHistoryEntry
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { formatDateTime } from '@/lib/utils'
import { ExternalLink, Trash2 } from 'lucide-vue-next'

const props = defineProps<{ deal: Deal; pipeline: Pipeline | null }>()
const emit = defineEmits<{ close: []; changed: [] }>()

const { t, locale } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

const draft = ref({ ...props.deal })
const history = ref<DealHistoryEntry[]>([])
const owners = ref<Array<{ id: string; name: string }>>([])
const isSaving = ref(false)

const canWrite = computed(() => authStore.hasPermission('deals', 'write'))
const canDelete = computed(() => authStore.hasPermission('deals', 'delete'))

watch(() => props.deal, deal => {
  draft.value = { ...deal }
  loadHistory()
})

const statusVariant = computed(() => {
  if (draft.value.status === 'won') return 'default'
  if (draft.value.status === 'lost') return 'destructive'
  return 'secondary'
})

/** Human stage durations: "2d 4h" reads; 187,412 seconds does not. */
function duration(seconds: number): string {
  if (seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  if (days > 0) return t('pipeline.daysHours', { days, hours })
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours > 0) return t('pipeline.hoursMinutes', { hours, minutes })
  return t('pipeline.minutes', { minutes })
}

async function loadHistory() {
  try {
    const { data: envelope } = await dealsService.history(props.deal.id)
    const data = (envelope as any)?.data ?? envelope
    history.value = data.history || []
  } catch {
    history.value = []
  }
}

async function loadOwners() {
  try {
    const { data: envelope } = await usersService.list()
    const data = (envelope as any)?.data ?? envelope
    // full_name, which is what the users endpoint returns. It has never had a
    // `name`, so this fell through to the email every time and the owner picker
    // read "lena.fischer@demo.whatomate.local" where the rest of the product
    // says "Lena Fischer". The email stays as the fallback for an account that
    // genuinely has no name on it yet.
    owners.value = (data.users || data || []).map((u: any) => ({
      id: u.id,
      name: u.full_name || u.email
    }))
  } catch {
    owners.value = []
  }
}

async function save() {
  isSaving.value = true
  try {
    const { data: envelope } = await dealsService.update(props.deal.id, {
      title: draft.value.title,
      value: Number(draft.value.value) || 0,
      owner_id: draft.value.owner_id || '',
      expected_close_date: draft.value.expected_close_date || undefined,
      clear_close_date: !draft.value.expected_close_date,
      lost_reason: draft.value.lost_reason
    })
    const data = (envelope as any)?.data ?? envelope
    draft.value = { ...data.deal }
    toast.success(t('common.savedSuccess'))
    emit('changed')
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    isSaving.value = false
  }
}

async function moveTo(stageId: string) {
  try {
    const { data: envelope } = await dealsService.move(props.deal.id, { stage_id: stageId })
    const data = (envelope as any)?.data ?? envelope
    draft.value = { ...data.deal }
    await loadHistory()
    emit('changed')
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('pipeline.moveFailed'))
  }
}

async function remove() {
  try {
    await dealsService.delete(props.deal.id)
    toast.success(t('common.deletedSuccess'))
    emit('changed')
    emit('close')
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

const money = computed(() =>
  new Intl.NumberFormat(locale.value, {
    style: 'currency',
    currency: draft.value.currency || 'USD'
  }).format(draft.value.value)
)

onMounted(() => {
  loadHistory()
  loadOwners()
})
</script>

<template>
  <Sheet :open="true" @update:open="open => !open && emit('close')">
    <SheetContent class="w-full overflow-y-auto sm:max-w-md">
      <SheetHeader>
        <SheetTitle class="pr-6">{{ draft.title }}</SheetTitle>
      </SheetHeader>

      <div class="mt-4 space-y-4">
        <div class="flex items-center gap-2">
          <Badge :variant="statusVariant">{{ t(`pipeline.status_${draft.status}`) }}</Badge>
          <span class="text-lg font-semibold tabular-nums">{{ money }}</span>
        </div>

        <Button
          variant="link"
          class="h-auto gap-1 p-0 text-sm"
          @click="router.push(`/contacts/${draft.contact_id}`)"
        >
          {{ draft.contact_name || draft.contact_phone }}
          <ExternalLink class="h-3 w-3" />
        </Button>

        <Separator />

        <div class="space-y-3">
          <div class="space-y-1.5">
            <Label>{{ t('pipeline.title') }}</Label>
            <Input v-model="draft.title" :disabled="!canWrite" />
          </div>

          <div class="space-y-1.5">
            <Label>{{ t('pipeline.value') }}</Label>
            <Input v-model="draft.value" type="number" min="0" step="0.01" :disabled="!canWrite" />
          </div>

          <div class="space-y-1.5">
            <Label>{{ t('pipeline.owner') }}</Label>
            <Select v-model="draft.owner_id" :disabled="!canWrite">
              <SelectTrigger><SelectValue :placeholder="t('pipeline.unassigned')" /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="o in owners" :key="o.id" :value="o.id">{{ o.name }}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div class="space-y-1.5">
            <Label>{{ t('pipeline.expectedClose') }}</Label>
            <Input
              :model-value="draft.expected_close_date?.slice(0, 10) || ''"
              type="date"
              :disabled="!canWrite"
              @update:model-value="v => draft.expected_close_date = v ? `${v}T00:00:00Z` : null"
            />
          </div>

          <div class="space-y-1.5">
            <Label>{{ t('pipeline.stage') }}</Label>
            <Select :model-value="draft.stage_id" :disabled="!canWrite" @update:model-value="value => typeof value === 'string' && moveTo(value)">
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="s in pipeline?.stages || []" :key="s.id" :value="s.id">
                  {{ s.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div v-if="draft.status === 'lost'" class="space-y-1.5">
            <Label>{{ t('pipeline.lostReason') }}</Label>
            <Input v-model="draft.lost_reason" :disabled="!canWrite" />
          </div>
        </div>

        <div v-if="canWrite" class="flex items-center gap-2">
          <Button :disabled="isSaving" @click="save">{{ t('common.save') }}</Button>
          <!-- Pushed away from Save: they were touching, and one of them cannot
               be undone. -->
          <Button
            v-if="canDelete"
            variant="ghost"
            size="icon"
            class="ml-auto text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
            :aria-label="t('common.delete')"
            @click="remove"
          >
            <Trash2 class="h-4 w-4" />
          </Button>
        </div>

        <Separator />

        <!-- Stage history: how long this actually took, which the board alone
             can never say. -->
        <div class="space-y-2">
          <h3 class="text-sm font-medium">{{ t('pipeline.history') }}</h3>
          <ol v-if="history.length" class="space-y-2">
            <li v-for="entry in history" :key="entry.id" class="text-sm">
              <div class="flex items-baseline justify-between gap-2">
                <span>
                  <template v-if="entry.from_stage_name">
                    {{ entry.from_stage_name }} → </template>
                  <span class="font-medium">{{ entry.to_stage_name }}</span>
                </span>
                <span class="shrink-0 text-xs text-muted-foreground">
                  {{ formatDateTime(entry.created_at) }}
                </span>
              </div>
              <p v-if="entry.from_stage_name" class="text-xs text-muted-foreground">
                {{ t('pipeline.spentInStage', { duration: duration(entry.duration_seconds) }) }}
              </p>
            </li>
          </ol>
          <p v-else class="text-sm text-muted-foreground">{{ t('pipeline.noHistory') }}</p>
        </div>
      </div>
    </SheetContent>
  </Sheet>
</template>
