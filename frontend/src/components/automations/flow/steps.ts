/**
 * Every kind of step the canvas can hold, with the icon and tone its card
 * wears. Flow control (a question, a wait) is visually distinct from the
 * actions, because it changes the shape of the path rather than doing work.
 */
import type { Component } from 'vue'
import {
  Send, LayoutTemplate, Tag, Eraser, PenLine, UserCog, UsersRound, CircleDot,
  StickyNote, ListTodo, Bell, Briefcase, KanbanSquare, Webhook, Split, Hourglass
} from 'lucide-vue-next'
import { actionGroups } from '@/components/crmactions/schema'
import { CONDITION, WAIT } from './tree'

export const stepIcons: Record<string, Component> = {
  send_message: Send,
  send_template: LayoutTemplate,
  add_tags: Tag,
  remove_tags: Eraser,
  set_field: PenLine,
  set_contact_owner: UserCog,
  assign_conversation: UsersRound,
  set_conversation_status: CircleDot,
  add_note: StickyNote,
  create_task: ListTodo,
  notify_users: Bell,
  create_deal: Briefcase,
  move_deal_stage: KanbanSquare,
  call_webhook: Webhook,
  [CONDITION]: Split,
  [WAIT]: Hourglass
}

export type StepTone = 'trigger' | 'condition' | 'wait' | 'action'

export function toneOf(type: string): StepTone {
  if (type === CONDITION) return 'condition'
  if (type === WAIT) return 'wait'
  return 'action'
}

/**
 * The icon tile's colour per tone. Colour marks what a step does to the path:
 * emerald starts it, violet splits it, sky pauses it; actions stay neutral so
 * the path's shape is what stands out. Amber is kept for one meaning only —
 * something still needs finishing — so it is never a step's own colour.
 */
export const toneTile: Record<StepTone, string> = {
  trigger: 'bg-emerald-500/15 text-emerald-300 light:bg-emerald-50 light:text-emerald-700',
  condition: 'bg-violet-500/15 text-violet-300 light:bg-violet-50 light:text-violet-700',
  wait: 'bg-sky-500/15 text-sky-300 light:bg-sky-50 light:text-sky-700',
  action: 'bg-white/[0.06] text-white/70 light:bg-gray-100 light:text-gray-600'
}

/** The add-step menu: flow control first, then actions by what they touch. */
export function paletteGroups(available: string[]): { key: string; types: string[] }[] {
  const offered = new Set(available)
  const groups = [{ key: 'flow', types: [CONDITION, WAIT] }]
  for (const group of actionGroups) {
    const types = group.types.filter(type => offered.has(type))
    if (types.length) groups.push({ key: group.key, types })
  }
  // Anything the backend offers that the menu does not know yet still appears.
  const known = new Set(actionGroups.flatMap(group => group.types))
  const unknown = available.filter(type => !known.has(type))
  if (unknown.length) groups.push({ key: 'other', types: unknown })
  return groups
}
