/**
 * Plain-English sentences for every part of an automation.
 *
 * A card that says "assign_conversation · team_id: 3f2a…" asks the reader to
 * run the rule in their head. A card that says "Hand the conversation to the
 * Billing queue" is the rule. Each card gets a title (what it does) and a
 * detail (what it does it with). The detail is built from parts, so the
 * values somebody chose — the tag, the team, the day count — can carry weight
 * against the words around them; the rule as a whole is written out as prose
 * from the same parts, so the canvas and the summary cannot disagree.
 */
import type { FilterNode, FilterFieldInfo } from '@/services/api'
import { CONDITION, WAIT, type FlowStep } from './tree'
import { triggerDef } from './triggers'
import { templateParamNames } from '@/components/crmactions/schema'
import type { useLookups } from '../useLookups'

type T = (key: string, ...args: any[]) => string
type Lookups = ReturnType<typeof useLookups>

/** A run of text; `value` marks something the person chose. */
export interface Part {
  text: string
  value?: boolean
}

export interface Sentence {
  title: string
  detail?: string
  parts?: Part[]
}

const val = (text: string): Part => ({ text, value: true })
const txt = (text: string): Part => ({ text })

/**
 * Stands in for a value inside a translated message, so the words around it
 * keep the translation's order while the value itself is marked.
 */
export const SLOT = '\u2063'

/** Splits a message rendered with SLOT for its one parameter around `value`. */
export function around(message: string, value: Part[]): Part[] {
  const at = message.indexOf(SLOT)
  if (at < 0) return [txt(message)]
  return [txt(message.slice(0, at)), ...value, txt(message.slice(at + SLOT.length))].filter(p => p.text)
}

/** Joins groups of parts with a separator, dropping empty groups. */
function joinParts(groups: Part[][], separator: string): Part[] {
  const kept = groups.filter(g => g.some(p => p.text))
  return kept.flatMap((g, i) => (i ? [txt(separator), ...g] : g))
}

export const textOf = (parts: Part[] = []) => parts.map(p => p.text).join('')

function sentence(title: string, groups: Part[][] = []): Sentence {
  const parts = joinParts(groups, ' · ')
  return parts.length ? { title, detail: textOf(parts), parts } : { title }
}

/** Joins a list the way a person says it: "A, B or C". */
export function joinList(t: T, items: string[], conjunction: 'or' | 'and' = 'or'): string {
  const clean = items.filter(Boolean)
  if (clean.length <= 1) return clean[0] || ''
  const last = clean[clean.length - 1]
  return t(`automations.list.${conjunction}`, { items: clean.slice(0, -1).join(', '), last })
}

export function durationText(t: T, value?: { amount?: number; unit?: string } | null): string {
  const amount = Number(value?.amount)
  if (!value || !(amount > 0)) return ''
  const unit = value.unit || 'days'
  return t(`automations.duration.${unit}`, { n: amount }, amount)
}

/** The human name of a placeholder path: "Company", not contact.fields.company. */
export function variableLabel(path: string, lookups: Lookups): string {
  if (path.startsWith('contact.fields.')) {
    const field = lookups.field(path.slice('contact.fields.'.length))
    if (field) return field.label
  }
  const variable = lookups.state.variables.find(v => v.path === path)
  return variable?.label || path.split('.').pop() || path
}

/**
 * Shows a placeholder as what goes there — ‹Contact name› — rather than as
 * the syntax that puts it there. The people reading the canvas are not the
 * people who should have to parse {{contact.fields.company}}.
 */
export function readable(text: string, lookups: Lookups): string {
  return text.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (_, path: string) => `‹${variableLabel(path, lookups)}›`)
}

function quote(lookups: Lookups, text: unknown, max = 72): string {
  const s = readable(String(text ?? ''), lookups).replace(/\s+/g, ' ').trim()
  if (!s) return ''
  return `“${s.length > max ? s.slice(0, max - 1) + '…' : s}”`
}

/** A label used mid-sentence: "for the contact's owner", not "for The…". */
export function lowerFirst(text: string): string {
  return text ? text.charAt(0).toLowerCase() + text.slice(1) : text
}

function optionLabel(field: FilterFieldInfo | undefined, value: unknown): string {
  const option = field?.options?.find(o => o.value === value)
  return option?.label ?? String(value)
}

