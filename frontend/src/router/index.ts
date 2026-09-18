import { createRouter, createWebHistory, type RouteLocationGeneric } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

// Permission-based route meta type
declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    permission?: string // Resource permission required (e.g., 'analytics', 'chat')
  }
}

// Get base path from server-injected config or fallback to Vite's BASE_URL
const basePath = (window as any).__BASE_PATH__ ?? import.meta.env.BASE_URL ?? '/'
const normalizedBasePath = basePath.endsWith('/') ? basePath : basePath + '/'

const router = createRouter({
  history: createWebHistory(normalizedBasePath),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/auth/LoginView.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/register',
      name: 'register',
      component: () => import('@/views/auth/RegisterView.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/auth/sso/callback',
      name: 'sso-callback',
      component: () => import('@/views/auth/SSOCallbackView.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/',
      component: () => import('@/components/layout/AppLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        {
          path: '',
          name: 'dashboard',
          component: () => import('@/views/dashboard/DashboardView.vue'),
          meta: { permission: 'analytics' }
        },
        {
          // Where an agent lands (plan 04). The Dashboard needs `analytics`,
          // which agents do not have, so signing in used to put them on a page
          // they could not open.
          path: 'home',
          name: 'home',
          component: () => import('@/views/dashboard/HomeView.vue')
        },
        {
          // The merged conversation surface: the queue and the thread, which
          // were two doors onto the same conversations table (plan 10, §8).
          path: 'inbox/:contactId?',
          name: 'inbox',
          component: () => import('@/views/inbox/InboxView.vue'),
          props: true,
          meta: { permission: 'chat', stableKey: true }
        },
        {
          // Everything written before the merge — bookmarks, notification
          // deep links, the transfer toast — still points at /chat.
          path: 'chat/:contactId?',
          redirect: to => ({
            name: 'inbox',
            params: to.params.contactId ? { contactId: to.params.contactId } : {}
          })
        },
        {
          path: 'profile',
          name: 'profile',
          component: () => import('@/views/profile/ProfileView.vue')
          // All roles can access profile
        },
        {
          path: 'templates',
          name: 'templates',
          component: () => import('@/views/settings/TemplatesView.vue'),
          meta: { permission: 'templates' }
        },
        {
          path: 'templates/:id',
          name: 'template-detail',
          component: () => import('@/views/settings/TemplateDetailView.vue'),
          meta: { permission: 'templates' }
        },
        {
          path: 'flows',
          name: 'flows',
          component: () => import('@/views/settings/FlowsView.vue'),
          meta: { permission: 'flows.whatsapp' }
        },
        {
          path: 'campaigns',
          name: 'campaigns',
          component: () => import('@/views/settings/CampaignsView.vue'),
          meta: { permission: 'campaigns' }
        },
        {
          path: 'campaigns/:id',
          name: 'campaign-detail',
          component: () => import('@/views/settings/CampaignDetailView.vue'),
          meta: { permission: 'campaigns' }
        },
        {
          path: 'chatbot',
          name: 'chatbot',
          component: () => import('@/views/chatbot/ChatbotView.vue'),
          meta: { permission: 'settings.chatbot' }
        },
        {
          path: 'chatbot/settings',
          redirect: '/settings/chatbot'
        },
        {
          path: 'chatbot/keywords',
          name: 'chatbot-keywords',
          component: () => import('@/views/chatbot/KeywordsView.vue'),
          meta: { permission: 'chatbot.keywords' }
        },
        {
          path: 'chatbot/keywords/:id',
          name: 'keyword-detail',
          component: () => import('@/views/chatbot/KeywordDetailView.vue'),
          meta: { permission: 'chatbot.keywords' }
        },
        {
          path: 'chatbot/flows',
          name: 'chatbot-flows',
          component: () => import('@/views/chatbot/ChatbotFlowsView.vue'),
          meta: { permission: 'flows.chatbot' }
        },
        {
          path: 'chatbot/flows/new',
          name: 'chatbot-flow-new',
          component: () => import('@/views/chatbot/ChatbotFlowBuilderView.vue'),
          meta: { permission: 'flows.chatbot' }
        },
        {
          path: 'chatbot/flows/:id/edit',
          name: 'chatbot-flow-edit',
          component: () => import('@/views/chatbot/ChatbotFlowBuilderView.vue'),
          meta: { permission: 'flows.chatbot' }
        },
        {
          path: 'chatbot/ai',
          name: 'chatbot-ai',
          component: () => import('@/views/chatbot/AIContextsView.vue'),
          meta: { permission: 'chatbot.ai' }
        },
        {
          path: 'chatbot/ai/:id',
          name: 'ai-context-detail',
          component: () => import('@/views/chatbot/AIContextDetailView.vue'),
          meta: { permission: 'chatbot.ai' }
        },
        {
          // The transfer queue is the Inbox's Unassigned view now (plan 03,
          // plan 10 §4.2). Two screens showing the same waiting customers is
          // how an agent ends up answering one twice; the path redirects
          // because it is in bookmarks and in the dashboard shortcuts.
          path: 'chatbot/transfers',
          redirect: { path: '/inbox', query: { view: 'unassigned' } }
        },
        {
          // The supervisor's SLA view keeps its own page: it is a different
          // question ("who is late?") from the queue ("what is next?").
          path: 'chatbot/transfers/sla',
          name: 'chatbot-transfers',
          component: () => import('@/views/chatbot/AgentTransfersView.vue'),
          meta: { permission: 'transfers' }
        },
        {
          path: 'analytics/agents',
          name: 'agent-analytics',
          component: () => import('@/views/analytics/AgentAnalyticsView.vue'),
          meta: { permission: 'analytics.agents' }
        },
        {
          path: 'analytics/meta-insights',
          name: 'meta-insights',
          component: () => import('@/views/analytics/MetaInsightsView.vue'),
          meta: { permission: 'analytics' }
        },
        {
          path: 'settings',
          name: 'settings',
          component: () => import('@/views/settings/SettingsView.vue'),
          meta: { permission: 'settings.general' }
        },
        {
          path: 'settings/chatbot',
          name: 'chatbot-settings',
          component: () => import('@/views/settings/ChatbotSettingsView.vue'),
          meta: { permission: 'settings.chatbot' }
        },
        {
          path: 'settings/accounts',
          name: 'accounts',
          component: () => import('@/views/settings/AccountsView.vue'),
          meta: { permission: 'accounts' }
        },
        {
          path: 'settings/accounts/:id',
          name: 'account-detail',
          component: () => import('@/views/settings/AccountDetailView.vue'),
          meta: { permission: 'accounts' }
        },
        {
          path: 'settings/canned-responses',
          name: 'canned-responses',
          component: () => import('@/views/settings/CannedResponsesView.vue'),
          meta: { permission: 'canned_responses' }
        },
        {
          path: 'settings/canned-responses/:id',
          name: 'canned-response-detail',
          component: () => import('@/views/settings/CannedResponseDetailView.vue'),
          // stableKey reuses the component instance when :id flips from "new"
          // to the new UUID after create, so the locally-set response (and the
          // resulting Save-button reactivity) survives the route change.
          meta: { permission: 'canned_responses', stableKey: true }
        },
        {
          path: 'contacts',
          name: 'contacts-module',
          component: () => import('@/views/contacts/ContactsModuleView.vue'),
          meta: { permission: 'contacts' }
        },
        {
          path: 'pipeline',
          name: 'pipeline-board',
          component: () => import('@/views/pipeline/PipelineBoardView.vue'),
          meta: { permission: 'deals' }
        },

        {
          path: 'tasks',
          name: 'tasks',
          component: () => import('@/views/tasks/TasksView.vue'),
          meta: { permission: 'tasks' }
        },
        {
          path: 'segments',
          name: 'segments',
          component: () => import('@/views/segments/SegmentsView.vue'),
          meta: { permission: 'segments' }
        },
        {
          path: 'contacts/duplicates',
          name: 'contact-duplicates',
          component: () => import('@/views/contacts/DuplicatesView.vue'),
          meta: { permission: 'contacts' }
        },
        {
          path: 'contacts/:id',
          name: 'contact-profile',
          component: () => import('@/views/contacts/ContactProfileView.vue'),
          meta: { permission: 'contacts' }
        },
        {
          path: 'analytics/crm',
          name: 'crm-reports',
          component: () => import('@/views/analytics/CrmReportsView.vue'),
          meta: { permission: 'reports' }
        },
        {
          path: 'automations',
          name: 'automations',
          component: () => import('@/views/automations/AutomationsView.vue'),
          meta: { permission: 'automations' }
        },
        {
          path: 'automations/:id',
          name: 'automation-detail',
          component: () => import('@/views/automations/AutomationDetailView.vue'),
          meta: { permission: 'automations' }
        },
        {
          path: 'settings/pipelines',
          name: 'pipelines',
          component: () => import('@/views/settings/PipelinesView.vue'),
          meta: { permission: 'pipelines' }
        },
        {
          // Contacts moved out of Settings into their own module (plan 01).
          // The old paths redirect rather than 404: they are in people's
          // bookmarks, in links colleagues have sent each other, and in the
          // dashboard shortcuts saved before the move.
          path: 'settings/contacts',
          redirect: { name: 'contacts-module' }
        },
        {
          path: 'settings/contacts/:id',
          redirect: (to: RouteLocationGeneric) => ({
            name: 'contact-profile',
            params: { id: to.params.id }
          })
        },
        {
          path: 'settings/contact-fields',
          name: 'contact-fields',
          component: () => import('@/views/settings/ContactFieldsView.vue'),
          meta: { permission: 'contact_fields' }
        },
        {
          path: 'settings/task-types',
          name: 'task-types',
          component: () => import('@/views/settings/TaskTypesView.vue'),
          meta: { permission: 'tasks' }
        },
        {
          path: 'settings/tags',
          name: 'tags',
          component: () => import('@/views/settings/TagsView.vue'),
          meta: { permission: 'tags' }
        },
        {
          path: 'settings/users',
          name: 'users',
          component: () => import('@/views/settings/UsersView.vue'),
          meta: { permission: 'users' }
        },
        {
          path: 'settings/users/:id',
          name: 'user-detail',
          component: () => import('@/views/settings/UserDetailView.vue'),
          meta: { permission: 'users' }
        },
        {
          path: 'settings/roles',
          name: 'roles',
          component: () => import('@/views/settings/RolesView.vue'),
          meta: { permission: 'roles' }
        },
        {
          path: 'settings/roles/:id',
          name: 'role-detail',
          component: () => import('@/views/settings/RoleDetailView.vue'),
          meta: { permission: 'roles' }
        },
        {
          path: 'settings/teams',
          name: 'teams',
          component: () => import('@/views/settings/TeamsView.vue'),
          meta: { permission: 'teams' }
        },
        {
          path: 'settings/teams/:id',
          name: 'team-detail',
          component: () => import('@/views/settings/TeamDetailView.vue'),
          meta: { permission: 'teams' }
        },
        {
          path: 'settings/api-keys',
          name: 'api-keys',
          component: () => import('@/views/settings/APIKeysView.vue'),
          meta: { permission: 'api_keys' }
        },
        {
          path: 'settings/api-keys/:id',
          name: 'api-key-detail',
          component: () => import('@/views/settings/APIKeyDetailView.vue'),
          meta: { permission: 'api_keys' }
        },
        {
          path: 'settings/webhooks',
          name: 'webhooks',
          component: () => import('@/views/settings/WebhooksView.vue'),
          meta: { permission: 'webhooks' }
        },
        {
          path: 'settings/webhooks/:id',
          name: 'webhook-detail',
          component: () => import('@/views/settings/WebhookDetailView.vue'),
          meta: { permission: 'webhooks' }
        },
        {
          path: 'settings/sso',
          name: 'sso-settings',
          component: () => import('@/views/settings/SSOSettingsView.vue'),
          meta: { permission: 'settings.sso' }
        },
        {
          path: 'settings/custom-actions',
          name: 'custom-actions',
          component: () => import('@/views/settings/CustomActionsView.vue'),
          meta: { permission: 'custom_actions' }
        },
        {
          path: 'settings/audit-logs',
          name: 'audit-logs',
          component: () => import('@/views/settings/AuditLogsView.vue'),
          meta: { permission: 'audit_logs' }
        },
        {
          path: 'settings/audit-logs/:id',
          name: 'audit-log-detail',
          component: () => import('@/views/settings/AuditLogDetailView.vue'),
          meta: { permission: 'audit_logs' }
        },
        {
          path: 'calling',
          redirect: '/calling/logs'
        },
        {
          path: 'calling/logs',
          name: 'call-logs',
          component: () => import('@/views/calling/CallLogsView.vue'),
          meta: { permission: 'call_logs' }
        },
        {
          path: 'calling/ivr-flows',
          name: 'ivr-flows',
          component: () => import('@/views/calling/IVRFlowsView.vue'),
          meta: { permission: 'ivr_flows' }
        },
        {
          path: 'calling/ivr-flows/:id/edit',
          name: 'ivr-flow-editor',
          component: () => import('@/views/calling/IVRFlowEditorView.vue'),
          meta: { permission: 'ivr_flows' }
        },
        {
          path: 'calling/transfers',
          name: 'call-transfers',
          component: () => import('@/views/calling/CallTransfersView.vue'),
          meta: { permission: 'call_transfers' }
        },
        {
          // Inside the layout, so somebody signed in who mistypes a URL keeps
          // the sidebar and can carry on. The catch-all below this one is
          // outside it, and threw them out of the application entirely — for a
          // typo, with "Go Back" and "Go Home" as the only way in.
          path: ':pathMatch(.*)*',
          name: 'not-found-in-app',
          component: () => import('@/views/NotFoundView.vue')
        }
      ]
    },
    {
      // The signed-out case, which has no shell to keep.
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue')
    }
  ]
})

