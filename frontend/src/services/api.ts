import axios, { type AxiosInstance, type AxiosError, type InternalAxiosRequestConfig } from 'axios'

// Get base path from server-injected config or fallback
const basePath = ((window as any).__BASE_PATH__ ?? '').replace(/\/$/, '')
const API_BASE_URL = import.meta.env.VITE_API_URL || `${basePath}/api`

export const api: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json'
  }
})

// Helper to read a cookie by name
function getCookie(name: string): string | null {
  const match = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'))
  return match ? decodeURIComponent(match[1]) : null
}

/**
 * Build standard headers for native fetch() calls.
 * Includes X-Organization-ID (for org switching) and optionally X-CSRF-Token (for mutating requests).
 */
export function getRequestHeaders(opts?: { csrf?: boolean }): Record<string, string> {
  const headers: Record<string, string> = {}
  const selectedOrgId = localStorage.getItem('selected_organization_id')
  if (selectedOrgId) {
    headers['X-Organization-ID'] = selectedOrgId
  }
  if (opts?.csrf) {
    const csrfToken = getCookie('whm_csrf')
    if (csrfToken) {
      headers['X-CSRF-Token'] = csrfToken
    }
  }
  return headers
}

// Request interceptor to add CSRF token and organization header
api.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    // Add CSRF token on mutating requests (cookie-based auth sends cookies automatically)
    const method = (config.method || '').toUpperCase()
    if (method === 'POST' || method === 'PUT' || method === 'DELETE' || method === 'PATCH') {
      const csrfToken = getCookie('whm_csrf')
      if (csrfToken) {
        config.headers['X-CSRF-Token'] = csrfToken
      }
    }
    // Add organization override header for org switching
    const selectedOrgId = localStorage.getItem('selected_organization_id')
    if (selectedOrgId) {
      config.headers['X-Organization-ID'] = selectedOrgId
    }
    return config
  },
  (error: AxiosError) => {
    return Promise.reject(error)
  }
)

// Token refresh mutex — ensures only one refresh runs at a time.
// Without this, multiple concurrent 401s each trigger a refresh, but the
// single-use refresh token (JTI deleted from Redis) causes all but the first
// to fail, which clears auth and logs the user out.
let isRefreshing = false
let refreshSubscribers: Array<(success: boolean) => void> = []

function onRefreshComplete(success: boolean) {
  refreshSubscribers.forEach(cb => cb(success))
  refreshSubscribers = []
}

// refreshAccessToken posts to /auth/refresh. The refresh token is single-use
// with rotation, so two tabs refreshing at once would have the loser's token
// rejected as "revoked" and get logged out. The Web Locks API serializes the
// refresh across all same-origin tabs: each tab refreshes in turn using the
// cookie rotated by the previous holder, so every refresh succeeds instead of
// racing. The in-tab `isRefreshing` mutex still dedupes concurrent 401s within
// a single tab. Falls back to a plain request where Web Locks is unavailable
// (older browsers / insecure contexts).
async function refreshAccessToken(): Promise<void> {
  const doRefresh = () =>
    axios.post(`${API_BASE_URL}/auth/refresh`, {}, { withCredentials: true })

  if (navigator.locks?.request) {
    await navigator.locks.request('whm-token-refresh', async () => {
      await doRefresh()
    })
  } else {
    await doRefresh()
  }
}

// Response interceptor for error handling
api.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean }

    // Skip token refresh logic for auth endpoints
    const isAuthEndpoint = originalRequest?.url?.startsWith('/auth/')

    // Handle 401 errors - try to refresh token (but not for auth endpoints)
    if (error.response?.status === 401 && !originalRequest._retry && !isAuthEndpoint) {
      originalRequest._retry = true

      // If a refresh is already in flight, queue this request to wait for it
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          refreshSubscribers.push((success: boolean) => {
            if (success) {
              resolve(api(originalRequest))
            } else {
              reject(error)
            }
          })
        })
      }

      isRefreshing = true

      try {
        // Browser sends whm_refresh cookie automatically via withCredentials.
        // Serialized across tabs via Web Locks to avoid single-use-token races.
        await refreshAccessToken()

        // Cookies are updated by the server response — notify waiting requests
        onRefreshComplete(true)
        isRefreshing = false

        // Retry the original request
        return api(originalRequest)
      } catch {
        // Refresh failed — notify waiting requests and redirect to login
        onRefreshComplete(false)
        isRefreshing = false

        localStorage.removeItem('user')
        localStorage.removeItem('auth_token')
        localStorage.removeItem('refresh_token')
        window.location.href = basePath + '/login'
      }
    }

    return Promise.reject(error)
  }
)

// API service methods
export const authService = {
  getWSToken: () => api.get('/auth/ws-token'),
}

export const usersService = {
  list: (params?: { search?: string; page?: number; limit?: number; role_id?: string; online_only?: boolean }) =>
    api.get('/users', { params }),
  get: (id: string) => api.get(`/users/${id}`),
  create: (data: { email: string; password: string; full_name: string; role_id?: string }) =>
    api.post('/users', data),
  update: (id: string, data: { email?: string; password?: string; full_name?: string; role_id?: string; is_active?: boolean }) =>
    api.put(`/users/${id}`, data),
  delete: (id: string) => api.delete(`/users/${id}`),
  me: () => api.get('/me'),
  updateSettings: (data: {
    email_notifications: boolean
    new_message_alerts: boolean
    campaign_updates: boolean
    timezone?: string
    /**
     * Per-type in-app and sound preferences (plan 00, F5).
     *
     * The server has honoured these since the bell shipped; nothing could set
     * them, so every user sat on the default for every type.
     */
    notifications?: Record<string, { in_app: boolean; sound: boolean }>
  }) => api.put('/me/settings', data),
  changePassword: (data: { current_password: string; new_password: string }) =>
    api.put('/me/password', data),
  updateAvailability: (isAvailable: boolean) =>
    api.put('/me/availability', { is_available: isAvailable }),
  listMyOrganizations: () => api.get('/me/organizations'),
}

export const apiKeysService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ api_keys: any[]; total?: number }>('/api-keys', { params }),
  get: (id: string) => api.get(`/api-keys/${id}`),
  create: (data: { name: string; expires_at?: string }) =>
    api.post('/api-keys', data),
  update: (id: string, data: { is_active?: boolean }) =>
    api.put(`/api-keys/${id}`, data),
  delete: (id: string) => api.delete(`/api-keys/${id}`)
}

export const accountsService = {
  list: () => api.get('/accounts')
}

export const contactsService = {
  list: (params?: { search?: string; page?: number; limit?: number; tags?: string }) =>
    api.get('/contacts', { params }),
  get: (id: string) => api.get(`/contacts/${id}`),
  create: (data: any) => api.post('/contacts', data),
  update: (id: string, data: any) => api.put(`/contacts/${id}`, data),
  delete: (id: string) => api.delete(`/contacts/${id}`),
  assign: (id: string, userId: string | null) =>
    api.put(`/contacts/${id}/assign`, { user_id: userId }),
  updateTags: (id: string, tags: string[]) =>
    api.put(`/contacts/${id}/tags`, { tags }),
  getSessionData: (id: string) => api.get(`/contacts/${id}/session-data`),
  markRead: (id: string) => api.post(`/contacts/${encodeURIComponent(id)}/mark-read`),

  // Contacts list v2 (plan 01). The filter is sent in the body because a
  // filter tree does not fit sensibly in a query string.
  search: (body: ContactSearchRequest) =>
    api.post('/contacts/search', body),
  filterFields: () => api.get('/contacts/filter-fields')
}

// --- Contact custom fields (plan 01) ---

export type ContactFieldType = 'text' | 'number' | 'date' | 'dropdown' | 'email' | 'phone'

export interface ContactFieldOption {
  value: string
  label?: string
  color?: string
}

export interface ContactField {
  id: string
  key: string
  label: string
  description: string
  type: ContactFieldType
  options: ContactFieldOption[]
  validation: Record<string, unknown>
  is_system: boolean
  is_required: boolean
  /** What a new contact gets when nobody fills the field in. */
  default_value?: { value?: unknown } | null
  show_in_list: boolean
  show_in_chat_panel: boolean
  group_label: string
  position: number
  archived_at?: string
  created_at: string
  updated_at: string
}

/** One condition or nested group in a contact filter. */
export interface FilterNode {
  op?: 'and' | 'or'
  rules?: FilterNode[]
  field?: string
  operator?: string
  value?: unknown
}

