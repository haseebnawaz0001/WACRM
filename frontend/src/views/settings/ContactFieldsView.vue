<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
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
import { contactFieldsService, type ContactField, type ContactFieldType } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { Plus, Pencil, Trash2, ListChecks, Lock, Archive } from 'lucide-vue-next'
import { getErrorMessage } from '@/lib/api-utils'

const { t } = useI18n()
const authStore = useAuthStore()

const fields = ref<ContactField[]>([])
const isLoading = ref(true)
const fetchError = ref(false)

const canWrite = computed(() => authStore.hasPermission('contact_fields', 'write'))
const canDelete = computed(() => authStore.hasPermission('contact_fields', 'delete'))

const breadcrumbs = computed(() => [
  { label: t('nav.settings'), href: '/settings' },
  { label: t('contactFields.title') }
])

const fieldTypes: Array<{ value: ContactFieldType; labelKey: string }> = [
  { value: 'text', labelKey: 'contactFields.typeText' },
  { value: 'number', labelKey: 'contactFields.typeNumber' },
  { value: 'date', labelKey: 'contactFields.typeDate' },
  { value: 'dropdown', labelKey: 'contactFields.typeDropdown' },
  { value: 'email', labelKey: 'contactFields.typeEmail' },
  { value: 'phone', labelKey: 'contactFields.typePhone' }
]

const columns = computed<Column<ContactField>[]>(() => [
  { key: 'label', label: t('contactFields.fieldLabel') },
  { key: 'key', label: t('contactFields.fieldKey') },
  { key: 'type', label: t('contactFields.fieldType') },
  { key: 'group', label: t('contactFields.group') },
  { key: 'visibility', label: t('contactFields.showInList') },
  { key: 'actions', label: t('common.actions'), align: 'right' }
])

// --- Editor dialog ---

const dialogOpen = ref(false)
const isSaving = ref(false)
const editing = ref<ContactField | null>(null)

const form = ref({
  key: '',
  label: '',
  description: '',
  type: 'text' as ContactFieldType,
  group_label: '',
  optionsText: '',
  is_required: false,
  show_in_list: false,
  show_in_chat_panel: true,
  archived: false,
  position: 0
})

// The key and type are fixed once a field exists: filters and templates refer
// to the key, and changing the type would strand existing values.
const isEditing = computed(() => editing.value !== null)

function resetForm() {
  form.value = {
    key: '', label: '', description: '', type: 'text', group_label: '',
    optionsText: '', is_required: false, show_in_list: false,
    show_in_chat_panel: true, archived: false, position: 0
  }
}

function openCreate() {
  editing.value = null
  resetForm()
  dialogOpen.value = true
}

function openEdit(field: ContactField) {
  editing.value = field
  form.value = {
    key: field.key,
    label: field.label,
    description: field.description,
    type: field.type,
    group_label: field.group_label,
    optionsText: (field.options || [])
      .map((o) => (o.label && o.label !== o.value ? `${o.value}|${o.label}` : o.value))
      .join('\n'),
    is_required: field.is_required,
    show_in_list: field.show_in_list,
    show_in_chat_panel: field.show_in_chat_panel,
    archived: Boolean(field.archived_at),
    position: field.position
  }
  dialogOpen.value = true
}

/** Parses the options textarea, accepting "value" or "value|Label" per line. */
function parseOptions() {
  return form.value.optionsText
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [value, label] = line.split('|')
      return { value: value.trim(), label: (label ?? value).trim() }
    })
}

/**
 * Works out which option values were renamed rather than replaced.
 *
 * Editing the textarea, a rename is editing a line in place — so a line whose
 * value changed, where the old value is gone from the list and the new one was
 * not in it before, is a rename. Without this the server sees one option
 * removed and another added, and everything already set to the old value is
 * orphaned: it renders as empty and filters skip it.
 *
 * Only when the list is the same length, because adding or removing a line
 * shifts the rest and the positions stop meaning anything.
 */
function detectRenames(before: { value: string }[], after: { value: string }[]) {
  if (before.length !== after.length) return []

  const wasThere = new Set(before.map((o) => o.value))
  const stillThere = new Set(after.map((o) => o.value))

  const renames: { from: string; to: string }[] = []
  for (let i = 0; i < before.length; i++) {
    const from = before[i].value
    const to = after[i].value
    if (from !== to && !stillThere.has(from) && !wasThere.has(to)) {
      renames.push({ from, to })
    }
  }
  return renames
}

/** Suggests a key from the label, so the common case needs no thought. */
function suggestKey() {
  if (isEditing.value || form.value.key) return
  form.value.key = form.value.label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .slice(0, 64)
}

