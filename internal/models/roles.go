package models

import (
	"github.com/google/uuid"
)

// Permission represents a granular permission for a specific resource and action
type Permission struct {
	BaseModel
	Resource    string `gorm:"size:50;not null;uniqueIndex:idx_permission_resource_action" json:"resource"`
	Action      string `gorm:"size:20;not null;uniqueIndex:idx_permission_resource_action" json:"action"`
	Description string `gorm:"size:200" json:"description"`

	// Group is the area of the product this permission belongs to (plan 10,
	// S1). The role editor used to render a flat alphabetical list of raw
	// resource names, which became unreadable as the CRM added a dozen more;
	// grouping is what keeps it a page somebody can actually configure.
	Group string `gorm:"size:40;not null;default:'other'" json:"group"`
}

// Permission groups. The value is stored, so renaming one changes what the
// role editor shows; the strings are deliberately plain.
const (
	PermissionGroupInbox     = "inbox"
	PermissionGroupCRM       = "crm"
	PermissionGroupMessaging = "messaging"
	PermissionGroupCalling   = "calling"
	PermissionGroupInsights  = "insights"
	PermissionGroupAdmin     = "admin"
	PermissionGroupOther     = "other"
)

// PermissionGroupFor buckets a resource into an area of the product.
//
// It is a function rather than a column on every catalog entry so that adding a
// permission cannot silently leave it ungrouped: anything unrecognised lands in
// "other", which is visible in the UI and obviously wrong.
func PermissionGroupFor(resource string) string {
	switch resource {
	case ResourceChat, ResourceChatAssign, ResourceTransfers, ResourceCannedResponses:
		return PermissionGroupInbox

	case ResourceContacts, ResourceContactFields, ResourceTags, ResourceTasks,
		ResourceSegments, ResourcePipelines, ResourceDeals, ResourceAutomations:
		return PermissionGroupCRM

	case ResourceAccounts, ResourceTemplates, ResourceCampaigns,
		ResourceFlowsWhatsApp, ResourceFlowsChatbot,
		ResourceChatbotKeywords, ResourceChatbotAI,
		ResourceSettingsChatbot, ResourceSettingsChatbotAgents,
		ResourceSettingsChatbotHours, ResourceSettingsChatbotSLA,
		ResourceSettingsChatbotAI:
		return PermissionGroupMessaging

	case ResourceCallLogs, ResourceIVRFlows, ResourceCallTransfers,
		ResourceOutgoingCalls, ResourceSettingsCalling:
		return PermissionGroupCalling

	case ResourceAnalytics, ResourceAnalyticsAgents, ResourceReports:
		return PermissionGroupInsights

	case ResourceUsers, ResourceRoles, ResourceTeams, ResourceAPIKeys,
		ResourceWebhooks, ResourceCustomActions, ResourceOrganizations,
		ResourceAuditLogs, ResourceSettingsGeneral, ResourceSettingsSSO:
		return PermissionGroupAdmin
	}
	return PermissionGroupOther
}

func (Permission) TableName() string {
	return "permissions"
}

