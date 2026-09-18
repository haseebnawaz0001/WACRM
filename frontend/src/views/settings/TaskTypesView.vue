<script setup lang="ts">
/**
 * Task types settings (plan 04).
 *
 * The five seeded types describe a shop: call back, send quote, check in,
 * follow up, other. A clinic books procedures and a lender chases documents,
 * and with no way to add those, every other kind of work was filed under
 * "Other" — which makes "tasks per agent by type" report nothing worth
 * reading.
 *
 * Built-ins can be relabelled into the words a team actually uses, but never
 * deleted or archived: the product creates tasks of those kinds itself, from
 * call outcomes and automations, and a kind with nowhere to file its work
 * fails quietly.
 */
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle
} from '@/components/ui/dialog'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import {
  PageHeader, DataTable, DeleteConfirmDialog, ErrorState, type Column
} from '@/components/shared'
import { taskTypesService, type TaskType } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { Plus, Pencil, Trash2, ListTodo, Lock, Archive, ArrowUp, ArrowDown } from 'lucide-vue-next'
import { getErrorMessage, unwrapListResponse } from '@/lib/api-utils'

const { t } = useI18n()
const authStore = useAuthStore()

const types = ref<TaskType[]>([])
const isLoading = ref(true)
const fetchError = ref(false)

// Plan 04 gates the types themselves on tasks:delete — an agent creates tasks
// without being able to redefine what a task is.
const canManage = computed(() => authStore.hasPermission('tasks', 'delete'))

const breadcrumbs = computed(() => [
  { label: t('nav.settings'), href: '/settings' },
  { label: t('taskTypes.title') }
])

const columns = computed<Column<TaskType>[]>(() => [
  { key: 'label', label: t('taskTypes.typeLabel') },
  { key: 'key', label: t('taskTypes.typeKey') },
  { key: 'due', label: t('taskTypes.defaultDue') },
  { key: 'color', label: t('taskTypes.colour') },
  { key: 'actions', label: t('common.actions'), align: 'right' }
])

/**
 * Deadline presets, in minutes.
 *
 * Offered as a list because the useful answers are few and a free-form minutes
 * box invites 10080 when somebody meant a week. "None" is 0: due immediately,
 * which is what a task with no natural deadline means.
 */
const duePresets = [
  { minutes: 0, labelKey: 'taskTypes.dueNow' },
  { minutes: 60, labelKey: 'taskTypes.dueHour' },
  { minutes: 240, labelKey: 'taskTypes.dueFourHours' },
  { minutes: 1440, labelKey: 'taskTypes.dueTomorrow' },
  { minutes: 4320, labelKey: 'taskTypes.dueThreeDays' },
  { minutes: 10080, labelKey: 'taskTypes.dueWeek' },
  { minutes: 43200, labelKey: 'taskTypes.dueMonth' }
]

const colours = ['gray', 'blue', 'green', 'amber', 'purple', 'red']

const dialogOpen = ref(false)
const isSaving = ref(false)
const editing = ref<TaskType | null>(null)
const isEditing = computed(() => editing.value !== null)

const form = ref({
  label: '',
  icon: 'check-square',
  color: 'gray',
  default_due_offset_minutes: 1440,
  archived: false
})

function resetForm() {
  form.value = {
    label: '', icon: 'check-square', color: 'gray',
    default_due_offset_minutes: 1440, archived: false
  }
}

function openCreate() {
  editing.value = null
  resetForm()
  dialogOpen.value = true
}

function openEdit(type: TaskType) {
  editing.value = type
  form.value = {
    label: type.label,
    icon: type.icon,
    color: type.color,
    default_due_offset_minutes: type.default_due_offset_minutes,
    archived: Boolean(type.archived_at)
  }
  dialogOpen.value = true
}

async function save() {
  if (!form.value.label.trim()) {
    toast.error(t('taskTypes.labelRequired'))
    return
  }
  isSaving.value = true
  try {
    const payload: Record<string, unknown> = {
      label: form.value.label.trim(),
      icon: form.value.icon,
      color: form.value.color,
      default_due_offset_minutes: form.value.default_due_offset_minutes
    }

    if (isEditing.value && editing.value) {
      // Only a type the product does not create itself can be archived.
      if (!editing.value.is_system) payload.archived = form.value.archived
      await taskTypesService.update(editing.value.id, payload)
      toast.success(t('common.updatedSuccess', { resource: t('resources.TaskType') }))
    } else {
      // The key is derived from the label on the server, so the form asks for
      // one thing instead of two.
      await taskTypesService.create(payload)
      toast.success(t('common.createdSuccess', { resource: t('resources.TaskType') }))
    }

    dialogOpen.value = false
    await fetchTypes()
  } catch (e) {
    toast.error(getErrorMessage(e, t('common.failedSave', { resource: t('resources.taskType') })))
  } finally {
    isSaving.value = false
  }
}

