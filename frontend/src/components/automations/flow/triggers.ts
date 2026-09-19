/**
 * What can start an automation, in the words of the person building one.
 *
 * The backend catalog says which triggers exist and which settings each
 * accepts. This file says how to ask for those settings: a tag picker for
 * tags, a person picker for people, "3 days" for a wait. Every setting is
 * optional — leaving one empty means "any" — so a trigger works the moment it
 * is chosen and gets narrower as the person adds detail.
 */
import type { Component } from 'vue'
import {
  UserPlus, Tag, TagsIcon, PenLine, UserCheck, Milestone, MessageSquarePlus,
  MessagesSquare, UsersRound, AlarmClock, PhoneMissed, PhoneCall, Bot, Megaphone,
  ListPlus, ListChecks, CalendarX2, Briefcase, ArrowRightLeft, Trophy, XCircle,
  Hourglass, MessageCircleQuestion, CalendarClock
} from 'lucide-vue-next'

export type TriggerFieldKind =
  | 'tags' | 'users' | 'teams' | 'multiChoice' | 'contactField' | 'fieldValues'
  | 'lifecycleStages' | 'accounts' | 'flows' | 'campaigns' | 'taskTypes'
  | 'pipeline' | 'stages' | 'duration' | 'dateOffset' | 'hour' | 'choice'

export interface TriggerField {
  key: string
  kind: TriggerFieldKind
  /** i18n key under automations.triggerFields */
  label: string
  /** Choices for choice/multiChoice: values; labels under automations.choices */
  options?: string[]
  /** Must be set for the trigger to mean anything (a date field, a delay). */
  required?: boolean
  advanced?: boolean
}

export interface TriggerDef {
  type: string
  group: string
  icon: Component
  fields: TriggerField[]
}

const contactSources = ['inbound', 'manual', 'import', 'campaign', 'api', 'call']
const statuses = ['open', 'pending', 'snoozed', 'resolved']
const resolutionReasons = [
  'agent', 'automation', 'client_inactivity', 'pending_timeout', 'sla_auto_close', 'bulk'
]