// CustomRole represents a role with specific permissions
type CustomRole struct {
	BaseModel
	OrganizationID uuid.UUID    `gorm:"type:uuid;index;not null" json:"organization_id"`
	Name           string       `gorm:"size:100;not null" json:"name"`
	Description    string       `gorm:"size:500" json:"description"`
	IsSystem       bool         `gorm:"default:false" json:"is_system"`  // true for default admin/manager/agent
	IsDefault      bool         `gorm:"default:false" json:"is_default"` // default role for new users in org
	Permissions    []Permission `gorm:"many2many:role_permissions;" json:"permissions"`

	// Relations
	Organization *Organization `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
}

func (CustomRole) TableName() string {
	return "custom_roles"
}

// RolePermission is the junction table for CustomRole and Permission many-to-many relationship
type RolePermission struct {
	CustomRoleID uuid.UUID `gorm:"type:uuid;primaryKey" json:"custom_role_id"`
	PermissionID uuid.UUID `gorm:"type:uuid;primaryKey" json:"permission_id"`
}

func (RolePermission) TableName() string {
	return "role_permissions"
}

// PermissionResource constants for available resources
const (
	ResourceUsers                = "users"
	ResourceTeams                = "teams"
	ResourceRoles                = "roles"
	ResourceSettingsGeneral      = "settings.general"
	ResourceSettingsChatbot      = "settings.chatbot"
	ResourceSettingsSSO          = "settings.sso"
	ResourceSettingsCalling      = "settings.calling"
	ResourceSettingsNotification = "settings.notification"
	// Chatbot sub-resources — used only as audit_log resource_type values
	// for per-tab activity feeds, not checked by the permission system.
	ResourceSettingsChatbotMessages = "settings.chatbot.messages"
	ResourceSettingsChatbotAgents   = "settings.chatbot.agents"
	ResourceSettingsChatbotHours    = "settings.chatbot.hours"
	ResourceSettingsChatbotSLA      = "settings.chatbot.sla"
	ResourceSettingsChatbotAI       = "settings.chatbot.ai"
	ResourceAccounts                = "accounts"
	ResourceTemplates               = "templates"
	ResourceFlowsWhatsApp           = "flows.whatsapp"
	ResourceFlowsChatbot            = "flows.chatbot"
	ResourceCampaigns               = "campaigns"
	ResourceChatbotKeywords         = "chatbot.keywords"
	ResourceChatbotAI               = "chatbot.ai"
	ResourceChat                    = "chat"
	ResourceChatAssign              = "chat.assign"
	ResourceContacts                = "contacts"
	ResourceContactFields           = "contact_fields"
	ResourceTasks                   = "tasks"
	ResourcePipelines               = "pipelines"
	ResourceDeals                   = "deals"
	ResourceAutomations             = "automations"
	ResourceReports                 = "reports"
	ResourceSegments                = "segments"
	ResourceTags                    = "tags"
	ResourceAnalytics               = "analytics"
	ResourceAnalyticsAgents         = "analytics.agents"
	ResourceTransfers               = "transfers"
	ResourceWebhooks                = "webhooks"
	ResourceAPIKeys                 = "api_keys"
	ResourceCannedResponses         = "canned_responses"
	ResourceCustomActions           = "custom_actions"
	ResourceOrganizations           = "organizations"
	ResourceCallLogs                = "call_logs"
	ResourceIVRFlows                = "ivr_flows"
	ResourceCallTransfers           = "call_transfers"
	ResourceOutgoingCalls           = "outgoing_calls"
	ResourceAuditLogs               = "audit_logs"
)

// PermissionAction constants for available actions
const (
	ActionRead    = "read"
	ActionWrite   = "write"
	ActionDelete  = "delete"
	ActionSync    = "sync"
	ActionExecute = "execute"
	ActionImport  = "import"
	ActionExport  = "export"
	ActionPickup  = "pickup"
	ActionAssign  = "assign"
)

// DefaultPermissions returns the list of all available permissions to seed
func DefaultPermissions() []Permission {
	return []Permission{
		// Users
		{Resource: ResourceUsers, Action: ActionRead, Description: "View users"},
		{Resource: ResourceUsers, Action: ActionWrite, Description: "Create and edit users"},
		{Resource: ResourceUsers, Action: ActionDelete, Description: "Delete users"},

		// Teams
		{Resource: ResourceTeams, Action: ActionRead, Description: "View teams"},
		{Resource: ResourceTeams, Action: ActionWrite, Description: "Create and edit teams"},
		{Resource: ResourceTeams, Action: ActionDelete, Description: "Delete teams"},

		// Roles
		{Resource: ResourceRoles, Action: ActionRead, Description: "View roles"},
		{Resource: ResourceRoles, Action: ActionWrite, Description: "Create and edit roles"},
		{Resource: ResourceRoles, Action: ActionDelete, Description: "Delete roles"},

		// Settings
		{Resource: ResourceSettingsGeneral, Action: ActionRead, Description: "View general settings"},
		{Resource: ResourceSettingsGeneral, Action: ActionWrite, Description: "Edit general settings"},
		{Resource: ResourceSettingsChatbot, Action: ActionRead, Description: "View chatbot settings"},
		{Resource: ResourceSettingsChatbot, Action: ActionWrite, Description: "Edit chatbot settings"},
		{Resource: ResourceSettingsSSO, Action: ActionRead, Description: "View SSO settings"},
		{Resource: ResourceSettingsSSO, Action: ActionWrite, Description: "Edit SSO settings"},

		// Accounts
		{Resource: ResourceAccounts, Action: ActionRead, Description: "View WhatsApp accounts"},
		{Resource: ResourceAccounts, Action: ActionWrite, Description: "Create and edit WhatsApp accounts"},
		{Resource: ResourceAccounts, Action: ActionDelete, Description: "Delete WhatsApp accounts"},

		// Templates
		{Resource: ResourceTemplates, Action: ActionRead, Description: "View message templates"},
		{Resource: ResourceTemplates, Action: ActionWrite, Description: "Create and edit templates"},
		{Resource: ResourceTemplates, Action: ActionDelete, Description: "Delete templates"},
		{Resource: ResourceTemplates, Action: ActionSync, Description: "Sync templates with Meta"},

		// WhatsApp Flows
		{Resource: ResourceFlowsWhatsApp, Action: ActionRead, Description: "View WhatsApp flows"},
		{Resource: ResourceFlowsWhatsApp, Action: ActionWrite, Description: "Create and edit WhatsApp flows"},
		{Resource: ResourceFlowsWhatsApp, Action: ActionDelete, Description: "Delete WhatsApp flows"},

		// Chatbot Flows
		{Resource: ResourceFlowsChatbot, Action: ActionRead, Description: "View chatbot flows"},
		{Resource: ResourceFlowsChatbot, Action: ActionWrite, Description: "Create and edit chatbot flows"},
		{Resource: ResourceFlowsChatbot, Action: ActionDelete, Description: "Delete chatbot flows"},

		// Campaigns
		{Resource: ResourceCampaigns, Action: ActionRead, Description: "View campaigns"},
		{Resource: ResourceCampaigns, Action: ActionWrite, Description: "Create and edit campaigns"},
		{Resource: ResourceCampaigns, Action: ActionDelete, Description: "Delete campaigns"},
		{Resource: ResourceCampaigns, Action: ActionExecute, Description: "Execute campaigns"},

		// Chatbot Keywords
		{Resource: ResourceChatbotKeywords, Action: ActionRead, Description: "View keyword rules"},
		{Resource: ResourceChatbotKeywords, Action: ActionWrite, Description: "Create and edit keyword rules"},
		{Resource: ResourceChatbotKeywords, Action: ActionDelete, Description: "Delete keyword rules"},

		// Chatbot AI
		{Resource: ResourceChatbotAI, Action: ActionRead, Description: "View AI contexts"},
		{Resource: ResourceChatbotAI, Action: ActionWrite, Description: "Create and edit AI contexts"},
		{Resource: ResourceChatbotAI, Action: ActionDelete, Description: "Delete AI contexts"},

		// Chat
		{Resource: ResourceChat, Action: ActionRead, Description: "View chat conversations"},
		{Resource: ResourceChat, Action: ActionWrite, Description: "Send messages"},
		{Resource: ResourceChatAssign, Action: ActionWrite, Description: "Assign conversations to agents"},

		// Contacts
		{Resource: ResourceContacts, Action: ActionRead, Description: "View contacts"},
		{Resource: ResourceContacts, Action: ActionWrite, Description: "Create and edit contacts"},
		{Resource: ResourceContacts, Action: ActionDelete, Description: "Delete contacts"},
		{Resource: ResourceContacts, Action: ActionImport, Description: "Import contacts"},
		{Resource: ResourceContacts, Action: ActionExport, Description: "Export contacts"},

		// Contact fields (plan 01). Defining a field changes what every
		// contact record can hold, so it is a separate permission from
		// editing a contact.
		{Resource: ResourceContactFields, Action: ActionRead, Description: "View contact fields"},
		{Resource: ResourceContactFields, Action: ActionWrite, Description: "Create and edit contact fields"},
		{Resource: ResourceContactFields, Action: ActionDelete, Description: "Delete contact fields"},

		// Tasks (plan 04)
		{Resource: ResourceTasks, Action: ActionRead, Description: "View tasks"},
		{Resource: ResourceTasks, Action: ActionWrite, Description: "Create and edit tasks"},
		{Resource: ResourceTasks, Action: ActionDelete, Description: "Delete tasks"},

		// Pipelines and deals (plan 07). Shaping the board is a separate
		// permission from working it: an agent moves cards, a manager decides
		// what the columns mean.
		{Resource: ResourcePipelines, Action: ActionRead, Description: "View pipelines"},
		{Resource: ResourcePipelines, Action: ActionWrite, Description: "Create and edit pipelines and stages"},
		{Resource: ResourcePipelines, Action: ActionDelete, Description: "Delete pipelines and stages"},
		{Resource: ResourceDeals, Action: ActionRead, Description: "View deals"},
		{Resource: ResourceDeals, Action: ActionWrite, Description: "Create, edit and move deals"},
		{Resource: ResourceDeals, Action: ActionDelete, Description: "Delete deals"},

		// Automations (plan 08). A rule can message customers, so writing one
		// is a larger permission than any single action it performs.
		{Resource: ResourceAutomations, Action: ActionRead, Description: "View automations and their run history"},
		{Resource: ResourceAutomations, Action: ActionWrite, Description: "Create, edit, enable and test automations"},
		{Resource: ResourceAutomations, Action: ActionDelete, Description: "Delete automations"},

		// CRM reports (plan 09). Reading these means reading the whole team's
		// numbers, which is a different question from reading the dashboard.
		{Resource: ResourceReports, Action: ActionRead, Description: "View CRM reports"},
		{Resource: ResourceReports, Action: ActionExport, Description: "Export CRM reports"},

		// Segments (plan 05). A saved audience is what a campaign is aimed at,
		// so editing one changes who gets messaged.
		{Resource: ResourceSegments, Action: ActionRead, Description: "View segments"},
		{Resource: ResourceSegments, Action: ActionWrite, Description: "Create and edit segments"},
		{Resource: ResourceSegments, Action: ActionDelete, Description: "Delete segments"},

		// Tags
		{Resource: ResourceTags, Action: ActionRead, Description: "View tags"},
		{Resource: ResourceTags, Action: ActionWrite, Description: "Create and edit tags"},
		{Resource: ResourceTags, Action: ActionDelete, Description: "Delete tags"},
		// import_export.go serves tags as well as contacts and checks
		// tags:import / tags:export. Without these rows nobody could ever hold
		// the permission, so the feature was unreachable rather than
		// restricted (plan 10, X11).
		{Resource: ResourceTags, Action: ActionImport, Description: "Import tags"},
		{Resource: ResourceTags, Action: ActionExport, Description: "Export tags"},

		// Analytics
		{Resource: ResourceAnalytics, Action: ActionRead, Description: "View analytics dashboard"},
		{Resource: ResourceAnalytics, Action: ActionWrite, Description: "Create and edit dashboard widgets"},
		{Resource: ResourceAnalytics, Action: ActionDelete, Description: "Delete dashboard widgets"},
		{Resource: ResourceAnalyticsAgents, Action: ActionRead, Description: "View agent analytics"},

		// Transfers
		{Resource: ResourceTransfers, Action: ActionRead, Description: "View agent transfers"},
		{Resource: ResourceTransfers, Action: ActionWrite, Description: "Create transfers"},
		{Resource: ResourceTransfers, Action: ActionPickup, Description: "Pickup transfers from queue"},

		// Webhooks
		{Resource: ResourceWebhooks, Action: ActionRead, Description: "View webhooks"},
		{Resource: ResourceWebhooks, Action: ActionWrite, Description: "Create and edit webhooks"},
		{Resource: ResourceWebhooks, Action: ActionDelete, Description: "Delete webhooks"},

		// API Keys
		{Resource: ResourceAPIKeys, Action: ActionRead, Description: "View API keys"},
		{Resource: ResourceAPIKeys, Action: ActionWrite, Description: "Create API keys"},
		{Resource: ResourceAPIKeys, Action: ActionDelete, Description: "Delete API keys"},

		// Canned Responses
		{Resource: ResourceCannedResponses, Action: ActionRead, Description: "View canned responses"},
		{Resource: ResourceCannedResponses, Action: ActionWrite, Description: "Create and edit canned responses"},
		{Resource: ResourceCannedResponses, Action: ActionDelete, Description: "Delete canned responses"},

		// Custom Actions
		{Resource: ResourceCustomActions, Action: ActionRead, Description: "View custom actions"},
		{Resource: ResourceCustomActions, Action: ActionWrite, Description: "Create and edit custom actions"},
		{Resource: ResourceCustomActions, Action: ActionDelete, Description: "Delete custom actions"},

		// Organizations
		{Resource: ResourceOrganizations, Action: ActionRead, Description: "View organizations"},
		{Resource: ResourceOrganizations, Action: ActionWrite, Description: "Create organizations"},
		{Resource: ResourceOrganizations, Action: ActionDelete, Description: "Delete organizations"},
		{Resource: ResourceOrganizations, Action: ActionAssign, Description: "Manage organization members"},

		// Call Logs
		{Resource: ResourceCallLogs, Action: ActionRead, Description: "View call logs"},

		// IVR Flows
		{Resource: ResourceIVRFlows, Action: ActionRead, Description: "View IVR flows"},
		{Resource: ResourceIVRFlows, Action: ActionWrite, Description: "Create and edit IVR flows"},
		{Resource: ResourceIVRFlows, Action: ActionDelete, Description: "Delete IVR flows"},

		// Call Transfers
		{Resource: ResourceCallTransfers, Action: ActionRead, Description: "View call transfers"},
		{Resource: ResourceCallTransfers, Action: ActionWrite, Description: "Accept and manage call transfers"},

		// Outgoing Calls
		{Resource: ResourceOutgoingCalls, Action: ActionRead, Description: "View outgoing call status"},
		{Resource: ResourceOutgoingCalls, Action: ActionWrite, Description: "Initiate outgoing calls"},

		// Audit Logs
		{Resource: ResourceAuditLogs, Action: ActionRead, Description: "View audit logs"},
	}
}

// SystemRolePermissions returns the default permission mappings for system roles
func SystemRolePermissions() map[string][]string {
	// Format: "resource:action"
	allPermissions := []string{}
	for _, p := range DefaultPermissions() {
		allPermissions = append(allPermissions, p.Resource+":"+p.Action)
	}

	managerPermissions := []string{
		// Teams (read only)
		"teams:read",
		// Settings
		"settings.general:read", "settings.general:write",
		"settings.chatbot:read", "settings.chatbot:write",
		// Accounts
		"accounts:read", "accounts:write", "accounts:delete",
		// Templates
		"templates:read", "templates:write", "templates:delete", "templates:sync",
		// Flows
		"flows.whatsapp:read", "flows.whatsapp:write", "flows.whatsapp:delete",
		"flows.chatbot:read", "flows.chatbot:write", "flows.chatbot:delete",
		// Campaigns
		"campaigns:read", "campaigns:write", "campaigns:delete", "campaigns:execute",
		// Chatbot
		"chatbot.keywords:read", "chatbot.keywords:write", "chatbot.keywords:delete",
		"chatbot.ai:read", "chatbot.ai:write", "chatbot.ai:delete",
		// Chat
		"chat:read", "chat:write", "chat.assign:write",
		// Contacts
		"contacts:read", "contacts:write", "contacts:delete", "contacts:import", "contacts:export",
		// Contact fields: a manager shapes the record, an admin can remove
		// fields entirely (which discards their values).
		"contact_fields:read", "contact_fields:write",
		// Tasks
		"tasks:read", "tasks:write", "tasks:delete",
		// Pipelines and deals
		"pipelines:read", "pipelines:write", "pipelines:delete",
		"deals:read", "deals:write", "deals:delete",
		// Automations: a manager shapes how the team's work is chased
		"automations:read", "automations:write",
		// CRM reports
		"reports:read", "reports:export",
		// Segments
		"segments:read", "segments:write", "segments:delete",
		// Tags
		// Managers already manage tags outright and hold contacts:import /
		// contacts:export; import/export is one page, so splitting the two
		// resources across roles would only make it half-work for them.
		"tags:read", "tags:write", "tags:delete", "tags:import", "tags:export",
		// Analytics
		"analytics:read", "analytics.agents:read",
		// Transfers
		"transfers:read", "transfers:write", "transfers:pickup",
		// Webhooks
		"webhooks:read", "webhooks:write", "webhooks:delete",
		// Canned Responses
		"canned_responses:read", "canned_responses:write", "canned_responses:delete",
		// Custom Actions
		"custom_actions:read", "custom_actions:write", "custom_actions:delete",
		// Organizations (read only)
		"organizations:read",
		// Calling
		"call_logs:read",
		"ivr_flows:read", "ivr_flows:write", "ivr_flows:delete",
		"call_transfers:read", "call_transfers:write",
		"outgoing_calls:read", "outgoing_calls:write",
	}

	agentPermissions := []string{
		// Accounts (read only)
		"accounts:read",
		// Chat
		"chat:read", "chat:write",
		// Contacts (read only)
		"contacts:read",
		// Contact fields: agents see field values, they do not shape the record
		"contact_fields:read",
		// Tasks: agents own and complete their own follow-ups
		"tasks:read", "tasks:write",
		// Deals: agents work the board, they do not redesign it
		"pipelines:read",
		"deals:read", "deals:write",
		// Segments: an agent can filter a list; saving an audience that a
		// campaign will message is a supervisor's decision.
		"segments:read",
		// Tags (read only - agents can see tags on contacts)
		"tags:read",
		// Analytics (own)
		"analytics.agents:read",
		// Transfers
		"transfers:read", "transfers:write", "transfers:pickup",
		// Canned Responses (read only)
		"canned_responses:read",
		// Call Logs: agents see only their own (no call_logs:read permission)
		// Call Transfers
		"call_transfers:read", "call_transfers:write",
		// Outgoing Calls
		"outgoing_calls:read", "outgoing_calls:write",
	}

	return map[string][]string{
		"admin":   allPermissions,
		"manager": managerPermissions,
		"agent":   agentPermissions,
	}
}
