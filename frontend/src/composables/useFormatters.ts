import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'

/**
 * useFormatters renders dates and times the way this organization reads them
 * (plan 10, S11).
 *
 * Every date in the product went through `formatDate`/`formatTime` in
 * `lib/utils.ts`, which hardcoded `'en-US'` and the browser's own timezone. The
 * organization's `timezone` and `date_format` settings existed and were applied
 * nowhere, so a team in Mumbai read American dates, and two agents in different
 * countries looking at the same conversation could disagree about what day a
 * message arrived — which matters as soon as anyone quotes a date to a
 * customer.
 *
 * The zone is the user's own override if they set one, then the organization's,
 * then the browser's. The locale follows the UI language, so switching the
 * interface to French switches the month names too.
 */

/** Date format presets, matching what the settings page offers. */
const DATE_STYLES: Record<string, Intl.DateTimeFormatOptions> = {
  'DD/MM/YYYY': { day: '2-digit', month: '2-digit', year: 'numeric' },
  'MM/DD/YYYY': { month: '2-digit', day: '2-digit', year: 'numeric' },
  'YYYY-MM-DD': { year: 'numeric', month: '2-digit', day: '2-digit' },
  'DD MMM YYYY': { day: 'numeric', month: 'short', year: 'numeric' },
}

const DEFAULT_DATE_STYLE: Intl.DateTimeFormatOptions = {
  year: 'numeric',
  month: 'short',
  day: 'numeric',
}

export function useFormatters() {
  const auth = useAuthStore()
  const { locale } = useI18n()

  const timeZone = computed(() => auth.displayTimezone)
  const dateStyle = computed(() => DATE_STYLES[auth.displayDateFormat] || DEFAULT_DATE_STYLE)

  function toDate(value: string | Date): Date | null {
    const d = typeof value === 'string' ? new Date(value) : value
    return Number.isNaN(d.getTime()) ? null : d
  }

  /** format renders a date, falling back to the raw value if it is unparseable. */
  function format(value: string | Date, options: Intl.DateTimeFormatOptions): string {
    const d = toDate(value)
    if (!d) return typeof value === 'string' ? value : ''
    try {
      return new Intl.DateTimeFormat(locale.value, { timeZone: timeZone.value, ...options }).format(d)
    } catch {
      // An unknown IANA zone must not blank out a screen; fall back to the
      // browser's.
      return new Intl.DateTimeFormat(locale.value, options).format(d)
    }
  }

  function formatDate(value: string | Date, options?: Intl.DateTimeFormatOptions): string {
    return format(value, { ...dateStyle.value, ...options })
  }

  function formatTime(value: string | Date): string {
    return format(value, { hour: '2-digit', minute: '2-digit' })
  }

  function formatDateTime(value: string | Date): string {
    return `${formatDate(value)} ${formatTime(value)}`
  }

  /**
   * formatRelative reads as "3 minutes ago" up to a week, then as a date.
   * Beyond a week "412 days ago" is arithmetic, not information.
   */
  function formatRelative(value: string | Date): string {
    const d = toDate(value)
    if (!d) return ''

    const seconds = Math.round((d.getTime() - Date.now()) / 1000)
    const magnitude = Math.abs(seconds)
    if (magnitude > 7 * 24 * 60 * 60) return formatDate(d)

    const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
      ['second', 60],
      ['minute', 60],
      ['hour', 24],
      ['day', 7],
    ]
    let value_ = seconds
    for (const [unit, step] of units) {
      if (Math.abs(value_) < step) {
        return new Intl.RelativeTimeFormat(locale.value, { numeric: 'auto' })
          .format(Math.round(value_), unit)
      }
      value_ /= step
    }
    return formatDate(d)
  }

  return {
    timeZone,
    format,
    formatDate,
    formatTime,
    formatDateTime,
    formatRelative,
  }
}
