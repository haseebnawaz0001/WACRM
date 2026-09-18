import {
  LayoutDashboard,
  Home,
  MessageSquare,
  Bot,
  FileText,
  Megaphone,
  Settings,
  Users,
  Contact,
  ListChecks,
  ListTodo,
  Workflow,
  Sparkles,
  Key,
  UserX,
  MessageSquareText,
  Webhook,
  BarChart3,
  ShieldCheck,
  Zap,
  Shield,
  LineChart,
  Tags,
  PhoneCall,
  PhoneForwarded,
  ScrollText,
  KanbanSquare,
  PieChart,
  Inbox
} from 'lucide-vue-next'
import type { Component } from 'vue'

export interface NavItem {
  name: string
  path: string
  icon: Component
  permission?: string
  /** Optional module this item belongs to; hidden when the module is off. */
  module?: string
  /**
   * Which live count to show as a badge (plan 10, S12).
   *
   * The sidebar previously had no way to say "this item has a number", so the
   * unread count lived inside the chat view and the overdue task count
   * nowhere — an agent had to open Tasks to find out they were late.
   */
  badgeKey?: NavBadgeKey
  childPermissions?: string[]
  children?: NavItem[]
}

/** Counts the sidebar knows how to show. */
export type NavBadgeKey = 'inboxUnread' | 'tasksDue' 

/**
 * How a section is presented in the sidebar.
 *
 * The rail used to render all five sections as one flat scroll of nineteen
 * rows, which put the Analytics section below the fold on a 900px screen and
 * gave a destination used twice a month the same weight as the inbox. Sections
 * now say what they are, and the sidebar gives each the treatment it earns:
 *
 * - `workspace`: always visible, no header. The places work happens.
 * - `group`: collapsible, remembers its state, opens itself for the current
 *   route. Used weekly rather than hourly.
 * - `manage`: not in the rail at all. Opens in a drawer with room to show all
 *   sixteen settings pages at once, instead of eight inside a capped strip
 *   that covered the sections above it.
 */
export type NavSectionKind = 'workspace' | 'group' | 'manage'

export interface NavSection {
  label: string
  items: NavItem[]
  /** Permissions needed to show section — at least one must pass */
  permissions: string[]
  /** How the sidebar presents it. Defaults to `group`. */
  kind?: NavSectionKind
  /** Icon for a collapsed group header, and for the drawer's entry row. */
  icon?: Component
  /** Pin to bottom of sidebar */
  pinBottom?: boolean
}

