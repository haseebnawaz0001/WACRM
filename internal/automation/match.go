package automation

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
)

// MatchTrigger reports whether an event satisfies a rule's trigger settings.
//
// The settings narrow an event type to the cases the author meant: "tag added"
// is rarely what somebody wants — "the VIP tag was added" is. An empty setting
// means "any", so a rule with no configuration still fires.
func MatchTrigger(rule *models.AutomationRule, event crmevents.Event) bool {
	if rule.TriggerType != event.Type {
		return false
	}
	cfg := RuleTriggerConfig(rule)

	switch rule.TriggerType {
	case "contact.created":
		return anyOrMember(cfg.Strings("sources"), dataString(event, "source"))

	case "contact.tag_added", "contact.tag_removed":
		// Tags are compared case-insensitively: "vip" and "VIP" are the same
		// tag to everyone except a string comparison.
		return anyOrMemberFold(cfg.Strings("tags"), dataString(event, "tag"))

	case "contact.field_changed":
		if field := cfg.Str("field"); field != "" && field != dataString(event, "field") {
			return false
		}
		// The builder saves "to" as a list of values, the shape the other
		// "changed to" triggers use; the matcher read only an {operator,
		// value} object, found none, and so matched every value. Both
		// shapes are honoured.
		if values := cfg.Strings("to"); len(values) > 0 {
			return anyOrMemberFold(values, strings.TrimSpace(toText(event.Data["to"])))
		}
		return matchValueCondition(cfg.Object("to"), event.Data["to"])

	case "contact.assigned":
		return anyOrMember(cfg.Strings("to_user_ids"), dataString(event, "to"))

	case "conversation.created":
		return anyOrMember(cfg.Strings("accounts"), dataString(event, "account"))

	case "conversation.status_changed":
		if !anyOrMember(cfg.Strings("to"), dataString(event, "to")) {
			return false
		}
		return anyOrMember(cfg.Strings("reasons"), dataString(event, "reason"))

	case "conversation.assigned":
		if len(cfg.Strings("user_ids")) == 0 && len(cfg.Strings("team_ids")) == 0 {
			return true
		}
		return member(cfg.Strings("user_ids"), dataString(event, "assignee_id")) ||
			member(cfg.Strings("team_ids"), dataString(event, "team_id"))

	case "task.created", "task.completed", "task.overdue":
		return anyOrMember(cfg.Strings("type_keys"), dataString(event, "type_key"))

	case "deal.created", "deal.won", "deal.lost":
		return anyOrEquals(cfg.Str("pipeline_id"), dataString(event, "pipeline_id"))

	case "deal.stage_changed":
		if !anyOrEquals(cfg.Str("pipeline_id"), dataString(event, "pipeline_id")) {
			return false
		}
		return anyOrMember(cfg.Strings("to_stage_ids"), dataString(event, "stage_id"))

	case "contact.lifecycle_stage_changed":
		return anyOrMember(cfg.Strings("to"), dataString(event, "stage"))

	case "call.missed", "call.completed":
		// Direction matters: "we could not reach them" and "they could not
		// reach us" call for opposite follow-ups.
		return anyOrEquals(cfg.Str("direction"), dataString(event, "direction"))

	case "chatbot.flow_completed":
		return anyOrMember(cfg.Strings("flow_ids"), dataString(event, "flow_id"))

	case "campaign.replied":
		return anyOrMember(cfg.Strings("campaign_ids"), dataString(event, "campaign_id"))

	case "conversation.sla_breached":
		// A breach has nothing to narrow by: it already means one thing.
		return true
	}

	// A time trigger's own job decides what matches; by the time a synthetic
	// event reaches here it is already the right one.
	return IsTimeTrigger(rule.TriggerType)
}

// matchValueCondition applies an optional {operator, value} test to a changed
// field's new value.
func matchValueCondition(cfg crmactions.Config, value any) bool {
	operator := cfg.Str("operator")
	if operator == "" {
		return true
	}

	actual := strings.TrimSpace(toText(value))
	expected := strings.TrimSpace(toText(cfg["value"]))

	switch operator {
	case "equals":
		return strings.EqualFold(actual, expected)
	case "not_equals":
		return !strings.EqualFold(actual, expected)
	case "contains":
		return strings.Contains(strings.ToLower(actual), strings.ToLower(expected))
	case "is_empty":
		return actual == ""
	case "is_not_empty":
		return actual != ""
	}
	return false
}

func dataString(event crmevents.Event, key string) string {
	if event.Data == nil {
		return ""
	}
	return toText(event.Data[key])
}

func toText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		// Event data arrives as decoded JSON, where every number is a float.
		// Formatting it as an integer when it is one keeps a stage or type id
		// comparable with the string the rule stored.
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// anyOrEquals treats an unset setting as "any value".
func anyOrEquals(expected, value string) bool {
	return expected == "" || expected == value
}

// anyOrMember treats an empty list as "any value".
func anyOrMember(allowed []string, value string) bool {
	return len(allowed) == 0 || member(allowed, value)
}

func anyOrMemberFold(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func member(allowed []string, value string) bool {
	if value == "" {
		return false
	}
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}