/** One condition: "Lifecycle stage is **Customer**". */
function ruleParts(t: T, lookups: Lookups, fields: FilterFieldInfo[], rule: FilterNode): Part[] {
  if (rule.rules) {
    const inner = rule.rules.map(r => ruleParts(t, lookups, fields, r)).filter(p => p.length)
    if (!inner.length) return []
    return [txt('('), ...listParts(t, inner, rule.op === 'or' ? 'or' : 'and'), txt(')')]
  }
  const field = fields.find(f => f.key === rule.field)
  const name = field?.label || rule.field || ''
  const values = Array.isArray(rule.value) ? rule.value : rule.value === '' || rule.value == null ? [] : [rule.value]
  // "is any of Customer" is how a list operator reads with one value in it;
  // a person would say "is Customer".
  const single = values.length === 1
  const op = single && rule.operator === 'in'
    ? t('filters.op.is')
    : single && (rule.operator === 'contains_any' || rule.operator === 'contains_all')
      ? t('automations.sentence.has')
      : t(`filters.op.${rule.operator}`, String(rule.operator || '').replace(/_/g, ' '))
  const shown = values.map(v => field?.type === 'user' ? lookups.userName(String(v)) || String(v) : optionLabel(field, v))
  const out = [txt(`${name} ${op}`)]
  if (shown.length) out.push(txt(' '), val(shown.join(', ')))
  return out
}

/** Several clauses joined the way a person lists them, keeping their parts. */
function listParts(t: T, items: Part[][], conjunction: 'or' | 'and'): Part[] {
  if (items.length <= 1) return items[0] || []
  // Render the joined list once with slots, then put each item's parts back.
  const slots = items.map((_, i) => `${SLOT}${i}${SLOT}`)
  const message = joinList(t, slots, conjunction)
  const out: Part[] = []
  for (const piece of message.split(new RegExp(`${SLOT}(\\d+)${SLOT}`))) {
    const index = /^\d+$/.test(piece) ? Number(piece) : -1
    if (index >= 0 && items[index]) out.push(...items[index])
    else if (piece) out.push(txt(piece))
  }
  return out
}

/** A whole filter as parts, or [] when it has no conditions. */
export function filterParts(t: T, lookups: Lookups, fields: FilterFieldInfo[], filter?: FilterNode | null): Part[] {
  if (!filter?.rules?.length) return []
  const clauses = filter.rules.map(rule => ruleParts(t, lookups, fields, rule)).filter(p => p.length)
  return listParts(t, clauses, filter.op === 'or' ? 'or' : 'and')
}

/** A whole filter as one clause, or '' when it has no conditions. */
export function filterText(t: T, lookups: Lookups, fields: FilterFieldInfo[], filter?: FilterNode | null): string {
  return textOf(filterParts(t, lookups, fields, filter))
}

function names(list: unknown, resolve: (id: string) => string | undefined): string[] {
  return Array.isArray(list) ? list.map(id => resolve(String(id)) || String(id)) : []
}

/** What starts the rule. */
export function triggerSentence(t: T, lookups: Lookups, type: string, config: Record<string, any> = {}): Sentence {
  const title = t(`automations.triggers.${type}`, type)
  const def = triggerDef(type)
  const groups: Part[][] = []
  const plain = (text: string) => groups.push([val(text)])
  const framed = (key: string, param: string, text: string) => groups.push(around(t(key, { [param]: SLOT }), [val(text)]))

  for (const field of def?.fields || []) {
    const value = config[field.key]
    if (value === undefined || value === null || value === '' || (Array.isArray(value) && !value.length)) continue
    switch (field.kind) {
      case 'tags':
      case 'accounts':
        plain(joinList(t, value))
        break
      case 'users':
        plain(joinList(t, names(value, lookups.userName)))
        break
      case 'teams':
        plain(joinList(t, names(value, lookups.teamName)))
        break
      case 'multiChoice':
      case 'choice': {
        const list = Array.isArray(value) ? value : [value]
        plain(joinList(t, list.map((v: string) => t(`automations.choices.${v}`, v))))
        break
      }
      case 'lifecycleStages':
      case 'fieldValues': {
        const f = lookups.field(field.kind === 'lifecycleStages' ? 'lifecycle_stage' : config.field)
        const list = Array.isArray(value) ? value : [value]
        framed('automations.sentence.toValue', 'value', joinList(t, list.map((v: string) => f?.options?.find(o => o.value === v)?.label || v)))
        break
      }
      case 'contactField':
        plain(lookups.field(value)?.label || value)
        break
      case 'flows':
        plain(joinList(t, names(value, lookups.flowName)))
        break
      case 'campaigns':
        plain(joinList(t, names(value, lookups.campaignName)))
        break
      case 'taskTypes':
        plain(joinList(t, (value as string[]).map(k => lookups.taskTypeLabel(k) || k)))
        break
      case 'pipeline':
        plain(lookups.pipeline(value)?.name || '')
        break
      case 'stages':
        framed('automations.sentence.toValue', 'value', joinList(t, (value as string[]).map(id => lookups.stage(id)?.stage.name || id)))
        break
      case 'duration':
        framed('automations.sentence.forDuration', 'duration', durationText(t, value))
        break
      case 'dateOffset': {
        const n = Number(value)
        if (n === 0) plain(t('automations.sentence.onTheDay'))
        else if (n < 0) plain(t('automations.sentence.daysBefore', { n: -n }, -n))
        else plain(t('automations.sentence.daysAfter', { n }, n))
        break
      }
      case 'hour':
        framed('automations.sentence.atHour', 'hour', `${String(value).padStart(2, '0')}:00`)
        break
    }
  }
  return sentence(title, groups)
}

