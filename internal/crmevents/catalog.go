package crmevents

import "sort"

// Spec declares how one event type is handled by the relay's sinks.
type Spec struct {
	// Type is the dotted catalog name.
	Type string

	// RecordActivity writes a contact_activities row for the timeline.
	// Consumed by the activity sink once plan 00's F3 lands; until then the
	// flag is carried but no sink reads it.
	RecordActivity bool

	// Webhook exposes the event to outbound webhook subscribers. Only types
	// flagged here may be selected when creating a webhook.
	Webhook bool

	// Automatable puts the event on the Redis stream the automation engine
	// (plan 08) consumes.
	Automatable bool
}

// catalog holds every event the product can emit today.
//
// It deliberately lists only events that existing code actually fires. The
// full target catalog in docs/feature-plans/README.md also names task.*,
// deal.*, conversation.* and similar, but those cannot fire until their plans
// ship, and listing them early would let an admin subscribe a webhook to an
// event that never arrives. Each plan adds its own entries here as it lands.
var catalog = map[string]Spec{
	// Messaging. Not recorded as activity: messages are already the chat
	// thread, so a timeline row would duplicate them.
	"message.incoming": {Type: "message.incoming", Webhook: true},
	"message.outgoing": {Type: "message.outgoing", Webhook: true},
	"message.sent":     {Type: "message.sent", Webhook: true},

	// Contacts. Delete and restore are not automation triggers: a rule acting
	// on a contact that has just been removed is more likely to surprise than
	// to help.
	"contact.created": {Type: "contact.created", RecordActivity: true, Webhook: true, Automatable: true},
	"contact.deleted": {Type: "contact.deleted", RecordActivity: true, Webhook: true},
	// field_changed is not exposed as its own webhook: it is already carried
	// by contact.updated, and a separate delivery per edited field would
	// flood subscribers during an import.
	"contact.field_changed": {Type: "contact.field_changed", RecordActivity: true, Automatable: true},
	"contact.restored":      {Type: "contact.restored", RecordActivity: true, Webhook: true},
	// Tags and ownership (plan 08). These are the changes people most often
	// want to react to — "tag a lead VIP and it goes to the senior team" is
	// the canonical automation — so they are triggers as well as history.
	"contact.tag_added":   {Type: "contact.tag_added", RecordActivity: true, Webhook: true, Automatable: true},
	"contact.tag_removed": {Type: "contact.tag_removed", RecordActivity: true, Webhook: true, Automatable: true},
	"contact.assigned":    {Type: "contact.assigned", RecordActivity: true, Webhook: true, Automatable: true},

	// Conversations (plan 03).
	"conversation.created":        {Type: "conversation.created", RecordActivity: true, Webhook: true, Automatable: true},
	"conversation.status_changed": {Type: "conversation.status_changed", RecordActivity: true, Webhook: true, Automatable: true},
	"conversation.assigned":       {Type: "conversation.assigned", RecordActivity: true, Webhook: true, Automatable: true},

	// Tasks (plan 04). task.updated is not a webhook: a reassignment is
	// internal bookkeeping, and delivering one per edit would be noise.
	"task.created":   {Type: "task.created", RecordActivity: true, Webhook: true, Automatable: true},
	"task.completed": {Type: "task.completed", RecordActivity: true, Webhook: true, Automatable: true},
	"task.cancelled": {Type: "task.cancelled", RecordActivity: true},
	"task.updated":   {Type: "task.updated", RecordActivity: true},
	"task.due":       {Type: "task.due", Automatable: true},
	"task.overdue":   {Type: "task.overdue", RecordActivity: true, Webhook: true, Automatable: true},

	// Merge (plan 06). Not an automation trigger: a rule firing on a contact
	// that has just absorbed another is more likely to surprise than help.
	"contact.merged": {Type: "contact.merged", RecordActivity: true, Webhook: true},

	// Deals (plan 07).
	"deal.created":       {Type: "deal.created", RecordActivity: true, Webhook: true, Automatable: true},
	"deal.stage_changed": {Type: "deal.stage_changed", RecordActivity: true, Webhook: true, Automatable: true},
	"deal.won":           {Type: "deal.won", RecordActivity: true, Webhook: true, Automatable: true},
	"deal.lost":          {Type: "deal.lost", RecordActivity: true, Webhook: true, Automatable: true},
	// Edits and deletions are worth a webhook and a timeline entry, but
	// they are not a trigger: automating on "someone changed a field"
	// fires on every keystroke-sized save.
	"deal.updated": {Type: "deal.updated", RecordActivity: true, Webhook: true},
	"deal.deleted": {Type: "deal.deleted", RecordActivity: true, Webhook: true},

	// Agent transfers.
	"transfer.created":  {Type: "transfer.created", RecordActivity: true, Webhook: true},
	"transfer.assigned": {Type: "transfer.assigned", RecordActivity: true, Webhook: true},
	"transfer.resumed":  {Type: "transfer.resumed", RecordActivity: true, Webhook: true},
}

// Lookup returns the spec for an event type.
func Lookup(eventType string) (Spec, bool) {
	s, ok := catalog[eventType]
	return s, ok
}

// IsKnown reports whether the event type is in the catalog.
func IsKnown(eventType string) bool {
	_, ok := catalog[eventType]
	return ok
}

// WebhookEventTypes returns the sorted event types a webhook may subscribe to.
// It is the validation list for webhook creation (plan 00, F11) and the source
// for the backend-driven event catalog the UI renders (plan 10, S9).
func WebhookEventTypes() []string {
	out := make([]string, 0, len(catalog))
	for t, s := range catalog {
		if s.Webhook {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// AutomatableEventTypes returns the sorted event types that reach the
// automation stream.
func AutomatableEventTypes() []string {
	out := make([]string, 0, len(catalog))
	for t, s := range catalog {
		if s.Automatable {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}