export const navigationSections: NavSection[] = [
  {
    label: 'nav.sectionMain',
    kind: 'workspace',
    permissions: ['analytics', 'chat', 'contacts', 'deals', 'automations', 'tasks', 'segments'],
    items: [
      {
        name: 'nav.dashboard',
        path: '/',
        icon: LayoutDashboard,
        permission: 'analytics'
      },
      {
        // Home has no permission: everyone can see their own work, and that is
        // the whole page.
        name: 'nav.home',
        path: '/home',
        icon: Home
      },
      {
        name: 'nav.chat',
        path: '/chat',
        icon: MessageSquare,
        permission: 'chat'
      },
      {
        name: 'nav.inbox',
        badgeKey: 'inboxUnread',
        path: '/inbox',
        icon: Inbox,
        permission: 'chat'
      },
      {
        name: 'nav.contacts',
        path: '/contacts',
        icon: Contact,
        permission: 'contacts'
      },
      {
        name: 'nav.tasks',
        badgeKey: 'tasksDue',
        path: '/tasks',
        icon: ListChecks,
        permission: 'tasks'
      },
      {
        name: 'nav.segments',
        path: '/segments',
        icon: Users,
        permission: 'segments'
      },
      {
        name: 'nav.pipeline',
        path: '/pipeline',
        icon: KanbanSquare,
        permission: 'deals',
        // Pipelines are optional per organization; the API already says so
        // through /organizations/current, and this is what connects that
        // answer to the menu (plan 07, plan 10 S12).
        module: 'pipelines'
      },
      {
        name: 'nav.automations',
        path: '/automations',
        icon: Zap,
        permission: 'automations'
      },
    ]
  },
  {
    label: 'nav.sectionMessaging',
    kind: 'group',
    icon: Megaphone,
    permissions: ['settings.chatbot', 'chatbot.keywords', 'flows.chatbot', 'chatbot.ai', 'transfers', 'campaigns', 'templates', 'flows.whatsapp'],
    items: [
      {
        name: 'nav.chatbot',
        path: '/chatbot',
        icon: Bot,
        permission: 'settings.chatbot',
        childPermissions: ['settings.chatbot', 'chatbot.keywords', 'flows.chatbot', 'chatbot.ai', 'transfers'],
        children: [
          { name: 'nav.overview', path: '/chatbot', icon: Bot, permission: 'settings.chatbot' },
          { name: 'nav.keywords', path: '/chatbot/keywords', icon: Key, permission: 'chatbot.keywords' },
          { name: 'nav.flows', path: '/chatbot/flows', icon: Workflow, permission: 'flows.chatbot' },
          { name: 'nav.aiContexts', path: '/chatbot/ai', icon: Sparkles, permission: 'chatbot.ai' },
          { name: 'nav.transferSla', path: '/chatbot/transfers/sla', icon: UserX, permission: 'transfers' }
        ]
      },
      {
        name: 'nav.campaigns',
        path: '/campaigns',
        icon: Megaphone,
        permission: 'campaigns'
      },
      {
        name: 'nav.templates',
        path: '/templates',
        icon: FileText,
        permission: 'templates'
      },
      {
        name: 'nav.flows',
        path: '/flows',
        icon: Workflow,
        permission: 'flows.whatsapp'
      },
    ]
  },
  {
    label: 'nav.sectionCalling',
    kind: 'group',
    icon: PhoneCall,
    permissions: ['call_logs', 'ivr_flows', 'call_transfers'],
    items: [
      { name: 'nav.callLogs', path: '/calling/logs', icon: PhoneCall, permission: 'call_logs' },
      { name: 'nav.ivrFlows', path: '/calling/ivr-flows', icon: Workflow, permission: 'ivr_flows' },
      { name: 'nav.callTransfers', path: '/calling/transfers', icon: PhoneForwarded, permission: 'call_transfers' },
    ]
  },
  {
    label: 'nav.sectionAnalytics',
    kind: 'group',
    icon: BarChart3,
    permissions: ['analytics.agents', 'analytics', 'reports'],
    items: [
      {
        name: 'nav.crmReports',
        path: '/analytics/crm',
        icon: PieChart,
        permission: 'reports'
      },
      {
        name: 'nav.agentAnalytics',
        path: '/analytics/agents',
        icon: BarChart3,
        permission: 'analytics.agents'
      },
      {
        name: 'nav.metaInsights',
        path: '/analytics/meta-insights',
        icon: LineChart,
        permission: 'analytics'
      },
    ]
  },
  {
    label: 'nav.manage',
    kind: 'manage',
    icon: Settings,
    permissions: ['settings.general', 'settings.chatbot', 'accounts', 'contacts', 'canned_responses', 'tags', 'teams', 'users', 'roles', 'api_keys', 'webhooks', 'custom_actions', 'settings.sso', 'audit_logs'],
    pinBottom: true,
    items: [
      {
        name: 'nav.settings',
        path: '/settings',
        icon: Settings,
        permission: 'settings.general',
        childPermissions: ['settings.general', 'settings.chatbot', 'accounts', 'contacts', 'pipelines', 'canned_responses', 'tags', 'teams', 'users', 'roles', 'api_keys', 'webhooks', 'custom_actions', 'settings.sso', 'audit_logs'],
        children: [
          { name: 'nav.general', path: '/settings', icon: Settings, permission: 'settings.general' },
          { name: 'nav.chatbot', path: '/settings/chatbot', icon: Bot, permission: 'settings.chatbot' },
          { name: 'nav.accounts', path: '/settings/accounts', icon: Users, permission: 'accounts' },
          { name: 'nav.contactFields', path: '/settings/contact-fields', icon: ListChecks, permission: 'contact_fields' },
          { name: 'nav.pipelines', path: '/settings/pipelines', icon: KanbanSquare, permission: 'pipelines' },
          { name: 'nav.taskTypes', path: '/settings/task-types', icon: ListTodo, permission: 'tasks' },
          { name: 'nav.cannedResponses', path: '/settings/canned-responses', icon: MessageSquareText, permission: 'canned_responses' },
          { name: 'nav.tags', path: '/settings/tags', icon: Tags, permission: 'tags' },
          { name: 'nav.teams', path: '/settings/teams', icon: Users, permission: 'teams' },
          { name: 'nav.users', path: '/settings/users', icon: Users, permission: 'users' },
          { name: 'nav.roles', path: '/settings/roles', icon: Shield, permission: 'roles' },
          { name: 'nav.apiKeys', path: '/settings/api-keys', icon: Key, permission: 'api_keys' },
          { name: 'nav.webhooks', path: '/settings/webhooks', icon: Webhook, permission: 'webhooks' },
          { name: 'nav.customActions', path: '/settings/custom-actions', icon: Zap, permission: 'custom_actions' },
          { name: 'nav.sso', path: '/settings/sso', icon: ShieldCheck, permission: 'settings.sso' },
          { name: 'nav.auditLogs', path: '/settings/audit-logs', icon: ScrollText, permission: 'audit_logs' }
        ]
      }
    ]
  }
]

