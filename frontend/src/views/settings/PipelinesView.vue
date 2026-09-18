<script setup lang="ts">
/**
 * Settings → Pipelines (plan 07).
 *
 * The board's columns are the organization's process. Every business has a
 * different one, and a fixed set of stages would force people to describe their
 * work in someone else's words — which is exactly why deals ended up in a
 * spreadsheet in the first place.
 */
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import { PageHeader, ErrorState } from '@/components/shared'
import { pipelinesService, type Pipeline, type PipelineStage } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { KanbanSquare, Plus, Trash2, ChevronUp, ChevronDown } from 'lucide-vue-next'

const { t } = useI18n()
const authStore = useAuthStore()

const pipelines = ref<Pipeline[]>([])
const selectedId = ref('')
const isLoading = ref(true)
const fetchError = ref(false)

const canWrite = computed(() => authStore.hasPermission('pipelines', 'write'))
const canDelete = computed(() => authStore.hasPermission('pipelines', 'delete'))
const selected = computed(() => pipelines.value.find(p => p.id === selectedId.value) || null)

const stageTypes = ['open', 'won', 'lost'] as const

async function fetchPipelines() {
  try {
    const { data: envelope } = await pipelinesService.list()
    const data = (envelope as any)?.data ?? envelope
    pipelines.value = data.pipelines || []
    if (!selectedId.value && pipelines.value.length) {
      selectedId.value = (pipelines.value.find(p => p.is_default) || pipelines.value[0]).id
    }
    fetchError.value = false
  } catch {
    fetchError.value = true
  } finally {
    isLoading.value = false
  }
}

// --- Pipelines ---

const showCreate = ref(false)
const newPipeline = ref({ name: '', currency: 'USD' })