// Navigation items with permissions in priority order (matches AppLayout.vue)
// Used to find the first accessible route for a user
const navigationOrder = [
  { path: '/', permission: 'analytics' },
  { path: '/home' },
  { path: '/chat', permission: 'chat' },
  { path: '/chatbot', permission: 'settings.chatbot', childPaths: [
    { path: '/chatbot', permission: 'settings.chatbot' },
    { path: '/chatbot/keywords', permission: 'chatbot.keywords' },
    { path: '/chatbot/flows', permission: 'flows.chatbot' },
    { path: '/chatbot/ai', permission: 'chatbot.ai' }
  ]},
  { path: '/chatbot/transfers/sla', permission: 'transfers' },
  { path: '/analytics/agents', permission: 'analytics.agents' },
  { path: '/analytics/meta-insights', permission: 'analytics' },
  { path: '/templates', permission: 'templates' },
  { path: '/flows', permission: 'flows.whatsapp' },
  { path: '/campaigns', permission: 'campaigns' },
  { path: '/calling/logs', permission: 'call_logs', childPaths: [
    { path: '/calling/logs', permission: 'call_logs' },
    { path: '/calling/ivr-flows', permission: 'ivr_flows' },
    { path: '/calling/transfers', permission: 'call_transfers' }
  ]},
  { path: '/contacts', permission: 'contacts' },
  { path: '/pipeline', permission: 'deals' },
  { path: '/inbox', permission: 'chat' },
  { path: '/tasks', permission: 'tasks' },
  { path: '/segments', permission: 'segments' },
  { path: '/automations', permission: 'automations' },
  { path: '/analytics/crm', permission: 'reports' },
  { path: '/settings', permission: 'settings.general', childPaths: [
    { path: '/settings', permission: 'settings.general' },
    { path: '/settings/chatbot', permission: 'settings.chatbot' },
    { path: '/settings/accounts', permission: 'accounts' },
    { path: '/settings/canned-responses', permission: 'canned_responses' },
    { path: '/settings/pipelines', permission: 'pipelines' },
    { path: '/settings/contact-fields', permission: 'contact_fields' },
  { path: '/settings/task-types', permission: 'tasks' },
    { path: '/settings/tags', permission: 'tags' },
    { path: '/settings/teams', permission: 'teams' },
    { path: '/settings/users', permission: 'users' },
    { path: '/settings/roles', permission: 'roles' },
    { path: '/settings/api-keys', permission: 'api_keys' },
    { path: '/settings/webhooks', permission: 'webhooks' },
    { path: '/settings/custom-actions', permission: 'custom_actions' },
    { path: '/settings/sso', permission: 'settings.sso' }
  ]}
]

