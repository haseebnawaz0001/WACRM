package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AutoDisableAfter is how many consecutive failed runs turn a rule off.
//
// A rule that has failed ten times in a row is broken, and leaving it running
// only buries the evidence under more failures.
const AutoDisableAfter = 10

// ActionResult is one action's outcome, stored on the run.
type ActionResult struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Status string         `json:"status"` // succeeded | failed | skipped
	Error  string         `json:"error,omitempty"`
	Output map[string]any `json:"output,omitempty"`
}

// Engine runs rules against events.
type Engine struct {
	Rules *Service
	Deps  crmactions.Deps

	// Redis backs the per-rule hourly rate limit. Without it the limit is not
	// enforced, which is stated rather than silently ignored.
	Redis *redis.Client

	Log Logger

	// Location resolves an organization's timezone for date-sensitive actions.
	Location func(orgID uuid.UUID) *time.Location
}

func (e *Engine) db() *gorm.DB { return e.Deps.DB }

func (e *Engine) log() Logger {
	if e.Log != nil {
		return e.Log
	}
	return slogLogger{slog.Default()}
}

// Handle runs every rule that matches an event.
//
// It returns the runs it recorded, which is what the tests assert on and what
// the caller acknowledges the stream entry against.
func (e *Engine) Handle(ctx context.Context, event crmevents.Event) ([]*models.AutomationRun, error) {
	rules, err := e.Rules.Enabled(ctx, event.OrgID, event.Type)
	if err != nil {
		return nil, err
	}

	out := make([]*models.AutomationRun, 0, len(rules))
	for i := range rules {
		run, err := e.runRule(ctx, &rules[i], event, false)
		if err != nil {
			// One broken rule must not stop the others: they are independent
			// promises to different people.
			e.log().Error("Automation rule failed", "error", err,
				"rule_id", rules[i].ID, "event_type", event.Type)
			continue
		}
		if run != nil {
			out = append(out, run)
		}
	}
	return out, nil
}

// DryRun evaluates a rule against an event without changing anything.
func (e *Engine) DryRun(ctx context.Context, rule *models.AutomationRule, event crmevents.Event) (*models.AutomationRun, error) {
	return e.runRule(ctx, rule, event, true)
}

func (e *Engine) runRule(ctx context.Context, rule *models.AutomationRule, event crmevents.Event, dryRun bool) (*models.AutomationRun, error) {
	started := time.Now().UTC()

	run := &models.AutomationRun{
		ID:             uuid.New(),
		OrganizationID: rule.OrganizationID,
		RuleID:         rule.ID,
		EventID:        event.ID,
		EventType:      event.Type,
		ContactID:      event.ContactID,
		Depth:          event.Origin.Depth,
		DryRun:         dryRun,
		StartedAt:      started,
		ActionResults:  models.JSONB{},
	}

	if reason := e.skipReason(ctx, rule, event, dryRun); reason != "" {
		if reason == models.SkipTriggerNotMet && !dryRun {
			// Not recorded: every rule on an org is asked about every event of
			// its type, so logging the ones whose settings did not match would
			// bury the runs that mean something.
			return nil, nil
		}
		run.Status = models.AutomationSkipped
		run.SkipReason = reason
		return e.finish(ctx, rule, run, subjectKey(event))
	}

	// Claim the run before doing anything. The unique (rule_id, event_id)
	// index is what makes a redelivered stream entry a no-op rather than a
	// second message to the customer.
	if !dryRun {
		claimed, err := e.claim(ctx, run)
		if err != nil {
			return nil, err
		}
		if !claimed {
			return nil, nil
		}
	}

	results := e.execute(ctx, rule, event, dryRun)
	run.Status = statusFor(results)
	run.ActionResults = models.JSONB{"list": toAnySlice(results)}

	return e.finish(ctx, rule, run, subjectKey(event))
}

