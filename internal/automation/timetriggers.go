package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
)

// TimeTriggerBatch bounds how many subjects one rule may fire for in one tick.
//
// Without it, switching on a rule against a long-quiet inbox would message
// every customer at once — which is the failure people most fear from
// automation, and the hardest to take back.
const TimeTriggerBatch = 1000

// timeTriggerNamespace makes a time trigger's synthetic event id deterministic.
// The same rule and subject always produce the same id, so the unique run
// index turns a repeated tick into a no-op.
var timeTriggerNamespace = uuid.MustParse("6f1a2f0e-8a5e-4a2f-9a9c-0d6f2b6f3a11")

// RunTimeTriggers fires every time-based rule whose condition now holds.
//
// Time triggers have no event behind them: nothing happens when a customer
// does not reply, which is exactly why somebody has to go looking.
func (e *Engine) RunTimeTriggers(ctx context.Context) (int, error) {
	var rules []models.AutomationRule
	if err := e.db().WithContext(ctx).
		Where("enabled = true AND trigger_type IN ?", []string{
			TriggerNoCustomerReply, TriggerNoAgentReply, TriggerDateField,
		}).Find(&rules).Error; err != nil {
		return 0, err
	}

	fired := 0
	for i := range rules {
		rule := &rules[i]

		events, err := e.timeTriggerEvents(ctx, rule)
		if err != nil {
			e.log().Error("Automation time trigger query failed", "error", err, "rule_id", rule.ID)
			continue
		}
		for _, event := range events {
			run, err := e.runRule(ctx, rule, event, false)
			if err != nil {
				e.log().Error("Automation time trigger failed", "error", err, "rule_id", rule.ID)
				continue
			}
			if run != nil && run.Status != models.AutomationSkipped {
				fired++
			}
		}
	}
	return fired, nil
}

// timeTriggerEvents finds the subjects a rule should fire for right now.
func (e *Engine) timeTriggerEvents(ctx context.Context, rule *models.AutomationRule) ([]crmevents.Event, error) {
	cfg := RuleTriggerConfig(rule)

	switch rule.TriggerType {
	case TriggerNoCustomerReply:
		after, ok := cfg.Duration("after")
		if !ok {
			return nil, nil
		}
		statuses := cfg.Strings("statuses")
		if len(statuses) == 0 {
			statuses = []string{string(models.ConversationOpen), string(models.ConversationPending)}
		}

		var rows []models.Conversation
		err := e.db().WithContext(ctx).
			Where("organization_id = ? AND status IN ?", rule.OrganizationID, statuses).
			Where("last_agent_message_at IS NOT NULL AND last_agent_message_at < ?",
				time.Now().UTC().Add(-after)).
			// The customer has said nothing since we did. A reply re-arms the
			// rule by moving last_customer_message_at past ours.
			Where("last_customer_message_at IS NULL OR last_customer_message_at < last_agent_message_at").
			Limit(TimeTriggerBatch).Find(&rows).Error
		if err != nil {
			return nil, err
		}

		out := make([]crmevents.Event, 0, len(rows))
		for _, row := range rows {
			// The subject is the waiting period, not the conversation: a new
			// agent message starts a new period and the rule may fire again.
			subject := fmt.Sprintf("%s:%d", row.ID, row.LastAgentMessageAt.UTC().Unix())
			out = append(out, e.syntheticEvent(rule, row.ContactID, subject, map[string]any{
				"conversation_id": row.ID.String(),
				"status":          string(row.Status),
			}))
		}
		return out, nil

	case TriggerNoAgentReply:
		after, ok := cfg.Duration("after")
		if !ok {
			return nil, nil
		}

		var rows []models.Conversation
		err := e.db().WithContext(ctx).
			Where("organization_id = ? AND status <> ?", rule.OrganizationID, models.ConversationResolved).
			// Bot-handled conversations are excluded: nobody is late when the
			// chatbot is mid-answer.
			Where("handling <> ?", models.HandlingBot).
			Where("waiting_since IS NOT NULL AND waiting_since < ?", time.Now().UTC().Add(-after)).
			Limit(TimeTriggerBatch).Find(&rows).Error
		if err != nil {
			return nil, err
		}

		out := make([]crmevents.Event, 0, len(rows))
		for _, row := range rows {
			subject := fmt.Sprintf("%s:%d", row.ID, row.WaitingSince.UTC().Unix())
			out = append(out, e.syntheticEvent(rule, row.ContactID, subject, map[string]any{
				"conversation_id": row.ID.String(),
				"waiting_since":   row.WaitingSince.UTC(),
			}))
		}
		return out, nil

	case TriggerDateField:
		return e.dateFieldEvents(ctx, rule, cfg)
	}

	return nil, nil
}

// dateFieldEvents fires "N days before or after a date field", in the
// organization's own timezone and at the hour its author chose.
func (e *Engine) dateFieldEvents(ctx context.Context, rule *models.AutomationRule, cfg crmactions.Config) ([]crmevents.Event, error) {
	field := cfg.Str("field")
	if field == "" {
		return nil, nil
	}
	offset := cfg.Integer("offset_days")

	location := e.location(rule.OrganizationID)
	now := time.Now().In(location)

	// "Nine in the morning" means nine where the business is. Firing at the
	// server's nine would message half the customers overnight.
	if hour, ok := cfg.Number("at_local_hour"); ok && now.Hour() != int(hour) {
		return nil, nil
	}

	// A date field with an offset of -7 fires when the stored date is seven
	// days from today.
	target := now.AddDate(0, 0, -offset).Format("2006-01-02")

	type row struct {
		ContactID uuid.UUID
		ValueDate time.Time
	}
	var rows []row
	err := e.db().WithContext(ctx).Table("custom_field_values AS cfv").
		Select("cfv.entity_id AS contact_id, cfv.value_date").
		Joins("JOIN custom_field_definitions d ON d.id = cfv.field_id").
		Where("cfv.entity_type = 'contact' AND d.organization_id = ? AND d.key = ?",
			rule.OrganizationID, field).
		Where("cfv.value_date IS NOT NULL AND to_char(cfv.value_date, 'YYYY-MM-DD') = ?", target).
		Limit(TimeTriggerBatch).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]crmevents.Event, 0, len(rows))
	for _, r := range rows {
		subject := fmt.Sprintf("%s:%s:%d", field, r.ValueDate.Format("2006-01-02"), offset)
		out = append(out, e.syntheticEvent(rule, r.ContactID, subject, map[string]any{
			"field": field,
			"date":  r.ValueDate.Format("2006-01-02"),
		}))
	}
	return out, nil
}

// syntheticEvent builds the event a time trigger stands in for.
func (e *Engine) syntheticEvent(rule *models.AutomationRule, contactID uuid.UUID, subject string, data map[string]any) crmevents.Event {
	data["subject_key"] = subject

	event := crmevents.Event{
		// Deterministic: the same rule and subject always produce the same id,
		// so a rule that has already fired for this waiting period conflicts on
		// the run index instead of messaging the customer twice.
		ID:         uuid.NewSHA1(timeTriggerNamespace, []byte(rule.ID.String()+"|"+subject)),
		Type:       rule.TriggerType,
		OrgID:      rule.OrganizationID,
		ContactID:  &contactID,
		Actor:      crmevents.SystemActor(),
		Data:       data,
		OccurredAt: time.Now().UTC(),
	}
	return event
}
