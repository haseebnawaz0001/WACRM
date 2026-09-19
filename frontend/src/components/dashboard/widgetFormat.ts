/**
 * Putting a widget's numbers into words.
 *
 * A measure knows what its number is — money, minutes, a count — and the card
 * says it that way: "€12.4K", "1h 20m", "38". A bare 4800 on a card called
 * "First reply time" is a number the reader has to decode.
 */
import type { Component } from 'vue'
import {
  MessageSquare, Users, Briefcase, ListTodo, Zap, Phone, Send, Bot, Link2, BarChart3
} from 'lucide-vue-next'
import type { WidgetData } from '@/services/api'

type T = (key: string, ...args: any[]) => string

/** A count, compact past a thousand. */
export function formatCount(value: number): string {
  if (Math.abs(value) >= 1_000_000) return `${+(value / 1_000_000).toFixed(1)}M`
  if (Math.abs(value) >= 10_000) return `${+(value / 1_000).toFixed(1)}K`
  return Math.round(value).toLocaleString()
}

/** A length of time from minutes: "45m", "1h 20m", "2d 4h". */
export function formatMinutes(minutes: number): string {
  if (!minutes || minutes < 0) return '0m'
  if (minutes < 1) return `${Math.round(minutes * 60)}s`
  if (minutes < 60) return `${Math.round(minutes)}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) {
    const rest = Math.round(minutes - hours * 60)
    return rest ? `${hours}h ${rest}m` : `${hours}h`
  }
  const days = Math.floor(hours / 24)
  const restHours = hours - days * 24
  return restHours ? `${days}d ${restHours}h` : `${days}d`
}

export function formatMoney(value: number, currency = 'USD'): string {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
      notation: Math.abs(value) >= 10_000 ? 'compact' : 'standard',
      maximumFractionDigits: Math.abs(value) >= 10_000 ? 1 : 0
    }).format(value)
  } catch {
    return `${formatCount(value)} ${currency}`
  }
}

/** A widget's number, the way its measure means it. */
export function formatValue(value: number, data?: Pick<WidgetData, 'unit' | 'currency'> | null): string {
  switch (data?.unit) {
    case 'money': return formatMoney(value, data.currency)
    // An average of nothing is not zero minutes — "0m" would read as instant
    // replies in a period with no replies at all.
    case 'minutes': return value > 0 ? formatMinutes(value) : '—'
    case 'seconds': return value > 0 ? formatMinutes(value / 60) : '—'
    default: return formatCount(value)
  }
}

/**
 * Whether a change is good news. More deals won is; more overdue follow-ups
 * is not, so the arrow's colour follows the measure rather than the sign.
 */
export function changeTone(change: number, lowerIsBetter?: boolean): 'good' | 'bad' | 'flat' {
  if (!change) return 'flat'
  const up = change > 0
  return up !== !!lowerIsBetter ? 'good' : 'bad'
}

export const AREA_ICONS: Record<string, Component> = {
  inbox: MessageSquare,
  contacts: Users,
  deals: Briefcase,
  followups: ListTodo,
  automations: Zap,
  calls: Phone,
  messaging: Send,
  chatbot: Bot,
  links: Link2
}

/** The area a widget belongs to, from its measure or its original source. */
export function widgetArea(widget: { data_source: string; config?: Record<string, any> }, measureArea?: (key: string) => string | undefined): string {
  const measure = widget.config?.measure as string | undefined
  if (measure) return measureArea?.(measure) || areaOfMeasure(measure)
  switch (widget.data_source) {
    case 'messages': case 'campaigns': return 'messaging'
    case 'contacts': return 'contacts'
    case 'sessions': case 'transfers': return 'chatbot'
    case 'tasks': return 'followups'
    case 'deals': return 'deals'
    case 'conversations': return 'inbox'
    case 'shortcuts': return 'links'
  }
  return ''
}

/** A measure key's area, from its prefix, for when the catalog is not loaded. */
function areaOfMeasure(key: string): string {
  if (key.startsWith('conversations') || key === 'first_reply_time') return 'inbox'
  if (key.startsWith('contacts') || key === 'lifecycle_funnel') return 'contacts'
  if (key.startsWith('deals') || key === 'pipeline_funnel') return 'deals'
  if (key.startsWith('tasks')) return 'followups'
  if (key.startsWith('automation')) return 'automations'
  if (key.startsWith('call')) return 'calls'
  if (key.startsWith('messages')) return 'messaging'
  if (key.startsWith('chatbot') || key.startsWith('handoffs')) return 'chatbot'
  return ''
}

export function areaIcon(area: string): Component {
  return AREA_ICONS[area] || BarChart3
}

/**
 * The words for a value a widget was split by. Enums ("resolved", "high") are
 * put into the viewer's language; names the server resolved are kept; a blank
 * says so.
 */
export function splitLabel(t: T, te: (key: string) => boolean, dim: string | undefined, point: { label: string; key?: string }): string {
  const key = point.key ?? point.label
  if (!key || key === '(empty)') return t('dashboard.notSet')
  // A name the server looked up (a person, a stage, a field's own label) is
  // already in words.
  if (point.label && point.label !== key) return point.label
  return valueLabel(t, te, dim, key)
}

/** The words for one value of a dimension, where the product has them. */
export function valueLabel(t: T, te: (key: string) => boolean, dim: string | undefined, value: string): string {
  if (dim && te(`dashboard.values.${dim}.${value}`)) return t(`dashboard.values.${dim}.${value}`)
  return value.replace(/_/g, ' ').replace(/^./, c => c.toUpperCase())
}
