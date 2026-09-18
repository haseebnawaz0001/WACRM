<script setup lang="ts">
/**
 * The pipeline board (plan 07).
 *
 * Opportunities used to live in a spreadsheet beside the product, which went
 * stale the moment anyone forgot to update it. A board is the one view that
 * answers "what is in play and where has it got to" without anybody having to
 * maintain it separately.
 *
 * Moves are optimistic: the card lands where it was dropped immediately and
 * only reverts if the server refuses. Waiting for a round trip before a card
 * moves makes dragging feel broken.
 */
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import draggable from 'vuedraggable'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import { PageHeader, SearchInput, ErrorState } from '@/components/shared'
import DealCard from './DealCard.vue'
import DealDetailSheet from './DealDetailSheet.vue'
import {
  pipelinesService, dealsService, contactsService,
  type Pipeline, type BoardColumn, type Deal
} from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { wsService } from '@/services/websocket'
import { toast } from 'vue-sonner'
import { useDebounceFn } from '@vueuse/core'
import { KanbanSquare, Plus, Settings2 } from 'lucide-vue-next'

const { t, locale } = useI18n()
const authStore = useAuthStore()


const pipelines = ref<Pipeline[]>([])
const pipeline = ref<Pipeline | null>(null)
const columns = ref<BoardColumn[]>([])

const isLoading = ref(true)
const fetchError = ref(false)

const search = ref('')
const ownerFilter = ref('all')
const statusFilter = ref('open')

const selectedDeal = ref<Deal | null>(null)
const showCreate = ref(false)
const canWrite = computed(() => authStore.hasPermission('deals', 'write'))
const canConfigure = computed(() => authStore.hasPermission('pipelines', 'write'))

/** The board's own word for what it tracks: deals, cases, bookings. */
const objectSingular = computed(() => pipeline.value?.object_label_singular || t('pipeline.deal'))
const objectPlural = computed(() => pipeline.value?.object_label_plural || t('pipeline.deals'))

// --- Loading ---

async function fetchPipelines() {
  try {
    const { data: envelope } = await pipelinesService.list()
    const data = (envelope as any)?.data ?? envelope
    pipelines.value = data.pipelines || []
    if (pipelines.value.length && !pipeline.value) {
      pipeline.value = pipelines.value.find(p => p.is_default) || pipelines.value[0]
    }
  } catch {
    fetchError.value = true
  }
}

