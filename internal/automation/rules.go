package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// ErrNotFound is returned when a rule does not exist in the organization.
var ErrNotFound = errors.New("automation: rule not found")

// Limits. These are low on purpose: a rule nobody can read is a rule nobody
// can debug, and an organization with a thousand rules has a process problem
// that more rules will not fix.
const (
	MaxActionsPerRule = 10
	MaxRulesPerOrg    = 200
	// MaxDepth stops a chain of rules triggering each other. Three is enough
	// for "tag → assign → notify" and short enough to notice a loop.
	MaxDepth = 3
)

// ActionSpec is one entry in a rule's action list.
type ActionSpec struct {
	// ID is stable across retries, so a retry can skip the actions that
	// already succeeded rather than sending the message twice.
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Config crmactions.Config `json:"config"`
	// ContinueOnError keeps the rest of the list running when this one fails,
	// for actions that are nice-to-have rather than the point of the rule.
	ContinueOnError bool `json:"continue_on_error"`
}

// RunPolicy bounds how often a rule may act.
type RunPolicy struct {
	OncePerContact  bool `json:"once_per_contact"`
	CooldownMinutes int  `json:"cooldown_minutes"`
	MaxRunsPerHour  int  `json:"max_runs_per_hour"`
}

// DefaultMaxRunsPerHour is the cap a rule gets when its author sets none. An
// unbounded rule is one bad event away from messaging every customer at once.
const DefaultMaxRunsPerHour = 500

// Service stores and validates rules.
type Service struct {
	DB *gorm.DB

	// BuildRegistry supplies the filter fields a rule's conditions may use.
	// It is injected because the full set depends on the organization's custom
	// fields and pipeline stages, which live in packages this one must not
	// import to avoid a cycle. Without it only the built-in fields are known,
	// which is the right behaviour for a process that has no such context.
	BuildRegistry func(ctx context.Context, orgID uuid.UUID) (*contactquery.Registry, error)
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// Input describes a rule to create or update.
type Input struct {
	Name          string
	Description   string
	Enabled       *bool
	TriggerType   string
	TriggerConfig map[string]any
	ContactFilter *contactquery.Filter
	Actions       []ActionSpec
	RunPolicy     *RunPolicy
	ActorID       *uuid.UUID
}

// Validate rejects a rule the engine could not run.
//
// Everything checkable is checked here rather than at run time, because a rule
// that fails at three in the morning is discovered by a customer.
func (s *Service) Validate(ctx context.Context, orgID uuid.UUID, in Input) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("automation: a rule needs a name")
	}
	if err := validateTriggerConfig(in.TriggerType, in.TriggerConfig); err != nil {
		return err
	}
	if len(in.Actions) == 0 {
		return fmt.Errorf("automation: a rule with no actions does nothing")
	}
	if len(in.Actions) > MaxActionsPerRule {
		return fmt.Errorf("automation: a rule may have at most %d actions", MaxActionsPerRule)
	}

	seen := map[string]bool{}
	for i, action := range in.Actions {
		if action.ID == "" {
			return fmt.Errorf("automation: action %d needs an id", i+1)
		}
		if seen[action.ID] {
			return fmt.Errorf("automation: two actions share the id %q", action.ID)
		}
		seen[action.ID] = true

		if err := crmactions.Validate(action.Type, action.Config); err != nil {
			return fmt.Errorf("automation: action %d (%s): %w", i+1, action.Type, err)
		}
	}

	if in.ContactFilter != nil && !in.ContactFilter.IsEmpty() {
		registry, err := s.Registry(ctx, orgID)
		if err != nil {
			return err
		}
		if err := contactquery.Validate(registry, *in.ContactFilter); err != nil {
			return err
		}
	}
	return nil
}

// Registry builds the filter registry a rule's conditions compile against.
//
// It is a field so the caller can supply the org's custom fields and pipeline
// stages; without them a condition on a custom field would be rejected as an
// unknown field.
func (s *Service) Registry(ctx context.Context, orgID uuid.UUID) (*contactquery.Registry, error) {
	if s.BuildRegistry != nil {
		return s.BuildRegistry(ctx, orgID)
	}
	return contactquery.NewRegistry(), nil
}

// Create stores a new rule.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in Input) (*models.AutomationRule, error) {
	var existing int64
	if err := s.DB.WithContext(ctx).Model(&models.AutomationRule{}).
		Where("organization_id = ?", orgID).Count(&existing).Error; err != nil {
		return nil, err
	}
	if existing >= MaxRulesPerOrg {
		return nil, fmt.Errorf("automation: an organization may have at most %d rules", MaxRulesPerOrg)
	}

	if err := s.Validate(ctx, orgID, in); err != nil {
		return nil, err
	}

	triggerConfig := models.JSONB(in.TriggerConfig)
	if triggerConfig == nil {
		triggerConfig = models.JSONB{}
	}

	rule := &models.AutomationRule{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: orgID,
		Name:           strings.TrimSpace(in.Name),
		Description:    in.Description,
		TriggerType:    in.TriggerType,
		TriggerConfig:  triggerConfig,
		Actions:        actionsToJSON(in.Actions),
		RunPolicy:      policyToJSON(in.RunPolicy),
		CreatedByID:    in.ActorID,
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	if in.ContactFilter != nil && !in.ContactFilter.IsEmpty() {
		rule.ContactFilter = filterToJSON(*in.ContactFilter)
	}
	if err := s.DB.WithContext(ctx).Create(rule).Error; err != nil {
		return nil, err
	}
	return rule, nil
}

