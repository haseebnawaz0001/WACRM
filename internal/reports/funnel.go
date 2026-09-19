package reports

import (
	"context"
	"strings"

	"github.com/google/uuid"
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
		ContactID uuid.UUID
		Stage     string
	}
	var rows []reach
	err = s.DB.WithContext(ctx).Table("contact_activities AS a").
		Select("DISTINCT a.contact_id, lower(a.data->>'to') AS stage").
		Where("a.organization_id = ? AND a.type = ?", v.OrgID, "contact.field_changed").
		Where("a.data->>'field' = ?", LifecycleField).
		Where("a.occurred_at >= ? AND a.occurred_at <= ?", r.From, r.To).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	// Each contact counts once, at the furthest stage it reached. Counting
	// contacts per stage and then summing up the funnel counted a contact who
	// went lead → customer in the period at both, so every earlier step was
	// inflated by exactly the people who progressed — the ones the funnel
	// exists to measure.
	position := make(map[string]int, len(stages))
	for i, stage := range stages {
		position[strings.ToLower(stage.Value)] = i
	}
	furthest := make(map[uuid.UUID]int, len(rows))
	for _, row := range rows {
		i, known := position[row.Stage]
		if !known {
			continue
		}
		if best, seen := furthest[row.ContactID]; !seen || i > best {
			furthest[row.ContactID] = i
		}
	}
	endedAt := make([]int64, len(stages))
	for _, i := range furthest {
		endedAt[i]++
	}

	// A contact that reached a later stage necessarily passed the earlier
	// ones, so each step includes everything below it. Without this, a funnel
	// where people skip a stage reads as a collapse rather than a shortcut.
	cumulative := make([]int64, len(stages))
	running := int64(0)
	for i := len(stages) - 1; i >= 0; i-- {
		running += endedAt[i]
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