async function save() {
  isSaving.value = true
  try {
    const payload: Record<string, unknown> = {
      label: form.value.label,
      description: form.value.description,
      group_label: form.value.group_label,
      is_required: form.value.is_required,
      show_in_list: form.value.show_in_list,
      show_in_chat_panel: form.value.show_in_chat_panel,
      position: form.value.position
    }
    if (form.value.type === 'dropdown') {
      const options = parseOptions()
      payload.options = options
      if (isEditing.value && editing.value) {
        const renames = detectRenames(editing.value.options || [], options)
        if (renames.length) payload.option_renames = renames
      }
    }

    if (isEditing.value && editing.value) {
      payload.archived = form.value.archived
      await contactFieldsService.update(editing.value.id, payload)
      toast.success(t('common.updatedSuccess', { resource: t('resources.ContactField') }))
    } else {
      payload.key = form.value.key
      payload.type = form.value.type
      await contactFieldsService.create(payload)
      toast.success(t('common.createdSuccess', { resource: t('resources.ContactField') }))
    }

    dialogOpen.value = false
    await fetchFields()
  } catch (e) {
    toast.error(getErrorMessage(e, t('common.failedSave', { resource: t('resources.contactField') })))
  } finally {
    isSaving.value = false
  }
}

// --- Delete ---

const deleteDialogOpen = ref(false)
const fieldToDelete = ref<ContactField | null>(null)
const isDeleting = ref(false)

function openDelete(field: ContactField) {
  fieldToDelete.value = field
  deleteDialogOpen.value = true
}

async function confirmDelete() {
  if (!fieldToDelete.value) return
  isDeleting.value = true
  try {
    await contactFieldsService.delete(fieldToDelete.value.id)
    toast.success(t('common.deletedSuccess', { resource: t('resources.ContactField') }))
    deleteDialogOpen.value = false
    fieldToDelete.value = null
    await fetchFields()
  } catch (e) {
    toast.error(getErrorMessage(e, t('common.failedDelete', { resource: t('resources.contactField') })))
  } finally {
    isDeleting.value = false
  }
}

async function fetchFields() {
  isLoading.value = true
  fetchError.value = false
  try {
    const response = await contactFieldsService.list()
    fields.value = response.data?.data?.fields ?? []
  } catch {
    fetchError.value = true
    toast.error(t('common.failedLoad', { resource: t('resources.contactFields') }))
  } finally {
    isLoading.value = false
  }
}

function typeLabel(type: string) {
  const match = fieldTypes.find((f) => f.value === type)
  return match ? t(match.labelKey) : type
}

onMounted(() => fetchFields())
</script>