/** A filterable field as the backend describes it, used to build the UI. */
export interface FilterFieldInfo {
  key: string
  label: string
  type: string
  operators: string[]
  options?: ContactFieldOption[]
  sortable: boolean
}

export interface ContactSearchRequest {
  filter?: FilterNode
  search?: string
  sort?: Array<{ field: string; dir: 'asc' | 'desc' }>
  page?: number
  limit?: number
  include?: Array<'fields' | 'unread'>
}

export interface ContactSearchRow {
  id: string
  phone_number: string
  profile_name: string
  whatsapp_account: string
  tags: string[]
  assigned_user_id?: string
  source?: string
  last_message_at?: string
  last_message_preview?: string
  marketing_opt_out: boolean
  fields?: Record<string, unknown>
  unread_count?: number
  created_at: string
}

export const contactFieldsService = {
  list: () => api.get('/contact-fields'),
  create: (data: Partial<ContactField>) => api.post('/contact-fields', data),
  update: (id: string, data: Partial<ContactField> & { archived?: boolean }) =>
    api.put(`/contact-fields/${id}`, data),
  delete: (id: string) => api.delete(`/contact-fields/${id}`),
  reorder: (ids: string[]) => api.put('/contact-fields/reorder', { ids }),
  /** Metadata keys an organization is already collecting, for promotion. */
  metadataKeys: () => api.get<{ keys: string[] }>('/contact-fields/metadata-keys'),
  /**
   * Turns a metadata key into a real field and copies the values across
   * (plan 01). The metadata is left in place: an integration is probably still
   * writing to it.
   */
  promoteMetadata: (metadataKey: string, field: Record<string, unknown>) =>
    api.post<{ field: ContactField; promoted: number; skipped: number }>(
      '/contact-fields/promote-metadata', { metadata_key: metadataKey, field }
    )
}

// --- Tasks (plan 04) ---

export interface Task {
  id: string
  contact_id: string
  contact_name?: string
  conversation_id?: string
  type_id: string
  type_key?: string
  type_label?: string
  title: string
  description: string
  owner_id: string
  priority: string
  status: string
  due_at: string
  all_day: boolean
  remind_at?: string
  overdue: boolean
  completed_at?: string
  source: string
  created_at: string
}

export interface TaskType {
  id: string
  key: string
  label: string
  icon: string
  color: string
  default_due_offset_minutes: number
  position: number
  /** Built-ins can be relabelled and reordered, never deleted or archived. */
  is_system?: boolean
  archived_at?: string | null
}

export const tasksService = {
  list: (params: { view?: string; status?: string; contact_id?: string; limit?: number; offset?: number } = {}) =>
    api.get<{
      tasks: Task[]
      total: number
      view: string
      /** Past their deadline. */
      overdue: number
      due_today: number
      /** overdue + due today — what the sidebar badge shows (plan 04). */
      due_count: number
    }>(`/tasks${toQuery(params)}`),
  create: (data: Record<string, any>) => api.post<{ task: Task }>('/tasks', data),
  complete: (id: string) => api.post<{ task: Task }>(`/tasks/${id}/complete`, {}),
  cancel: (id: string) => api.post<{ task: Task }>(`/tasks/${id}/cancel`, {}),
  reassign: (id: string, ownerId: string) =>
    api.post<{ task: Task }>(`/tasks/${id}/reassign`, { owner_id: ownerId }),
  types: () => api.get<{ task_types: TaskType[] }>('/task-types'),
  get: (id: string) => api.get<{ task: Task }>(`/tasks/${id}`),
  update: (id: string, data: Record<string, any>) => api.put<{ task: Task }>(`/tasks/${id}`, data),
  reopen: (id: string) => api.post<{ task: Task }>(`/tasks/${id}/reopen`, {}),
  delete: (id: string) => api.delete(`/tasks/${id}`),
  /**
   * One action across a selection (plan 04). Failures come back per task, so
   * one row somebody else just completed does not undo the other forty-nine.
   */
  bulk: (
    ids: string[],
    action: 'complete' | 'cancel' | 'reassign' | 'reschedule' | 'delete',
    payload: { owner_id?: string; due_at?: string } = {}
  ) =>
    api.post<{ applied: number; failed: number; failures: Record<string, string> }>(
      '/tasks/bulk', { ids, action, ...payload }
    )
}

/**
 * Task types are settings, not tasks (plan 04).
 *
 * The five built-ins describe a shop. A clinic books procedures and a lender
 * chases documents, and with no way to add those every other kind of work
 * became "Other" — which makes the type column useless for reporting the
 * moment anyone relies on it.
 */
export const taskTypesService = {
  list: (includeArchived = false) =>
    api.get<{ task_types: TaskType[] }>(
      `/task-types${includeArchived ? '?include_archived=true' : ''}`
    ),
  create: (data: Record<string, unknown>) => api.post<{ task_type: TaskType }>('/task-types', data),
  update: (id: string, data: Record<string, unknown>) =>
    api.put<{ task_type: TaskType }>(`/task-types/${id}`, data),
  reorder: (ids: string[]) => api.put('/task-types/reorder', { ids }),
  delete: (id: string) => api.delete(`/task-types/${id}`)
}

// --- Inbox and conversations (plan 03) ---

export type ConversationHandling = 'bot' | 'human' | 'handoff_pending' | 'none'

export interface InboxRow {
  id: string
  contact_id: string
  contact_name?: string
  contact_phone?: string
  status: string
  assignee_id?: string
  team_id?: string
  /** Who is dealing with it now: bot | human | handoff_pending | none. */
  handling: ConversationHandling
  /** Derived from `handling`; kept for callers written before it existed. */
  bot_active: boolean
  whatsapp_account?: string
  snoozed_until?: string | null
  opened_at: string
  last_message_at?: string | null
  last_message_preview?: string
  /** When the oldest unanswered customer message arrived, shown as "waiting 2h". */
  waiting_since?: string | null
  first_response_at?: string | null
  message_count: number
  reopened_count: number
}

export interface InboxCounts {
  mine: number
  unassigned: number
  bot: number
  /** Conversations with a customer message nobody has answered yet. */
  unanswered: number
  all: number
}

export const inboxService = {
  list: (params: { view?: string; status?: string; sort?: string; limit?: number; offset?: number } = {}) =>
    api.get<{ conversations: InboxRow[]; total: number }>(`/inbox${toQuery(params)}`),
  counts: () => api.get<InboxCounts>('/inbox/counts'),
  resolve: (contactId: string, reason?: string) =>
    api.post('/conversations/resolve', { contact_id: contactId, reason }),
  snooze: (contactId: string, until: string) =>
    api.post('/conversations/snooze', { contact_id: contactId, until }),
  assign: (contactId: string, assigneeId?: string, teamId?: string) =>
    api.post('/conversations/assign', { contact_id: contactId, assignee_id: assigneeId, team_id: teamId }),
  forContact: (contactId: string) => api.get(`/contacts/${contactId}/conversation`)
}

// --- Timeline (plan 02) ---

export interface TimelineItem {
  id: string
  type: string
  occurred_at: string
  actor: { type: string; id?: string; name?: string }
  summary: string
  data?: Record<string, any>
  group?: { count: number; from_customer: number; from: string; to: string }
}

export const conversationsService = {
  /** One contact's conversation history — the profile's "have we spoken before?" */
  forContact: (contactId: string) =>
    api.get<{ conversations: any[]; total: number }>(`/contacts/${contactId}/conversations`),
  /** One conversation by its own id, including resolved ones a link names. */
  get: (id: string) => api.get<{ conversation: any }>(`/conversations/${id}`),
  bulk: (
    ids: string[],
    action: 'resolve' | 'pending' | 'snooze' | 'assign',
    payload: { until?: string; assignee_id?: string | null; team_id?: string | null } = {}
  ) =>
    api.post<{ applied: number; failed: number; failures: Record<string, string> }>(
      '/conversations/bulk', { ids, action, ...payload }
    )
}

export const timelineService = {
  forContact: (
    contactId: string,
    params: {
      types?: string
      limit?: number
      before?: string
      /** Inclusive YYYY-MM-DD bounds, resolved in the organization's zone. */
      from?: string
      to?: string
    } = {}
  ) =>
    api.get<{ items: TimelineItem[]; next_before?: string }>(
      `/contacts/${contactId}/timeline${toQuery(params)}`
    )
}

// --- Segments (plan 05) ---

