package reports

import (
	"context"

	"github.com/shridarpatil/whatomate/internal/models"
)

// FunnelStep is one stage of a funnel.
type FunnelStep struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Reached is how many records got to this stage or past it during the
	// period. "Reached" rather than "are currently in" is the standard CRM
	// reading: a funnel of current positions double-counts nobody but also
	// tells you nothing about flow.
	Reached int64 `json:"reached"`
	// Conversion is the share of the previous step that got this far.
	Conversion float64 `json:"conversion"`
	// MedianDaysFromPrevious is how long the step usually takes.
	MedianDaysFromPrevious *float64 `json:"median_days_from_previous,omitempty"`
}

// LifecycleFunnel is R2: how many contacts move through each stage.
type LifecycleFunnel struct {
	Steps []FunnelStep `json:"steps"`
	// Note explains the counting rule, because a funnel whose semantics are
	// guessed is a funnel that starts an argument.
	Note string `json:"note"`
}

// LifecycleFunnel builds the contact lifecycle funnel for a period.
func (s *Service) LifecycleFunnel(ctx context.Context, v Viewer, r Range) (*LifecycleFunnel, error) {
	stages, err := s.lifecycleStages(ctx, v.OrgID)
	if err != nil {
		return nil, err
	}
	if len(stages) == 0 {
		return &LifecycleFunnel{Steps: []FunnelStep{}}, nil
	}

	// Every stage a contact reached in the period, from the activity log.
	// The log is the only place that records movement; the current value on
	// the contact says where they ended up, not how they got there.
	type reach struct {
		Stage string
		Count int64
	}
	var rows []reach
	err = s.DB.WithContext(ctx).Table("contact_activities AS a").
		Select("lower(a.data->>'to') AS stage, count(DISTINCT a.contact_id) AS count").
		Where("a.organization_id = ? AND a.type = ?", v.OrgID, "contact.field_changed").
		Where("a.data->>'field' = ?", LifecycleField).
		Where("a.occurred_at >= ? AND a.occurred_at <= ?", r.From, r.To).
		Group("stage").Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	reached := make(map[string]int64, len(rows))
	for _, row := range rows {
		reached[row.Stage] = row.Count
	}

	// A contact that reached a later stage necessarily passed the earlier
	// ones, so each step includes everything below it. Without this, a funnel
	// where people skip a stage reads as a collapse rather than a shortcut.
	cumulative := make([]int64, len(stages))
	running := int64(0)
	for i := len(stages) - 1; i >= 0; i-- {
		running += reached[stages[i].Value]
		cumulative[i] = running
	}

	steps := make([]FunnelStep, 0, len(stages))
	for i, stage := range stages {
		step := FunnelStep{Key: stage.Value, Label: stage.Label, Reached: cumulative[i]}
		if i > 0 && cumulative[i-1] > 0 {
			step.Conversion = float64(cumulative[i]) / float64(cumulative[i-1]) * 100
		} else if i == 0 {
			step.Conversion = 100
		}
		steps = append(steps, step)
	}

	return &LifecycleFunnel{
		Steps: steps,
		Note:  "A contact counts at every stage up to the furthest one it reached in this period.",
	}, nil
}

// stageOption is one configured lifecycle value.
type stageOption struct {
	Value string
	Label string
}

// lifecycleStages reads the organization's own stage list, in its own order.
//
// The order is the funnel: an organization that added "Trialling" between Lead
// and Customer must see it in that position, not alphabetically.
func (s *Service) lifecycleStages(ctx context.Context, orgID interface{ String() string }) ([]stageOption, error) {
	var def models.CustomFieldDefinition
	err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND entity_type = ? AND key = ?",
			orgID, models.FieldEntityContact, LifecycleField).
		First(&def).Error
	if err != nil {
		return nil, nil
	}

	out := make([]stageOption, 0, len(def.Options))
	for _, raw := range def.Options {
		option, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		value, _ := option["value"].(string)
		if value == "" {
			continue
		}
		label, _ := option["label"].(string)
		if label == "" {
			label = value
		}
		out = append(out, stageOption{Value: value, Label: label})
	}
	return out, nil
}