const deleteDialogOpen = ref(false)
const typeToDelete = ref<TaskType | null>(null)
const isDeleting = ref(false)

function openDelete(type: TaskType) {
  typeToDelete.value = type
  deleteDialogOpen.value = true
}

async function confirmDelete() {
  if (!typeToDelete.value) return
  isDeleting.value = true
  try {
    await taskTypesService.delete(typeToDelete.value.id)
    toast.success(t('common.deletedSuccess', { resource: t('resources.TaskType') }))
    deleteDialogOpen.value = false
    typeToDelete.value = null
    await fetchTypes()
  } catch (e) {
    // A type with tasks against it is refused with a count, so the message the
    // server sends is more useful than a generic failure.
    toast.error(getErrorMessage(e, t('common.failedDelete', { resource: t('resources.taskType') })))
  } finally {
    isDeleting.value = false
  }
}

/**
 * Moves a type one place and saves the whole order.
 *
 * The order is what the task form offers first, so it is worth controlling:
 * the kind of work a team does twenty times a day should not be the fourth
 * option down.
 */
async function move(type: TaskType, direction: -1 | 1) {
  const index = types.value.findIndex((candidate) => candidate.id === type.id)
  const target = index + direction
  if (index < 0 || target < 0 || target >= types.value.length) return

  const reordered = [...types.value]
  const [moved] = reordered.splice(index, 1)
  reordered.splice(target, 0, moved)
  types.value = reordered

  try {
    await taskTypesService.reorder(reordered.map((item) => item.id))
  } catch (e) {
    toast.error(getErrorMessage(e, t('taskTypes.reorderFailed')))
    await fetchTypes()
  }
}

async function fetchTypes() {
  isLoading.value = true
  fetchError.value = false
  try {
    // Archived types are shown here and nowhere else: this is the only page
    // that can bring one back.
    types.value = unwrapListResponse<TaskType>(await taskTypesService.list(true), 'task_types')
  } catch {
    fetchError.value = true
    toast.error(t('common.failedLoad', { resource: t('resources.taskTypes') }))
  } finally {
    isLoading.value = false
  }
}

function dueLabel(minutes: number) {
  const preset = duePresets.find((p) => p.minutes === minutes)
  return preset ? t(preset.labelKey) : t('taskTypes.dueMinutes', { count: minutes })
}