export interface Segment {
  id: string
  name: string
  description: string
  filter: FilterNode
  visibility: 'shared' | 'private'
  created_by_id?: string
  contact_count?: number
  counted_at?: string
  last_used_at?: string
  created_at: string
}

export const segmentsService = {
  list: (params: { search?: string; visibility?: string } = {}) =>
    api.get<{ segments: Segment[] }>(`/segments${toQuery(params)}`),
  get: (id: string) => api.get<{ segment: Segment }>(`/segments/${id}`),
  create: (data: Partial<Segment>) => api.post<{ segment: Segment }>('/segments', data),
  update: (id: string, data: Partial<Segment>) =>
    api.put<{ segment: Segment }>(`/segments/${id}`, data),
  delete: (id: string) => api.delete(`/segments/${id}`),
  count: (id: string) => api.post<{ count: number }>(`/segments/${id}/count`, {}),
  previewCount: (filter: FilterNode) =>
    api.post<{ count: number }>('/segments/preview-count', { filter }),
  contacts: (id: string, body: Record<string, any> = {}) =>
    api.post<{ contacts: ContactSearchRow[]; total: number; segment: Segment }>(
      `/segments/${id}/contacts`, body
    ),
  /**
   * Exports the segment's contacts as CSV (plan 05).
   *
   * Aimed by the segment rather than by filters reassembled in the export
   * dialog: the audience an organization defined once is the audience that
   * leaves the product.
   */
  export: (id: string) => api.post(`/segments/${id}/export`, {}, { responseType: 'text' })
}

// --- Campaign audience (plan 05) ---

export interface AudiencePreview {
  count: number
  /** Who is being left out and why, keyed by reason. */
  excluded: Record<string, number>
  sample: Array<{
    contact_id: string
    name: string
    phone_number: string
    preview: string
  }>
}

export const campaignAudienceService = {
  set: (campaignId: string, segmentId: string | null) =>
    api.put(`/campaigns/${campaignId}/audience`, {
      audience_type: segmentId ? 'segment' : 'list',
      segment_id: segmentId || undefined
    }),
  preview: (campaignId: string) =>
    api.post<AudiencePreview>(`/campaigns/${campaignId}/audience/preview`, {}),
  /**
   * Say where each template variable's value comes from (plan 05).
   *
   * Sent on the audience endpoint because it is the same decision: who this
   * goes to, and what it says to each of them.
   */
  setParamMappings: (
    campaignId: string,
    mappings: Record<string, { source: string; value: string; fallback: string }>
  ) => api.put(`/campaigns/${campaignId}/audience`, { param_mappings: mappings })
}

// --- Duplicates and merge (plan 06) ---

export interface DuplicateContactSummary {
  id: string
  profile_name: string
  phone_number: string
  tags: string[]
  source?: string
  created_at: string
  last_message_at?: string
  message_count: number
}

export interface DuplicateCandidate {
  id: string
  score: number
  reasons: string[]
  status: string
  detected_at: string
  contact_a: DuplicateContactSummary
  contact_b: DuplicateContactSummary
}

export const duplicatesService = {
  list: (limit = 50) => api.get<{ candidates: DuplicateCandidate[] }>(`/contacts/duplicates?limit=${limit}`),
  scan: () => api.post<{ found: number }>('/contacts/duplicates/scan', {}),
  dismiss: (id: string) => api.post(`/contacts/duplicates/${id}/dismiss`, {}),
  merge: (primaryId: string, secondaryId: string) =>
    api.post('/contacts/merge', { primary_id: primaryId, secondary_id: secondaryId }),
  forContact: (contactId: string) => api.get(`/contacts/${contactId}/merges`)
}

// --- Pipelines and deals (plan 07) ---

export interface PipelineStage {
  id: string
  pipeline_id: string
  name: string
  position: number
  stage_type: 'open' | 'won' | 'lost'
  probability: number
  color: string
  rotting_days: number
}

export interface Pipeline {
  id: string
  name: string
  object_label_singular: string
  object_label_plural: string
  currency: string
  is_default: boolean
  position: number
  archived_at?: string | null
  stages: PipelineStage[]
}

export interface Deal {
  id: string
  pipeline_id: string
  stage_id: string
  stage_name?: string
  contact_id: string
  contact_name?: string
  contact_phone?: string
  conversation_id?: string
  title: string
  value: number
  currency: string
  owner_id?: string
  expected_close_date?: string | null
  status: 'open' | 'won' | 'lost'
  lost_reason?: string
  stage_entered_at: string
  board_position: string
  closed_at?: string | null
  rotting: boolean
  created_at: string
}

export interface BoardColumn {
  stage: PipelineStage
  count: number
  total_value: number
  weighted_value: number
  deals: Deal[]
  has_more: boolean
}

export interface DealHistoryEntry {
  id: string
  deal_id: string
  from_stage_id?: string
  to_stage_id: string
  from_stage_name?: string
  to_stage_name: string
  moved_by_id?: string
  duration_seconds: number
  created_at: string
}

export interface BoardFilters {
  owner_id?: string
  close_from?: string
  close_to?: string
  search?: string
  status?: string
  limit?: number
}

export const pipelinesService = {
  list: () => api.get<{ pipelines: Pipeline[] }>('/pipelines'),
  get: (id: string) => api.get<{ pipeline: Pipeline }>(`/pipelines/${id}`),
  create: (data: Partial<Pipeline>) => api.post<{ pipeline: Pipeline }>('/pipelines', data),
  update: (id: string, data: Partial<Pipeline>) =>
    api.put<{ pipeline: Pipeline }>(`/pipelines/${id}`, data),
  delete: (id: string) => api.delete(`/pipelines/${id}`),
  archive: (id: string) => api.delete(`/pipelines/${id}?archive=true`),

  board: (id: string, filters: BoardFilters = {}) =>
    api.get<{ pipeline: Pipeline; columns: BoardColumn[] }>(
      `/pipelines/${id}/board${toQuery(filters)}`
    ),
  stageDeals: (id: string, stageId: string, cursor: string, filters: BoardFilters = {}) =>
    api.get<{ deals: Deal[]; has_more: boolean; next_cursor: string }>(
      `/pipelines/${id}/stages/${stageId}/deals${toQuery({ ...filters, cursor })}`
    ),

  createStage: (pipelineId: string, data: Partial<PipelineStage>) =>
    api.post<{ stage: PipelineStage }>(`/pipelines/${pipelineId}/stages`, data),
  updateStage: (stageId: string, data: Partial<PipelineStage>) =>
    api.put<{ stage: PipelineStage }>(`/pipeline-stages/${stageId}`, data),
  reorderStages: (pipelineId: string, stageIds: string[]) =>
    api.put<{ pipeline: Pipeline }>(`/pipelines/${pipelineId}/stages/reorder`, { stage_ids: stageIds }),
  deleteStage: (stageId: string, moveDealsTo?: string) =>
    api.delete(`/pipeline-stages/${stageId}${moveDealsTo ? `?move_deals_to=${moveDealsTo}` : ''}`)
}

export const dealsService = {
  list: (params: { pipeline_id?: string; contact_id?: string; owner_id?: string; status?: string } = {}) =>
    api.get<{ deals: Deal[]; total: number }>(`/deals${toQuery(params)}`),
  get: (id: string) => api.get<{ deal: Deal }>(`/deals/${id}`),
  create: (data: Partial<Deal>) => api.post<{ deal: Deal }>('/deals', data),
  update: (id: string, data: Partial<Deal> & { clear_close_date?: boolean }) =>
    api.put<{ deal: Deal }>(`/deals/${id}`, data),
  delete: (id: string) => api.delete(`/deals/${id}`),
  move: (id: string, data: { stage_id: string; before_id?: string; after_id?: string; lost_reason?: string }) =>
    api.post<{ deal: Deal }>(`/deals/${id}/move`, data),
  history: (id: string) => api.get<{ history: DealHistoryEntry[] }>(`/deals/${id}/history`),
  forContact: (contactId: string, status = 'all') =>
    api.get<{ deals: Deal[] }>(`/contacts/${contactId}/deals?status=${status}`)
}

/** toQuery drops empty values so the URL only carries filters that are set. */
function toQuery(params: object): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    search.set(key, String(value))
  }
  const query = search.toString()
  return query ? `?${query}` : ''
}

// --- Automations (plan 08) ---

export interface AutomationActionSpec {
  id: string
  type: string
  config: Record<string, any>
  continue_on_error?: boolean
}

