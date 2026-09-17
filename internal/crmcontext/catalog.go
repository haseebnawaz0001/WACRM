package crmcontext

import "sort"

// The variable catalog (plan 10, S6).
//
// Every screen that accepts a template needs a variable picker, and each one
// used to carry its own hardcoded list. They drifted: the canned-response
// picker offered `{{contact_name}}`, which nothing else understood, while
// `contact.fields.<key>` — the thing authors actually wanted — appeared in no
// picker at all. A backend catalog means the picker and the renderer cannot
// disagree, because there is one list.

// Context names where a template can be written. Each offers a different set,
// because a campaign has no acting user and a canned response has no event.
const (
	ContextCanned       = "canned"
	ContextCampaign     = "campaign"
	ContextAutomation   = "automation"
	ContextChatbot      = "chatbot"
	ContextCustomAction = "custom_action"
	ContextKeyword      = "keyword"
	ContextIVR          = "ivr"
)

// Variable is one offering in the picker.
type Variable struct {
	// Path is what the author types, without braces.
	Path string `json:"path"`
	// Label is the human name.
	Label string `json:"label"`
	// Group sorts the picker into sections.
	Group string `json:"group"`
	// Example shows what it renders to, so an author can tell
	// `contact.name` from `contact.phone_number` without trying both.
	Example string `json:"example,omitempty"`
	// Dynamic marks a prefix whose suffix is chosen by the organization —
	// `contact.fields.` is completed from their own field definitions.
	Dynamic bool `json:"dynamic,omitempty"`
}

// contexts maps each context to the groups it offers.
var contexts = map[string][]string{
	ContextCanned:       {"contact", "user", "org"},
	ContextCampaign:     {"contact", "org"},
	ContextAutomation:   {"contact", "conversation", "org", "event"},
	ContextChatbot:      {"contact", "conversation", "org"},
	ContextCustomAction: {"contact", "user", "org"},
	ContextKeyword:      {"contact", "conversation", "org"},
	ContextIVR:          {"contact", "org"},
}

var catalog = []Variable{
	{Path: "contact.name", Label: "Contact name", Group: "contact", Example: "Amara Okafor"},
	{Path: "contact.phone_number", Label: "Phone number", Group: "contact", Example: "+2348012345678"},
	{Path: "contact.id", Label: "Contact ID", Group: "contact"},
	{Path: "contact.tags", Label: "Tags", Group: "contact", Example: "vip, renewal"},
	{Path: "contact.lifecycle_stage", Label: "Lifecycle stage", Group: "contact", Example: "Lead"},
	{Path: "contact.source", Label: "Source", Group: "contact", Example: "Import"},
	{Path: "contact.created_at", Label: "First seen", Group: "contact"},
	{Path: "contact.owner.name", Label: "Owner name", Group: "contact", Example: "Sam Ade"},
	{Path: "contact.owner.email", Label: "Owner email", Group: "contact"},
	{Path: "contact.fields.", Label: "Custom field", Group: "contact", Dynamic: true},

	{Path: "conversation.status", Label: "Conversation status", Group: "conversation", Example: "open"},
	{Path: "conversation.handling", Label: "Handled by", Group: "conversation", Example: "human"},
	{Path: "conversation.assignee.name", Label: "Assigned agent", Group: "conversation"},
	{Path: "conversation.team.name", Label: "Assigned team", Group: "conversation"},
	{Path: "conversation.opened_at", Label: "Conversation opened", Group: "conversation"},

	{Path: "user.name", Label: "Your name", Group: "user", Example: "Sam Ade"},
	{Path: "user.email", Label: "Your email", Group: "user"},

	{Path: "org.name", Label: "Organisation name", Group: "org", Example: "Walk-in Urgent Care"},
	{Path: "org.timezone", Label: "Organisation timezone", Group: "org", Example: "Africa/Lagos"},

	{Path: "event.type", Label: "Trigger event", Group: "event", Example: "conversation.created"},

	{Path: "now", Label: "Current time", Group: "org"},
}

// Catalog returns the variables offered in one context.
//
// An unknown context returns nothing rather than everything: a picker showing
// variables that will not resolve is worse than a picker showing none, because
// the author only finds out after the message has gone.
func Catalog(contextName string) []Variable {
	groups, ok := contexts[contextName]
	if !ok {
		return nil
	}
	allowed := make(map[string]bool, len(groups))
	for _, g := range groups {
		allowed[g] = true
	}

	out := make([]Variable, 0, len(catalog))
	for _, v := range catalog {
		if allowed[v.Group] {
			out = append(out, v)
		}
	}
	return out
}

// Contexts lists the context names, sorted, for validation and documentation.
func Contexts() []string {
	out := make([]string, 0, len(contexts))
	for name := range contexts {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
