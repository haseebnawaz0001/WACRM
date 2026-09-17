package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/zerodha/fastglue"
)

// Route access declarations (plan 10, S1).
//
// Server-side permission checks live inside the handlers, which is the right
// place for them but the wrong place to audit them: reading two hundred
// handler bodies is the only way to answer "is this endpoint protected?", and
// a new route added without a check looks exactly like a route that is
// deliberately public. That is how the product ended up shipping whole handler
// files — campaigns, templates, webhooks, custom actions, canned responses,
// roles, messages, flows — where an agent without the permission got 200.
//
// This table states the intent for every registered /api route, and
// checkRoutePermissions compares it against the routes fastglue actually has
// at startup. A route with no entry fails the check, so the next person to add
// an endpoint has to say what protects it — even if the answer is "nothing".
// The reverse is also checked: an entry for a route that no longer exists is a
// stale claim of protection, so it fails too.
//
// The resource/action pair records the permission the handler enforces. It is
// documentation and a review aid, not a second enforcement point: putting the
// check here as well would mean two places to change and a silent divergence
// the first time they disagree.
type accessKind uint8

const (
	// accessPermission: the handler requires resource:action.
	accessPermission accessKind = iota
	// accessPublic: no authentication at all. Login, Meta's webhook, and the
	// one-time-token custom action redirect.
	accessPublic
	// accessSelf: any authenticated user, acting on their own data — their
	// profile, their notifications, their organizations.
	accessSelf
	// accessScoped: any authenticated user; which rows they see is decided by
	// the contact/conversation scope (S9), not by a flat permission.
	accessScoped
)

type routeAccess struct {
	kind     accessKind
	resource string
	action   string
}

func needs(resource, action string) routeAccess {
	return routeAccess{kind: accessPermission, resource: resource, action: action}
}
func public() routeAccess { return routeAccess{kind: accessPublic} }
func self() routeAccess   { return routeAccess{kind: accessSelf} }
func scoped() routeAccess { return routeAccess{kind: accessScoped} }