export interface AutomationRunPolicy {
  once_per_contact: boolean
  cooldown_minutes: number
  max_runs_per_hour: number
}

export interface Automation {
  id: string
  name: string
  description: string
  enabled: boolean
  trigger_type: string
  trigger_config: Record<string, any>
  contact_filter?: FilterNode | null
  actions: AutomationActionSpec[]
  run_policy: AutomationRunPolicy
  last_run_at?: string | null
  run_count: number
  error_count: number
  created_at: string
  stats?: { runs_24h: number; failures_24h: number }
}

export interface AutomationActionResult {
  id: string
  type: string
  status: 'succeeded' | 'failed' | 'skipped'
  error?: string
  output?: Record<string, any>
}

export interface AutomationRun {
  id: string
  rule_id: string
  event_id: string
  event_type: string
  contact_id?: string
  status: 'succeeded' | 'partially_failed' | 'failed' | 'skipped'
  skip_reason?: string
  depth: number
  action_results: { list?: AutomationActionResult[] }
  dry_run: boolean
  started_at: string
  finished_at?: string
}

export interface AutomationTrigger {
  type: string
  kind: 'event' | 'time'
  group: string
  config_keys: string[]
}

export interface AutomationCatalog {
  triggers: AutomationTrigger[]
  actions: string[]
  limits: {
    max_actions_per_rule: number
    max_rules_per_org: number
    max_depth: number
  }
}

export const automationsService = {
  list: () => api.get<{ automations: Automation[] }>('/automations'),
  get: (id: string) => api.get<{ automation: Automation }>(`/automations/${id}`),
  create: (data: Partial<Automation>) =>
    api.post<{ automation: Automation }>('/automations', data),
  update: (id: string, data: Partial<Automation>) =>
    api.put<{ automation: Automation }>(`/automations/${id}`, data),
  delete: (id: string) => api.delete(`/automations/${id}`),
  enable: (id: string) => api.post<{ automation: Automation }>(`/automations/${id}/enable`, {}),
  disable: (id: string) => api.post<{ automation: Automation }>(`/automations/${id}/disable`, {}),
  test: (id: string, contactId: string, eventData: Record<string, any> = {}) =>
    api.post<{ run: AutomationRun }>(`/automations/${id}/test`, {
      contact_id: contactId,
      event_data: eventData
    }),
  runs: (id: string, params: { status?: string; contact_id?: string; limit?: number } = {}) =>
    api.get<{ runs: AutomationRun[] }>(`/automations/${id}/runs${toQuery(params)}`),
  catalog: () => api.get<AutomationCatalog>('/automations/catalog'),
  forContact: (contactId: string) =>
    api.get<{ runs: AutomationRun[] }>(`/contacts/${contactId}/automation-runs`)
}

// --- CRM reports (plan 09) ---

export interface ReportBucket {
  period: string
  series: string
  count: number
}

export interface SourceRow {
  source: string
  contacts: number
  share: number
  became_customer: number
}

export interface ContactsBySourceReport {
  buckets: ReportBucket[]
  totals: SourceRow[]
  total: number
}

export interface FunnelStep {
  key: string
  label: string
  reached: number
  conversion: number
  median_days_from_previous?: number
}

export interface LifecycleFunnelReport {
  steps: FunnelStep[]
  note: string
}

export interface PipelineFunnelReport {
  steps: FunnelStep[]
  win_rate: number
  won: number
  lost: number
  lost_reasons: Array<{ reason: string; count: number; value: number }>
}

export interface PipelineForecastReport {
  months: Array<{ month: string; deals: number; value: number; weighted_value: number }>
  overdue: number
  overdue_value: number
  undated: number
  undated_value: number
}

export interface AgentPerformanceRow {
  user_id: string
  name: string
  handled: number
  first_response_median_seconds?: number
  first_response_p90_seconds?: number
  resolution_median_seconds?: number
  resolution_p90_seconds?: number
  resolved: number
  reopened_rate: number
}

export interface AgentPerformanceReport {
  rows: AgentPerformanceRow[]
  note: string
}

export interface TaskAgentRow {
  user_id: string
  name: string
  open: number
  overdue: number
  due_today: number
  completed: number
  on_time_rate: number
  median_late_seconds?: number
}

export interface ReportRange {
  from?: string
  to?: string
  interval?: string
  team_id?: string
  pipeline_id?: string
}

/** R6: did the campaign work, as opposed to arrive (plan 09)? */
export interface CampaignReplyRow {
  campaign_id: string
  name: string
  started_at: string
  recipients: number
  delivered: number
  replied: number
  /** Replies over **delivered**: a message that never arrived cannot be replied to. */
  reply_rate: number
}

export interface CampaignRepliesReport {
  rows: CampaignReplyRow[]
  recipients: number
  delivered: number
  replied: number
  reply_rate: number
  counting_rule: string
}

export const reportsService = {
  contactsBySource: (params: ReportRange = {}) =>
    api.get<ContactsBySourceReport>(`/reports/contacts-by-source${toQuery(params)}`),
  lifecycleFunnel: (params: ReportRange = {}) =>
    api.get<LifecycleFunnelReport>(`/reports/lifecycle-funnel${toQuery(params)}`),
  pipelineFunnel: (params: ReportRange = {}) =>
    api.get<PipelineFunnelReport>(`/reports/pipeline-funnel${toQuery(params)}`),
  pipelineForecast: (params: ReportRange & { months?: number } = {}) =>
    api.get<PipelineForecastReport>(`/reports/pipeline-forecast${toQuery(params)}`),
  tasksByAgent: (params: ReportRange = {}) =>
    api.get<{ rows: TaskAgentRow[] }>(`/reports/tasks-by-agent${toQuery(params)}`),
  agentPerformance: (params: ReportRange = {}) =>
    api.get<AgentPerformanceReport>(`/reports/agent-performance${toQuery(params)}`),
  campaignReplies: (params: ReportRange = {}) =>
    api.get<CampaignRepliesReport>(`/reports/campaign-replies${toQuery(params)}`),

  /** The CSV URL, so the browser downloads it rather than the app buffering it. */
  exportUrl: (key: string, params: ReportRange = {}) =>
    `/api/reports/${key}/export.csv${toQuery(params)}`
}

// Generic Import/Export Service
export interface ExportColumn {
  key: string
  label: string
}

export interface ExportConfig {
  table: string
  columns: ExportColumn[]
  default_columns: string[]
}

export interface ImportConfig {
  table: string
  required_columns: ExportColumn[]
  optional_columns: ExportColumn[]
  unique_column: string
}

export interface ImportResult {
  created: number
  updated: number
  skipped: number
  errors: number
  messages: string[]
  /** Rows collapsed because another row in the same file named the same person. */
  merged_in_file?: number
  /** Records created alongside an existing one and queued for review. */
  flagged?: number
}

/** What an import does with a row that names somebody already on file (plan 06). */
export type OnMatch = 'skip' | 'update' | 'create_anyway'

export const dataService = {
  // Get export configuration for a table
  getExportConfig: (table: string) => api.get<ExportConfig>(`/export/${table}/config`),

  // Get import configuration for a table
  getImportConfig: (table: string) => api.get<ImportConfig>(`/import/${table}/config`),

  // Export data - returns CSV blob
  exportData: async (table: string, columns?: string[], filters?: Record<string, string>) => {
    const response = await api.post('/export', { table, columns, filters }, {
      responseType: 'blob'
    })
    return response
  },

  // Import data from CSV file
  importData: (table: string, file: File, onMatch?: OnMatch, columnMapping?: Record<string, string>) => {
    const formData = new FormData()
    formData.append('file', file)
    formData.append('table', table)
    if (onMatch) {
      formData.append('on_match', onMatch)
      // The older flag, for any server that has not been updated yet.
      if (onMatch === 'update') formData.append('update_on_duplicate', 'true')
    }
    if (columnMapping) {
      formData.append('column_mapping', JSON.stringify(columnMapping))
    }
    return api.post<ImportResult>('/import', formData, {
      headers: { 'Content-Type': 'multipart/form-data' }
    })
  }
}