<template>
  <div class="flex flex-col h-full bg-[#0a0a0b] light:bg-gray-50">
    <PageHeader
      :title="$t('contactFields.title')"
      :icon="ListChecks"
      back-link="/settings"
      :breadcrumbs="breadcrumbs"
    >
      <template #actions>
        <Button v-if="canWrite" variant="outline" size="sm" @click="openCreate">
          <Plus class="h-4 w-4 mr-2" />{{ $t('contactFields.addField') }}
        </Button>
      </template>
    </PageHeader>

    <ErrorState
      v-if="fetchError && !isLoading"
      :title="$t('common.somethingWentWrong')"
      :description="$t('common.failedLoad', { resource: $t('resources.contactFields') })"
      class="flex-1"
    >
      <template #action><Button size="sm" @click="fetchFields">{{ $t('common.retry') }}</Button></template>
    </ErrorState>

    <ScrollArea v-else class="flex-1">
      <div class="p-6">
        <Card>
          <CardHeader>
            <CardTitle>{{ $t('contactFields.yourFields') }}</CardTitle>
            <CardDescription>{{ $t('contactFields.yourFieldsDesc') }}</CardDescription>
          </CardHeader>
          <CardContent>
            <DataTable
              :items="fields"
              :columns="columns"
              :is-loading="isLoading"
              :empty-icon="ListChecks"
              :empty-title="$t('contactFields.noFieldsYet')"
              :empty-description="$t('contactFields.noFieldsYetDesc')"
              item-name="fields"
            >
              <template #cell-label="{ item }">
                <div class="flex items-center gap-2">
                  <span class="font-medium">{{ item.label }}</span>
                  <Badge v-if="item.is_system" variant="secondary" class="gap-1">
                    <Lock class="h-3 w-3" />{{ $t('contactFields.builtIn') }}
                  </Badge>
                  <Badge v-if="item.archived_at" variant="outline" class="gap-1">
                    <Archive class="h-3 w-3" />{{ $t('contactFields.archived') }}
                  </Badge>
                </div>
              </template>

              <template #cell-key="{ item }">
                <code class="text-xs text-muted-foreground">{{ item.key }}</code>
              </template>

              <template #cell-type="{ item }">
                <Badge variant="outline">{{ typeLabel(item.type) }}</Badge>
              </template>

              <template #cell-group="{ item }">
                <span class="text-sm text-muted-foreground">{{ item.group_label || '—' }}</span>
              </template>

              <template #cell-visibility="{ item }">
                <span class="text-sm text-muted-foreground">
                  {{ item.show_in_list ? $t('common.yes') : $t('common.no') }}
                </span>
              </template>

              <template #cell-actions="{ item }">
                <div class="flex items-center justify-end gap-1">
                  <Button v-if="canWrite" variant="ghost" size="icon" @click="openEdit(item)">
                    <Pencil class="h-4 w-4" />
                  </Button>
                  <!-- Built-in fields are referenced by key elsewhere, so they
                       are archived rather than deleted. -->
                  <Button
                    v-if="canDelete && !item.is_system"
                    variant="ghost"
                    size="icon"
                    class="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                    @click="openDelete(item)"
                  >
                    <Trash2 class="h-4 w-4" />
                  </Button>
                </div>
              </template>
            </DataTable>
          </CardContent>
        </Card>
      </div>
    </ScrollArea>

    <!-- Editor -->
    <Dialog v-model:open="dialogOpen">
      <DialogContent class="max-w-lg max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {{ isEditing ? $t('contactFields.editField') : $t('contactFields.addField') }}
          </DialogTitle>
          <DialogDescription v-if="isEditing">{{ $t('contactFields.fieldTypeHint') }}</DialogDescription>
        </DialogHeader>

        <div class="space-y-4 py-2">
          <div class="space-y-2">
            <Label for="field-label">{{ $t('contactFields.fieldLabel') }}</Label>
            <Input id="field-label" v-model="form.label" @blur="suggestKey" />
          </div>

          <div class="space-y-2">
            <Label for="field-key">{{ $t('contactFields.fieldKey') }}</Label>
            <Input id="field-key" v-model="form.key" :disabled="isEditing" />
            <p class="text-xs text-muted-foreground">{{ $t('contactFields.fieldKeyHint') }}</p>
          </div>

          <div class="space-y-2">
            <Label>{{ $t('contactFields.fieldType') }}</Label>
            <Select v-model="form.type" :disabled="isEditing">
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="ft in fieldTypes" :key="ft.value" :value="ft.value">
                  {{ $t(ft.labelKey) }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div v-if="form.type === 'dropdown'" class="space-y-2">
            <Label for="field-options">{{ $t('contactFields.options') }}</Label>
            <Textarea id="field-options" v-model="form.optionsText" :rows="5" />
            <p class="text-xs text-muted-foreground">{{ $t('contactFields.optionsHint') }}</p>
          </div>

          <div class="space-y-2">
            <Label for="field-group">{{ $t('contactFields.group') }}</Label>
            <Input id="field-group" v-model="form.group_label" />
            <p class="text-xs text-muted-foreground">{{ $t('contactFields.groupHint') }}</p>
          </div>

          <div class="space-y-2">
            <Label for="field-description">{{ $t('contactFields.description') }}</Label>
            <Input id="field-description" v-model="form.description" />
          </div>

          <div class="flex items-center justify-between">
            <div>
              <Label>{{ $t('contactFields.required') }}</Label>
              <p class="text-xs text-muted-foreground">{{ $t('contactFields.requiredHint') }}</p>
            </div>
            <Switch v-model:checked="form.is_required" />
          </div>

          <div class="flex items-center justify-between">
            <Label>{{ $t('contactFields.showInList') }}</Label>
            <Switch v-model:checked="form.show_in_list" />
          </div>

          <div class="flex items-center justify-between">
            <Label>{{ $t('contactFields.showInChatPanel') }}</Label>
            <Switch v-model:checked="form.show_in_chat_panel" />
          </div>

          <div v-if="isEditing" class="flex items-center justify-between">
            <div>
              <Label>{{ $t('contactFields.archived') }}</Label>
              <p class="text-xs text-muted-foreground">{{ $t('contactFields.archivedHint') }}</p>
            </div>
            <Switch v-model:checked="form.archived" />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" @click="dialogOpen = false">{{ $t('common.cancel') }}</Button>
          <Button :disabled="isSaving || !form.label" @click="save">
            {{ isEditing ? $t('contactFields.updateField') : $t('contactFields.createField') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <DeleteConfirmDialog
      v-model:open="deleteDialogOpen"
      :title="$t('contactFields.deleteField')"
      :description="$t('contactFields.deleteWarning')"
      :is-deleting="isDeleting"
      @confirm="confirmDelete"
    />
  </div>
</template>
