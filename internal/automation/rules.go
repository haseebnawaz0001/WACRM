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

// ErrNoActions is returned when somebody tries to switch on a rule that has
// nothing to do. Saving such a rule is fine — that is a draft — but running
// one is not.
var ErrNoActions = errors.New("automation: add at least one action before turning this rule on")

// Limits. These are low on purpose: a rule nobody can read is a rule nobody
// can debug, and an organization with a thousand rules has a process problem
// that more rules will not fix. The per-rule step limit lives with the step
// model in steps.go.
const (
	MaxRulesPerOrg = 200
	// MaxDepth stops a chain of rules triggering each other. Three is enough
	// for "tag → assign → notify" and short enough to notice a loop.
	MaxDepth = 3
)

// ActionSpec is one step on a rule's path: an action from the shared library,
// or one of the flow-control steps in steps.go.
type ActionSpec struct {
	// ID is stable across retries and edits, so a retry can skip the actions
	// that already succeeded, and a paused run can find its place again in a
	// rule somebody has edited since.
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Config crmactions.Config `json:"config"`
	// ContinueOnError keeps the rest of the list running when this one fails,
	// for actions that are nice-to-have rather than the point of the rule.
	ContinueOnError bool `json:"continue_on_error"`
	// Then and Else are the two paths out of a condition step: the steps for
	// a contact who matches, and for one who does not.
	Then []ActionSpec `json:"then,omitempty"`
	Else []ActionSpec `json:"else,omitempty"`
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
// that fails at three in the morning is discovered by a customer. A rule that
// is switched off is a draft, though, and a draft is allowed to be unfinished:
// people build a rule the way they describe it — pick what starts it, add a
// step, then go and look up which template to send. Refusing to save until
// every blank is filled lost that work, so an unfinished step only stops a
// rule that is on (or being turned on). Structural mistakes are refused
// either way.
func (s *Service) Validate(ctx context.Context, orgID uuid.UUID, in Input) error {
	problems, err := s.check(ctx, orgID, in)
	if err != nil {
		return err
	}
	if in.Enabled != nil && *in.Enabled && len(problems) > 0 {
		return &StepError{problems[0]}
	}
	return nil
}

// Problems lists what stops a rule from running, step by step, without
// refusing anything. The builder shows them on the cards they belong to.
func (s *Service) Problems(ctx context.Context, orgID uuid.UUID, in Input) ([]StepProblem, error) {
	return s.check(ctx, orgID, in)
}

// ProblemsFor lists what stops a stored rule from running.
func (s *Service) ProblemsFor(ctx context.Context, rule *models.AutomationRule) []StepProblem {
	in := InputFromRule(rule)
	problems, err := s.check(ctx, rule.OrganizationID, in)
	if err != nil {
		return []StepProblem{{Message: plainError(err)}}
	}
	return problems
}

// check separates what is wrong with a rule's shape (returned as an error)
// from what is merely unfinished (returned as problems).
func (s *Service) check(ctx context.Context, orgID uuid.UUID, in Input) ([]StepProblem, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("automation: a rule needs a name")
	}
	if err := validateTriggerConfig(in.TriggerType, in.TriggerConfig); err != nil {
		return nil, err
	}

	var registry *contactquery.Registry
	loadRegistry := func() (*contactquery.Registry, error) {
		if registry != nil {
			return registry, nil
		}
		built, err := s.Registry(ctx, orgID)
		if err != nil {
			return nil, err
		}
		registry = built
		return registry, nil
	}

	var problems []StepProblem
	// A time trigger with nothing to measure never fires, and nothing says
	// so: the rule just sits there switched on. The setting it needs is a
	// problem on the start card.
	switch in.TriggerType {
	case TriggerNoCustomerReply, TriggerNoAgentReply:
		if _, ok := crmactions.Config(in.TriggerConfig).Duration("after"); !ok {
			problems = append(problems, StepProblem{StepID: "trigger", Message: "choose how long to wait for a reply"})
		}
	case TriggerDateField:
		if crmactions.Config(in.TriggerConfig).Str("field") == "" {
			problems = append(problems, StepProblem{StepID: "trigger", Message: "choose which date to watch"})
		}
	}

	checker := &stepChecker{ctx: ctx, registry: loadRegistry, seen: map[string]bool{}}
	checker.walk(in.Actions, 0)
	if checker.structural != nil {
		return nil, checker.structural
	}
	problems = append(problems, checker.problems...)

	// A rule with nothing to do is a draft, not a mistake: building one is how
	// everybody starts.
	if countSteps(in.Actions) == 0 {
		problems = append(problems, StepProblem{Message: "add at least one step"})
	}

	if in.ContactFilter != nil && !in.ContactFilter.IsEmpty() {
		reg, err := loadRegistry()
		if err != nil {
			return nil, err
		}
		if err := contactquery.Validate(reg, *in.ContactFilter); err != nil {
			problems = append(problems, StepProblem{StepID: "filter", Message: plainError(err)})
		}
	}
	return problems, nil
}