export const messagesService = {
  list: (
    contactId: string,
    params?: {
      page?: number
      limit?: number
      before_id?: string
      account?: string
      /**
       * Centre the page on one message (plan 02).
       *
       * Clicking an exchange on a contact's timeline has to land on the
       * messages it describes. Paging back from the newest until the right one
       * appears is not a substitute: on a contact with fifty thousand messages
       * the thing you clicked is two hundred requests away.
       */
      around?: string
    }
  ) => api.get(`/contacts/${contactId}/messages`, { params }),
  send: (
    contactId: string,
    data: {
      type: string
      content: any
      reply_to_message_id?: string
      whatsapp_account?: string
      // Interactive payload. Mirrors backend InteractiveContent.
      interactive?: {
        type: 'button' | 'cta_url' | 'list' | 'voice_call' | 'flow'
        body: string
        buttons?: Array<{ id: string; title: string }>
        button_text?: string
        url?: string
        // voice_call only
        display_text?: string
        ttl_minutes?: number
        // flow only
        flow_id?: string
        first_screen?: string
        header?: string
      }
    },
  ) => api.post(`/contacts/${contactId}/messages`, data),
  sendTemplate: (contactId: string, data: { template_name: string; template_params?: Record<string, string>; header_params?: Record<string, string>; button_params?: Record<string, string>; account_name?: string }, headerFile?: File) => {
    if (headerFile) {
      const formData = new FormData()
      formData.append('contact_id', contactId)
      formData.append('template_name', data.template_name)
      if (data.template_params) {
        formData.append('template_params', JSON.stringify(data.template_params))
      }
      if (data.header_params) {
        formData.append('header_params', JSON.stringify(data.header_params))
      }
      if (data.button_params) {
        formData.append('button_params', JSON.stringify(data.button_params))
      }
      if (data.account_name) {
        formData.append('account_name', data.account_name)
      }
      formData.append('header_file', headerFile)
      return api.post('/messages/template', formData, {
        headers: { 'Content-Type': 'multipart/form-data' }
      })
    }
    return api.post('/messages/template', { contact_id: contactId, ...data })
  },
  sendReaction: (contactId: string, messageId: string, emoji: string) =>
    api.post(`/contacts/${contactId}/messages/${messageId}/reaction`, { emoji })
}

export const templatesService = {
  list: (params?: { status?: string; category?: string; account?: string; search?: string; page?: number; limit?: number }) =>
    api.get<{ templates: any[]; total?: number }>('/templates', { params }),
  get: (id: string) => api.get(`/templates/${id}`),
  uploadMedia: (accountName: string, file: File) => {
    const formData = new FormData()
    formData.append('file', file)
    formData.append('account', accountName)
    return api.post('/templates/upload-media', formData, {
      headers: { 'Content-Type': 'multipart/form-data' }
    })
  }
}

export const flowsService = {
  list: (params?: { account?: string; search?: string; page?: number; limit?: number }) =>
    api.get<{ flows: any[]; total?: number }>('/flows', { params }),
  create: (data: any) => api.post('/flows', data),
  update: (id: string, data: any) => api.put(`/flows/${id}`, data),
  delete: (id: string) => api.delete(`/flows/${id}`),
  saveToMeta: (id: string) => api.post(`/flows/${id}/save-to-meta`),
  publish: (id: string) => api.post(`/flows/${id}/publish`),
  duplicate: (id: string) => api.post(`/flows/${id}/duplicate`),
  sync: (whatsappAccount: string) => api.post('/flows/sync', { whatsapp_account: whatsappAccount })
}

export const campaignsService = {
  list: (params?: { status?: string; from?: string; to?: string; search?: string; page?: number; limit?: number }) =>
    api.get('/campaigns', { params }),
  get: (id: string) => api.get(`/campaigns/${id}`),
  create: (data: any) => api.post('/campaigns', data),
  update: (id: string, data: any) => api.put(`/campaigns/${id}`, data),
  delete: (id: string) => api.delete(`/campaigns/${id}`),
  start: (id: string) => api.post(`/campaigns/${id}/start`),
  pause: (id: string) => api.post(`/campaigns/${id}/pause`),
  cancel: (id: string) => api.post(`/campaigns/${id}/cancel`),
  retryFailed: (id: string) => api.post(`/campaigns/${id}/retry-failed`),
  // Recipients
  getRecipients: (id: string, params?: { page?: number; limit?: number; status?: string }) =>
    api.get(`/campaigns/${id}/recipients`, { params }),
  addRecipients: (id: string, recipients: Array<{ phone_number: string; recipient_name?: string; template_params?: Record<string, any> }>) =>
    api.post(`/campaigns/${id}/recipients/import`, { recipients }),
  deleteRecipient: (campaignId: string, recipientId: string) =>
    api.delete(`/campaigns/${campaignId}/recipients/${recipientId}`),
  // Media
  uploadMedia: (campaignId: string, file: File) => {
    const formData = new FormData()
    formData.append('file', file)
    return api.post(`/campaigns/${campaignId}/media`, formData, {
      headers: { 'Content-Type': 'multipart/form-data' }
    })
  },
  getMedia: (campaignId: string) =>
    api.get(`/campaigns/${campaignId}/media`, { responseType: 'arraybuffer' })
}

export const chatbotService = {
  // Settings
  getSettings: () => api.get('/chatbot/settings'),
  updateSettings: (data: any) => api.put('/chatbot/settings', data),

  // Keywords
  listKeywords: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ rules: any[]; total?: number }>('/chatbot/keywords', { params }),
  getKeyword: (id: string) => api.get(`/chatbot/keywords/${id}`),
  createKeyword: (data: any) => api.post('/chatbot/keywords', data),
  updateKeyword: (id: string, data: any) => api.put(`/chatbot/keywords/${id}`, data),
  deleteKeyword: (id: string) => api.delete(`/chatbot/keywords/${id}`),

  // Flows
  listFlows: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ flows: any[]; total?: number }>('/chatbot/flows', { params }),
  getFlow: (id: string) => api.get(`/chatbot/flows/${id}`),
  createFlow: (data: any) => api.post('/chatbot/flows', data),
  updateFlow: (id: string, data: any) => api.put(`/chatbot/flows/${id}`, data),
  deleteFlow: (id: string) => api.delete(`/chatbot/flows/${id}`),

  // AI Contexts
  listAIContexts: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ contexts: any[]; total?: number }>('/chatbot/ai-contexts', { params }),
  getAIContext: (id: string) => api.get(`/chatbot/ai-contexts/${id}`),
  createAIContext: (data: any) => api.post('/chatbot/ai-contexts', data),
  updateAIContext: (id: string, data: any) => api.put(`/chatbot/ai-contexts/${id}`, data),
  deleteAIContext: (id: string) => api.delete(`/chatbot/ai-contexts/${id}`),

  // Agent Transfers
  listTransfers: (params?: {
    status?: string
    agent_id?: string
    team_id?: string
    limit?: number
    offset?: number
    include?: string // 'all' | 'contact,agent,team' etc.
  }) => api.get('/chatbot/transfers', { params }),
  createTransfer: (data: {
    contact_id: string
    whatsapp_account: string
    agent_id?: string
    notes?: string
    source?: string
  }) => api.post('/chatbot/transfers', data),
  pickNextTransfer: () => api.post('/chatbot/transfers/pick'),
  resumeTransfer: (id: string) => api.put(`/chatbot/transfers/${id}/resume`),
  assignTransfer: (id: string, agentId: string | null, teamId?: string | null) =>
    api.put(`/chatbot/transfers/${id}/assign`, { agent_id: agentId, team_id: teamId })
}

export interface CannedResponseButton {
  id: string
  title: string
  type?: 'reply' | 'url' | 'phone' | 'voice_call' | 'flow'
  url?: string
  phone_number?: string
  ttl_minutes?: number
  flow_id?: string
  screen?: string
}

export interface CannedResponse {
  id: string
  name: string
  shortcut: string
  content: string
  category: string
  is_active: boolean
  usage_count: number
  buttons?: CannedResponseButton[]
  created_at: string
  updated_at: string
}

interface CannedResponseUpsertPayload {
  name?: string
  shortcut?: string
  content?: string
  category?: string
  is_active?: boolean
  buttons?: CannedResponseButton[]
}