// Update edits a rule.
func (s *Service) Update(ctx context.Context, orgID, ruleID uuid.UUID, in Input) (*models.AutomationRule, error) {
	rule, err := s.Get(ctx, orgID, ruleID)
	if err != nil {
		return nil, err
	}
	if err := s.Validate(ctx, orgID, in); err != nil {
		return nil, err
	}

	updates := map[string]any{
		"name":           strings.TrimSpace(in.Name),
		"description":    in.Description,
		"trigger_type":   in.TriggerType,
		"trigger_config": jsonbOrEmpty(in.TriggerConfig),
		"actions":        actionsToJSON(in.Actions),
		"run_policy":     policyToJSON(in.RunPolicy),
		"updated_by_id":  in.ActorID,
		// Editing a rule is a fresh start: leaving the old failure count in
		// place would auto-disable a rule the author has just fixed.
		"consecutive_failures": 0,
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if in.ContactFilter == nil || in.ContactFilter.IsEmpty() {
		updates["contact_filter"] = nil
	} else {
		updates["contact_filter"] = filterToJSON(*in.ContactFilter)
	}

	if err := s.DB.WithContext(ctx).Model(&models.AutomationRule{}).
		Where("id = ?", rule.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, ruleID)
}

// SetEnabled turns a rule on or off.
func (s *Service) SetEnabled(ctx context.Context, orgID, ruleID uuid.UUID, enabled bool) (*models.AutomationRule, error) {
	updates := map[string]any{"enabled": enabled}
	if enabled {
		// Re-enabling forgives the failures that turned it off, or it would
		// disable itself again on the next error.
		updates["consecutive_failures"] = 0
	}
	res := s.DB.WithContext(ctx).Model(&models.AutomationRule{}).
		Where("id = ? AND organization_id = ?", ruleID, orgID).Updates(updates)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, orgID, ruleID)
}

// Get returns one rule.
func (s *Service) Get(ctx context.Context, orgID, ruleID uuid.UUID) (*models.AutomationRule, error) {
	var rule models.AutomationRule
	if err := s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", ruleID, orgID).
		First(&rule).Error; err != nil {
		return nil, ErrNotFound
	}
	return &rule, nil
}

// List returns an organization's rules, newest first.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]models.AutomationRule, error) {
	var out []models.AutomationRule
	err := s.DB.WithContext(ctx).Where("organization_id = ?", orgID).
		Order("created_at DESC").Find(&out).Error
	return out, err
}

// Delete removes a rule.
func (s *Service) Delete(ctx context.Context, orgID, ruleID uuid.UUID) error {
	res := s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", ruleID, orgID).
		Delete(&models.AutomationRule{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Enabled returns the enabled rules for one trigger type.
func (s *Service) Enabled(ctx context.Context, orgID uuid.UUID, triggerType string) ([]models.AutomationRule, error) {
	var out []models.AutomationRule
	err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND trigger_type = ? AND enabled = true", orgID, triggerType).
		Order("created_at").Find(&out).Error
	return out, err
}

// --- Decoding stored JSON ---

// RuleActions reads a rule's action list.
//
// The list is stored under a "list" key because the column is a JSON object,
// not an array: everything else in the schema uses the same map type and a
// bespoke array type for one column would be worse than one wrapper key.
func RuleActions(rule *models.AutomationRule) []ActionSpec {
	raw, err := json.Marshal(rule.Actions["list"])
	if err != nil {
		return nil
	}
	var out []ActionSpec
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// RulePolicy reads a rule's run policy, filling in the defaults that keep an
// unconfigured rule from running away.
func RulePolicy(rule *models.AutomationRule) RunPolicy {
	policy := RunPolicy{MaxRunsPerHour: DefaultMaxRunsPerHour}

	raw, err := json.Marshal(rule.RunPolicy)
	if err != nil {
		return policy
	}
	_ = json.Unmarshal(raw, &policy)
	if policy.MaxRunsPerHour <= 0 {
		policy.MaxRunsPerHour = DefaultMaxRunsPerHour
	}
	return policy
}

// RuleFilter reads a rule's contact conditions.
func RuleFilter(rule *models.AutomationRule) (contactquery.Filter, bool) {
	if len(rule.ContactFilter) == 0 {
		return contactquery.Filter{}, false
	}
	raw, err := json.Marshal(rule.ContactFilter)
	if err != nil {
		return contactquery.Filter{}, false
	}
	var filter contactquery.Filter
	if err := json.Unmarshal(raw, &filter); err != nil || filter.IsEmpty() {
		return contactquery.Filter{}, false
	}
	return filter, true
}

// RuleTriggerConfig reads a rule's trigger settings.
func RuleTriggerConfig(rule *models.AutomationRule) crmactions.Config {
	return crmactions.Config(rule.TriggerConfig)
}

func actionsToJSON(actions []ActionSpec) models.JSONB {
	raw, err := json.Marshal(actions)
	if err != nil {
		return models.JSONB{}
	}
	var out []any
	_ = json.Unmarshal(raw, &out)
	return models.JSONB{"list": out}
}

// jsonbOrEmpty keeps a not-null jsonb column from being handed a nil map.
func jsonbOrEmpty(m map[string]any) models.JSONB {
	if m == nil {
		return models.JSONB{}
	}
	return models.JSONB(m)
}

func policyToJSON(policy *RunPolicy) models.JSONB {
	if policy == nil {
		policy = &RunPolicy{MaxRunsPerHour: DefaultMaxRunsPerHour}
	}
	raw, _ := json.Marshal(policy)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return models.JSONB(out)
}

func filterToJSON(filter contactquery.Filter) models.JSONB {
	raw, _ := json.Marshal(filter)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return models.JSONB(out)
}