/** One step's card. */
export function stepSentence(t: T, lookups: Lookups, fields: FilterFieldInfo[], step: FlowStep): Sentence {
  const c = step.config || {}
  const title = t(`automations.steps.${step.type}.title`, step.type)
  const framed = (key: string, param: string, text: string): Part[] => text ? around(t(key, { [param]: SLOT }), [val(text)]) : []

  switch (step.type) {
    case CONDITION:
      return sentence(title, [filterParts(t, lookups, fields, c.filter)])
    case WAIT: {
      const d = durationText(t, c.for)
      return { title: d ? t('automations.sentence.waitFor', { duration: d }) : title }
    }
    case 'add_tags':
    case 'remove_tags':
      return sentence(title, [Array.isArray(c.tags) && c.tags.length ? [val(c.tags.join(', '))] : []])
    case 'set_field': {
      if (!c.field) return { title }
      const field = lookups.field(c.field)
      const value = field?.options?.find(o => o.value === c.value)?.label ?? c.value
      const shown = typeof value === 'string' ? readable(value, lookups) : value
      return sentence(title, [[
        txt(field?.label || c.field),
        ...(shown !== undefined && shown !== '' ? [txt(' → '), val(String(shown))] : [])
      ]])
    }
    case 'set_contact_owner':
      if (c.mode === 'user') return sentence(title, [[val(lookups.userName(c.user_id) || '')]])
      return sentence(title, [c.mode ? [val(t(`automations.choices.${c.mode}`))] : []])
    case 'set_conversation_status':
      if (c.status === 'snoozed') return sentence(title, [framed('automations.sentence.snoozeFor', 'duration', durationText(t, c.snooze_for))])
      return sentence(title, [c.status ? [val(t(`automations.choices.${c.status}`))] : []])
    case 'assign_conversation':
      if (c.mode === 'team') return sentence(title, [framed('automations.sentence.teamQueue', 'team', lookups.teamName(c.team_id) || '')])
      if (c.mode === 'user') return sentence(title, [[val(lookups.userName(c.user_id) || '')]])
      return sentence(title, [c.mode ? [val(t(`automations.choices.${c.mode}`))] : []])
    case 'create_task': {
      const owner = c.owner?.mode === 'user'
        ? lookups.userName(c.owner?.user_id) || ''
        : c.owner?.mode ? lowerFirst(t(`automations.choices.owner_${c.owner.mode}`)) : ''
      return sentence(title, [
        [val(quote(lookups, c.title, 48))],
        framed('automations.sentence.dueIn', 'duration', durationText(t, c.due_in)),
        framed('automations.sentence.forOwner', 'owner', owner)
      ])
    }
    case 'add_note':
      return sentence(title, [[val(quote(lookups, c.content))]])
    case 'notify_users': {
      const r = c.recipients || {}
      const who = [
        ...(r.contact_owner ? [t('automations.choices.owner_contact_owner')] : []),
        ...(r.conversation_assignee ? [t('automations.choices.owner_conversation_assignee')] : []),
        ...names(r.user_ids, lookups.userName),
        ...(Array.isArray(r.roles) ? r.roles.map((role: string) => t('automations.sentence.everyoneWithRole', { role })) : [])
      ]
      return sentence(title, [[val(joinList(t, who, 'and'))], [txt(quote(lookups, c.title, 40))]])
    }
    case 'send_message':
      return sentence(title, [[val(quote(lookups, c.text))]])
    case 'send_template': {
      const template = lookups.template(c.template_id)
      if (!template) return { title }
      const params = templateParamNames(template.body_content).length
      return sentence(title, [
        [val(template.name), ...(template.language ? [txt(` (${template.language})`)] : [])],
        params ? [txt(t('automations.sentence.params', { n: params }, params))] : []
      ])
    }
    case 'create_deal':
      return sentence(title, [
        [val(quote(lookups, c.title, 40))],
        c.value ? [val(Number(c.value).toLocaleString())] : [],
        [txt(lookups.pipeline(c.pipeline_id)?.name || '')]
      ])
    case 'move_deal_stage': {
      const found = lookups.stage(c.stage_id)
      return sentence(title, found ? [[val(found.stage.name)], [txt(found.pipeline.name)]] : [])
    }
    case 'call_webhook':
      return sentence(title, c.url ? [[txt(`${(c.method || 'POST').toUpperCase()} `), val(c.url)]] : [])
  }
  return { title }
}