// skipReason decides whether the rule should not act, and says why.
func (e *Engine) skipReason(ctx context.Context, rule *models.AutomationRule, event crmevents.Event, dryRun bool) string {
	if !MatchTrigger(rule, event) {
		return models.SkipTriggerNotMet
	}

	// Loop protection. A rule must never react to its own output, and a chain
	// of rules reacting to each other has to stop somewhere.
	if event.Origin.RuleID != nil && *event.Origin.RuleID == rule.ID {
		return models.SkipSelfTrigger
	}
	if event.Origin.Depth >= MaxDepth {
		return models.SkipLoopDepth
	}

	if event.ContactID != nil {
		if matched, err := e.conditionsMatch(ctx, rule, *event.ContactID); err != nil {
			e.log().Error("Automation conditions failed to evaluate", "error", err, "rule_id", rule.ID)
			return models.SkipConditionsNotMet
		} else if !matched {
			return models.SkipConditionsNotMet
		}
	}

	// A dry run is allowed past the policies on purpose: the author is asking
	// "what would this do", and answering "nothing, it is in cooldown" while
	// hiding the actions is not the question they asked.
	if dryRun {
		return ""
	}

	policy := RulePolicy(rule)
	if event.ContactID != nil {
		if reason := e.policyReason(ctx, rule, policy, *event.ContactID, subjectKey(event)); reason != "" {
			return reason
		}
	}
	if e.rateLimited(ctx, rule, policy) {
		return models.SkipRateLimited
	}
	return ""
}