async function fetchBoard() {
  if (!pipeline.value) {
    isLoading.value = false
    return
  }
  try {
    const { data: envelope } = await pipelinesService.board(pipeline.value.id, {
      search: search.value || undefined,
      owner_id: ownerFilter.value === 'me' ? 'me' : undefined,
      status: statusFilter.value
    })
    const data = (envelope as any)?.data ?? envelope
    pipeline.value = data.pipeline
    columns.value = data.columns || []
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

const debouncedSearch = useDebounceFn(fetchBoard, 300)
watch(search, debouncedSearch)
watch([ownerFilter, statusFilter], fetchBoard)
watch(pipeline, (next, previous) => {
  if (next?.id !== previous?.id) fetchBoard()
})

// --- Moving cards ---

/**
 * Called by vuedraggable after a drop. The list has already been mutated
 * locally, so the neighbours are read from the new arrangement and sent to the
 * server; ids rather than an index, because two people dragging at once
 * disagree about indexes but agree about which cards they dropped between.
 */
async function onDrop(column: BoardColumn, event: any) {
  const moved: Deal | undefined = event.added?.element || event.moved?.element
  if (!moved) return

  const index = column.deals.findIndex(d => d.id === moved.id)
  const after = index > 0 ? column.deals[index - 1].id : undefined
  const before = index < column.deals.length - 1 ? column.deals[index + 1].id : undefined

  // Dropping on a Lost column asks why before it commits: a board that cannot
  // say why anything was lost teaches nobody anything.
  if (column.stage.stage_type === 'lost') {
    pendingLoss.value = { deal: moved, stageId: column.stage.id, before, after }
    return
  }

  await commitMove(moved, column.stage.id, before, after)
}

const pendingLoss = ref<{ deal: Deal; stageId: string; before?: string; after?: string } | null>(null)
const lostReason = ref('')

async function confirmLoss() {
  if (!pendingLoss.value) return
  const { deal, stageId, before, after } = pendingLoss.value
  const reason = lostReason.value
  pendingLoss.value = null
  lostReason.value = ''
  await commitMove(deal, stageId, before, after, reason)
}

function cancelLoss() {
  pendingLoss.value = null
  lostReason.value = ''
  // The local lists were already rearranged by the drag, so the only honest
  // way back is to ask the server what the board actually looks like.
  fetchBoard()
}

async function commitMove(deal: Deal, stageId: string, before?: string, after?: string, lostReasonText?: string) {
  try {
    const { data: envelope } = await dealsService.move(deal.id, {
      stage_id: stageId,
      before_id: before,
      after_id: after,
      lost_reason: lostReasonText
    })
    const data = (envelope as any)?.data ?? envelope
    Object.assign(deal, data.deal)
    recountColumns()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('pipeline.moveFailed'))
    fetchBoard()
  }
}

/** Keeps the column headers honest after a local move, without a refetch. */
function recountColumns() {
  for (const column of columns.value) {
    column.count = column.deals.length
    column.total_value = column.deals.reduce((sum, d) => sum + (d.value || 0), 0)
    column.weighted_value = (column.total_value * column.stage.probability) / 100
  }
}

async function loadMore(column: BoardColumn) {
  if (!pipeline.value || !column.deals.length) return
  const cursor = column.deals[column.deals.length - 1].board_position
  try {
    const { data: envelope } = await pipelinesService.stageDeals(pipeline.value.id, column.stage.id, cursor, {
      search: search.value || undefined,
      owner_id: ownerFilter.value === 'me' ? 'me' : undefined,
      status: statusFilter.value
    })
    const data = (envelope as any)?.data ?? envelope
    column.deals.push(...(data.deals || []))
    column.has_more = data.has_more
  } catch {
    toast.error(t('pipeline.loadMoreFailed'))
  }
}

// --- Keyboard moves ---

/**
 * Dragging is not usable with a keyboard or a screen reader, so the adjacent
 * stage is always one shortcut away.
 */
function moveByKeyboard(deal: Deal, columnIndex: number, direction: -1 | 1) {
  const target = columns.value[columnIndex + direction]
  if (!target) return

  const source = columns.value[columnIndex]
  source.deals = source.deals.filter(d => d.id !== deal.id)
  target.deals.push(deal)
  recountColumns()

  announcement.value = t('pipeline.movedTo', { stage: target.stage.name })
  commitMove(deal, target.stage.id)
}

const announcement = ref('')

// --- Creating ---

const newDeal = ref<{ title: string; value: number | string; contact_id: string; stage_id: string }>(
  { title: '', value: 0, contact_id: '', stage_id: '' }
)
const contactQuery = ref('')
const contactResults = ref<Array<{ id: string; profile_name: string; phone_number: string }>>([])

const searchContacts = useDebounceFn(async () => {
  if (contactQuery.value.length < 2) {
    contactResults.value = []
    return
  }
  try {
    const { data: envelope } = await contactsService.list({ search: contactQuery.value, limit: 10 })
    const data = (envelope as any)?.data ?? envelope
    contactResults.value = data.contacts || data || []
  } catch {
    contactResults.value = []
  }
}, 300)
watch(contactQuery, searchContacts)

async function createDeal() {
  if (!newDeal.value.title || !newDeal.value.contact_id) return
  try {
    await dealsService.create({
      ...newDeal.value,
      pipeline_id: pipeline.value?.id,
      value: Number(newDeal.value.value) || 0
    })
    showCreate.value = false
    newDeal.value = { title: '', value: 0, contact_id: '', stage_id: '' }
    contactQuery.value = ''
    toast.success(t('pipeline.created', { object: objectSingular.value }))
    fetchBoard()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

// --- Live updates ---

/**
 * Boards are shared, so a card another person moves has to move here too. The
 * broadcast carries ids only, so the board refetches rather than trusting it.
 */
function onDealUpdated() {
  fetchBoard()
}

function money(value: number) {
  return new Intl.NumberFormat(locale.value, {
    style: 'currency',
    currency: pipeline.value?.currency || 'USD',
    maximumFractionDigits: 0
  }).format(value)
}

let unsubscribe: (() => void) | null = null
let unwatchBoard: (() => void) | null = null

/**
 * Watch the pipeline currently on screen (plan 10, S10).
 *
 * A board is a shared surface: two people move cards on it at once, and one
 * of them used to see yesterday's arrangement until they reloaded. The topic
 * carries ids only, so the client refetches what it is allowed to read.
 */
function watchBoardTopic(pipelineId: string) {
  unwatchBoard?.()
  unwatchBoard = wsService.subscribeTopics([`board:${pipelineId}`])
}

watch(() => pipeline.value?.id, id => {
  if (id) watchBoardTopic(id)
})

onMounted(async () => {
  await fetchPipelines()
  await fetchBoard()
  if (pipeline.value?.id) watchBoardTopic(pipeline.value.id)
  unsubscribe = wsService.subscribe('deal_updated', onDealUpdated)
})

onUnmounted(() => {
  unsubscribe?.()
  unwatchBoard?.()
})
</script>

<template>
  <div class="flex h-full flex-col">
    <PageHeader :title="objectPlural" :icon="KanbanSquare">
      <template #actions>
        <Button v-if="canWrite" size="sm" @click="showCreate = true">
          <Plus class="mr-1.5 h-4 w-4" />
          {{ t('pipeline.newObject', { object: objectSingular }) }}
        </Button>
        <Button
          v-if="canConfigure"
          size="sm"
          variant="outline"
          as="router-link"
          to="/settings/pipelines"
          :aria-label="t('pipeline.configure')"
        >
          <Settings2 class="h-4 w-4" />
        </Button>
      </template>
    </PageHeader>

    <!-- Toolbar -->
    <div class="flex flex-wrap items-center gap-2 border-b px-4 py-2">
      <Select
        v-if="pipelines.length > 1"
        :model-value="pipeline?.id"
        @update:model-value="id => pipeline = pipelines.find(p => p.id === id) || pipeline"
      >
        <SelectTrigger class="h-8 w-44"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem v-for="p in pipelines" :key="p.id" :value="p.id">{{ p.name }}</SelectItem>
        </SelectContent>
      </Select>

      <SearchInput v-model="search" :placeholder="t('pipeline.searchPlaceholder')" class="h-8 w-56" />

      <Select v-model="ownerFilter">
        <SelectTrigger class="h-8 w-36"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="all">{{ t('pipeline.allOwners') }}</SelectItem>
          <SelectItem value="me">{{ t('pipeline.mine') }}</SelectItem>
        </SelectContent>
      </Select>

      <Select v-model="statusFilter">
        <SelectTrigger class="h-8 w-32"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="open">{{ t('pipeline.statusOpen') }}</SelectItem>
          <SelectItem value="won">{{ t('pipeline.statusWon') }}</SelectItem>
          <SelectItem value="lost">{{ t('pipeline.statusLost') }}</SelectItem>
          <SelectItem value="all">{{ t('pipeline.statusAll') }}</SelectItem>
        </SelectContent>
      </Select>
    </div>

    <ErrorState v-if="fetchError" :message="t('pipeline.loadFailed')" @retry="fetchBoard" />

    <!-- Columns of cards, which is what arrives. A centred "Loading" makes the
         board look empty until the moment it does not. -->
    <div v-else-if="isLoading" class="flex flex-1 gap-3 overflow-hidden p-4" aria-hidden="true">
      <div v-for="col in 4" :key="col" class="w-72 shrink-0 space-y-2">
        <div class="flex items-center justify-between px-1">
          <Skeleton class="h-4 w-24" />
          <Skeleton class="h-3 w-12" />
        </div>
        <div v-for="card in 3" :key="card" class="space-y-2 rounded-sm border border-white/[0.06] p-3 light:border-gray-200">
          <Skeleton class="h-4 w-40" />
          <Skeleton class="h-5 w-20" />
          <Skeleton class="h-3 w-28" />
        </div>
      </div>
    </div>

    <div
      v-else-if="!pipeline"
      class="flex flex-1 flex-col items-center justify-center gap-3 text-muted-foreground"
    >
      <KanbanSquare class="h-10 w-10" />
      <p>{{ t('pipeline.noPipelines') }}</p>
    </div>

    <!-- Columns. Horizontal scroll with snap so a phone shows one column at a time. -->
    <div v-else class="flex flex-1 gap-3 overflow-x-auto p-4 snap-x snap-mandatory md:snap-none">
      <section
        v-for="(column, columnIndex) in columns"
        :key="column.stage.id"
        class="flex w-72 shrink-0 snap-start flex-col rounded-lg bg-muted/40"
        :aria-label="column.stage.name"
      >
        <header class="flex items-baseline justify-between gap-2 px-3 py-2">
          <div class="flex min-w-0 items-center gap-2">
            <span class="truncate text-sm font-medium">{{ column.stage.name }}</span>
            <Badge variant="secondary" class="px-1.5 py-0 text-[11px]">{{ column.count }}</Badge>
          </div>
          <span
            class="shrink-0 text-xs tabular-nums text-muted-foreground"
            :title="t('pipeline.weighted', { value: money(column.weighted_value) })"
          >
            {{ money(column.total_value) }}
          </span>
        </header>

        <draggable
          v-model="column.deals"
          :group="canWrite ? 'deals' : { name: 'deals', pull: false, put: false }"
          item-key="id"
          class="flex min-h-[3rem] flex-1 flex-col gap-2 overflow-y-auto px-2 pb-2"
          :disabled="!canWrite"
          @change="(event: any) => onDrop(column, event)"
        >
          <template #item="{ element }">
            <div
              @click="selectedDeal = element"
              @keydown.enter="selectedDeal = element"
              @keydown.left.ctrl.prevent="moveByKeyboard(element, columnIndex, -1)"
              @keydown.right.ctrl.prevent="moveByKeyboard(element, columnIndex, 1)"
              @keydown.left.meta.prevent="moveByKeyboard(element, columnIndex, -1)"
              @keydown.right.meta.prevent="moveByKeyboard(element, columnIndex, 1)"
            >
              <DealCard :deal="element" :currency="pipeline?.currency || 'USD'" />
            </div>
          </template>
        </draggable>

        <Button
          v-if="column.has_more"
          variant="ghost"
          size="sm"
          class="mx-2 mb-2"
          @click="loadMore(column)"
        >
          {{ t('pipeline.loadMore') }}
        </Button>
      </section>
    </div>

    <!-- Screen readers hear what dragging shows everyone else. -->
    <div class="sr-only" role="status" aria-live="polite">{{ announcement }}</div>

    <!-- Lost reason -->
    <Dialog :open="!!pendingLoss" @update:open="open => !open && cancelLoss()">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('pipeline.whyLost') }}</DialogTitle>
          <DialogDescription>{{ t('pipeline.whyLostHint') }}</DialogDescription>
        </DialogHeader>
        <Textarea v-model="lostReason" :placeholder="t('pipeline.lostReasonPlaceholder')" :rows="3" />
        <DialogFooter>
          <Button variant="outline" @click="cancelLoss">{{ t('common.cancel') }}</Button>
          <Button @click="confirmLoss">{{ t('pipeline.markLost') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- New deal -->
    <Dialog v-model:open="showCreate">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('pipeline.newObject', { object: objectSingular }) }}</DialogTitle>
        </DialogHeader>

        <div class="space-y-3">
          <div class="space-y-1.5">
            <Label>{{ t('pipeline.title') }}</Label>
            <Input v-model="newDeal.title" :placeholder="t('pipeline.titlePlaceholder')" />
          </div>

          <div class="space-y-1.5">
            <Label>{{ t('pipeline.contact') }}</Label>
            <Input v-model="contactQuery" :placeholder="t('pipeline.contactPlaceholder')" />
            <ul v-if="contactResults.length" class="max-h-40 overflow-y-auto rounded-md border">
              <li
                v-for="c in contactResults"
                :key="c.id"
                class="cursor-pointer px-3 py-1.5 text-sm hover:bg-accent"
                :class="newDeal.contact_id === c.id ? 'bg-accent' : ''"
                @click="newDeal.contact_id = c.id; contactQuery = c.profile_name || c.phone_number"
              >
                {{ c.profile_name || c.phone_number }}
              </li>
            </ul>
          </div>

          <div class="space-y-1.5">
            <Label>{{ t('pipeline.value') }}</Label>
            <Input v-model="newDeal.value" type="number" min="0" step="0.01" />
          </div>

          <div class="space-y-1.5">
            <Label>{{ t('pipeline.stage') }}</Label>
            <Select v-model="newDeal.stage_id">
              <SelectTrigger><SelectValue :placeholder="t('pipeline.firstOpenStage')" /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="s in pipeline?.stages || []" :key="s.id" :value="s.id">
                  {{ s.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" @click="showCreate = false">{{ t('common.cancel') }}</Button>
          <Button :disabled="!newDeal.title || !newDeal.contact_id" @click="createDeal">
            {{ t('common.create') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <DealDetailSheet
      v-if="selectedDeal"
      :deal="selectedDeal"
      :pipeline="pipeline"
      @close="selectedDeal = null"
      @changed="fetchBoard"
    />
  </div>
</template>