onMounted(() => fetchTypes())
</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <PageHeader
      :title="$t('taskTypes.title')"
      :icon="ListTodo"
      back-link="/settings"
      :breadcrumbs="breadcrumbs"
    >
      <template #actions>
        <Button v-if="canManage" variant="outline" size="sm" @click="openCreate">
          <Plus class="h-4 w-4 mr-2" />{{ $t('taskTypes.addType') }}
        </Button>
      </template>
    </PageHeader>

    <ErrorState
      v-if="fetchError && !isLoading"
      :title="$t('common.somethingWentWrong')"
      :description="$t('common.failedLoad', { resource: $t('resources.taskTypes') })"
      class="flex-1"
    >
      <template #action><Button size="sm" @click="fetchTypes">{{ $t('common.retry') }}</Button></template>
    </ErrorState>

    <ScrollArea v-else class="flex-1">
      <div class="p-6">
        <Card>
          <CardHeader>
            <CardTitle>{{ $t('taskTypes.yourTypes') }}</CardTitle>
            <CardDescription>{{ $t('taskTypes.yourTypesDesc') }}</CardDescription>
          </CardHeader>
          <CardContent>
            <DataTable
              :items="types"
              :columns="columns"
              :is-loading="isLoading"
              :empty-icon="ListTodo"
              :empty-title="$t('taskTypes.noTypesYet')"
              :empty-description="$t('taskTypes.noTypesYetDesc')"
              item-name="task types"
            >
              <template #cell-label="{ item }">
                <div class="flex items-center gap-2">
                  <span class="font-medium">{{ item.label }}</span>
                  <Badge v-if="item.is_system" variant="secondary" class="gap-1">
                    <Lock class="h-3 w-3" />{{ $t('taskTypes.builtIn') }}
                  </Badge>
                  <Badge v-if="item.archived_at" variant="outline" class="gap-1">
                    <Archive class="h-3 w-3" />{{ $t('taskTypes.archived') }}
                  </Badge>
                </div>
              </template>

              <template #cell-key="{ item }">
                <code class="text-xs text-muted-foreground">{{ item.key }}</code>
              </template>

              <template #cell-due="{ item }">
                <span class="text-sm text-muted-foreground">
                  {{ dueLabel(item.default_due_offset_minutes) }}
                </span>
              </template>

              <template #cell-color="{ item }">
                <Badge variant="outline">{{ item.color }}</Badge>
              </template>

              <template #cell-actions="{ item }">
                <div class="flex items-center justify-end gap-1">
                  <template v-if="canManage">
                    <Button variant="ghost" size="icon" :title="$t('taskTypes.moveUp')" @click="move(item, -1)">
                      <ArrowUp class="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="icon" :title="$t('taskTypes.moveDown')" @click="move(item, 1)">
                      <ArrowDown class="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="icon" @click="openEdit(item)">
                      <Pencil class="h-4 w-4" />
                    </Button>
                    <!-- The product creates built-in kinds itself, so they are
                         relabelled rather than removed. -->
                    <Button
                      v-if="!item.is_system"
                      variant="ghost"
                      size="icon"
                      class="text-red-400 hover:text-red-300"
                      @click="openDelete(item)"
                    >
                      <Trash2 class="h-4 w-4" />
                    </Button>
                  </template>
                </div>
              </template>
            </DataTable>
          </CardContent>
        </Card>
      </div>
    </ScrollArea>

    <Dialog v-model:open="dialogOpen">
      <DialogContent class="sm:max-w-[480px]">
        <DialogHeader>
          <DialogTitle>
            {{ isEditing ? $t('taskTypes.editType') : $t('taskTypes.addType') }}
          </DialogTitle>
          <DialogDescription>{{ $t('taskTypes.dialogDesc') }}</DialogDescription>
        </DialogHeader>

        <div class="space-y-4 py-2">
          <div class="space-y-2">
            <Label for="task-type-label">{{ $t('taskTypes.typeLabel') }}</Label>
            <Input
              id="task-type-label"
              v-model="form.label"
              :placeholder="$t('taskTypes.labelPlaceholder')"
            />
            <p v-if="isEditing && editing?.is_system" class="text-xs text-muted-foreground">
              {{ $t('taskTypes.builtInHint') }}
            </p>
          </div>

          <div class="space-y-2">
            <Label>{{ $t('taskTypes.defaultDue') }}</Label>
            <Select
              :model-value="String(form.default_due_offset_minutes)"
              @update:model-value="form.default_due_offset_minutes = Number($event)"
            >
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="preset in duePresets" :key="preset.minutes" :value="String(preset.minutes)">
                  {{ $t(preset.labelKey) }}
                </SelectItem>
              </SelectContent>
            </Select>
            <p class="text-xs text-muted-foreground">{{ $t('taskTypes.defaultDueHint') }}</p>
          </div>

          <div class="space-y-2">
            <Label>{{ $t('taskTypes.colour') }}</Label>
            <Select v-model="form.color">
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="colour in colours" :key="colour" :value="colour">{{ colour }}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div v-if="isEditing && !editing?.is_system" class="flex items-center justify-between">
            <div>
              <Label for="task-type-archived">{{ $t('taskTypes.archived') }}</Label>
              <p class="text-xs text-muted-foreground">{{ $t('taskTypes.archivedHint') }}</p>
            </div>
            <Switch id="task-type-archived" v-model:checked="form.archived" />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" @click="dialogOpen = false">{{ $t('common.cancel') }}</Button>
          <Button :disabled="isSaving" @click="save">
            {{ isSaving ? $t('common.saving') : $t('common.save') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <DeleteConfirmDialog
      v-model:open="deleteDialogOpen"
      :title="$t('taskTypes.deleteTitle')"
      :description="$t('taskTypes.deleteDesc', { label: typeToDelete?.label ?? '' })"
      :is-deleting="isDeleting"
      @confirm="confirmDelete"
    />
  </div>
</template>
