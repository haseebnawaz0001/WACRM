/**
 * The CRM action library's shape, for every surface that edits one
 * (plan 10, S7).
 *
 * The automation builder, the chatbot's CRM-action node and keyword rules all
 * configure the same actions, so what each action needs is described once,
 * here. Each setting names the kind of input it takes — a tag picker, a
 * person, a template, a length of time, text that may use variables — so every
 * surface offers a real choice instead of asking somebody to type an id they
 * would have to look up.
 *
 * `required` mirrors the server's own validation. It is what lets a builder
 * mark an unfinished step the moment it is added rather than after a save.
 */

/** One configurable action on a rule, node or keyword. */
export interface CrmActionSpec {
  id: string
  type: string
  config: Record<string, any>
  continue_on_error?: boolean
}

export type FieldKind =
  | 'text'
  | 'longtext'
  | 'number'
  | 'boolean'
  | 'choice'
  | 'tags'
  | 'user'
  | 'team'
  | 'template'
  | 'pipeline'
  | 'stage'
  | 'taskType'
  | 'contactField'
  | 'fieldValue'
  | 'duration'
  | 'recipients'
  | 'owner'
  | 'headers'
  | 'templateParams'

export interface ActionField {
  key: string
  kind: FieldKind
  /** i18n key under automations.fields */
  label: string
  /** i18n key under automations.hints */
  hint?: string
  required?: boolean
  /** Text that may use {{variables}}. */
  variables?: boolean
  /** Choices for a 'choice' field: value → i18n key under automations.choices */
  options?: string[]
  /** Shown only when this returns true for the step's config. */
  showIf?: (config: Record<string, any>) => boolean
  /** Tucked under "More options": useful, but not what most people need. */
  advanced?: boolean
  placeholder?: string
}

/** Action groups, in the order the "add a step" menu offers them. */
export const actionGroups: { key: string; types: string[] }[] = [
  { key: 'message', types: ['send_message', 'send_template'] },
  { key: 'contact', types: ['add_tags', 'remove_tags', 'set_field', 'set_contact_owner'] },
  { key: 'conversation', types: ['assign_conversation', 'set_conversation_status', 'add_note'] },
  { key: 'work', types: ['create_task', 'notify_users'] },
  { key: 'deals', types: ['create_deal', 'move_deal_stage'] },
  { key: 'connect', types: ['call_webhook'] }
]

export const actionFields: Record<string, ActionField[]> = {
  add_tags: [{ key: 'tags', kind: 'tags', label: 'tags', required: true }],
  remove_tags: [{ key: 'tags', kind: 'tags', label: 'tags', required: true }],
  set_field: [
    { key: 'field', kind: 'contactField', label: 'field', required: true },
    { key: 'value', kind: 'fieldValue', label: 'newValue', required: true, variables: true }
  ],
  set_contact_owner: [
    {
      key: 'mode', kind: 'choice', label: 'newOwner', required: true,
      options: ['user', 'conversation_assignee', 'unassign']
    },
    { key: 'user_id', kind: 'user', label: 'person', required: true, showIf: c => c.mode === 'user' }
  ],
  set_conversation_status: [
    {
      key: 'status', kind: 'choice', label: 'status', required: true,
      options: ['open', 'resolved', 'snoozed']
    },
    {
      key: 'snooze_for', kind: 'duration', label: 'snoozeFor', required: true,
      showIf: c => c.status === 'snoozed'
    }
  ],
  assign_conversation: [
    {
      key: 'mode', kind: 'choice', label: 'assignTo', required: true,
      options: ['team', 'user', 'unassign']
    },
    { key: 'team_id', kind: 'team', label: 'team', required: true, showIf: c => c.mode === 'team', hint: 'teamQueue' },
    { key: 'user_id', kind: 'user', label: 'person', required: true, showIf: c => c.mode === 'user' }
  ],
  create_task: [
    { key: 'title', kind: 'text', label: 'taskTitle', required: true, variables: true, placeholder: 'taskTitle' },
    { key: 'due_in', kind: 'duration', label: 'dueIn' },
    { key: 'owner', kind: 'owner', label: 'taskOwner' },
    { key: 'type_key', kind: 'taskType', label: 'taskType' },
    { key: 'description', kind: 'longtext', label: 'details', variables: true, advanced: true },
    { key: 'priority', kind: 'choice', label: 'priority', options: ['low', 'normal', 'high'], advanced: true }
  ],
  add_note: [{ key: 'content', kind: 'longtext', label: 'note', required: true, variables: true }],
  notify_users: [
    { key: 'recipients', kind: 'recipients', label: 'whoToTell', required: true },
    { key: 'title', kind: 'text', label: 'notificationTitle', required: true, variables: true },
    { key: 'body', kind: 'longtext', label: 'notificationBody', variables: true }
  ],
  send_message: [
    { key: 'text', kind: 'longtext', label: 'message', required: true, variables: true, hint: 'serviceWindow' }
  ],
  send_template: [
    { key: 'template_id', kind: 'template', label: 'template', required: true, hint: 'templateAnytime' },
    { key: 'param_mappings', kind: 'templateParams', label: 'templateParams' }
  ],
  create_deal: [
    { key: 'title', kind: 'text', label: 'dealTitle', required: true, variables: true },
    { key: 'value', kind: 'number', label: 'dealValue' },
    { key: 'pipeline_id', kind: 'pipeline', label: 'pipeline' },
    { key: 'stage_id', kind: 'stage', label: 'stage', showIf: c => !!c.pipeline_id }
  ],
  move_deal_stage: [
    { key: 'stage_id', kind: 'stage', label: 'moveTo', required: true, hint: 'latestOpenDeal' }
  ],
  call_webhook: [
    { key: 'url', kind: 'text', label: 'url', required: true, variables: true, placeholder: 'url' },
    {
      key: 'method', kind: 'choice', label: 'method',
      options: ['POST', 'GET', 'PUT', 'PATCH', 'DELETE']
    },
    {
      key: 'body', kind: 'longtext', label: 'webhookBody', variables: true,
      showIf: c => (c.method || 'POST') !== 'GET'
    },
    { key: 'headers', kind: 'headers', label: 'headers', advanced: true },
    { key: 'sign', kind: 'boolean', label: 'sign', advanced: true, hint: 'sign' },
    { key: 'signing_secret', kind: 'text', label: 'signingSecret', advanced: true, showIf: c => !!c.sign }
  ]
}

