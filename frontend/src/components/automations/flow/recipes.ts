/**
 * Starting points, named for what somebody wants to stop worrying about.
 *
 * A blank builder asks people to invent a process before they know what the
 * product can do. Each recipe is a real rule — questions, waits and all — so
 * opening one teaches the shape of a good automation. Anything that needs the
 * organization's own choice (which template, which team) is left blank on
 * purpose and shows up in amber, so the recipe walks the person to exactly
 * the decisions only they can make.
 */
import type { Component } from 'vue'
import {
  Hourglass, MessageCircleQuestion, PhoneMissed, Sparkles, HeartHandshake, CalendarClock, Trophy,
  CalendarX2, UserPlus
} from 'lucide-vue-next'
import { newStepId, type FlowStep } from './tree'

type T = (key: string, ...args: any[]) => string

export interface Recipe {
  key: string
  goal: 'waiting' | 'followUp' | 'records' | 'team'
  icon: Component
  build: (t: T) => {
    name: string
    trigger_type: string
    trigger_config: Record<string, any>
    contact_filter?: Record<string, any> | null
    actions: FlowStep[]
  }
}

function step(type: string, config: Record<string, any> = {}, extra: Partial<FlowStep> = {}): FlowStep {
  return { id: newStepId(), type, config, ...extra }
}
const wait = (amount: number, unit: string) => step('wait', { for: { amount, unit } })
const tagged = (tag: string) => ({
  filter: { op: 'and', rules: [{ field: 'tags', operator: 'contains_any', value: [tag] }] }
})
const lifecycleIs = (stage: string) => ({
  filter: { op: 'and', rules: [{ field: 'field.lifecycle_stage', operator: 'in', value: [stage] }] }
})
/** Placeholders written as text, since braces are template syntax elsewhere. */
const v = (path: string) => `${'{'}${'{'}${path}${'}'}${'}'}`

export const recipes: Recipe[] = [
  {
    key: 'chaseQuiet', goal: 'waiting', icon: Hourglass,
    build: t => ({
      name: t('automations.recipes.chaseQuiet.name'),
      trigger_type: 'time.no_customer_reply',
      trigger_config: { after: { amount: 2, unit: 'days' } },
      actions: [
        step('add_tags', { tags: [t('automations.recipes.chaseQuiet.tag')] }),
        step('create_task', {
          title: t('automations.recipes.chaseQuiet.task', { name: v('contact.name') }),
          due_in: { amount: 1, unit: 'days' },
          owner: { mode: 'contact_owner' }
        })
      ]
    })
  },
  {
    key: 'nobodyAnswered', goal: 'waiting', icon: MessageCircleQuestion,
    build: t => ({
      name: t('automations.recipes.nobodyAnswered.name'),
      trigger_type: 'time.no_agent_reply',
      trigger_config: { after: { amount: 15, unit: 'minutes' } },
      actions: [
        step('notify_users', {
          recipients: { conversation_assignee: true, contact_owner: true },
          title: t('automations.recipes.nobodyAnswered.title', { name: v('contact.name') }),
          body: t('automations.recipes.nobodyAnswered.body')
        })
      ]
    })
  },
  {
    key: 'missedCall', goal: 'followUp', icon: PhoneMissed,
    build: t => ({
      name: t('automations.recipes.missedCall.name'),
      trigger_type: 'call.missed',
      trigger_config: { direction: 'incoming' },
      actions: [
        step('create_task', {
          title: t('automations.recipes.missedCall.task', { name: v('contact.name') }),
          type_key: 'call_back',
          due_in: { amount: 1, unit: 'hours' },
          owner: { mode: 'contact_owner' }
        }),
        step('notify_users', {
          recipients: { contact_owner: true },
          title: t('automations.recipes.missedCall.title', { name: v('contact.name') })
        })
      ]
    })
  },
  {
    key: 'vipSplit', goal: 'followUp', icon: Sparkles,
    build: t => ({
      name: t('automations.recipes.vipSplit.name'),
      trigger_type: 'contact.tag_added',
      trigger_config: { tags: ['VIP'] },
      actions: [
        step('condition', lifecycleIs('customer'), {
          then: [step('create_task', {
            title: t('automations.recipes.vipSplit.task', { name: v('contact.name') }),
            due_in: { amount: 1, unit: 'days' },
            owner: { mode: 'contact_owner' },
            priority: 'high'
          })],
          else: [
            step('assign_conversation', { mode: 'team' }),
            step('notify_users', {
              recipients: { contact_owner: true },
              title: t('automations.recipes.vipSplit.lead', { name: v('contact.name') })
            })
          ]
        })
      ]
    })
  },
  {
    key: 'welcome', goal: 'followUp', icon: UserPlus,
    build: t => ({
      name: t('automations.recipes.welcome.name'),
      trigger_type: 'contact.created',
      trigger_config: { sources: ['inbound'] },
      actions: [
        wait(5, 'minutes'),
        step('send_message', { text: t('automations.recipes.welcome.message', { name: v('contact.name') }) })
      ]
    })
  },
  {
    key: 'feedback', goal: 'followUp', icon: HeartHandshake,
    build: t => ({
      name: t('automations.recipes.feedback.name'),
      trigger_type: 'conversation.status_changed',
      trigger_config: { to: ['resolved'] },
      actions: [
        wait(1, 'days'),
        step('condition', tagged(t('automations.recipes.feedback.tag')), {
          then: [],
          else: [
            step('send_template', {}),
            step('add_tags', { tags: [t('automations.recipes.feedback.tag')] })
          ]
        })
      ]
    })
  },
  {
    key: 'dateReminder', goal: 'followUp', icon: CalendarClock,
    build: t => ({
      name: t('automations.recipes.dateReminder.name'),
      trigger_type: 'time.date_field',
      trigger_config: { offset_days: -7, at_local_hour: 9 },
      actions: [
        step('create_task', {
          title: t('automations.recipes.dateReminder.task', { name: v('contact.name') }),
          due_in: { amount: 2, unit: 'days' },
          owner: { mode: 'contact_owner' }
        }),
        step('send_template', {})
      ]
    })
  },
  {
    key: 'dealWon', goal: 'records', icon: Trophy,
    build: t => ({
      name: t('automations.recipes.dealWon.name'),
      trigger_type: 'deal.won',
      trigger_config: {},
      actions: [
        step('set_field', { field: 'lifecycle_stage', value: 'customer' }),
        step('add_tags', { tags: [t('automations.recipes.dealWon.tag')] }),
        step('create_task', {
          title: t('automations.recipes.dealWon.task', { name: v('contact.name') }),
          due_in: { amount: 2, unit: 'days' },
          owner: { mode: 'contact_owner' }
        })
      ]
    })
  },
  {
    key: 'overdue', goal: 'team', icon: CalendarX2,
    build: t => ({
      name: t('automations.recipes.overdue.name'),
      trigger_type: 'task.overdue',
      trigger_config: {},
      actions: [
        step('notify_users', {
          recipients: { contact_owner: true, conversation_assignee: true },
          title: t('automations.recipes.overdue.title', { name: v('contact.name') })
        })
      ]
    })
  }
]

export const recipeGoals: Recipe['goal'][] = ['waiting', 'followUp', 'records', 'team']