// conditionsMatch asks the database whether this contact satisfies the rule's
// filter. Compiling the filter and adding "AND contacts.id = ?" reuses one
// query language rather than reimplementing the operators in Go.
func (e *Engine) conditionsMatch(ctx context.Context, rule *models.AutomationRule, contactID uuid.UUID) (bool, error) {
	filter, ok := RuleFilter(rule)
	if !ok {
		return true, nil
	}

	registry, err := e.Rules.Registry(ctx, rule.OrganizationID)
	if err != nil {
		return false, err
	}

	viewer := contactquery.Viewer{
		OrgID: rule.OrganizationID,
		// A rule is not a person: it sees whatever its conditions name, or an
		// automation would behave differently depending on who saved it.
		CanSeeAllContacts: true,
		Location:          e.location(rule.OrganizationID),
	}

	query, err := contactquery.Apply(e.db().WithContext(ctx).Model(&models.Contact{}), registry, viewer, filter)
	if err != nil {
		return false, err
	}

	var count int64
	if err := query.Where("contacts.id = ?", contactID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// policyReason enforces once-per-contact and cooldown.
func (e *Engine) policyReason(ctx context.Context, rule *models.AutomationRule, policy RunPolicy, contactID uuid.UUID, subject string) string {
	if !policy.OncePerContact && policy.CooldownMinutes <= 0 {
		return ""
	}

	var state models.AutomationContactState
	err := e.db().WithContext(ctx).
		Where("rule_id = ? AND contact_id = ? AND subject_key = ?", rule.ID, contactID, subject).
		First(&state).Error
	if err != nil {
		return ""
	}

	if policy.OncePerContact {
		return models.SkipOncePerContact
	}
	if policy.CooldownMinutes > 0 {
		if time.Since(state.LastRunAt) < time.Duration(policy.CooldownMinutes)*time.Minute {
			return models.SkipCooldown
		}
	}
	return ""
}

// rateLimited enforces the per-rule hourly cap with a Redis counter.
//
// The counter is per clock hour rather than a sliding window: an exact window
// costs a sorted set per rule, and "no more than N in an hour" is the promise
// the setting makes.
func (e *Engine) rateLimited(ctx context.Context, rule *models.AutomationRule, policy RunPolicy) bool {
	if e.Redis == nil || policy.MaxRunsPerHour <= 0 {
		return false
	}

	key := fmt.Sprintf("wacrm:auto:rate:%s:%d", rule.ID, time.Now().UTC().Unix()/3600)
	count, err := e.Redis.Incr(ctx, key).Result()
	if err != nil {
		// A Redis outage must not stop automations; the other limits still
		// apply and the failure is visible in the logs.
		e.log().Warn("Automation rate limit unavailable", "error", err, "rule_id", rule.ID)
		return false
	}
	if count == 1 {
		e.Redis.Expire(ctx, key, 2*time.Hour)
	}
	return count > int64(policy.MaxRunsPerHour)
}

// claim inserts the run row, returning false when this event has already been
// handled by this rule.
func (e *Engine) claim(ctx context.Context, run *models.AutomationRun) (bool, error) {
	res := e.db().WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(run)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// execute performs the rule's actions in order.
func (e *Engine) execute(ctx context.Context, rule *models.AutomationRule, event crmevents.Event, dryRun bool) []ActionResult {
	actions := RuleActions(rule)
	results := make([]ActionResult, 0, len(actions))

	ruleID := rule.ID
	rc := crmactions.RunContext{
		OrgID: rule.OrganizationID,
		Event: event,
		Actor: crmevents.Actor{
			Type: crmevents.ActorAutomation,
			ID:   &ruleID,
			Name: rule.Name,
		},
		// Everything this rule causes is one step further from the change a
		// person actually made.
		Origin: crmevents.Origin{RuleID: &ruleID, Depth: event.Origin.Depth + 1},
		// An automation is not a person, so actions that need a human — a
		// task's last-resort owner, a note's author — fall back to whoever
		// wrote the rule.
		CreatorID: rule.CreatedByID,
		DryRun:    dryRun,
		Location:  e.location(rule.OrganizationID),
	}
	if event.ContactID != nil {
		rc.ContactID = *event.ContactID
	}
	rc.Vars = e.variables(ctx, rule, event, rc.ContactID)

	for _, action := range actions {
		output, err := crmactions.Execute(ctx, e.Deps, rc, action.Type, action.Config)
		result := ActionResult{ID: action.ID, Type: action.Type, Output: output}

		if err == nil {
			result.Status = "succeeded"
			results = append(results, result)
			continue
		}

		result.Status = "failed"
		result.Error = err.Error()
		results = append(results, result)

		if !action.ContinueOnError {
			// The remaining actions are recorded as skipped rather than left
			// out, so the run log shows the whole intended sequence.
			for _, remaining := range actions[len(results):] {
				results = append(results, ActionResult{
					ID: remaining.ID, Type: remaining.Type, Status: "skipped",
					Error: "an earlier action failed",
				})
			}
			break
		}
	}
	return results
}

// variables are what a rule's templated text can refer to.
func (e *Engine) variables(ctx context.Context, rule *models.AutomationRule, event crmevents.Event, contactID uuid.UUID) map[string]any {
	vars := map[string]any{
		"rule":  map[string]any{"name": rule.Name},
		"event": map[string]any{"type": event.Type, "data": event.Data},
		"now":   time.Now().In(e.location(rule.OrganizationID)).Format("2006-01-02 15:04"),
	}

	if contactID == uuid.Nil {
		return vars
	}

	var contact models.Contact
	if err := e.db().WithContext(ctx).Where("id = ?", contactID).First(&contact).Error; err != nil {
		return vars
	}
	vars["contact"] = map[string]any{
		"name":         contact.ProfileName,
		"phone_number": contact.PhoneNumber,
		"tags":         contact.Tags,
	}
	return vars
}

// finish records the outcome and keeps the rule's counters honest.
func (e *Engine) finish(ctx context.Context, rule *models.AutomationRule, run *models.AutomationRun, subject string) (*models.AutomationRun, error) {
	finished := time.Now().UTC()
	run.FinishedAt = &finished

	if run.DryRun {
		// A dry run is still recorded, so "I tested it and it said X" is
		// checkable rather than remembered.
		if err := e.db().WithContext(ctx).Create(run).Error; err != nil {
			return nil, err
		}
		return run, nil
	}

	if run.Status == models.AutomationSkipped && run.SkipReason != "" {
		// Skips are not claimed up front, so they are inserted here. A
		// duplicate means the same event was already judged; leave it alone.
		if _, err := e.claim(ctx, run); err != nil {
			return nil, err
		}
		return run, nil
	}

	if err := e.db().WithContext(ctx).Model(&models.AutomationRun{}).
		Where("id = ?", run.ID).
		Updates(map[string]any{
			"status":         run.Status,
			"action_results": run.ActionResults,
			"finished_at":    finished,
		}).Error; err != nil {
		return nil, err
	}

	e.updateRuleCounters(ctx, rule, run)
	if run.ContactID != nil {
		e.recordContactState(ctx, rule, *run.ContactID, subject, run)
	}
	return run, nil
}

func (e *Engine) updateRuleCounters(ctx context.Context, rule *models.AutomationRule, run *models.AutomationRun) {
	updates := map[string]any{
		"last_run_at": run.StartedAt,
		"run_count":   gorm.Expr("run_count + 1"),
	}

	failed := run.Status == models.AutomationFailed
	if failed {
		updates["error_count"] = gorm.Expr("error_count + 1")
		updates["consecutive_failures"] = gorm.Expr("consecutive_failures + 1")
	} else {
		updates["consecutive_failures"] = 0
	}

	if err := e.db().WithContext(ctx).Model(&models.AutomationRule{}).
		Where("id = ?", rule.ID).Updates(updates).Error; err != nil {
		e.log().Error("Failed to update automation counters", "error", err, "rule_id", rule.ID)
		return
	}

	if failed {
		e.maybeAutoDisable(ctx, rule)
	}
}

// maybeAutoDisable turns off a rule that keeps failing and tells its author.
func (e *Engine) maybeAutoDisable(ctx context.Context, rule *models.AutomationRule) {
	var current models.AutomationRule
	if err := e.db().WithContext(ctx).Where("id = ?", rule.ID).First(&current).Error; err != nil {
		return
	}
	if current.ConsecutiveFailures < AutoDisableAfter || !current.Enabled {
		return
	}

	if err := e.db().WithContext(ctx).Model(&models.AutomationRule{}).
		Where("id = ?", rule.ID).Update("enabled", false).Error; err != nil {
		return
	}
	e.log().Warn("Automation rule auto-disabled after repeated failures",
		"rule_id", rule.ID, "failures", current.ConsecutiveFailures)

	if current.CreatedByID == nil {
		return
	}
	_ = notify.New(e.db()).Send(ctx, notify.Input{
		OrgID:   rule.OrganizationID,
		UserIDs: []uuid.UUID{*current.CreatedByID},
		Type:    models.NotificationAutomation,
		Title:   "Automation disabled",
		Body: fmt.Sprintf("%q failed %d times in a row and has been turned off.",
			current.Name, current.ConsecutiveFailures),
		Entity: notify.Entity{Type: "automation", ID: &rule.ID},
	})
}

// recordContactState remembers that this rule has acted on this contact, which
// is what once-per-contact and cooldown read.
func (e *Engine) recordContactState(ctx context.Context, rule *models.AutomationRule, contactID uuid.UUID, subject string, run *models.AutomationRun) {
	state := models.AutomationContactState{
		RuleID:     rule.ID,
		ContactID:  contactID,
		SubjectKey: subject,
		LastRunAt:  run.StartedAt,
		RunCount:   1,
	}

	err := e.db().WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "rule_id"}, {Name: "contact_id"}, {Name: "subject_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_run_at": run.StartedAt,
			"run_count":   gorm.Expr("automation_contact_state.run_count + 1"),
		}),
	}).Create(&state).Error
	if err != nil {
		e.log().Error("Failed to record automation contact state", "error", err, "rule_id", rule.ID)
	}
}

