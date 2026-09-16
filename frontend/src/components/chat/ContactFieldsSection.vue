<script setup lang="ts">
/**
 * The contact's details in the chat side panel (plan 01).
 *
 * An agent mid-conversation is the person most likely to learn a customer's
 * company or email, and the least likely to leave the thread to record it. The
 * fields the organization marked "show in chat panel" are editable here, saved
 * one at a time on blur, so noting something costs a click rather than a
 * detour.
 */
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue
} from '@/components/ui/select'
import { contactFieldsService, contactsService, type ContactField } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { toast } from 'vue-sonner'
import { ClipboardList } from 'lucide-vue-next'

const props = defineProps<{ contactId: string; values?: Record<string, any> | null }>()

const { t } = useI18n()
const authStore = useAuthStore()

const fields = ref<ContactField[]>([])
const draft = ref<Record<string, any>>({})
const saving = ref<string | null>(null)

const canEdit = computed(() => authStore.hasPermission('contacts', 'write'))

/** Only the fields the organization chose to show here, in its own order. */
const panelFields = computed(() =>
  // An archived field keeps its values readable elsewhere, but offering it for
  // editing would suggest the organization still uses it.
  fields.value.filter(field => field.show_in_chat_panel && !field.archived_at)
)

async function loadFields() {
  try {
    const { data } = await contactFieldsService.list()
    fields.value = data.fields || []
  } catch {
    fields.value = []
  }
}

watch(() => props.values, values => {
  draft.value = { ...(values || {}) }
}, { immediate: true })

/**
 * Saves one field. Sending only the changed key means two agents editing
 * different fields on the same contact do not overwrite each other.
 */
async function save(field: ContactField) {
  const value = draft.value[field.key] ?? ''
  if ((props.values?.[field.key] ?? '') === value) return

  saving.value = field.key
  try {
    await contactsService.update(props.contactId, { fields: { [field.key]: value } })
  } catch (error: any) {
    // Put the stored value back, so the panel never shows something the
    // server rejected as though it had been saved.
    draft.value[field.key] = props.values?.[field.key] ?? ''
    toast.error(error?.response?.data?.message || t('common.error'))
  } finally {
    saving.value = null
  }
}

onMounted(loadFields)
</script>

<template>
  <div v-if="panelFields.length" class="pb-4">
    <h5 class="flex items-center gap-2 py-2 text-sm font-medium">
      <ClipboardList class="h-4 w-4 text-muted-foreground" />
      {{ t('contactFields.details') }}
    </h5>

    <div class="space-y-2">
      <div v-for="field in panelFields" :key="field.id" class="space-y-1">
        <Label class="text-xs text-muted-foreground">{{ field.label }}</Label>

        <Select
          v-if="field.type === 'dropdown'"
          v-model="draft[field.key]"
          :disabled="!canEdit || saving === field.key"
          @update:model-value="() => save(field)"
        >
          <SelectTrigger class="h-8">
            <SelectValue :placeholder="t('contactFields.notSet')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="option in field.options || []" :key="option.value" :value="option.value">
              {{ option.label }}
            </SelectItem>
          </SelectContent>
        </Select>

        <Input
          v-else
          v-model="draft[field.key]"
          class="h-8"
          :type="field.type === 'number' ? 'number' : field.type === 'date' ? 'date' : 'text'"
          :disabled="!canEdit || saving === field.key"
          :placeholder="t('contactFields.notSet')"
          @blur="save(field)"
          @keyup.enter="save(field)"
        />
      </div>
    </div>
  </div>
</template>