// routePermissions maps "METHOD /path" to how that route is protected.
var routePermissions = map[string]routeAccess{
	"GET /api/accounts":                                      needs(models.ResourceAccounts, models.ActionRead),          // ListAccounts
	"POST /api/accounts":                                     needs(models.ResourceAccounts, models.ActionWrite),         // CreateAccount
	"POST /api/accounts/exchange-token":                      needs(models.ResourceAccounts, models.ActionWrite),         // ExchangeToken
	"DELETE /api/accounts/{id}":                              needs(models.ResourceAccounts, models.ActionDelete),        // DeleteAccount
	"GET /api/accounts/{id}":                                 needs(models.ResourceAccounts, models.ActionRead),          // GetAccount
	"PUT /api/accounts/{id}":                                 needs(models.ResourceAccounts, models.ActionWrite),         // UpdateAccount
	"GET /api/accounts/{id}/business_profile":                needs(models.ResourceAccounts, models.ActionRead),          // GetBusinessProfile
	"PUT /api/accounts/{id}/business_profile":                needs(models.ResourceAccounts, models.ActionWrite),         // UpdateBusinessProfile
	"POST /api/accounts/{id}/business_profile/photo":         needs(models.ResourceAccounts, models.ActionWrite),         // UpdateProfilePicture
	"POST /api/accounts/{id}/register":                       needs(models.ResourceAccounts, models.ActionWrite),         // RegisterPhoneNumber
	"POST /api/accounts/{id}/subscribe":                      needs(models.ResourceAccounts, models.ActionWrite),         // SubscribeApp
	"POST /api/accounts/{id}/test":                           needs(models.ResourceAccounts, models.ActionWrite),         // TestAccountConnection
	"GET /api/analytics/agents":                              needs(models.ResourceAnalytics, models.ActionRead),         // GetAgentAnalytics
	"GET /api/analytics/agents/comparison":                   needs(models.ResourceAnalytics, models.ActionRead),         // GetAgentComparison
	"GET /api/analytics/agents/{id}":                         needs(models.ResourceAnalytics, models.ActionRead),         // GetAgentDetails
	"GET /api/analytics/chatbot":                             needs(models.ResourceAnalytics, models.ActionRead),         // GetChatbotAnalytics
	"GET /api/analytics/dashboard":                           needs(models.ResourceAnalytics, models.ActionRead),         // GetDashboardStats
	"GET /api/analytics/messages":                            needs(models.ResourceAnalytics, models.ActionRead),         // GetMessageAnalytics
	"GET /api/analytics/meta":                                needs(models.ResourceAnalytics, models.ActionRead),         // GetMetaAnalytics
	"GET /api/analytics/meta/accounts":                       needs(models.ResourceAnalytics, models.ActionRead),         // ListMetaAccountsForAnalytics
	"POST /api/analytics/meta/refresh":                       needs(models.ResourceAnalytics, models.ActionRead),         // RefreshMetaAnalyticsCache
	"GET /api/api-keys":                                      needs(models.ResourceAPIKeys, models.ActionRead),           // ListAPIKeys
	"POST /api/api-keys":                                     needs(models.ResourceAPIKeys, models.ActionWrite),          // CreateAPIKey
	"DELETE /api/api-keys/{id}":                              needs(models.ResourceAPIKeys, models.ActionDelete),         // DeleteAPIKey
	"GET /api/api-keys/{id}":                                 needs(models.ResourceAPIKeys, models.ActionRead),           // GetAPIKey
	"PUT /api/api-keys/{id}":                                 needs(models.ResourceAPIKeys, models.ActionWrite),          // UpdateAPIKey
	"GET /api/audit-logs":                                    needs(models.ResourceAuditLogs, models.ActionRead),         // ListAuditLogs
	"GET /api/audit-logs/catalog":                            needs(models.ResourceAuditLogs, models.ActionRead),         // GetAuditCatalog
	"GET /api/audit-logs/{id}":                               needs(models.ResourceAuditLogs, models.ActionRead),         // GetAuditLog
	"POST /api/auth/login":                                   public(),                                                   // Login
	"POST /api/auth/logout":                                  public(),                                                   // Logout
	"POST /api/auth/refresh":                                 public(),                                                   // RefreshToken
	"POST /api/auth/register":                                public(),                                                   // Register
	"GET /api/auth/sso/providers":                            public(),                                                   // GetPublicSSOProviders
	"GET /api/auth/sso/{provider}/callback":                  public(),                                                   // CallbackSSO
	"GET /api/auth/sso/{provider}/init":                      public(),                                                   // InitSSO
	"POST /api/auth/switch-org":                              self(),                                                     // SwitchOrg
	"GET /api/auth/ws-token":                                 self(),                                                     // GetWSToken
	"GET /api/automations":                                   needs(models.ResourceAutomations, models.ActionRead),       // ListAutomations
	"POST /api/automations":                                  needs(models.ResourceAutomations, models.ActionWrite),      // CreateAutomation
	"GET /api/automations/catalog":                           needs(models.ResourceAutomations, models.ActionRead),       // AutomationCatalog
	"DELETE /api/automations/{id}":                           needs(models.ResourceAutomations, models.ActionDelete),     // DeleteAutomation
	"GET /api/automations/{id}":                              needs(models.ResourceAutomations, models.ActionRead),       // GetAutomation
	"PUT /api/automations/{id}":                              needs(models.ResourceAutomations, models.ActionWrite),      // UpdateAutomation
	"POST /api/automations/{id}/disable":                     needs(models.ResourceAutomations, models.ActionWrite),      // DisableAutomation
	"POST /api/automations/{id}/enable":                      needs(models.ResourceAutomations, models.ActionWrite),      // EnableAutomation
	"GET /api/automations/{id}/runs":                         needs(models.ResourceAutomations, models.ActionRead),       // AutomationRuns
	"POST /api/automations/{id}/test":                        needs(models.ResourceAutomations, models.ActionWrite),      // TestAutomation
	"GET /api/call-logs":                                     needs(models.ResourceCallLogs, models.ActionRead),          // ListCallLogs
	"GET /api/call-logs/{id}":                                needs(models.ResourceCallLogs, models.ActionRead),          // GetCallLog
	"POST /api/call-logs/{id}/hold":                          needs(models.ResourceCallTransfers, models.ActionWrite),    // HoldCall
	"GET /api/call-logs/{id}/recording":                      needs(models.ResourceCallLogs, models.ActionRead),          // GetCallRecording
	"POST /api/call-logs/{id}/resume":                        needs(models.ResourceCallTransfers, models.ActionWrite),    // ResumeCall
	"POST /api/call-logs/{id}/outcome":                       needs(models.ResourceCallLogs, models.ActionRead),          // RecordCallOutcome — call_logs:read, or the agent who took the call
	"GET /api/call-transfers":                                needs(models.ResourceCallTransfers, models.ActionRead),     // ListCallTransfers
	"POST /api/call-transfers/initiate":                      needs(models.ResourceCallTransfers, models.ActionWrite),    // InitiateAgentTransfer
	"GET /api/call-transfers/{id}":                           needs(models.ResourceCallTransfers, models.ActionRead),     // GetCallTransfer
	"POST /api/call-transfers/{id}/connect":                  needs(models.ResourceCallTransfers, models.ActionWrite),    // ConnectCallTransfer
	"POST /api/call-transfers/{id}/hangup":                   needs(models.ResourceCallTransfers, models.ActionWrite),    // HangupCallTransfer
	"GET /api/calls/ice-servers":                             needs(models.ResourceOutgoingCalls, models.ActionWrite),    // GetICEServers
	"POST /api/calls/outgoing":                               needs(models.ResourceOutgoingCalls, models.ActionWrite),    // InitiateOutgoingCall
	"POST /api/calls/outgoing/{id}/hangup":                   needs(models.ResourceOutgoingCalls, models.ActionWrite),    // HangupOutgoingCall
	"POST /api/calls/permission-request":                     needs(models.ResourceOutgoingCalls, models.ActionWrite),    // SendCallPermissionRequest
	"GET /api/calls/permission/{contactId}":                  needs(models.ResourceOutgoingCalls, models.ActionRead),     // GetCallPermission
	"GET /api/campaigns":                                     needs(models.ResourceCampaigns, models.ActionRead),         // ListCampaigns
	"POST /api/campaigns":                                    needs(models.ResourceCampaigns, models.ActionWrite),        // CreateCampaign
	"DELETE /api/campaigns/{id}":                             needs(models.ResourceCampaigns, models.ActionDelete),       // DeleteCampaign
	"GET /api/campaigns/{id}":                                needs(models.ResourceCampaigns, models.ActionRead),         // GetCampaign
	"PUT /api/campaigns/{id}":                                needs(models.ResourceCampaigns, models.ActionWrite),        // UpdateCampaign
	"PUT /api/campaigns/{id}/audience":                       needs(models.ResourceCampaigns, models.ActionWrite),        // SetCampaignAudience
	"POST /api/campaigns/{id}/audience/preview":              needs(models.ResourceCampaigns, models.ActionRead),         // PreviewCampaignAudience
	"POST /api/campaigns/{id}/cancel":                        needs(models.ResourceCampaigns, models.ActionExecute),      // CancelCampaign
	"GET /api/campaigns/{id}/media":                          needs(models.ResourceCampaigns, models.ActionRead),         // ServeCampaignMedia
	"POST /api/campaigns/{id}/media":                         needs(models.ResourceCampaigns, models.ActionWrite),        // UploadCampaignMedia
	"POST /api/campaigns/{id}/pause":                         needs(models.ResourceCampaigns, models.ActionExecute),      // PauseCampaign
	"GET /api/campaigns/{id}/progress":                       needs(models.ResourceCampaigns, models.ActionRead),         // GetCampaign
	"GET /api/campaigns/{id}/recipients":                     needs(models.ResourceCampaigns, models.ActionRead),         // GetCampaignRecipients
	"POST /api/campaigns/{id}/recipients/import":             needs(models.ResourceCampaigns, models.ActionWrite),        // ImportRecipients
	"DELETE /api/campaigns/{id}/recipients/{recipientId}":    needs(models.ResourceCampaigns, models.ActionWrite),        // DeleteCampaignRecipient
	"POST /api/campaigns/{id}/retry-failed":                  needs(models.ResourceCampaigns, models.ActionExecute),      // RetryFailed
	"POST /api/campaigns/{id}/start":                         needs(models.ResourceCampaigns, models.ActionExecute),      // StartCampaign
	"GET /api/canned-responses":                              needs(models.ResourceCannedResponses, models.ActionRead),   // ListCannedResponses
	"POST /api/canned-responses":                             needs(models.ResourceCannedResponses, models.ActionWrite),  // CreateCannedResponse
	"DELETE /api/canned-responses/{id}":                      needs(models.ResourceCannedResponses, models.ActionDelete), // DeleteCannedResponse
	"GET /api/canned-responses/{id}":                         needs(models.ResourceCannedResponses, models.ActionRead),   // GetCannedResponse
	"PUT /api/canned-responses/{id}":                         needs(models.ResourceCannedResponses, models.ActionWrite),  // UpdateCannedResponse
	"POST /api/canned-responses/{id}/resolve":                needs(models.ResourceCannedResponses, models.ActionRead),   // ResolveCannedResponse
	"POST /api/canned-responses/{id}/use":                    needs(models.ResourceCannedResponses, models.ActionRead),   // IncrementCannedResponseUsage
	"GET /api/catalogs":                                      needs(models.ResourceAccounts, models.ActionRead),          // ListCatalogs
	"POST /api/catalogs":                                     needs(models.ResourceAccounts, models.ActionWrite),         // CreateCatalog
	"POST /api/catalogs/sync":                                needs(models.ResourceAccounts, models.ActionWrite),         // SyncCatalogs
	"DELETE /api/catalogs/{id}":                              needs(models.ResourceAccounts, models.ActionDelete),        // DeleteCatalog
	"GET /api/catalogs/{id}":                                 needs(models.ResourceAccounts, models.ActionRead),          // GetCatalog
	"GET /api/catalogs/{id}/products":                        needs(models.ResourceAccounts, models.ActionRead),          // ListCatalogProducts
	"POST /api/catalogs/{id}/products":                       needs(models.ResourceAccounts, models.ActionWrite),         // CreateCatalogProduct
	"GET /api/chatbot/ai-contexts":                           needs(models.ResourceChatbotAI, models.ActionRead),         // ListAIContexts
	"POST /api/chatbot/ai-contexts":                          needs(models.ResourceChatbotAI, models.ActionWrite),        // CreateAIContext
	"DELETE /api/chatbot/ai-contexts/{id}":                   needs(models.ResourceChatbotAI, models.ActionDelete),       // DeleteAIContext
	"GET /api/chatbot/ai-contexts/{id}":                      needs(models.ResourceChatbotAI, models.ActionRead),         // GetAIContext
	"PUT /api/chatbot/ai-contexts/{id}":                      needs(models.ResourceChatbotAI, models.ActionWrite),        // UpdateAIContext
	"GET /api/chatbot/flows":                                 needs(models.ResourceFlowsChatbot, models.ActionRead),      // ListChatbotFlows
	"POST /api/chatbot/flows":                                needs(models.ResourceFlowsChatbot, models.ActionWrite),     // CreateChatbotFlow
	"DELETE /api/chatbot/flows/{id}":                         needs(models.ResourceFlowsChatbot, models.ActionDelete),    // DeleteChatbotFlow
	"GET /api/chatbot/flows/{id}":                            needs(models.ResourceFlowsChatbot, models.ActionRead),      // GetChatbotFlow
	"PUT /api/chatbot/flows/{id}":                            needs(models.ResourceFlowsChatbot, models.ActionWrite),     // UpdateChatbotFlow
	"GET /api/chatbot/keywords":                              needs(models.ResourceChatbotKeywords, models.ActionRead),   // ListKeywordRules
	"POST /api/chatbot/keywords":                             needs(models.ResourceChatbotKeywords, models.ActionWrite),  // CreateKeywordRule
	"DELETE /api/chatbot/keywords/{id}":                      needs(models.ResourceChatbotKeywords, models.ActionDelete), // DeleteKeywordRule
	"GET /api/chatbot/keywords/{id}":                         needs(models.ResourceChatbotKeywords, models.ActionRead),   // GetKeywordRule
	"PUT /api/chatbot/keywords/{id}":                         needs(models.ResourceChatbotKeywords, models.ActionWrite),  // UpdateKeywordRule
	"GET /api/chatbot/sessions":                              needs(models.ResourceSettingsChatbot, models.ActionRead),   // ListChatbotSessions
	"GET /api/chatbot/sessions/{id}":                         needs(models.ResourceSettingsChatbot, models.ActionRead),   // GetChatbotSession
	"GET /api/chatbot/settings":                              needs(models.ResourceSettingsChatbot, models.ActionRead),   // GetChatbotSettings
	"PUT /api/chatbot/settings":                              needs(models.ResourceSettingsChatbot, models.ActionWrite),  // UpdateChatbotSettings
	"GET /api/chatbot/transfers":                             needs(models.ResourceTransfers, models.ActionWrite),        // ListAgentTransfers
	"POST /api/chatbot/transfers":                            needs(models.ResourceTransfers, models.ActionWrite),        // CreateAgentTransfer
	"POST /api/chatbot/transfers/pick":                       needs(models.ResourceTransfers, models.ActionWrite),        // PickNextTransfer
	"PUT /api/chatbot/transfers/{id}/assign":                 needs(models.ResourceTransfers, models.ActionWrite),        // AssignAgentTransfer
	"PUT /api/chatbot/transfers/{id}/resume":                 needs(models.ResourceTransfers, models.ActionWrite),        // ResumeFromTransfer
	"GET /api/contact-fields":                                needs(models.ResourceContactFields, models.ActionRead),     // ListContactFields
	"POST /api/contact-fields":                               needs(models.ResourceContactFields, models.ActionWrite),    // CreateContactField
	"DELETE /api/contact-fields/{id}":                        needs(models.ResourceContactFields, models.ActionDelete),   // DeleteContactField
	"PUT /api/contact-fields/{id}":                           needs(models.ResourceContactFields, models.ActionWrite),    // UpdateContactField
	"GET /api/contacts":                                      scoped(),                                                   // ListContacts
	"POST /api/contacts":                                     needs(models.ResourceContacts, models.ActionWrite),         // CreateContact
	"GET /api/contacts/duplicates":                           needs(models.ResourceContacts, models.ActionWrite),         // ListDuplicates
	"POST /api/contacts/duplicates/scan":                     needs(models.ResourceContacts, models.ActionWrite),         // ScanDuplicates
	"POST /api/contacts/duplicates/{id}/dismiss":             needs(models.ResourceContacts, models.ActionWrite),         // DismissDuplicate
	"GET /api/contacts/filter-fields":                        needs(models.ResourceContacts, models.ActionRead),          // GetContactFilterFields
	"POST /api/contacts/merge":                               needs(models.ResourceContacts, models.ActionDelete),        // MergeContacts
	"POST /api/contacts/bulk":                                needs(models.ResourceContacts, models.ActionWrite),         // BulkUpdateContacts
	"POST /api/contacts/search":                              needs(models.ResourceContacts, models.ActionRead),          // SearchContacts
	"DELETE /api/contacts/{id}":                              needs(models.ResourceContacts, models.ActionDelete),        // DeleteContact
	"GET /api/contacts/{id}":                                 needs(models.ResourceContacts, models.ActionRead),          // GetContact
	"PUT /api/contacts/{id}":                                 needs(models.ResourceContacts, models.ActionWrite),         // UpdateContact
	"PUT /api/contacts/{id}/assign":                          needs(models.ResourceContacts, models.ActionWrite),         // AssignContact
	"GET /api/contacts/{id}/automation-runs":                 needs(models.ResourceAutomations, models.ActionRead),       // ContactAutomationRuns
	"GET /api/contacts/{id}/conversation":                    needs(models.ResourceChat, models.ActionRead),              // GetConversation
	"GET /api/contacts/{id}/deals":                           needs(models.ResourceDeals, models.ActionRead),             // ContactDeals
	"POST /api/contacts/{id}/mark-read":                      needs(models.ResourceChat, models.ActionRead),              // MarkContactRead
	"GET /api/contacts/{id}/merges":                          needs(models.ResourceContacts, models.ActionRead),          // ContactMergeHistory
	"GET /api/contacts/{id}/messages":                        needs(models.ResourceContacts, models.ActionRead),          // GetMessages
	"POST /api/contacts/{id}/messages":                       needs(models.ResourceChat, models.ActionWrite),             // SendMessage
	"POST /api/contacts/{id}/messages/{message_id}/reaction": needs(models.ResourceChat, models.ActionWrite),             // SendReaction
	"GET /api/contacts/{id}/notes":                           needs(models.ResourceChat, models.ActionRead),              // ListConversationNotes
	"POST /api/contacts/{id}/notes":                          needs(models.ResourceChat, models.ActionWrite),             // CreateConversationNote
	"DELETE /api/contacts/{id}/notes/{note_id}":              needs(models.ResourceChat, models.ActionWrite),             // DeleteConversationNote
	"PUT /api/contacts/{id}/notes/{note_id}":                 needs(models.ResourceChat, models.ActionWrite),             // UpdateConversationNote
	"GET /api/contacts/{id}/session-data":                    needs(models.ResourceContacts, models.ActionRead),          // GetContactSessionData
	"PUT /api/contacts/{id}/tags":                            needs(models.ResourceContacts, models.ActionWrite),         // UpdateContactTags
	"GET /api/contacts/{id}/timeline":                        needs(models.ResourceContacts, models.ActionRead),          // GetContactTimeline
	"POST /api/conversations/assign":                         needs(models.ResourceChatAssign, models.ActionWrite),       // AssignConversation
	"POST /api/conversations/pending":                        needs(models.ResourceChat, models.ActionWrite),             // MarkConversationPending
	"POST /api/conversations/reopen":                         needs(models.ResourceChat, models.ActionWrite),             // ReopenConversation
	"POST /api/conversations/resolve":                        needs(models.ResourceChat, models.ActionWrite),             // ResolveConversation
	"POST /api/conversations/snooze":                         needs(models.ResourceChat, models.ActionWrite),             // SnoozeConversation
	"GET /api/custom-actions":                                needs(models.ResourceCustomActions, models.ActionRead),     // ListCustomActions
	"POST /api/custom-actions":                               needs(models.ResourceCustomActions, models.ActionWrite),    // CreateCustomAction
	"GET /api/custom-actions/redirect/{token}":               public(),                                                   // CustomActionRedirect
	"DELETE /api/custom-actions/{id}":                        needs(models.ResourceCustomActions, models.ActionDelete),   // DeleteCustomAction
	"GET /api/custom-actions/{id}":                           needs(models.ResourceCustomActions, models.ActionRead),     // GetCustomAction
	"PUT /api/custom-actions/{id}":                           needs(models.ResourceCustomActions, models.ActionWrite),    // UpdateCustomAction
	"POST /api/custom-actions/{id}/execute":                  needs(models.ResourceChat, models.ActionWrite),             // ExecuteCustomAction
	"GET /api/deals":                                         needs(models.ResourceDeals, models.ActionRead),             // ListDeals
	"POST /api/deals":                                        needs(models.ResourceDeals, models.ActionWrite),            // CreateDeal
	"DELETE /api/deals/{id}":                                 needs(models.ResourceDeals, models.ActionDelete),           // DeleteDeal
	"GET /api/deals/{id}":                                    needs(models.ResourceDeals, models.ActionRead),             // GetDeal
	"PUT /api/deals/{id}":                                    needs(models.ResourceDeals, models.ActionWrite),            // UpdateDeal
	"GET /api/deals/{id}/history":                            needs(models.ResourceDeals, models.ActionRead),             // DealHistory
	"POST /api/deals/{id}/move":                              needs(models.ResourceDeals, models.ActionWrite),            // MoveDeal
	"GET /api/embedded-signup/config":                        public(),                                                   // GetEmbeddedSignupConfig
	"POST /api/export":                                       needs("config.Resource", models.ActionExport),              // ExportData
	"GET /api/export/{table}/config":                         needs("config.Resource", models.ActionExport),              // GetExportConfig
	"GET /api/flows":                                         needs(models.ResourceFlowsWhatsApp, models.ActionRead),     // ListFlows
	"POST /api/flows":                                        needs(models.ResourceFlowsWhatsApp, models.ActionWrite),    // CreateFlow
	"POST /api/flows/sync":                                   needs(models.ResourceFlowsWhatsApp, models.ActionWrite),    // SyncFlows
	"DELETE /api/flows/{id}":                                 needs(models.ResourceFlowsWhatsApp, models.ActionDelete),   // DeleteFlow
	"GET /api/flows/{id}":                                    needs(models.ResourceFlowsWhatsApp, models.ActionRead),     // GetFlow
	"PUT /api/flows/{id}":                                    needs(models.ResourceFlowsWhatsApp, models.ActionWrite),    // UpdateFlow
	"POST /api/flows/{id}/deprecate":                         needs(models.ResourceFlowsWhatsApp, models.ActionWrite),    // DeprecateFlow
	"POST /api/flows/{id}/duplicate":                         needs(models.ResourceFlowsWhatsApp, models.ActionWrite),    // DuplicateFlow
	"POST /api/flows/{id}/publish":                           needs(models.ResourceFlowsWhatsApp, models.ActionWrite),    // PublishFlow
	"POST /api/flows/{id}/save-to-meta":                      needs(models.ResourceFlowsWhatsApp, models.ActionWrite),    // SaveFlowToMeta
	"POST /api/import":                                       needs("config.Resource", models.ActionImport),              // ImportData
	"GET /api/import/{table}/config":                         needs("config.Resource", models.ActionImport),              // GetImportConfig
	"GET /api/inbox":                                         needs(models.ResourceChat, models.ActionRead),              // ListInbox
	"GET /api/inbox/counts":                                  needs(models.ResourceChat, models.ActionRead),              // GetInboxCounts
	"GET /api/ivr-flows":                                     needs(models.ResourceIVRFlows, models.ActionRead),          // ListIVRFlows
	"POST /api/ivr-flows":                                    needs(models.ResourceIVRFlows, models.ActionWrite),         // CreateIVRFlow
	"POST /api/ivr-flows/audio":                              needs(models.ResourceIVRFlows, models.ActionWrite),         // UploadIVRAudio
	"GET /api/ivr-flows/audio/{filename}":                    needs(models.ResourceIVRFlows, models.ActionRead),          // ServeIVRAudio
	"DELETE /api/ivr-flows/{id}":                             needs(models.ResourceIVRFlows, models.ActionDelete),        // DeleteIVRFlow
	"GET /api/ivr-flows/{id}":                                needs(models.ResourceIVRFlows, models.ActionRead),          // GetIVRFlow
	"PUT /api/ivr-flows/{id}":                                needs(models.ResourceIVRFlows, models.ActionWrite),         // UpdateIVRFlow
	"GET /api/me":                                            self(),                                                     // GetCurrentUser
	"PUT /api/me/availability":                               self(),                                                     // UpdateAvailability
	"GET /api/me/organizations":                              self(),                                                     // ListMyOrganizations
	"PUT /api/me/password":                                   self(),                                                     // ChangePassword
	"PUT /api/me/settings":                                   self(),                                                     // UpdateCurrentUserSettings
	"GET /api/media/{message_id}":                            needs(models.ResourceContacts, models.ActionRead),          // ServeMedia
	"POST /api/messages":                                     needs(models.ResourceChat, models.ActionWrite),             // SendMessage
	"POST /api/messages/media":                               needs(models.ResourceChat, models.ActionWrite),             // SendMediaMessage
	"POST /api/messages/template":                            needs(models.ResourceChat, models.ActionWrite),             // SendTemplateMessage
	"PUT /api/messages/{id}/read":                            needs(models.ResourceChat, models.ActionRead),              // MarkMessageRead
	"GET /api/notifications":                                 self(),                                                     // ListNotifications
	"POST /api/notifications/read-all":                       self(),                                                     // MarkAllNotificationsRead
	"GET /api/notifications/unread-count":                    self(),                                                     // GetUnreadNotificationCount
	"POST /api/notifications/{id}/read":                      self(),                                                     // MarkNotificationRead
	"POST /api/org/audio":                                    needs(models.ResourceOrganizations, models.ActionWrite),    // UploadOrgAudio
	"GET /api/org/settings":                                  self(),                                                     // GetOrganizationSettings
	"PUT /api/org/settings":                                  needs(models.ResourceAccounts, models.ActionWrite),         // UpdateOrganizationSettings
	"GET /api/organizations":                                 needs(models.ResourceOrganizations, models.ActionRead),     // ListOrganizations
	"POST /api/organizations":                                needs(models.ResourceOrganizations, models.ActionWrite),    // CreateOrganization
	"GET /api/organizations/current":                         self(),                                                     // GetCurrentOrganization
	"GET /api/organizations/members":                         needs(models.ResourceOrganizations, models.ActionRead),     // ListOrganizationMembers
	"POST /api/organizations/members":                        needs(models.ResourceOrganizations, models.ActionAssign),   // AddOrganizationMember
	"DELETE /api/organizations/members/{member_id}":          needs(models.ResourceOrganizations, models.ActionAssign),   // RemoveOrganizationMember
	"PUT /api/organizations/members/{member_id}":             needs(models.ResourceOrganizations, models.ActionAssign),   // UpdateOrganizationMemberRole
	"GET /api/permissions":                                   needs(models.ResourceRoles, models.ActionRead),             // ListPermissions
	"DELETE /api/pipeline-stages/{id}":                       needs(models.ResourcePipelines, models.ActionDelete),       // DeleteStage
	"PUT /api/pipeline-stages/{id}":                          needs(models.ResourcePipelines, models.ActionWrite),        // UpdateStage
	"GET /api/pipelines":                                     needs(models.ResourcePipelines, models.ActionRead),         // ListPipelines
	"POST /api/pipelines":                                    needs(models.ResourcePipelines, models.ActionWrite),        // CreatePipeline
	"DELETE /api/pipelines/{id}":                             needs(models.ResourcePipelines, models.ActionDelete),       // DeletePipeline
	"GET /api/pipelines/{id}":                                needs(models.ResourcePipelines, models.ActionRead),         // GetPipeline
	"PUT /api/pipelines/{id}":                                needs(models.ResourcePipelines, models.ActionWrite),        // UpdatePipeline
	"GET /api/pipelines/{id}/board":                          needs(models.ResourceDeals, models.ActionRead),             // Board
	"POST /api/pipelines/{id}/stages":                        needs(models.ResourcePipelines, models.ActionWrite),        // CreateStage
	"PUT /api/pipelines/{id}/stages/reorder":                 needs(models.ResourcePipelines, models.ActionWrite),        // ReorderStages
	"GET /api/pipelines/{id}/stages/{stageId}/deals":         needs(models.ResourceDeals, models.ActionRead),             // StageDeals
	"DELETE /api/products/{id}":                              needs(models.ResourceAccounts, models.ActionDelete),        // DeleteCatalogProduct
	"GET /api/products/{id}":                                 needs(models.ResourceAccounts, models.ActionRead),          // GetCatalogProduct
	"PUT /api/products/{id}":                                 needs(models.ResourceAccounts, models.ActionWrite),         // UpdateCatalogProduct
	"GET /api/reports/agent-performance":                     needs(models.ResourceAnalyticsAgents, models.ActionRead),   // AgentPerformanceReport
	"GET /api/reports/contacts-by-source":                    needs(models.ResourceReports, models.ActionRead),           // ContactsBySourceReport
	"GET /api/reports/lifecycle-funnel":                      needs(models.ResourceReports, models.ActionRead),           // LifecycleFunnelReport
	"GET /api/reports/pipeline-forecast":                     needs(models.ResourceReports, models.ActionRead),           // PipelineForecastReport
	"GET /api/reports/pipeline-funnel":                       needs(models.ResourceReports, models.ActionRead),           // PipelineFunnelReport
	"GET /api/reports/campaign-replies":                      needs(models.ResourceReports, models.ActionRead),           // CampaignRepliesReport
	"GET /api/reports/tasks-by-agent":                        needs(models.ResourceTasks, models.ActionRead),             // TasksByAgentReport
	"GET /api/reports/{key}/export.csv":                      needs(models.ResourceReports, models.ActionExport),         // ExportReport
	"GET /api/roles":                                         needs(models.ResourceRoles, models.ActionRead),             // ListRoles
	"POST /api/roles":                                        needs(models.ResourceRoles, models.ActionWrite),            // CreateRole
	"DELETE /api/roles/{id}":                                 needs(models.ResourceRoles, models.ActionDelete),           // DeleteRole
	"GET /api/roles/{id}":                                    needs(models.ResourceRoles, models.ActionRead),             // GetRole
	"PUT /api/roles/{id}":                                    needs(models.ResourceRoles, models.ActionWrite),            // UpdateRole
	"GET /api/segments":                                      needs(models.ResourceSegments, models.ActionRead),          // ListSegments
	"POST /api/segments":                                     needs(models.ResourceSegments, models.ActionWrite),         // CreateSegment
	"POST /api/segments/preview-count":                       needs(models.ResourceContacts, models.ActionRead),          // PreviewSegmentCount
	"DELETE /api/segments/{id}":                              needs(models.ResourceSegments, models.ActionDelete),        // DeleteSegment
	"GET /api/segments/{id}":                                 needs(models.ResourceSegments, models.ActionRead),          // GetSegment
	"PUT /api/segments/{id}":                                 needs(models.ResourceSegments, models.ActionWrite),         // UpdateSegment
	"POST /api/segments/{id}/contacts":                       needs(models.ResourceSegments, models.ActionRead),          // SegmentContacts
	"POST /api/segments/{id}/count":                          needs(models.ResourceSegments, models.ActionRead),          // CountSegment
	"GET /api/settings/sso":                                  needs(models.ResourceSettingsSSO, models.ActionRead),       // GetSSOSettings
	"DELETE /api/settings/sso/{provider}":                    needs(models.ResourceSettingsSSO, models.ActionWrite),      // DeleteSSOProvider
	"PUT /api/settings/sso/{provider}":                       needs(models.ResourceSettingsSSO, models.ActionWrite),      // UpdateSSOProvider
	"GET /api/tags":                                          needs(models.ResourceTags, models.ActionRead),              // ListTags
	"POST /api/tags":                                         needs(models.ResourceTags, models.ActionWrite),             // CreateTag
	"DELETE /api/tags/{name}":                                needs(models.ResourceTags, models.ActionDelete),            // DeleteTag
	"PUT /api/tags/{name}":                                   needs(models.ResourceTags, models.ActionWrite),             // UpdateTag
	"GET /api/task-types":                                    needs(models.ResourceTasks, models.ActionRead),             // ListTaskTypes
	"GET /api/tasks":                                         needs(models.ResourceTasks, models.ActionRead),             // ListTasks
	"POST /api/tasks":                                        needs(models.ResourceTasks, models.ActionWrite),            // CreateTask
	"PUT /api/tasks/{id}":                                    needs(models.ResourceTasks, models.ActionWrite),            // UpdateTask
	"POST /api/tasks/{id}/reopen":                            needs(models.ResourceTasks, models.ActionWrite),            // ReopenTask
	"POST /api/tasks/{id}/cancel":                            needs(models.ResourceTasks, models.ActionWrite),            // CancelTask
	"POST /api/tasks/{id}/complete":                          needs(models.ResourceTasks, models.ActionWrite),            // CompleteTask
	"POST /api/tasks/{id}/reassign":                          needs(models.ResourceTasks, models.ActionWrite),            // ReassignTask
	"GET /api/teams":                                         needs(models.ResourceTeams, models.ActionRead),             // ListTeams
	"POST /api/teams":                                        needs(models.ResourceTeams, models.ActionWrite),            // CreateTeam
	"DELETE /api/teams/{id}":                                 needs(models.ResourceTeams, models.ActionDelete),           // DeleteTeam
	"GET /api/teams/{id}":                                    needs(models.ResourceTeams, models.ActionRead),             // GetTeam
	"PUT /api/teams/{id}":                                    needs(models.ResourceTeams, models.ActionWrite),            // UpdateTeam
	"GET /api/teams/{id}/members":                            needs(models.ResourceTeams, models.ActionRead),             // ListTeamMembers
	"POST /api/teams/{id}/members":                           needs(models.ResourceTeams, models.ActionWrite),            // AddTeamMember
	"DELETE /api/teams/{id}/members/{member_user_id}":        needs(models.ResourceTeams, models.ActionWrite),            // RemoveTeamMember
	"GET /api/templates":                                     needs(models.ResourceTemplates, models.ActionRead),         // ListTemplates
	"POST /api/templates":                                    needs(models.ResourceTemplates, models.ActionWrite),        // CreateTemplate
	"POST /api/templates/sync":                               needs(models.ResourceTemplates, models.ActionSync),         // SyncTemplates
	"POST /api/templates/upload-media":                       needs(models.ResourceTemplates, models.ActionWrite),        // UploadTemplateMedia
	"DELETE /api/templates/{id}":                             needs(models.ResourceTemplates, models.ActionDelete),       // DeleteTemplate
	"GET /api/templates/{id}":                                needs(models.ResourceTemplates, models.ActionRead),         // GetTemplate
	"PUT /api/templates/{id}":                                needs(models.ResourceTemplates, models.ActionWrite),        // UpdateTemplate
	"POST /api/templates/{id}/publish":                       needs(models.ResourceTemplates, models.ActionWrite),        // SubmitTemplate
	"GET /api/users":                                         needs(models.ResourceUsers, models.ActionRead),             // ListUsers
	"POST /api/users":                                        needs(models.ResourceUsers, models.ActionWrite),            // CreateUser
	"DELETE /api/users/{id}":                                 needs(models.ResourceUsers, models.ActionDelete),           // DeleteUser
	"GET /api/variables":                                     needs(models.ResourceChat, models.ActionRead),              // GetVariableCatalog
	"GET /api/users/{id}":                                    needs(models.ResourceUsers, models.ActionRead),             // GetUser
	"PUT /api/users/{id}":                                    needs(models.ResourceUsers, models.ActionWrite),            // UpdateUser
	"GET /api/webhook":                                       public(),                                                   // WebhookVerify
	"POST /api/webhook":                                      public(),                                                   // WebhookHandler
	"GET /api/webhooks":                                      needs(models.ResourceWebhooks, models.ActionRead),          // ListWebhooks
	"POST /api/webhooks":                                     needs(models.ResourceWebhooks, models.ActionWrite),         // CreateWebhook
	"DELETE /api/webhooks/{id}":                              needs(models.ResourceWebhooks, models.ActionDelete),        // DeleteWebhook
	"GET /api/webhooks/{id}":                                 needs(models.ResourceWebhooks, models.ActionRead),          // GetWebhook
	"PUT /api/webhooks/{id}":                                 needs(models.ResourceWebhooks, models.ActionWrite),         // UpdateWebhook
	"POST /api/webhooks/{id}/test":                           needs(models.ResourceWebhooks, models.ActionWrite),         // TestWebhook
	"GET /api/webhooks/{id}/deliveries":                      needs(models.ResourceWebhooks, models.ActionRead),          // ListWebhookDeliveries
	"GET /api/widgets":                                       needs(models.ResourceAnalytics, models.ActionRead),         // ListWidgets
	"POST /api/widgets":                                      needs(models.ResourceAnalytics, models.ActionWrite),        // CreateWidget
	"GET /api/widgets/data":                                  needs(models.ResourceAnalytics, models.ActionRead),         // GetAllWidgetsData
	"GET /api/widgets/data-sources":                          needs(models.ResourceAnalytics, models.ActionRead),         // GetWidgetDataSources
	"POST /api/widgets/layout":                               needs(models.ResourceAnalytics, models.ActionRead),         // SaveWidgetLayout
	"DELETE /api/widgets/{id}":                               needs(models.ResourceAnalytics, models.ActionDelete),       // DeleteWidget
	"GET /api/widgets/{id}":                                  needs(models.ResourceAnalytics, models.ActionRead),         // GetWidget
	"PUT /api/widgets/{id}":                                  needs(models.ResourceAnalytics, models.ActionWrite),        // UpdateWidget
	"GET /api/widgets/{id}/data":                             needs(models.ResourceAnalytics, models.ActionRead),         // GetWidgetData
}

