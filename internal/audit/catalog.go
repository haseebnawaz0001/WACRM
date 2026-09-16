package audit

import "sort"

// ResourceType is one auditable resource, as the audit log filter offers it.
type ResourceType struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Group string `json:"group"`
}

// Action is one auditable action.
type Action struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// resourceLabels is the catalog (plan 10, S9).
//
// The audit log's resource filter was a hand-kept list in the Vue component. It
// offered ten resources; the server wrote fifteen. Filtering for the five it had
// never heard of — accounts, AI contexts, canned responses, roles and webhooks —
// was simply impossible, and one of its ten (chatbot_settings) was never written
// at all, so choosing it always returned nothing. An audit log you cannot filter
// to the thing you are investigating is not much of an audit log.
//
// Keeping the list here rather than in the component means the picker is a
// projection of what the server writes; the test alongside it reads the call
// sites and fails when the two diverge.
var resourceLabels = map[string]ResourceType{
	"account":                   {"account", "WhatsApp Account", "Channels"},
	"ai_context":                {"ai_context", "AI Context", "Automation"},
	"automations":               {"automations", "Automation Rule", "Automation"},
	"campaigns":                 {"campaigns", "Campaign", "Outreach"},
	"canned_response":           {"canned_response", "Canned Response", "Outreach"},
	"chatbot_flow":              {"chatbot_flow", "Chatbot Flow", "Automation"},
	"contact_fields":            {"contact_fields", "Contact Field", "CRM"},
	"contacts":                  {"contacts", "Contact", "CRM"},
	"deals":                     {"deals", "Deal", "CRM"},
	"ivr_flow":                  {"ivr_flow", "IVR Flow", "Calling"},
	"keyword_rule":              {"keyword_rule", "Keyword Rule", "Automation"},
	"organization":              {"organization", "Organization", "Administration"},
	"organization_member":       {"organization_member", "Organization Member", "Administration"},
	"pipelines":                 {"pipelines", "Pipeline", "CRM"},
	"role":                      {"role", "Role", "Administration"},
	"segments":                  {"segments", "Segment", "CRM"},
	"settings.calling":          {"settings.calling", "Settings — Calling", "Settings"},
	"settings.chatbot.agents":   {"settings.chatbot.agents", "Settings — Chatbot Agents", "Settings"},
	"settings.chatbot.ai":       {"settings.chatbot.ai", "Settings — Chatbot AI", "Settings"},
	"settings.chatbot.hours":    {"settings.chatbot.hours", "Settings — Business Hours", "Settings"},
	"settings.chatbot.messages": {"settings.chatbot.messages", "Settings — Chatbot Messages", "Settings"},
	"settings.chatbot.sla":      {"settings.chatbot.sla", "Settings — SLA", "Settings"},
	"settings.general":          {"settings.general", "Settings — General", "Settings"},
	"settings.notification":     {"settings.notification", "Settings — Notifications", "Settings"},
	"tasks":                     {"tasks", "Task", "CRM"},
	"team":                      {"team", "Team", "Administration"},
	"template":                  {"template", "Template", "Outreach"},
	"user":                      {"user", "User", "Administration"},
	"webhook":                   {"webhook", "Webhook", "Integrations"},
}

// ResourceTypes returns the catalog, sorted by label so the picker reads
// alphabetically rather than in map order.
func ResourceTypes() []ResourceType {
	out := make([]ResourceType, 0, len(resourceLabels))
	for _, rt := range resourceLabels {
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// KnownResourceType reports whether a resource type is in the catalog.
func KnownResourceType(value string) bool {
	_, ok := resourceLabels[value]
	return ok
}

// Actions returns the auditable actions.
func Actions() []Action {
	return []Action{
		{"created", "Created"},
		{"updated", "Updated"},
		{"deleted", "Deleted"},
	}
}
