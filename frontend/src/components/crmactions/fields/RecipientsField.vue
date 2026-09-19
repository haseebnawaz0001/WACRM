<script setup lang="ts">
/**
 * Who hears about it: the people connected to this customer, particular
 * colleagues, or everyone with a role.
 */
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Checkbox } from '@/components/ui/checkbox'
import { useLookups } from '@/components/automations/useLookups'
import OptionPicker from './OptionPicker.vue'

interface Recipients {
  contact_owner?: boolean
  conversation_assignee?: boolean
  user_ids?: string[]
  roles?: string[]
}

const props = defineProps<{ modelValue?: Recipients | null; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: Recipients] }>()

const { t } = useI18n()
const lookups = useLookups()
onMounted(() => { lookups.ensure('users', 'roles') })

const value = computed<Recipients>(() => props.modelValue || {})

function set(patch: Partial<Recipients>) {
  emit('update:modelValue', { ...value.value, ...patch })
}

const people = computed(() =>
  lookups.state.users.filter(u => u.is_active !== false).map(u => ({ value: u.id, label: u.full_name, hint: u.email })))
const roles = computed(() => lookups.state.roles.map(r => ({ value: r.name, label: r.name })))
</script>

<template>
  <div class="space-y-2.5">
    <label class="flex items-center gap-2.5 text-sm">
      <Checkbox
        :checked="!!value.contact_owner"
        :disabled="disabled"
        @update:checked="v => set({ contact_owner: v === true })"
      />
      {{ t('automations.recipients.contactOwner') }}
    </label>
    <label class="flex items-center gap-2.5 text-sm">
      <Checkbox
        :checked="!!value.conversation_assignee"
        :disabled="disabled"
        @update:checked="v => set({ conversation_assignee: v === true })"
      />
      {{ t('automations.recipients.assignee') }}
    </label>
    <OptionPicker
      :model-value="value.user_ids || []"
      :options="people"
      multiple
      :disabled="disabled"
      :placeholder="t('automations.recipients.people')"
      :aria-label="t('automations.recipients.people')"
      @update:model-value="v => set({ user_ids: v })"
    />
    <OptionPicker
      :model-value="value.roles || []"
      :options="roles"
      multiple
      :disabled="disabled"
      :placeholder="t('automations.recipients.roles')"
      :aria-label="t('automations.recipients.roles')"
      @update:model-value="v => set({ roles: v })"
    />
  </div>
</template>