export const triggerDefs: TriggerDef[] = [
  // People and records
  { type: 'contact.created', group: 'contacts', icon: UserPlus,
    fields: [{ key: 'sources', kind: 'multiChoice', label: 'sources', options: contactSources }] },
  { type: 'contact.tag_added', group: 'contacts', icon: Tag,
    fields: [{ key: 'tags', kind: 'tags', label: 'tags' }] },
  { type: 'contact.tag_removed', group: 'contacts', icon: TagsIcon,
    fields: [{ key: 'tags', kind: 'tags', label: 'tags' }] },
  { type: 'contact.lifecycle_stage_changed', group: 'contacts', icon: Milestone,
    fields: [{ key: 'to', kind: 'lifecycleStages', label: 'toStage' }] },
  { type: 'contact.field_changed', group: 'contacts', icon: PenLine,
    fields: [
      { key: 'field', kind: 'contactField', label: 'field' },
      { key: 'to', kind: 'fieldValues', label: 'toValues' }
    ] },
  { type: 'contact.assigned', group: 'contacts', icon: UserCheck,
    fields: [{ key: 'to_user_ids', kind: 'users', label: 'toPeople' }] },

  // Conversations
  { type: 'conversation.created', group: 'conversations', icon: MessageSquarePlus,
    fields: [{ key: 'accounts', kind: 'accounts', label: 'accounts' }] },
  { type: 'conversation.status_changed', group: 'conversations', icon: MessagesSquare,
    fields: [
      { key: 'to', kind: 'multiChoice', label: 'toStatus', options: statuses },
      { key: 'reasons', kind: 'multiChoice', label: 'reasons', options: resolutionReasons, advanced: true }
    ] },
  { type: 'conversation.assigned', group: 'conversations', icon: UsersRound,
    fields: [
      { key: 'user_ids', kind: 'users', label: 'toPeople' },
      { key: 'team_ids', kind: 'teams', label: 'toTeams' }
    ] },
  { type: 'conversation.sla_breached', group: 'conversations', icon: AlarmClock, fields: [] },

  // Waiting — the things that happen because nothing happened
  { type: 'time.no_customer_reply', group: 'time', icon: Hourglass,
    fields: [
      { key: 'after', kind: 'duration', label: 'quietFor', required: true },
      { key: 'statuses', kind: 'multiChoice', label: 'whileStatus', options: ['open', 'pending'], advanced: true }
    ] },
  { type: 'time.no_agent_reply', group: 'time', icon: MessageCircleQuestion,
    fields: [{ key: 'after', kind: 'duration', label: 'unansweredFor', required: true }] },
  { type: 'time.date_field', group: 'time', icon: CalendarClock,
    fields: [
      { key: 'field', kind: 'contactField', label: 'dateField', required: true },
      { key: 'offset_days', kind: 'dateOffset', label: 'when' },
      { key: 'at_local_hour', kind: 'hour', label: 'atHour' }
    ] },

  // Calls, bots, campaigns
  { type: 'call.missed', group: 'calls', icon: PhoneMissed,
    fields: [{ key: 'direction', kind: 'choice', label: 'direction', options: ['incoming', 'outgoing'] }] },
  { type: 'call.completed', group: 'calls', icon: PhoneCall,
    fields: [{ key: 'direction', kind: 'choice', label: 'direction', options: ['incoming', 'outgoing'] }] },
  { type: 'chatbot.flow_completed', group: 'chatbot', icon: Bot,
    fields: [{ key: 'flow_ids', kind: 'flows', label: 'flows' }] },
  { type: 'campaign.replied', group: 'campaigns', icon: Megaphone,
    fields: [{ key: 'campaign_ids', kind: 'campaigns', label: 'campaigns' }] },

  // Follow-ups
  { type: 'task.created', group: 'tasks', icon: ListPlus,
    fields: [{ key: 'type_keys', kind: 'taskTypes', label: 'taskTypes' }] },
  { type: 'task.completed', group: 'tasks', icon: ListChecks,
    fields: [{ key: 'type_keys', kind: 'taskTypes', label: 'taskTypes' }] },
  { type: 'task.overdue', group: 'tasks', icon: CalendarX2,
    fields: [{ key: 'type_keys', kind: 'taskTypes', label: 'taskTypes' }] },

  // Deals
  { type: 'deal.created', group: 'deals', icon: Briefcase,
    fields: [{ key: 'pipeline_id', kind: 'pipeline', label: 'pipeline' }] },
  { type: 'deal.stage_changed', group: 'deals', icon: ArrowRightLeft,
    fields: [
      { key: 'pipeline_id', kind: 'pipeline', label: 'pipeline' },
      { key: 'to_stage_ids', kind: 'stages', label: 'toStages' }
    ] },
  { type: 'deal.won', group: 'deals', icon: Trophy,
    fields: [{ key: 'pipeline_id', kind: 'pipeline', label: 'pipeline' }] },
  { type: 'deal.lost', group: 'deals', icon: XCircle,
    fields: [{ key: 'pipeline_id', kind: 'pipeline', label: 'pipeline' }] }
]

/** Group order in the trigger picker: what people reach for first. */
export const triggerGroupOrder = ['contacts', 'conversations', 'time', 'tasks', 'deals', 'calls', 'chatbot', 'campaigns']

export function triggerDef(type?: string): TriggerDef | undefined {
  return triggerDefs.find(def => def.type === type)
}

/** Trigger settings that must be set before the rule can run. */
export function missingTriggerFields(type: string, config: Record<string, any>): string[] {
  const def = triggerDef(type)
  if (!def) return []
  return def.fields.filter(field => {
    if (!field.required) return false
    const value = config?.[field.key]
    if (field.kind === 'duration') return !value || !(Number(value.amount) > 0)
    return value === undefined || value === null || value === ''
  }).map(field => field.key)
}