// InputFromRule reads a stored rule back into the shape Validate takes.
func InputFromRule(rule *models.AutomationRule) Input {
	in := Input{
		Name:          rule.Name,
		Description:   rule.Description,
		TriggerType:   rule.TriggerType,
		TriggerConfig: map[string]any(rule.TriggerConfig),
		Actions:       RuleActions(rule),
	}
	enabled := rule.Enabled
	in.Enabled = &enabled
	policy := RulePolicy(rule)
	in.RunPolicy = &policy
	if filter, ok := RuleFilter(rule); ok {
		in.ContactFilter = &filter
	}
	return in
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

	// Saving a rule with nothing to do is fine; saving one that is switched on
	// with nothing to do is not.
	if in.Enabled != nil && *in.Enabled && countSteps(in.Actions) == 0 {
		return nil, ErrNoActions
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
	// A rule that is on stays held to the full standard while it is edited:
	// the edit is live the moment it saves.
	willBeEnabled := rule.Enabled
	if in.Enabled != nil {
		willBeEnabled = *in.Enabled
	}
	if willBeEnabled && countSteps(in.Actions) == 0 {
		return nil, ErrNoActions
	}
	strict := in
	strict.Enabled = &willBeEnabled
	if err := s.Validate(ctx, orgID, strict); err != nil {
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
//
// Turning one on is where "a rule with no actions does nothing" actually
// bites, and where it was never checked: this path writes `enabled` straight
// to the row, so a rule with an empty action list could be switched on and sit
// there firing into nothing.
func (s *Service) SetEnabled(ctx context.Context, orgID, ruleID uuid.UUID, enabled bool) (*models.AutomationRule, error) {
	if enabled {
		rule, err := s.Get(ctx, orgID, ruleID)
		if err != nil {
			return nil, err
		}
		if countSteps(RuleActions(rule)) == 0 {
			return nil, ErrNoActions
		}
		// A draft may be unfinished; a rule that is about to run may not.
		in := InputFromRule(rule)
		in.Enabled = &enabled
		if err := s.Validate(ctx, orgID, in); err != nil {
			return nil, err
		}
	}
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

// DraftRule is a stored rule with unsaved edits applied, for a dry run. It is
// never written: the id is the stored rule's, so the test lands in that rule's
// history.
func DraftRule(saved *models.AutomationRule, in Input) *models.AutomationRule {
	draft := *saved
	draft.Name = strings.TrimSpace(in.Name)
	draft.TriggerType = in.TriggerType
	draft.TriggerConfig = jsonbOrEmpty(in.TriggerConfig)
	draft.Actions = actionsToJSON(in.Actions)
	draft.RunPolicy = policyToJSON(in.RunPolicy)
	draft.ContactFilter = nil
	if in.ContactFilter != nil && !in.ContactFilter.IsEmpty() {
		draft.ContactFilter = filterToJSON(*in.ContactFilter)
	}
	return &draft
}
