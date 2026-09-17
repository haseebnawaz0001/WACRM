<script setup lang="ts">
/**
 * One control for "who is dealing with this" (plan 10, S12).
 *
 * The chat had three separate things that all looked like assignment and all
 * did something different: an "Assign Contact" dialog that actually set the
 * long-lived owner and was gated by role *name*, a "Transfer to agent" menu
 * item that created a transfer, and a "Resume" button that handed the
 * conversation back to the bot. An agent had to know the product's internals to
 * pick the right one.
 *
 * This is the assignee — who is handling the conversation now. The owner is a
 * different, longer-lived relationship and is shown separately, because
 * conflating them is what made "assign" ambiguous in the first place.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from '@/components/ui/dropdown-menu'
import { UserCheck, ChevronDown } from 'lucide-vue-next'
import { useAuthStore } from '@/stores/auth'

const props = withDefaults(
  defineProps<{
    assigneeId?: string | null
    assigneeName?: string | null
    teamId?: string | null
    teamName?: string | null
    /** Who is dealing with it: bot | human | handoff_pending | none. */
    handling?: string | null
    /** Colleagues this conversation can be handed to. */
    users?: Array<{ id: string; full_name: string }>
    teams?: Array<{ id: string; name: string }>
    disabled?: boolean
  }>(),
  { users: () => [], teams: () => [], disabled: false }
)

const emit = defineEmits<{
  assign: [userId: string | null]
  assignTeam: [teamId: string]
  returnToQueue: []
  handBackToBot: []
}>()

const { t } = useI18n()
const auth = useAuthStore()
const open = ref(false)

const canAssign = computed(() => auth.hasPermission('chat.assign', 'write'))

/**
 * What the chip says now.
 *
 * "Out of hours" is its own state rather than an empty assignee, because a
 * customer who asked for a person and did not get one is waiting on us —
 * showing that as plain "Unassigned" is what let those conversations sit
 * unnoticed until morning.
 */
const label = computed(() => {
  if (props.assigneeName) return props.assigneeName
  if (props.handling === 'handoff_pending') return t('inbox.handoffPending')
  if (props.handling === 'bot') return t('inbox.botHandled')
  if (props.teamName) return props.teamName
  return t('inbox.unassigned')
})

function choose(action: () => void) {
  open.value = false
  action()
}
</script>

<template>
  <DropdownMenu v-model:open="open">
    <DropdownMenuTrigger as-child :disabled="disabled || !canAssign">
      <Button variant="outline" size="sm" class="max-w-[14rem] justify-start gap-1.5">
        <UserCheck class="h-3.5 w-3.5 shrink-0" />
        <span class="truncate">{{ label }}</span>
        <ChevronDown v-if="canAssign" class="ml-auto h-3 w-3 shrink-0 opacity-60" />
      </Button>
    </DropdownMenuTrigger>

    <DropdownMenuContent align="start" class="max-h-80 w-56 overflow-y-auto">
      <DropdownMenuItem @select="choose(() => emit('assign', auth.user?.id ?? null))">
        {{ t('inbox.assignToMe') }}
      </DropdownMenuItem>

      <template v-if="users.length">
        <DropdownMenuSeparator />
        <DropdownMenuItem
          v-for="user in users"
          :key="user.id"
          @select="choose(() => emit('assign', user.id))"
        >
          {{ user.full_name }}
        </DropdownMenuItem>
      </template>

      <template v-if="teams.length">
        <DropdownMenuSeparator />
        <DropdownMenuItem
          v-for="team in teams"
          :key="team.id"
          @select="choose(() => emit('assignTeam', team.id))"
        >
          {{ team.name }}
        </DropdownMenuItem>
      </template>

      <DropdownMenuSeparator />
      <!-- Returning to the queue keeps the contact's owner: stepping away from
           one conversation is not resigning the relationship (plan 10, S5). -->
      <DropdownMenuItem v-if="assigneeId" @select="choose(() => emit('returnToQueue'))">
        {{ t('inbox.returnToQueue') }}
      </DropdownMenuItem>
      <DropdownMenuItem v-if="handling !== 'bot'" @select="choose(() => emit('handBackToBot'))">
        {{ t('inbox.handBackToBot') }}
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