export const cannedResponsesService = {
  list: (params?: { category?: string; search?: string; active_only?: string; page?: number; limit?: number }) =>
    api.get<{ canned_responses: CannedResponse[]; total?: number }>('/canned-responses', { params }),
  get: (id: string) => api.get<CannedResponse>(`/canned-responses/${id}`),
  create: (data: CannedResponseUpsertPayload & { name: string; content: string }) =>
    api.post('/canned-responses', data),
  update: (id: string, data: CannedResponseUpsertPayload) =>
    api.put(`/canned-responses/${id}`, data),
  delete: (id: string) => api.delete(`/canned-responses/${id}`),
  use: (id: string) => api.post(`/canned-responses/${id}/use`),
  /**
   * Render a canned response against a contact on the server (plan 10, S6).
   *
   * The chat used to substitute tokens in the browser against a hardcoded list
   * of four names, so anything else — an owner, a custom field, the team —
   * reached the customer as a literal `{{...}}`.
   */
  resolve: (id: string, data: { contact_id?: string; params?: Record<string, string> }) =>
    api.post<{ content: string; buttons?: Record<string, any>[] }>(
      `/canned-responses/${id}/resolve`,
      data
    )
}

/** Template variables offered by a given editing context (plan 10, S6). */
export interface TemplateVariable {
  path: string
  label: string
  group: string
  example?: string
  dynamic?: boolean
}

export const variablesService = {
  list: (context: string) =>
    api.get<{ context: string; variables: TemplateVariable[] }>('/variables', {
      params: { context }
    })
}

export const agentAnalyticsService = {
  getSummary: (params?: { from?: string; to?: string; agent_id?: string }) =>
    api.get('/analytics/agents', { params })
}

// Meta WhatsApp Analytics Types
export type MetaAnalyticsType =
  | 'analytics'
  | 'conversation_analytics'
  | 'pricing_analytics'
  | 'template_analytics'
  | 'call_analytics'

export type MetaGranularity = 'HALF_HOUR' | 'DAY' | 'MONTH'

export interface MetaAnalyticsAccount {
  id: string
  name: string
  phone_id: string
}

export interface MetaMessagingDataPoint {
  start: number
  end: number
  sent: number
  delivered: number
}

interface MetaConversationDataPoint {
  start: number
  end: number
  conversation: number
  conversation_type: string
  conversation_direction: string
  conversation_category: string
  cost: number
}

export interface MetaPricingDataPoint {
  start: number
  end: number
  volume: number
  cost: number
  country?: string              // Country code (IN, US, etc.)
  pricing_type?: string         // FREE_CUSTOMER_SERVICE, FREE_ENTRY_POINT, REGULAR
  pricing_category?: string     // MARKETING, UTILITY, AUTHENTICATION, SERVICE, etc.
  tier?: string                 // Pricing tier
}

interface MetaTemplateCostItem {
  type: string    // amount_spent, cost_per_delivered, cost_per_url_button_click
  value?: number  // The cost value
}

interface MetaTemplateClickItem {
  type: string           // quick_reply_button, unique_url_button
  button_content: string // The button text
  count: number          // Number of clicks
}

export interface MetaTemplateDataPoint {
  start: number
  end: number
  template_id: string
  sent: number
  delivered: number
  read: number
  replied?: number
  clicked?: MetaTemplateClickItem[]  // Array of button click details
  cost?: MetaTemplateCostItem[]
}

export interface MetaCallDataPoint {
  start: number
  end: number
  count: number
  cost: number
  average_duration: number
  direction?: string // USER_INITIATED or BUSINESS_INITIATED
}

interface MetaAnalyticsData {
  id: string
  analytics?: {
    granularity: string
    data_points: MetaMessagingDataPoint[]
  }
  conversation_analytics?: {
    granularity: string
    data_points: MetaConversationDataPoint[]
  }
  pricing_analytics?: {
    granularity: string
    data_points: MetaPricingDataPoint[]
  }
  template_analytics?: {
    granularity: string
    data_points: MetaTemplateDataPoint[]
  }
  call_analytics?: {
    granularity: string
    data_points: MetaCallDataPoint[]
  }
}

export interface MetaAnalyticsResponse {
  account_id: string
  account_name: string
  data: MetaAnalyticsData | null
  template_names?: Record<string, string> // meta_template_id -> template name
  currency?: string // ISO 4217 code the WABA is billed in, from Meta
}

export const metaAnalyticsService = {
  get: (params: {
    account_id?: string
    analytics_type: MetaAnalyticsType
    start: string
    end: string
    granularity?: MetaGranularity
    template_ids?: string
  }) => api.get<{ accounts: MetaAnalyticsResponse[]; cached: boolean }>('/analytics/meta', { params }),

  getAccounts: () => api.get<{ accounts: MetaAnalyticsAccount[] }>('/analytics/meta/accounts'),

  refresh: () => api.post('/analytics/meta/refresh')
}

// Dashboard Widgets (customizable analytics)
export interface DashboardWidget {
  id: string
  name: string
  description: string
  data_source: string
  metric: string
  field: string
  filters: Array<{ field: string; operator: string; value: string }>
  display_type: string
  chart_type: string
  group_by_field: string
  show_change: boolean
  color: string
  size: string
  display_order: number
  grid_x: number
  grid_y: number
  grid_w: number
  grid_h: number
  config: Record<string, any>
  is_shared: boolean
  is_default: boolean
  is_owner: boolean
  created_by: string
  created_at: string
  updated_at: string
}

export interface WidgetData {
  widget_id: string
  value: number
  change: number
  prev_value: number
  chart_data: Array<{ label: string; value: number }>
  data_points: Array<{ label: string; value: number; color?: string }>
  grouped_series?: {
    labels: string[]
    datasets: Array<{ label: string; data: number[] }>
  }
  table_rows?: Array<{
    id: string
    label: string
    sub_label: string
    status: string
    direction?: string
    created_at: string
  }>
}

interface DataSourceInfo {
  name: string
  label: string
  fields: string[]
}

export interface LayoutItem {
  id: string
  grid_x: number
  grid_y: number
  grid_w: number
  grid_h: number
}

export const widgetsService = {
  list: () => api.get<{ widgets: DashboardWidget[] }>('/widgets'),
  create: (data: {
    name: string
    description?: string
    data_source: string
    metric: string
    field?: string
    filters?: Array<{ field: string; operator: string; value: string }>
    display_type?: string
    chart_type?: string
    group_by_field?: string
    show_change?: boolean
    color?: string
    size?: string
    config?: Record<string, any>
    is_shared?: boolean
  }) => api.post<DashboardWidget>('/widgets', data),
  update: (id: string, data: Partial<{
    name: string
    description: string
    data_source: string
    metric: string
    field: string
    filters: Array<{ field: string; operator: string; value: string }>
    display_type: string
    chart_type: string
    group_by_field: string
    show_change: boolean
    color: string
    size: string
    config: Record<string, any>
    is_shared: boolean
  }>) => api.put<DashboardWidget>(`/widgets/${id}`, data),
  delete: (id: string) => api.delete(`/widgets/${id}`),
  getAllData: (params?: { from?: string; to?: string }) =>
    api.get<{ data: Record<string, WidgetData> }>('/widgets/data', { params }),
  getDataSources: () => api.get<{
    data_sources: DataSourceInfo[]
    metrics: string[]
    display_types: string[]
    operators: Array<{ value: string; label: string }>
  }>('/widgets/data-sources'),
  saveLayout: (layout: LayoutItem[]) =>
    api.post('/widgets/layout', { layout })
}

export const organizationService = {
  getSettings: () => api.get('/org/settings'),
  updateSettings: (data: {
    mask_phone_numbers?: boolean
    marketing_frequency_cap_hours?: number
    timezone?: string
    date_format?: string
    name?: string
    calling_enabled?: boolean
    max_call_duration?: number
    transfer_timeout_secs?: number
    hold_music_file?: string
    ringback_file?: string
    meta_app_id?: string
    meta_config_id?: string
    meta_app_secret?: string
    /** Conversation lifecycle rules (plan 03). */
    inbox?: {
      reopen_window_hours: number
      auto_resolve_idle_hours: number
      pending_timeout_hours: number
      auto_pending_on_agent_reply: boolean
    }
  }) => api.put('/org/settings', data),
  uploadOrgAudio: (file: File, type: 'hold_music' | 'ringback') => {
    const formData = new FormData()
    formData.append('file', file)
    return api.post(`/org/audio?type=${type}`, formData, {
      headers: { 'Content-Type': 'multipart/form-data' }
    })
  }
}

// Organizations
export interface Organization {
  id: string
  name: string
  slug?: string
  created_at: string
}

export const organizationsService = {
  list: () => api.get<{ organizations: Organization[] }>('/organizations'),
  // The current org, including which optional modules it uses (plan 07).
  current: () =>
    api.get<Organization & { modules?: Record<string, boolean> }>('/organizations/current'),
  create: (data: { name: string }) => api.post('/organizations', data),
  // Members
  addMember: (data: { user_id?: string; email?: string; role_id?: string }) =>
    api.post('/organizations/members', data),
}