// checkRoutePermissions compares the declarations above against the routes
// fastglue has actually registered. It returns an error naming every
// disagreement, so one startup reports all of them rather than one per run.
func checkRoutePermissions(g *fastglue.Fastglue) error {
	registered := map[string]bool{}
	for method, paths := range g.Router.List() {
		for _, path := range paths {
			if !strings.HasPrefix(path, "/api") {
				continue
			}
			registered[method+" "+path] = true
		}
	}

	var undeclared, stale []string
	for route := range registered {
		if _, ok := routePermissions[route]; !ok {
			undeclared = append(undeclared, route)
		}
	}
	for route := range routePermissions {
		if !registered[route] {
			stale = append(stale, route)
		}
	}
	sort.Strings(undeclared)
	sort.Strings(stale)

	if len(undeclared) == 0 && len(stale) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("route permission table is out of date (cmd/wacrm/route_permissions.go)")
	if len(undeclared) > 0 {
		fmt.Fprintf(&b, "\n  %d registered route(s) with no declaration — add needs()/public()/self()/scoped():", len(undeclared))
		for _, r := range undeclared {
			b.WriteString("\n    " + r)
		}
	}
	if len(stale) > 0 {
		fmt.Fprintf(&b, "\n  %d declaration(s) for routes that are no longer registered — remove them:", len(stale))
		for _, r := range stale {
			b.WriteString("\n    " + r)
		}
	}
	return fmt.Errorf("%s", b.String())
}

// unprotectedRoutes lists the routes that deliberately require no permission.
// It exists so the set is reviewable in one place and in tests, rather than
// being scattered across the table.
func unprotectedRoutes() (publicRoutes, selfRoutes, scopedRoutes []string) {
	for route, access := range routePermissions {
		switch access.kind {
		case accessPublic:
			publicRoutes = append(publicRoutes, route)
		case accessSelf:
			selfRoutes = append(selfRoutes, route)
		case accessScoped:
			scopedRoutes = append(scopedRoutes, route)
		}
	}
	sort.Strings(publicRoutes)
	sort.Strings(selfRoutes)
	sort.Strings(scopedRoutes)
	return
}

// permissionForRoute reports the permission a route enforces, if it enforces
// one. Used by tests and by the startup summary.
func permissionForRoute(method, path string) (resource, action string, ok bool) {
	access, found := routePermissions[method+" "+path]
	if !found || access.kind != accessPermission {
		return "", "", false
	}
	return access.resource, access.action, true
}
