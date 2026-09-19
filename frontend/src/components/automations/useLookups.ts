/**
 * The organization's own things, for pickers and sentences (automation
 * builder).
 *
 * A step that says "Assign to team 3f2a…" is a step nobody can check. Every
 * card on the canvas names what it points at — the team, the person, the
 * template, the stage — so the lists are loaded once, on demand, and shared
 * by the pickers that choose them and the sentences that describe them.
 */
import { reactive } from 'vue'
import {
  tagsService, usersService, teamsService, templatesService, pipelinesService,
  taskTypesService, contactFieldsService, rolesService, chatbotService,
  campaignsService, accountsService, variablesService, contactsService,
  type Tag, type Team, type Pipeline, type TaskType, type ContactField,
  type Role, type TemplateVariable, type FilterFieldInfo
} from '@/services/api'
import { unwrapListResponse } from '@/lib/api-utils'

export interface LookupUser { id: string; full_name: string; email?: string; is_active?: boolean }
export interface LookupTemplate {
  id: string
  name: string
  language?: string
  category?: string
  status?: string
  body_content?: string
  whatsapp_account?: string
}
export interface LookupNamed { id: string; name: string }

type Kind =
  | 'tags' | 'users' | 'teams' | 'templates' | 'pipelines' | 'taskTypes' | 'fields'
  | 'roles' | 'flows' | 'campaigns' | 'accounts' | 'variables' | 'filterFields'

interface State {
  tags: Tag[]
  users: LookupUser[]
  teams: Team[]
  templates: LookupTemplate[]
  pipelines: Pipeline[]
  taskTypes: TaskType[]
  fields: ContactField[]
  roles: Role[]
  flows: LookupNamed[]
  campaigns: LookupNamed[]
  accounts: LookupNamed[]
  variables: TemplateVariable[]
  filterFields: FilterFieldInfo[]
  loaded: Record<Kind, boolean>
}

const state = reactive<State>({
  tags: [], users: [], teams: [], templates: [], pipelines: [], taskTypes: [],
  fields: [], roles: [], flows: [], campaigns: [], accounts: [], variables: [],
  filterFields: [],
  loaded: {
    tags: false, users: false, teams: false, templates: false, pipelines: false,
    taskTypes: false, fields: false, roles: false, flows: false, campaigns: false,
    accounts: false, variables: false, filterFields: false
  }
})

const inflight = new Map<Kind, Promise<void>>()

async function fetchKind(kind: Kind): Promise<void> {
  // Each list fails on its own: a person without template access still gets
  // every other picker, and the one they cannot use says so by being empty.
  try {
    switch (kind) {
      case 'tags':
        state.tags = unwrapListResponse<Tag>(await tagsService.list({ limit: 500 }), 'tags')
        break
      case 'users':
        state.users = unwrapListResponse<LookupUser>(await usersService.list({ limit: 500 }), 'users')
        break
      case 'teams':
        state.teams = unwrapListResponse<Team>(await teamsService.list({ limit: 200 }), 'teams')
        break
      case 'templates':
        state.templates = unwrapListResponse<LookupTemplate>(await templatesService.list({ limit: 500 }), 'templates')
        break
      case 'pipelines':
        state.pipelines = unwrapListResponse<Pipeline>(await pipelinesService.list(), 'pipelines')
        break
      case 'taskTypes':
        state.taskTypes = unwrapListResponse<TaskType>(await taskTypesService.list(), 'task_types')
        break
      case 'fields':
        state.fields = unwrapListResponse<ContactField>(await contactFieldsService.list(), 'fields')
        break
      case 'roles':
        state.roles = unwrapListResponse<Role>(await rolesService.list({ limit: 200 }), 'roles')
        break
      case 'flows':
        state.flows = unwrapListResponse<LookupNamed>(await chatbotService.listFlows({ limit: 200 }), 'flows')
        break
      case 'campaigns':
        state.campaigns = unwrapListResponse<LookupNamed>(await campaignsService.list({ limit: 200 }), 'campaigns')
        break
      case 'accounts':
        state.accounts = unwrapListResponse<LookupNamed>(await accountsService.list(), 'accounts')
        break
      case 'variables':
        state.variables = unwrapListResponse<TemplateVariable>(await variablesService.list('automation'), 'variables')
        break
      case 'filterFields':
        state.filterFields = unwrapListResponse<FilterFieldInfo>(await contactsService.filterFields(), 'fields')
        break
    }
  } catch {
    // Left empty; see above.
  } finally {
    state.loaded[kind] = true
  }
}

/** Loads the named lists once; later calls share the first request. */
export function ensureLookups(...kinds: Kind[]): Promise<void[]> {
  return Promise.all(kinds.map(kind => {
    if (state.loaded[kind]) return Promise.resolve()
    const pending = inflight.get(kind)
    if (pending) return pending
    const request = fetchKind(kind).finally(() => inflight.delete(kind))
    inflight.set(kind, request)
    return request
  }))
}

/** Forgets a list, so the next ensure refetches it (after creating a tag, say). */
export function invalidateLookup(kind: Kind) {
  state.loaded[kind] = false
}

export function useLookups() {
  return {
    state,
    ensure: ensureLookups,
    invalidate: invalidateLookup,
    userName: (id?: string) => state.users.find(u => u.id === id)?.full_name,
    teamName: (id?: string) => state.teams.find(t => t.id === id)?.name,
    template: (id?: string) => state.templates.find(t => t.id === id),
    pipeline: (id?: string) => state.pipelines.find(p => p.id === id),
    stage: (id?: string) => {
      for (const pipeline of state.pipelines) {
        const stage = pipeline.stages?.find(s => s.id === id)
        if (stage) return { stage, pipeline }
      }
      return undefined
    },
    taskTypeLabel: (key?: string) => state.taskTypes.find(t => t.key === key)?.label,
    field: (key?: string) => state.fields.find(f => f.key === key),
    flowName: (id?: string) => state.flows.find(f => f.id === id)?.name,
    campaignName: (id?: string) => state.campaigns.find(c => c.id === id)?.name,
    tagColor: (name: string) => state.tags.find(t => t.name.toLowerCase() === name.toLowerCase())?.color
  }
}
