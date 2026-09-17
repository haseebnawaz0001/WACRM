/**
 * List state that survives a reload and can be sent to a colleague
 * (plan 10, S12).
 *
 * Every list page kept its filters, sort and page in local refs. That made a
 * list un-shareable — "the overdue ones assigned to Sam" could only be
 * described in words — and lost the view on every reload, which is the moment
 * someone most wants it back. Keeping the state in the URL fixes both, and
 * costs nothing at the call site.
 *
 * The URL is the single source: the composable writes to it and reads back
 * from it, so the browser's back button moves through views the way a person
 * expects rather than leaving the address bar disagreeing with the screen.
 */
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

export interface ListViewState<F> {
  view: string
  search: string
  sortKey: string
  sortDir: 'asc' | 'desc'
  page: number
  filter: F | null
}

export interface ListViewOptions<F> {
  /** Named tab — "mine", "all", "overdue". */
  defaultView?: string
  defaultSortKey?: string
  defaultSortDir?: 'asc' | 'desc'
  /**
   * Query parameter prefix, so two lists on one page do not fight over
   * `?page=`.
   */
  prefix?: string
  /** Parse the filter from its URL form. Invalid input must return null. */
  parseFilter?: (raw: string) => F | null
  /** Serialise the filter for the URL. */
  serialiseFilter?: (filter: F) => string
}

export function useListViewState<F = unknown>(options: ListViewOptions<F> = {}) {
  const {
    defaultView = '',
    defaultSortKey = '',
    defaultSortDir = 'desc',
    prefix = '',
    parseFilter,
    serialiseFilter
  } = options

  const route = useRoute()
  const router = useRouter()

  const key = (name: string) => (prefix ? `${prefix}_${name}` : name)

  function readString(name: string, fallback: string): string {
    const raw = route.query[key(name)]
    return typeof raw === 'string' && raw !== '' ? raw : fallback
  }

  const view = ref(readString('view', defaultView))
  const search = ref(readString('q', ''))
  const sortKey = ref(readString('sort', defaultSortKey))
  const sortDir = ref<'asc' | 'desc'>(
    readString('dir', defaultSortDir) === 'asc' ? 'asc' : 'desc'
  )
  const page = ref(Math.max(1, Number(readString('page', '1')) || 1))

  const filter = ref<F | null>(null)
  if (parseFilter) {
    const raw = readString('filter', '')
    // A malformed filter in a pasted URL shows the unfiltered list rather than
    // an error: the person wanted to see something, and a broken query
    // parameter is not their problem to debug.
    filter.value = raw ? parseFilter(raw) : null
  }

  /** Everything a caller needs to build a request. */
  const state = computed<ListViewState<F>>(() => ({
    view: view.value,
    search: search.value,
    sortKey: sortKey.value,
    sortDir: sortDir.value,
    page: page.value,
    filter: filter.value as F | null
  }))

  let writing = false

  function syncToUrl() {
    const query: Record<string, string> = {}
    // Only non-default values go in, so a URL says what is unusual about this
    // view rather than restating every default.
    for (const [name, value, fallback] of [
      ['view', view.value, defaultView],
      ['q', search.value, ''],
      ['sort', sortKey.value, defaultSortKey],
      ['dir', sortDir.value, defaultSortDir],
      ['page', String(page.value), '1']
    ] as const) {
      if (value && value !== fallback) query[key(name)] = value
    }
    if (filter.value && serialiseFilter) {
      const raw = serialiseFilter(filter.value as F)
      if (raw) query[key('filter')] = raw
    }

    // Keep any query parameters that belong to somebody else on this page.
    const preserved: Record<string, any> = {}
    for (const [name, value] of Object.entries(route.query)) {
      if (!name.startsWith(prefix ? `${prefix}_` : '')) preserved[name] = value
      else if (prefix === '' && !['view', 'q', 'sort', 'dir', 'page', 'filter'].includes(name)) {
        preserved[name] = value
      }
    }

    writing = true
    // replace, not push: changing a filter is refining one view, not
    // navigating, and pushing would make Back walk through every keystroke.
    void router
      .replace({ query: { ...preserved, ...query } })
      .finally(() => {
        writing = false
      })
  }

  watch([view, search, sortKey, sortDir, page, filter], syncToUrl, { deep: true })

  // Changing the view, the search or the filter means a different list, so the
  // page number from the old one is meaningless — and "no results" on page 4 is
  // the most confusing way to show it.
  watch([view, search, filter], () => {
    page.value = 1
  }, { deep: true })

  // Follow the URL when it changes underneath us: the back button, a pasted
  // link, or a nav item pointing at a preset view.
  watch(
    () => route.query,
    () => {
      if (writing) return
      view.value = readString('view', defaultView)
      search.value = readString('q', '')
      sortKey.value = readString('sort', defaultSortKey)
      sortDir.value = readString('dir', defaultSortDir) === 'asc' ? 'asc' : 'desc'
      page.value = Math.max(1, Number(readString('page', '1')) || 1)
      if (parseFilter) {
        const raw = readString('filter', '')
        filter.value = raw ? parseFilter(raw) : null
      }
    }
  )

  function reset() {
    view.value = defaultView
    search.value = ''
    sortKey.value = defaultSortKey
    sortDir.value = defaultSortDir
    page.value = 1
    filter.value = null
  }

  return { view, search, sortKey, sortDir, page, filter, state, reset }
}

/** Filter serialisation for callers whose filter is a plain object. */
export function jsonFilterCodec<F>() {
  return {
    parseFilter: (raw: string): F | null => {
      try {
        return JSON.parse(decodeURIComponent(raw)) as F
      } catch {
        return null
      }
    },
    serialiseFilter: (filter: F): string => {
      try {
        return encodeURIComponent(JSON.stringify(filter))
      } catch {
        return ''
      }
    }
  }
}