// --- The rule, written out ---

/** A run of prose; `step` makes it a link to that card ('start' for the trigger). */
export interface ProseRun extends Part {
  step?: string
}
export interface Paragraph {
  /** How many questions deep this paragraph sits. */
  depth: number
  runs: ProseRun[]
  /** A quiet line (a hint), not part of the rule. */
  muted?: boolean
}

/** A card's parts inside a sentence: "(“Call ‹Name›”, due in 1 day)". */
function inParens(parts?: Part[]): ProseRun[] {
  if (!parts?.length) return []
  return [txt(' ('), ...parts.map(p => (p.text === ' · ' ? txt(', ') : p)), txt(')')]
}

function linked(runs: Part[], step: string): ProseRun[] {
  return runs.map(r => ({ ...r, step }))
}

/**
 * Writes the rule as a few short paragraphs:
 *
 *   When a tag is added (VIP), for contacts where Lifecycle stage is
 *   Customer, it will check whether Tags has VIP.
 *     If yes, it will hand the conversation over (to the Billing queue),
 *     then create a follow-up (“Call ‹Contact name›”, due in 1 day).
 *     If no, it will wait 1 day, then send a message (“Hi ‹Contact name›”).
 *   After that, it will add a note (“Followed up”).
 *
 * Every step's words link to its card.
 */
export function ruleProse(
  t: T, lookups: Lookups, fields: FilterFieldInfo[],
  trigger: { type: string; config: Record<string, any> },
  filter: FilterNode | null | undefined,
  steps: FlowStep[]
): Paragraph[] {
  const clause = (step: FlowStep): ProseRun[] => {
    if (step.type === WAIT) {
      const d = durationText(t, step.config?.for)
      if (!d) return [{ text: lowerFirst(t('automations.steps.wait.title')), step: step.id }]
      return linked(around(lowerFirst(t('automations.sentence.waitFor', { duration: SLOT })), [val(d)]), step.id)
    }
    const s = stepSentence(t, lookups, fields, step)
    return [{ text: lowerFirst(s.title), step: step.id }, ...inParens(s.parts)]
  }

  const question = (step: FlowStep): ProseRun[] => {
    const asked = filterParts(t, lookups, fields, step.config?.filter)
    if (!asked.length) return [{ text: t('automations.prose.checkUnset'), step: step.id }]
    return linked(around(t('automations.prose.checkWhether', { question: SLOT }), asked), step.id)
  }

  const paragraphs: Paragraph[] = []

  /** Writes a list of steps starting with `lead`; `after` says whether anything follows the list. */
  const write = (list: FlowStep[], depth: number, lead: ProseRun[], after: boolean) => {
    let runs: ProseRun[] = [...lead]
    let open = false
    const close = () => {
      if (open) paragraphs.push({ depth, runs: [...runs, txt('.')] })
      open = false
    }

    list.forEach((step, index) => {
      if (open) runs.push(txt(t('automations.prose.then')))
      else if (index > 0) runs = [txt(t('automations.prose.afterThat'))]
      open = true

      if (step.type !== CONDITION) {
        runs.push(...clause(step))
        return
      }
      runs.push(...question(step))
      close()
      const moreAfter = after || index < list.length - 1
      branch(step.then || [], depth + 1, 'Yes', moreAfter)
      branch(step.else || [], depth + 1, 'No', moreAfter)
    })
    close()
  }

  const branch = (list: FlowStep[], depth: number, which: 'Yes' | 'No', after: boolean) => {
    if (!list.length) {
      const key = after ? `automations.prose.if${which}MovesOn` : `automations.prose.if${which}Stops`
      paragraphs.push({ depth, runs: [txt(t(key))] })
      return
    }
    write(list, depth, [txt(t(`automations.prose.if${which}`))], after)
  }

  // The opening: what starts it and who it is for.
  const s = triggerSentence(t, lookups, trigger.type, trigger.config)
  const opening: ProseRun[] = around(
    t('automations.prose.when', { trigger: SLOT }),
    [{ text: lowerFirst(s.title), step: 'start' }, ...inParens(s.parts)]
  )
  const who = filterParts(t, lookups, fields, filter)
  if (who.length) opening.push(...around(t('automations.prose.forWho', { who: SLOT }), who))

  if (!steps.length) {
    paragraphs.push({ depth: 0, runs: [...opening, txt(t('automations.prose.nothingYet'))] })
    paragraphs.push({ depth: 0, runs: [txt(t('automations.prose.addHint'))], muted: true })
    return paragraphs
  }
  write(steps, 0, [...opening, txt(t('automations.prose.itWill'))], false)
  return paragraphs
}