export interface Webhook {
  id: string
  name: string
  url: string
  events: string[]
  headers: Record<string, string>
  is_active: boolean
  has_secret: boolean
  created_at: string
  updated_at: string
}

export interface WebhookEvent {
  value: string
  label: string
  description: string
}

export interface Team {
  id: string
  name: string
  description: string
  assignment_strategy: 'round_robin' | 'load_balanced' | 'manual'
  per_agent_timeout_secs: number
  is_active: boolean
  member_count: number
  members?: TeamMember[]
  created_by_id?: string
  created_by_name?: string
  updated_by_id?: string
  updated_by_name?: string
  created_at: string
  updated_at: string
}

export interface TeamMember {
  id: string
  team_id?: string
  user_id: string
  role: 'manager' | 'agent'
  last_assigned_at: string | null
  // Flat structure from API
  full_name: string
  email: string
  is_available: boolean
  // Optional nested user for local additions
  user?: {
    id: string
    full_name: string
    email: string
    is_available: boolean
  }
}

export const teamsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ teams: Team[] }>('/teams', { params }),
  get: (id: string) => api.get<{ team: Team }>(`/teams/${id}`),
  create: (data: {
    name: string
    description?: string
    assignment_strategy?: 'round_robin' | 'load_balanced' | 'manual'
    per_agent_timeout_secs?: number
  }) => api.post<{ team: Team }>('/teams', data),
  update: (id: string, data: {
    name?: string
    description?: string
    assignment_strategy?: 'round_robin' | 'load_balanced' | 'manual'
    per_agent_timeout_secs?: number
    is_active?: boolean
  }) => api.put<{ team: Team }>(`/teams/${id}`, data),
  delete: (id: string) => api.delete(`/teams/${id}`),
  // Members
  listMembers: (teamId: string) => api.get<{ members: TeamMember[] }>(`/teams/${teamId}/members`),
  addMember: (teamId: string, data: { user_id: string; role?: 'manager' | 'agent' }) =>
    api.post<{ member: TeamMember }>(`/teams/${teamId}/members`, data),
  removeMember: (teamId: string, userId: string) =>
    api.delete(`/teams/${teamId}/members/${userId}`)
}

// Audit Logs
export interface AuditLogChange {
  field: string
  old_value: any
  new_value: any
}

export interface AuditLogEntry {
  id: string
  resource_type: string
  resource_id: string
  user_id: string
  user_name: string
  action: 'created' | 'updated' | 'deleted'
  changes: AuditLogChange[]
  created_at: string
}

export interface AuditResourceType {
  value: string
  label: string
  group: string
}

export interface AuditActionOption {
  value: string
  label: string
}

export const auditLogsService = {
  get: (id: string) =>
    api.get<AuditLogEntry>(`/audit-logs/${id}`),
  list: (params?: {
    resource_type?: string
    resource_id?: string
    user_id?: string
    action?: string
    from?: string
    to?: string
    page?: number
    limit?: number
  }) =>
    api.get<{ audit_logs: AuditLogEntry[]; total: number }>('/audit-logs', { params }),
  // The filter options come from the server (plan 10, S9). The picker used to
  // be a hardcoded list here and silently offered a third of what the audit
  // log actually records.
  catalog: () =>
    api.get<{ resource_types: AuditResourceType[]; actions: AuditActionOption[] }>(
      '/audit-logs/catalog',
    ),
}

export const webhooksService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ webhooks: Webhook[]; available_events: WebhookEvent[]; total?: number }>('/webhooks', { params }),
  get: (id: string) => api.get<Webhook>(`/webhooks/${id}`),
  create: (data: {
    name: string
    url: string
    events: string[]
    headers?: Record<string, string>
    secret?: string
  }) => api.post<Webhook>('/webhooks', data),
  update: (id: string, data: {
    name?: string
    url?: string
    events?: string[]
    headers?: Record<string, string>
    secret?: string
    is_active?: boolean
  }) => api.put<Webhook>(`/webhooks/${id}`, data),
  delete: (id: string) => api.delete(`/webhooks/${id}`),
  test: (id: string) => api.post(`/webhooks/${id}/test`)
}

export interface CustomAction {
  id: string
  name: string
  icon: string
  action_type: 'webhook' | 'url' | 'javascript'
  config: {
    url?: string
    method?: string
    headers?: Record<string, string>
    body?: string
    open_in_new_tab?: boolean
    code?: string
  }
  is_active: boolean
  display_order: number
  created_at: string
  updated_at: string
}

export interface ActionResult {
  success: boolean
  message?: string
  redirect_url?: string
  clipboard?: string
  toast?: {
    message: string
    type: 'success' | 'error' | 'info' | 'warning'
  }
  data?: Record<string, any>
}

export const customActionsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ custom_actions: CustomAction[]; total?: number }>('/custom-actions', { params }),
  get: (id: string) => api.get<CustomAction>(`/custom-actions/${id}`),
  create: (data: {
    name: string
    icon?: string
    action_type: 'webhook' | 'url' | 'javascript'
    config: Record<string, any>
    is_active?: boolean
    display_order?: number
  }) => api.post<CustomAction>('/custom-actions', data),
  update: (id: string, data: {
    name?: string
    icon?: string
    action_type?: 'webhook' | 'url' | 'javascript'
    config?: Record<string, any>
    is_active?: boolean
    display_order?: number
  }) => api.put<CustomAction>(`/custom-actions/${id}`, data),
  delete: (id: string) => api.delete(`/custom-actions/${id}`),
  execute: (id: string, contactId: string) =>
    api.post<ActionResult>(`/custom-actions/${id}/execute`, { contact_id: contactId })
}

// Roles and Permissions
export interface Permission {
  id: string
  resource: string
  action: string
  description: string
  /** The area of the product this permission belongs to (inbox, crm, admin…). */
  group: string
  key: string // "resource:action"
}

export interface Role {
  id: string
  name: string
  description: string
  is_system: boolean
  is_default: boolean
  permissions: string[] // ["resource:action", ...]
  user_count: number
  created_at: string
  updated_at: string
}

export const rolesService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ roles: Role[] }>('/roles', { params }),
  get: (id: string) => api.get<Role>(`/roles/${id}`),
  create: (data: { name: string; description?: string; is_default?: boolean; permissions: string[] }) =>
    api.post<Role>('/roles', data),
  update: (id: string, data: { name?: string; description?: string; is_default?: boolean; permissions?: string[] }) =>
    api.put<Role>(`/roles/${id}`, data),
  delete: (id: string) => api.delete(`/roles/${id}`)
}

export const permissionsService = {
  list: () => api.get<{ permissions: Permission[] }>('/permissions')
}

// Tags
export interface Tag {
  name: string
  color: string
  created_at: string
  updated_at: string
}

export const tagsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ tags: Tag[]; total?: number; page?: number; limit?: number }>('/tags', { params }),
  create: (data: { name: string; color?: string }) =>
    api.post<Tag>('/tags', data),
  update: (name: string, data: { name?: string; color?: string }) =>
    api.put<Tag>(`/tags/${encodeURIComponent(name)}`, data),
  delete: (name: string) => api.delete(`/tags/${encodeURIComponent(name)}`)
}

// Conversation Notes
export interface ConversationNote {
  id: string
  contact_id: string
  created_by_id: string
  created_by_name: string
  content: string
  created_at: string
  updated_at: string
}

export const notesService = {
  list: (contactId: string, params?: { limit?: number; before?: string }) =>
    api.get<{ notes: ConversationNote[]; total: number; has_more: boolean }>(`/contacts/${contactId}/notes`, { params }),
  create: (contactId: string, data: { content: string }) =>
    api.post<ConversationNote>(`/contacts/${contactId}/notes`, data),
  update: (contactId: string, noteId: string, data: { content: string }) =>
    api.put<ConversationNote>(`/contacts/${contactId}/notes/${noteId}`, data),
  delete: (contactId: string, noteId: string) =>
    api.delete(`/contacts/${contactId}/notes/${noteId}`)
}