async function createPipeline() {
  if (!newPipeline.value.name) return
  try {
    const { data: envelope } = await pipelinesService.create(newPipeline.value)
    const data = (envelope as any)?.data ?? envelope
    showCreate.value = false
    newPipeline.value = { name: '', currency: 'USD' }
    await fetchPipelines()
    selectedId.value = data.pipeline.id
    toast.success(t('pipelines.created'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function savePipeline() {
  if (!selected.value) return
  try {
    await pipelinesService.update(selected.value.id, {
      name: selected.value.name,
      object_label_singular: selected.value.object_label_singular,
      object_label_plural: selected.value.object_label_plural,
      currency: selected.value.currency,
      is_default: selected.value.is_default
    })
    toast.success(t('common.savedSuccess'))
    fetchPipelines()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function deletePipeline() {
  if (!selected.value) return
  try {
    await pipelinesService.delete(selected.value.id)
    selectedId.value = ''
    await fetchPipelines()
    toast.success(t('common.deletedSuccess'))
  } catch (error: any) {
    // A pipeline with open deals answers 409: archiving is the way to retire
    // one that still has live work on it.
    if (error?.response?.status === 409) {
      toast.error(t('pipelines.hasOpenDeals'))
      return
    }
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function archivePipeline() {
  if (!selected.value) return
  try {
    await pipelinesService.archive(selected.value.id)
    selectedId.value = ''
    await fetchPipelines()
    toast.success(t('pipelines.archived'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

// --- Stages ---

async function addStage() {
  if (!selected.value) return
  try {
    await pipelinesService.createStage(selected.value.id, { name: t('pipelines.newStage') })
    fetchPipelines()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function saveStage(stage: PipelineStage) {
  try {
    await pipelinesService.updateStage(stage.id, {
      name: stage.name,
      stage_type: stage.stage_type,
      probability: Number(stage.probability),
      rotting_days: Number(stage.rotting_days)
    })
    toast.success(t('common.savedSuccess'))
    fetchPipelines()
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function moveStage(index: number, direction: -1 | 1) {
  if (!selected.value) return
  const stages = [...selected.value.stages]
  const target = index + direction
  if (target < 0 || target >= stages.length) return

  const [moved] = stages.splice(index, 1)
  stages.splice(target, 0, moved)
  selected.value.stages = stages

  try {
    await pipelinesService.reorderStages(selected.value.id, stages.map(s => s.id))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
    fetchPipelines()
  }
}

// Deleting a column with cards in it has to say where they go, so the server's
// 409 becomes a prompt rather than an error toast.
const pendingStageDelete = ref<PipelineStage | null>(null)
const moveDealsTo = ref('')

async function deleteStage(stage: PipelineStage) {
  try {
    await pipelinesService.deleteStage(stage.id)
    fetchPipelines()
    toast.success(t('common.deletedSuccess'))
  } catch (error: any) {
    if (error?.response?.status === 409) {
      pendingStageDelete.value = stage
      moveDealsTo.value = ''
      return
    }
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

async function confirmStageDelete() {
  if (!pendingStageDelete.value || !moveDealsTo.value) return
  try {
    await pipelinesService.deleteStage(pendingStageDelete.value.id, moveDealsTo.value)
    pendingStageDelete.value = null
    fetchPipelines()
    toast.success(t('common.deletedSuccess'))
  } catch (error: any) {
    toast.error(error?.response?.data?.message || t('common.error'))
  }
}

const otherStages = computed(() =>
  (selected.value?.stages || []).filter(s => s.id !== pendingStageDelete.value?.id)
)

onMounted(fetchPipelines)
</script>

<template>
  <!-- <main> is overflow-hidden, so this page needs its own scroller;
       without one everything past the fold was unreachable. -->
  <div class="flex h-full flex-col">
    <PageHeader :title="t('pipelines.title')" :description="t('pipelines.description')" :icon="KanbanSquare">
      <template #actions>
        <Button v-if="canWrite" size="sm" @click="showCreate = true">
          <Plus class="mr-1.5 h-4 w-4" />
          {{ t('pipelines.newPipeline') }}
        </Button>
      </template>
    </PageHeader>

    <ScrollArea class="flex-1">
      <div class="space-y-4 p-4">

    <ErrorState v-if="fetchError" :message="t('pipelines.loadFailed')" @retry="fetchPipelines" />
    <p v-else-if="isLoading" class="text-muted-foreground">{{ t('common.loading') }}</p>
    <p v-else-if="!pipelines.length" class="text-muted-foreground">{{ t('pipelines.none') }}</p>

    <template v-else>
      <Select v-if="pipelines.length > 1" v-model="selectedId">
        <SelectTrigger class="w-64" :aria-label="t('nav.pipeline')"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem v-for="p in pipelines" :key="p.id" :value="p.id">{{ p.name }}</SelectItem>
        </SelectContent>
      </Select>

      <Card v-if="selected">
        <CardHeader>
          <CardTitle class="text-base">{{ t('pipelines.settings') }}</CardTitle>
        </CardHeader>
        <CardContent class="space-y-3">
          <div class="grid gap-3 sm:grid-cols-2">
            <div class="space-y-1.5">
              <Label>{{ t('pipelines.name') }}</Label>
              <Input v-model="selected.name" :disabled="!canWrite" />
            </div>
            <div class="space-y-1.5">
              <Label>{{ t('pipelines.currency') }}</Label>
              <Input v-model="selected.currency" maxlength="3" :disabled="!canWrite" />
            </div>
            <div class="space-y-1.5">
              <Label>{{ t('pipelines.labelSingular') }}</Label>
              <Input v-model="selected.object_label_singular" :disabled="!canWrite" />
              <p class="text-xs text-muted-foreground">{{ t('pipelines.labelHint') }}</p>
            </div>
            <div class="space-y-1.5">
              <Label>{{ t('pipelines.labelPlural') }}</Label>
              <Input v-model="selected.object_label_plural" :disabled="!canWrite" />
            </div>
          </div>

          <div v-if="canWrite" class="flex flex-wrap items-center gap-2">
            <Button size="sm" @click="savePipeline">{{ t('common.save') }}</Button>
            <Button size="sm" variant="outline" @click="archivePipeline">
              {{ t('pipelines.archive') }}
            </Button>
            <Button v-if="canDelete" size="sm" variant="ghost" class="text-muted-foreground hover:bg-destructive/10 hover:text-destructive" @click="deletePipeline">
              <Trash2 class="mr-1.5 h-4 w-4" />
              {{ t('common.delete') }}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card v-if="selected">
        <CardHeader class="flex-row items-center justify-between space-y-0">
          <CardTitle class="text-base">{{ t('pipelines.stages') }}</CardTitle>
          <Button v-if="canWrite" size="sm" variant="outline" @click="addStage">
            <Plus class="mr-1.5 h-4 w-4" />
            {{ t('pipelines.addStage') }}
          </Button>
        </CardHeader>
        <CardContent class="space-y-2">
          <div
            v-for="(stage, index) in selected.stages"
            :key="stage.id"
            class="flex flex-wrap items-end gap-2 rounded-md border p-3"
          >
            <div class="flex flex-col gap-0.5">
              <Button
                variant="ghost" size="icon" class="h-5 w-5"
                :disabled="!canWrite || index === 0"
                :aria-label="t('pipelines.moveUp')"
                @click="moveStage(index, -1)"
              >
                <ChevronUp class="h-3 w-3" />
              </Button>
              <Button
                variant="ghost" size="icon" class="h-5 w-5"
                :disabled="!canWrite || index === selected.stages.length - 1"
                :aria-label="t('pipelines.moveDown')"
                @click="moveStage(index, 1)"
              >
                <ChevronDown class="h-3 w-3" />
              </Button>
            </div>

            <div class="min-w-40 flex-1 space-y-1.5">
              <Label class="text-xs">{{ t('pipelines.stageName') }}</Label>
              <Input v-model="stage.name" :disabled="!canWrite" />
            </div>

            <div class="w-28 space-y-1.5">
              <Label class="text-xs">{{ t('pipelines.stageType') }}</Label>
              <Select v-model="stage.stage_type" :disabled="!canWrite">
                <SelectTrigger :aria-label="$t('pipelines.stageType')"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="type in stageTypes" :key="type" :value="type">
                    {{ t(`pipelines.type_${type}`) }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div class="w-24 space-y-1.5">
              <Label class="text-xs">{{ t('pipelines.probability') }}</Label>
              <Input v-model="stage.probability" type="number" min="0" max="100" :disabled="!canWrite" />
            </div>

            <div class="w-24 space-y-1.5">
              <Label class="text-xs" :title="t('pipelines.rottingHint')">
                {{ t('pipelines.rottingDays') }}
              </Label>
              <Input v-model="stage.rotting_days" type="number" min="0" :disabled="!canWrite" />
            </div>

            <div v-if="canWrite" class="flex items-center gap-1">
              <Button size="sm" variant="outline" @click="saveStage(stage)">{{ t('common.save') }}</Button>
              <Button v-if="canDelete" size="icon" variant="ghost" class="text-muted-foreground hover:bg-destructive/10 hover:text-destructive" @click="deleteStage(stage)">
                <Trash2 class="h-4 w-4" />
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </template>

    <!-- New pipeline -->
      </div>
    </ScrollArea>

    <Dialog v-model:open="showCreate">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('pipelines.newPipeline') }}</DialogTitle>
          <DialogDescription>{{ t('pipelines.newPipelineHint') }}</DialogDescription>
        </DialogHeader>
        <div class="space-y-3">
          <div class="space-y-1.5">
            <Label>{{ t('pipelines.name') }}</Label>
            <Input v-model="newPipeline.name" />
          </div>
          <div class="space-y-1.5">
            <Label>{{ t('pipelines.currency') }}</Label>
            <Input v-model="newPipeline.currency" maxlength="3" />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" @click="showCreate = false">{{ t('common.cancel') }}</Button>
          <Button :disabled="!newPipeline.name" @click="createPipeline">{{ t('common.create') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Where do the deals go? -->
    <Dialog :open="!!pendingStageDelete" @update:open="open => !open && (pendingStageDelete = null)">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('pipelines.moveDealsTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('pipelines.moveDealsHint', { stage: pendingStageDelete?.name }) }}
          </DialogDescription>
        </DialogHeader>
        <Select v-model="moveDealsTo">
          <SelectTrigger :aria-label="$t('pipelines.chooseStage')"><SelectValue :placeholder="t('pipelines.chooseStage')" /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="s in otherStages" :key="s.id" :value="s.id">{{ s.name }}</SelectItem>
          </SelectContent>
        </Select>
        <DialogFooter>
          <Button variant="outline" @click="pendingStageDelete = null">{{ t('common.cancel') }}</Button>
          <Button :disabled="!moveDealsTo" @click="confirmStageDelete">{{ t('common.delete') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