// Find the first accessible route for the user
function getFirstAccessibleRoute(authStore: ReturnType<typeof useAuthStore>): string {
  for (const item of navigationOrder) {
    // An item with no permission is open to everyone — Home, which exists
    // precisely so an agent lands somewhere they can use (plan 04).
    if (!item.permission) {
      return item.path
    }
    // Check if user has permission for this item
    if (authStore.hasPermission(item.permission, 'read')) {
      return item.path
    }
    // Check child paths if available
    if (item.childPaths) {
      for (const child of item.childPaths) {
        if (authStore.hasPermission(child.permission, 'read')) {
          return child.path
        }
      }
    }
  }
  // Fallback to profile (always accessible)
  return '/profile'
}

// Navigation guard
router.beforeEach(async (to, _from, next) => {
  const authStore = useAuthStore()

  // Hydrate the store from localStorage before any route decision — an authenticated user hitting /login must be redirected away.
  if (!authStore.isAuthenticated && (to.meta.requiresAuth !== false || to.name === 'login' || to.name === 'register')) {
    authStore.restoreSession()
  }

  // Check if route requires auth
  if (to.meta.requiresAuth !== false) {
    if (!authStore.isAuthenticated) {
      return next({ name: 'login', query: { redirect: to.fullPath } })
    }

    // Check permission-based access
    const requiredPermission = to.meta.permission
    if (requiredPermission) {
      if (!authStore.hasPermission(requiredPermission, 'read')) {
        // Redirect to first accessible page
        return next({ path: getFirstAccessibleRoute(authStore) })
      }
    }
  } else {
    // Redirect to appropriate page if already logged in
    if (authStore.isAuthenticated && (to.name === 'login' || to.name === 'register')) {
      return next({ path: getFirstAccessibleRoute(authStore) })
    }
  }

  next()
})

export default router
