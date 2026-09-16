/**
 * The CRM action library's shape, for every surface that edits one
 * (plan 10, S7).
 *
 * The automation builder, the chatbot's CRM-action node, keyword rules and
 * bulk-action dialogs all let someone configure the same actions. They had
 * begun to diverge — the automation builder knew that `add_tags` takes a list
 * and `create_task` takes a title, and nothing else did — which is how three
 * editors end up disagreeing about what an action needs and only one of them
 * being right.
 */

/** One configurable action on a rule, node or keyword. */
export interface CrmActionSpec {
  id: string
  type: string
  config: Record<string, any>
  continue_on_error?: boolean
}

export type FieldKind = 'text' | 'list' | 'number' | 'textarea'

export interface ActionField {
  key: string
  label: string
  kind: FieldKind
}

/**
 * The fields worth a labelled input per action type.
 *
 * Deliberately not exhaustive: anything absent falls through to the raw
 * config rather than being hidden, so a setting the backend accepts is never
 * silently unreachable from the UI.
 */
export const actionFields: Record<string, ActionField[]> = {
  add_tags: [{ key: 'tags', label: 'tags', kind: 'list' }],
  remove_tags: [{ key: 'tags', label: 'tags', kind: 'list' }],
  set_field: [
    { key: 'field', label: 'field', kind: 'text' },
    { key: 'value', label: 'value', kind: 'text' }
  ],
  create_task: [
    { key: 'title', label: 'title', kind: 'text' },
    { key: 'description', label: 'description', kind: 'textarea' },
    { key: 'type_key', label: 'type_key', kind: 'text' }
  ],
  add_note: [{ key: 'content', label: 'content', kind: 'textarea' }],
  send_message: [{ key: 'text', label: 'text', kind: 'textarea' }],
  send_template: [{ key: 'template_id', label: 'template_id', kind: 'text' }],
  notify_users: [
    { key: 'title', label: 'title', kind: 'text' },
    { key: 'body', label: 'body', kind: 'textarea' }
  ],
  call_webhook: [
    { key: 'url', label: 'url', kind: 'text' },
    { key: 'method', label: 'method', kind: 'text' },
    { key: 'body', label: 'body', kind: 'textarea' }
  ],
  set_conversation_status: [{ key: 'status', label: 'status', kind: 'text' }],
  assign_conversation: [
    { key: 'mode', label: 'mode', kind: 'text' },
    { key: 'team_id', label: 'team_id', kind: 'text' },
    { key: 'user_id', label: 'user_id', kind: 'text' }
  ],
  set_contact_owner: [
    { key: 'mode', label: 'mode', kind: 'text' },
    { key: 'user_id', label: 'user_id', kind: 'text' }
  ],
  create_deal: [
    { key: 'title', label: 'title', kind: 'text' },
    { key: 'value', label: 'value', kind: 'number' }
  ],
  move_deal_stage: [{ key: 'stage_id', label: 'stage_id', kind: 'text' }]
}

export function fieldsFor(type: string): ActionField[] {
  return actionFields[type] || []
}

/** A new action, with an id stable enough to key a list on. */
export function newAction(type: string): CrmActionSpec {
  return {
    id: `a${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`,
    type,
    config: {},
    continue_on_error: false
  }
}

/** Renders a list-valued config field as the comma-separated text an input shows. */
export function configList(action: CrmActionSpec, key: string): string {
  const value = action.config[key]
  return Array.isArray(value) ? value.join(', ') : ''
}

/** Parses that text back into a list, dropping the empties a trailing comma leaves. */
export function setConfigList(action: CrmActionSpec, key: string, raw: string) {
  action.config[key] = raw.split(',').map(s => s.trim()).filter(Boolean)
}