func (e *Engine) location(orgID uuid.UUID) *time.Location {
	if e.Location == nil {
		return time.UTC
	}
	if loc := e.Location(orgID); loc != nil {
		return loc
	}
	return time.UTC
}

// statusFor summarises a run from its actions.
func statusFor(results []ActionResult) string {
	if len(results) == 0 {
		return models.AutomationSucceeded
	}

	succeeded, failed := 0, 0
	for _, result := range results {
		switch result.Status {
		case "succeeded":
			succeeded++
		case "failed":
			failed++
		}
	}
	switch {
	case failed == 0:
		return models.AutomationSucceeded
	case succeeded == 0:
		return models.AutomationFailed
	default:
		// Partially failed is its own status because "it sent the message but
		// did not create the task" is a different problem from either.
		return models.AutomationPartiallyFailed
	}
}

// subjectKey separates repeats that are about different things. Time triggers
// set it from the event so a rule re-arms per waiting period.
func subjectKey(event crmevents.Event) string {
	if event.Data == nil {
		return ""
	}
	key, _ := event.Data["subject_key"].(string)
	return key
}

func toAnySlice(results []ActionResult) []any {
	raw, err := json.Marshal(results)
	if err != nil {
		return nil
	}
	var out []any
	_ = json.Unmarshal(raw, &out)
	return out
}