// Flat list for backward compatibility (used by AppLayout computed)
export const navigationItems: NavItem[] = navigationSections.flatMap(s => s.items)

/**
 * How the Manage drawer arranges the settings pages.
 *
 * Sixteen pages in one alphabetical-ish column is a list you read rather than
 * scan, and it was the list that broke the sidebar: opening it inside a strip
 * capped at 45% height showed eight of them and hid the sections above. In
 * columns, grouped by what somebody came to do, all sixteen are visible at
 * once and the thing you want is found by heading rather than by scrolling.
 *
 * Paths, not duplicated definitions: the items themselves stay in the section
 * above, so a permission or a route only ever changes in one place.
 */
export interface ManageGroup {
  label: string
  paths: string[]
}

export const manageGroups: ManageGroup[] = [
  {
    label: 'nav.manageWorkspace',
    paths: ['/settings', '/settings/accounts', '/settings/tags', '/settings/canned-responses']
  },
  {
    label: 'nav.manageRecords',
    paths: ['/settings/contact-fields', '/settings/pipelines', '/settings/task-types']
  },
  {
    label: 'nav.managePeople',
    paths: ['/settings/teams', '/settings/users', '/settings/roles', '/settings/sso']
  },
  {
    label: 'nav.manageDeveloper',
    paths: ['/settings/api-keys', '/settings/webhooks', '/settings/custom-actions', '/settings/chatbot', '/settings/audit-logs']
  }
]

/** One dashboard shortcut: a destination the user can pin to their dashboard. */
export interface NavShortcut {
  key: string
  /** i18n key for the label, e.g. nav.contacts. */
  name: string
  path: string
  icon: Component
  permission?: string
  module?: string
}

// legacyShortcutKeys keeps shortcuts saved before the registry was derived from
// navigation working. Users have these keys stored against their dashboard; a
// key that no longer resolves renders as a gap they cannot remove.
const legacyShortcutKeys: Record<string, string> = {
  chat: '/chat',
  campaigns: '/campaigns',
  templates: '/templates',
  chatbot: '/chatbot',
  contacts: '/contacts',
  flows: '/flows',
  transfers: '/chatbot/transfers/sla',
  agentAnalytics: '/analytics/agents',
  metaInsights: '/analytics/meta-insights',
  settings: '/settings',
  accounts: '/settings/accounts',
  cannedResponses: '/settings/canned-responses',
  tags: '/settings/tags',
  teams: '/settings/teams',
  users: '/settings/users',
  roles: '/settings/roles',
  apiKeys: '/settings/api-keys',
  webhooks: '/settings/webhooks',
  customActions: '/settings/custom-actions',
  sso: '/settings/sso',
}

/** shortcutKeyForPath turns /settings/canned-responses into settingsCannedResponses. */
function shortcutKeyForPath(path: string): string {
  const parts = path.split('/').filter(Boolean)
  if (parts.length === 0) return 'dashboard'
  return parts
    .map(part => part.split('-'))
    .flat()
    .map((word, i) => (i === 0 ? word : word.charAt(0).toUpperCase() + word.slice(1)))
    .join('')
}

/**
 * navigationShortcuts is the dashboard's shortcut catalog, derived from the
 * sidebar so the two cannot drift.
 *
 * The dashboard kept its own hand-written list. It had never been updated for
 * anything the CRM added, so Contacts, Tasks, Pipeline, Segments, Automations,
 * Reports, the Inbox and the audit log could not be pinned at all — and its
 * "Contacts" entry pointed at the settings page while the sidebar's pointed at
 * the CRM list, so the same word went to two different screens.
 */
export function navigationShortcuts(): NavShortcut[] {
  const byPath = new Map<string, NavShortcut>()

  const add = (item: NavItem) => {
    if (byPath.has(item.path)) return
    byPath.set(item.path, {
      key: shortcutKeyForPath(item.path),
      name: item.name,
      path: item.path,
      icon: item.icon,
      permission: item.permission,
      module: item.module,
    })
    item.children?.forEach(add)
  }
  navigationSections.forEach(section => section.items.forEach(add))

  // Legacy keys point at the same destinations; give them the derived entry's
  // label and icon so a saved shortcut still renders.
  const out = [...byPath.values()]
  for (const [key, path] of Object.entries(legacyShortcutKeys)) {
    const derived = byPath.get(path)
    if (derived && derived.key !== key) {
      out.push({ ...derived, key })
    }
  }
  return out
}