// Calling - Call Logs & IVR Flows
export interface CallLog {
  disposition?: string
  notes?: string
  id: string
  organization_id: string
  whatsapp_account: string
  contact_id: string
  whatsapp_call_id: string
  caller_phone: string
  direction: 'incoming' | 'outgoing'
  status: 'ringing' | 'answered' | 'completed' | 'missed' | 'rejected' | 'failed' | 'initiating' | 'accepted' | 'transferring'
  duration: number
  ivr_flow_id?: string
  ivr_path?: Record<string, any>
  agent_id?: string
  started_at?: string
  answered_at?: string
  ended_at?: string
  disconnected_by?: 'client' | 'agent' | 'system'
  error_message?: string
  recording_s3_key?: string
  recording_duration?: number
  contact?: {
    id: string
    phone_number: string
    profile_name: string
  }
  agent?: {
    id: string
    full_name: string
    email: string
  }
  ivr_flow?: IVRFlow
  created_at: string
  updated_at: string
}

// v2 Node-based IVR Flow types
export type IVRNodeType = 'greeting' | 'menu' | 'gather' | 'http_callback' | 'transfer' | 'goto_flow' | 'timing' | 'crm_condition' | 'hangup'

export interface IVRNodePosition {
  x: number
  y: number
}

export interface IVRNode {
  id: string
  type: IVRNodeType
  label: string
  position: IVRNodePosition
  config: Record<string, any>
}

export interface IVREdge {
  from: string
  to: string
  condition: string
}

export interface IVRFlowData {
  version: 2
  nodes: IVRNode[]
  edges: IVREdge[]
  entry_node: string
}

// v2 Node-based Chatbot Flow types. Mirrors IVR's graph shape with a
// chat-specific node-type union. Only types listed in the union are
// implemented today; others land in Phase 3.
export type ChatNodeType =
  | 'start'
  | 'message'
  | 'buttons'
  | 'end'
  | 'prompt'
  | 'api_call'
  | 'condition'
  | 'timing'
  | 'set_variable'
  | 'ai_response'
  | 'transfer'
  | 'webhook'
  | 'goto_flow'
  | 'whatsapp_flow'
  // CRM nodes (plan 10, S7): run actions from the shared library, and branch
  // on what is true of the contact rather than on what they just typed.
  | 'crm_action'
  | 'crm_condition'

export interface ChatNode {
  id: string
  type: ChatNodeType
  label: string
  position: IVRNodePosition
  config: Record<string, any>
}

export interface ChatEdge {
  from: string
  to: string
  condition: string
}

export interface ChatFlowGraph {
  version: 2
  nodes: ChatNode[]
  edges: ChatEdge[]
  entry_node: string
}

export interface IVRFlow {
  id: string
  organization_id: string
  whatsapp_account: string
  name: string
  description: string
  is_active: boolean
  is_call_start: boolean
  is_outgoing_end: boolean
  menu: IVRFlowData
  welcome_audio_url: string
  created_at: string
  updated_at: string
}

export interface CallTransfer {
  id: string
  organization_id: string
  call_log_id: string
  whatsapp_call_id: string
  caller_phone: string
  contact_id: string
  whatsapp_account: string
  status: 'waiting' | 'connected' | 'completed' | 'abandoned' | 'no_answer'
  team_id?: string
  agent_id?: string
  initiating_agent_id?: string
  transferred_at: string
  connected_at?: string
  completed_at?: string
  hold_duration: number
  talk_duration: number
  ivr_path?: Record<string, any>
  contact?: {
    id: string
    phone_number: string
    profile_name: string
  }
  agent?: {
    id: string
    full_name: string
    email: string
  }
  initiating_agent?: {
    id: string
    full_name: string
    email: string
  }
  team?: {
    id: string
    name: string
  }
  call_log?: CallLog
  created_at: string
  updated_at: string
}

// Outgoing Calls
export interface CallPermission {
  id: string
  contact_id: string
  whatsapp_account: string
  status: 'pending' | 'accepted' | 'declined' | 'expired'
  message_id?: string
  requested_at: string
  responded_at?: string
  expires_at?: string
}

export const outgoingCallsService = {
  initiate: (data: { contact_id: string; whatsapp_account: string; sdp_offer: string }) =>
    api.post<{ call_log_id: string; sdp_answer: string }>('/calls/outgoing', data),
  hangup: (callLogId: string) =>
    api.post(`/calls/outgoing/${callLogId}/hangup`),
  requestPermission: (data: { contact_id: string; whatsapp_account: string }) =>
    api.post<{ permission_id: string }>('/calls/permission-request', data),
  getPermission: (contactId: string, whatsappAccount: string) =>
    api.get<CallPermission>(`/calls/permission/${contactId}`, { params: { whatsapp_account: whatsappAccount } }),
  getICEServers: () =>
    api.get<{ ice_servers: Array<{ urls: string[]; username?: string; credential?: string }> }>('/calls/ice-servers'),
}

export const callLogsService = {
  recordOutcome: (id: string, body: {
    disposition: string
    notes?: string
    follow_up?: boolean
    follow_up_at?: string
    follow_up_note?: string
    complete_task_id?: string
  }) => api.post(`/call-logs/${id}/outcome`, body),

  list: (params?: { status?: string; account?: string; contact_id?: string; direction?: string; ivr_flow_id?: string; phone?: string; from?: string; to?: string; page?: number; limit?: number }) =>
    api.get<{ call_logs: CallLog[]; total: number }>('/call-logs', { params }),
  get: (id: string) => api.get<CallLog>(`/call-logs/${id}`),
  getRecordingURL: (id: string) =>
    api.get<{ url: string; duration: number }>(`/call-logs/${id}/recording`),
  hold: (id: string) =>
    api.post<{ status: string }>(`/call-logs/${id}/hold`),
  resume: (id: string) =>
    api.post<{ status: string }>(`/call-logs/${id}/resume`),
}

export const callTransfersService = {
  list: (params?: { status?: string; page?: number; limit?: number }) =>
    api.get<{ call_transfers: CallTransfer[]; total: number }>('/call-transfers', { params }),
  get: (id: string) => api.get<CallTransfer>(`/call-transfers/${id}`),
  connect: (id: string, sdpOffer: string) =>
    api.post<{ sdp_answer: string }>(`/call-transfers/${id}/connect`, { sdp_offer: sdpOffer }),
  hangup: (id: string) =>
    api.post(`/call-transfers/${id}/hangup`),
  initiate: (data: { call_log_id: string; team_id: string; agent_id?: string }) =>
    api.post<{ status: string }>('/call-transfers/initiate', data),
}

export const ivrFlowsService = {
  list: (params?: { search?: string; page?: number; limit?: number }) =>
    api.get<{ ivr_flows: IVRFlow[]; total: number }>('/ivr-flows', { params }),
  get: (id: string) => api.get<IVRFlow>(`/ivr-flows/${id}`),
  create: (data: { whatsapp_account: string; name: string; description?: string; is_call_start?: boolean; menu: IVRFlowData; welcome_audio_url?: string }) =>
    api.post<IVRFlow>('/ivr-flows', data),
  update: (id: string, data: { name?: string; description?: string; is_active?: boolean; is_call_start?: boolean; is_outgoing_end?: boolean; menu?: IVRFlowData; welcome_audio_url?: string }) =>
    api.put<IVRFlow>(`/ivr-flows/${id}`, data),
  delete: (id: string) => api.delete(`/ivr-flows/${id}`),
  uploadAudio: (file: File) => {
    const formData = new FormData()
    formData.append('file', file)
    return api.post('/ivr-flows/audio', formData, {
      headers: { 'Content-Type': 'multipart/form-data' }
    })
  },
  getAudioUrl: (filename: string) => `${api.defaults.baseURL}/ivr-flows/audio/${encodeURIComponent(filename)}`
}

export default api

// Notifications (plan 00, F5)
//
// Stored per-user notifications, as opposed to the transient toasts the product
// used to fire from WebSocket handlers: anything that arrived while an agent was
// away or on another screen was simply lost. These rows survive, so the bell can
// show what was missed.
export interface AppNotification {
  id: string
  type: string
  title: string
  body: string
  link: string
  entity_type?: string
  entity_id?: string
  data: Record<string, any>
  read_at?: string | null
  created_at: string
}

export const notificationsService = {
  list: (params?: { limit?: number; cursor?: string; unread?: boolean }) =>
    api.get<{ notifications: AppNotification[]; next_cursor?: string }>('/notifications', { params }),
  unreadCount: () => api.get<{ count: number }>('/notifications/unread-count'),
  markRead: (id: string) => api.post(`/notifications/${id}/read`),
  markAllRead: () => api.post('/notifications/read-all')
}