export function fieldsFor(type: string): ActionField[] {
  return actionFields[type] || []
}

export function visibleFields(type: string, config: Record<string, any>): ActionField[] {
  return fieldsFor(type).filter(field => !field.showIf || field.showIf(config))
}

/** Defaults a new step starts with, so the obvious choice is already made. */
export const actionDefaults: Record<string, Record<string, any>> = {
  set_contact_owner: { mode: 'conversation_assignee' },
  set_conversation_status: { status: 'resolved' },
  assign_conversation: { mode: 'team' },
  create_task: { due_in: { amount: 1, unit: 'days' }, owner: { mode: 'contact_owner' } },
  notify_users: { recipients: { contact_owner: true } },
  call_webhook: { method: 'POST' }
}

/** A new action, with an id stable enough to key a list on. */
export function newAction(type: string): CrmActionSpec {
  return {
    id: `a${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`,
    type,
    config: structuredClone(actionDefaults[type] || {}),
    continue_on_error: false
  }
}

function isBlank(value: unknown): boolean {
  if (value === undefined || value === null) return true
  if (typeof value === 'string') return value.trim() === ''
  if (Array.isArray(value)) return value.length === 0
  return false
}

/**
 * The settings an action still needs, as field keys.
 *
 * The server's validation is the authority; this is the instant version of
 * it, so the card turns amber the moment a step is added and clears the moment
 * it is filled in.
 */
export function missingFields(type: string, config: Record<string, any>): string[] {
  const missing: string[] = []
  for (const field of visibleFields(type, config)) {
    if (!field.required) continue
    const value = config[field.key]
    if (field.kind === 'recipients') {
      const r = value || {}
      const any = (r.user_ids?.length || 0) + (r.roles?.length || 0) > 0 || r.contact_owner || r.conversation_assignee
      if (!any) missing.push(field.key)
      continue
    }
    if (field.kind === 'duration') {
      if (!value || !(Number(value.amount) > 0)) missing.push(field.key)
      continue
    }
    if (isBlank(value)) missing.push(field.key)
  }
  return missing
}

/** Placeholder names a template body uses: {{1}}, {{customer_name}}. */
export function templateParamNames(body: string | undefined | null): string[] {
  if (!body) return []
  const names: string[] = []
  for (const match of body.matchAll(/\{\{\s*([\w.]+)\s*\}\}/g)) {
    if (!names.includes(match[1])) names.push(match[1])
  }
  return names
}
