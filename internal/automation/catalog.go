// Package automation reacts to CRM changes (plan 08).
//
// Keyword rules and chatbot flows already answer messages. Nothing reacted to
// the record itself changing — a tag added, a task going overdue, a customer
// going quiet for a week — so the follow-up that matters most was the one
// somebody had to remember.
package automation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
)

// Trigger kinds.
const (
	KindEvent = "event"
	KindTime  = "time"
)

// Time trigger types. These have no event behind them; a scheduled job looks
// for the condition and synthesises one.
const (
	TriggerNoCustomerReply = "time.no_customer_reply"
	TriggerNoAgentReply    = "time.no_agent_reply"
	TriggerDateField       = "time.date_field"
)

// Trigger describes one thing a rule can react to.
type Trigger struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
	// Group buckets triggers in the builder's select.
	Group string `json:"group"`
	// ConfigKeys names the settings this trigger accepts, so the UI can render
	// the right form and the API can reject anything else.
	ConfigKeys []string `json:"config_keys"`
}

// triggers is every trigger the product can honour today.
//
// Event triggers are deliberately a subset of the event catalog: an event that
// exists is not automatically something it is safe to automate on. Message
// arrival in particular is left out, because keyword rules and chatbot flows
// already own live message handling and two bots answering one message is
// worse than none.
var triggers = map[string]Trigger{
	"contact.created":       {Type: "contact.created", Kind: KindEvent, Group: "contacts", ConfigKeys: []string{"sources"}},
	"contact.tag_added":     {Type: "contact.tag_added", Kind: KindEvent, Group: "contacts", ConfigKeys: []string{"tags"}},
	"contact.tag_removed":   {Type: "contact.tag_removed", Kind: KindEvent, Group: "contacts", ConfigKeys: []string{"tags"}},
	"contact.field_changed": {Type: "contact.field_changed", Kind: KindEvent, Group: "contacts", ConfigKeys: []string{"field", "to"}},
	"contact.assigned":      {Type: "contact.assigned", Kind: KindEvent, Group: "contacts", ConfigKeys: []string{"to_user_ids"}},
	// Lifecycle stage is the change people most want to act on — "became a
	// customer, send the onboarding template" — and matching it through the
	// generic field_changed trigger meant every rule author re-derived the
	// same filter (plan 01).
	"contact.lifecycle_stage_changed": {Type: "contact.lifecycle_stage_changed", Kind: KindEvent, Group: "contacts", ConfigKeys: []string{"to"}},

	"conversation.created":        {Type: "conversation.created", Kind: KindEvent, Group: "conversations", ConfigKeys: []string{"accounts"}},
	"conversation.status_changed": {Type: "conversation.status_changed", Kind: KindEvent, Group: "conversations", ConfigKeys: []string{"to", "reasons"}},
	"conversation.assigned":       {Type: "conversation.assigned", Kind: KindEvent, Group: "conversations", ConfigKeys: []string{"user_ids", "team_ids"}},
	// A breach is a promise the organization made and missed, which is the
	// canonical thing to escalate automatically (plan 08).
	"conversation.sla_breached": {Type: "conversation.sla_breached", Kind: KindEvent, Group: "conversations", ConfigKeys: nil},

	// Calls (plan 10, §4.4). "Missed call → callback task in an hour" is the
	// journey this trigger exists for.
	"call.missed":    {Type: "call.missed", Kind: KindEvent, Group: "calls", ConfigKeys: []string{"direction"}},
	"call.completed": {Type: "call.completed", Kind: KindEvent, Group: "calls", ConfigKeys: []string{"direction"}},

	// The chatbot finishing a qualification flow is when the CRM work starts.
	"chatbot.flow_completed": {Type: "chatbot.flow_completed", Kind: KindEvent, Group: "chatbot", ConfigKeys: []string{"flow_ids"}},

	// A reply to a campaign is the moment a blast becomes a conversation.
	"campaign.replied": {Type: "campaign.replied", Kind: KindEvent, Group: "campaigns", ConfigKeys: []string{"campaign_ids"}},

	"task.created":   {Type: "task.created", Kind: KindEvent, Group: "tasks", ConfigKeys: []string{"type_keys"}},
	"task.completed": {Type: "task.completed", Kind: KindEvent, Group: "tasks", ConfigKeys: []string{"type_keys"}},
	"task.overdue":   {Type: "task.overdue", Kind: KindEvent, Group: "tasks", ConfigKeys: []string{"type_keys"}},

	"deal.created":       {Type: "deal.created", Kind: KindEvent, Group: "deals", ConfigKeys: []string{"pipeline_id"}},
	"deal.stage_changed": {Type: "deal.stage_changed", Kind: KindEvent, Group: "deals", ConfigKeys: []string{"pipeline_id", "to_stage_ids"}},
	"deal.won":           {Type: "deal.won", Kind: KindEvent, Group: "deals", ConfigKeys: []string{"pipeline_id"}},
	"deal.lost":          {Type: "deal.lost", Kind: KindEvent, Group: "deals", ConfigKeys: []string{"pipeline_id"}},

	TriggerNoCustomerReply: {Type: TriggerNoCustomerReply, Kind: KindTime, Group: "time", ConfigKeys: []string{"after", "statuses"}},
	TriggerNoAgentReply:    {Type: TriggerNoAgentReply, Kind: KindTime, Group: "time", ConfigKeys: []string{"after"}},
	TriggerDateField:       {Type: TriggerDateField, Kind: KindTime, Group: "time", ConfigKeys: []string{"field", "offset_days", "at_local_hour"}},
}

// LookupTrigger returns a trigger by type.
func LookupTrigger(triggerType string) (Trigger, bool) {
	t, ok := triggers[triggerType]
	return t, ok
}

// Triggers lists every trigger, sorted by group then type so the builder's
// select reads in a stable order.
func Triggers() []Trigger {
	out := make([]Trigger, 0, len(triggers))
	for _, t := range triggers {
		// An event trigger whose event cannot fire yet would be a promise the
		// product does not keep, so the catalog checks.
		if t.Kind == KindEvent && !crmevents.IsKnown(t.Type) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// Actions lists every action a rule may perform.
func Actions() []string { return crmactions.Types() }

// IsTimeTrigger reports whether a trigger is driven by the scheduler.
func IsTimeTrigger(triggerType string) bool {
	t, ok := triggers[triggerType]
	return ok && t.Kind == KindTime
}

// validateTriggerConfig rejects settings the trigger does not understand.
//
// Silently ignoring an unknown key is how a rule ends up firing far more often
// than its author believes: they filtered on something the engine never read.
func validateTriggerConfig(triggerType string, cfg map[string]any) error {
	trigger, ok := triggers[triggerType]
	if !ok {
		return fmt.Errorf("automation: %q is not a trigger", triggerType)
	}

	allowed := make(map[string]bool, len(trigger.ConfigKeys))
	for _, key := range trigger.ConfigKeys {
		allowed[key] = true
	}
	unknown := make([]string, 0)
	for key := range cfg {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("automation: %s does not understand %s",
			triggerType, strings.Join(unknown, ", "))
	}

	// Time triggers cannot work without the thing they measure.
	switch triggerType {
	case TriggerNoCustomerReply, TriggerNoAgentReply:
		if _, ok := crmactions.Config(cfg).Duration("after"); !ok {
			return fmt.Errorf("automation: %s needs an \"after\" duration", triggerType)
		}
	case TriggerDateField:
		if strings.TrimSpace(fmt.Sprint(cfg["field"])) == "" || cfg["field"] == nil {
			return fmt.Errorf("automation: %s needs a date field", triggerType)
		}
	}
	return nil
}
