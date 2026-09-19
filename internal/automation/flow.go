package automation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Step result statuses beyond the three an action can end in.
const (
	// ResultWaiting is a wait step the run is parked at.
	ResultWaiting = "waiting"
)

// pause is where a run stopped to wait, and until when.
type pause struct {
	StepID   string
	ResumeAt time.Time
}

// stepRunner walks a rule's path for one contact.
//
// It is a small state machine rather than a loop because a path can now split
// and stop part-way: a question sends the contact down one of two branches,
// and a wait parks the whole run until later. Results are appended in the
// order steps ran, and branches not taken are simply absent, so the history
// shows the path this contact actually walked.
type stepRunner struct {
	e      *Engine
	ctx    context.Context
	rule   *models.AutomationRule
	rc     crmactions.RunContext
	dryRun bool

	results []ActionResult
	// stopped is set when a step failed and was not allowed to be skipped.
	stopped bool
	// paused is set when a wait parked the run.
	paused *pause
}

func (r *stepRunner) halted() bool { return r.stopped || r.paused != nil }

// run performs one list of steps in order, descending into whichever branch
// each question picks.
func (r *stepRunner) run(steps []ActionSpec) {
	for i, step := range steps {
		if r.paused != nil {
			return
		}
		if r.stopped {
			// The rest of this list is recorded as skipped rather than left
			// out, so the history shows the whole intended sequence.
			for _, remaining := range steps[i:] {
				r.results = append(r.results, ActionResult{
					ID: remaining.ID, Type: remaining.Type, Status: "skipped",
					Error: "an earlier step failed",
				})
			}
			return
		}

		switch step.Type {
		case StepCondition:
			r.runCondition(step)
		case StepWait:
			r.runWait(step)
		default:
			r.runAction(step)
		}
	}
}

func (r *stepRunner) runAction(step ActionSpec) {
	// A rule that is on cannot hold an unfinished step, but a draft being
	// tried on a contact can: "hand the conversation to… (no team chosen)"
	// must come back as something it could not do, not as done.
	if err := crmactions.Validate(step.Type, step.Config); err != nil {
		r.results = append(r.results, ActionResult{
			ID: step.ID, Type: step.Type, Status: "failed", Error: plainError(err),
		})
		if !step.ContinueOnError {
			r.stopped = true
		}
		return
	}
	output, err := crmactions.Execute(r.ctx, r.e.Deps, r.rc, step.Type, step.Config)
	result := ActionResult{ID: step.ID, Type: step.Type, Output: output}
	if err == nil {
		result.Status = "succeeded"
		r.results = append(r.results, result)
		return
	}
	result.Status = "failed"
	result.Error = err.Error()
	r.results = append(r.results, result)
	if !step.ContinueOnError {
		r.stopped = true
	}
}

func (r *stepRunner) runCondition(step ActionSpec) {
	matched := false
	filter, ok := conditionFilter(step.Config)
	if ok && r.rc.ContactID != uuid.Nil {
		var err error
		matched, err = r.e.filterMatches(r.ctx, r.rule.OrganizationID, filter, r.rc.ContactID)
		if err != nil {
			r.results = append(r.results, ActionResult{
				ID: step.ID, Type: step.Type, Status: "failed", Error: err.Error(),
			})
			r.stopped = true
			return
		}
	}

	branch := "else"
	next := step.Else
	if matched {
		branch = "then"
		next = step.Then
	}
	r.results = append(r.results, ActionResult{
		ID: step.ID, Type: step.Type, Status: "succeeded",
		Output: map[string]any{"matched": matched, "branch": branch},
	})
	r.run(next)
}

func (r *stepRunner) runWait(step ActionSpec) {
	wait, ok := waitDuration(step.Config)
	if !ok {
		r.results = append(r.results, ActionResult{
			ID: step.ID, Type: step.Type, Status: "failed", Error: "the wait has no length",
		})
		r.stopped = true
		return
	}
	until := time.Now().UTC().Add(wait)

	if r.dryRun {
		// A test shows the whole journey, so it does not stop here: it notes
		// when the contact would move on and carries on to the steps after.
		r.results = append(r.results, ActionResult{
			ID: step.ID, Type: step.Type, Status: "succeeded",
			Output: map[string]any{"until": until, "dry_run": true},
		})
		return
	}

	r.results = append(r.results, ActionResult{
		ID: step.ID, Type: step.Type, Status: ResultWaiting,
		Output: map[string]any{"until": until},
	})
	r.paused = &pause{StepID: step.ID, ResumeAt: until}
}

// runContext is what every step of one run shares.
func (e *Engine) runContext(ctx context.Context, rule *models.AutomationRule, event crmevents.Event, dryRun bool) crmactions.RunContext {
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
	return rc
}

// filterMatches asks the database whether one contact satisfies a filter.
// Compiling the filter and adding "AND contacts.id = ?" reuses one query
// language rather than reimplementing the operators in Go.
func (e *Engine) filterMatches(ctx context.Context, orgID uuid.UUID, filter contactquery.Filter, contactID uuid.UUID) (bool, error) {
	registry, err := e.Rules.Registry(ctx, orgID)
	if err != nil {
		return false, err
	}

	viewer := contactquery.Viewer{
		OrgID: orgID,
		// A rule is not a person: it sees whatever its conditions name, or an
		// automation would behave differently depending on who saved it.
		CanSeeAllContacts: true,
		Location:          e.location(orgID),
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

// park records a run waiting at a step.
func (e *Engine) park(ctx context.Context, rule *models.AutomationRule, run *models.AutomationRun, event crmevents.Event, at *pause) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return err
	}
	return e.db().WithContext(ctx).Create(&models.AutomationWait{
		ID:             uuid.New(),
		OrganizationID: rule.OrganizationID,
		RuleID:         rule.ID,
		RunID:          run.ID,
		ContactID:      run.ContactID,
		StepID:         at.StepID,
		Event:          models.JSONB(encoded),
		ResumeAt:       at.ResumeAt,
		Status:         models.WaitPending,
	}).Error
}

// ResumeBatch bounds how many parked runs one tick resumes.
const ResumeBatch = 200

// ResumeWaits carries on every run whose wait is over. It returns how many it
// picked up.
//
// Each wait is claimed by flipping its status in a single guarded update, so
// two replicas ticking at once cannot resume the same run twice — which would
// send the same customer the same message twice.
func (e *Engine) ResumeWaits(ctx context.Context) (int, error) {
	var due []models.AutomationWait
	if err := e.db().WithContext(ctx).
		Where("status = ? AND resume_at <= ?", models.WaitPending, time.Now().UTC()).
		Order("resume_at").Limit(ResumeBatch).Find(&due).Error; err != nil {
		return 0, err
	}

	resumed := 0
	for i := range due {
		wait := &due[i]
		claim := e.db().WithContext(ctx).Model(&models.AutomationWait{}).
			Where("id = ? AND status = ?", wait.ID, models.WaitPending).
			Update("status", models.WaitResumed)
		if claim.Error != nil {
			return resumed, claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		if err := e.resume(ctx, wait); err != nil {
			e.log().Error("Automation wait failed to resume", "error", err,
				"rule_id", wait.RuleID, "run_id", wait.RunID)
			continue
		}
		resumed++
	}
	return resumed, nil
}

// resume carries one parked run on from the step after its wait.
func (e *Engine) resume(ctx context.Context, wait *models.AutomationWait) error {
	var run models.AutomationRun
	if err := e.db().WithContext(ctx).Where("id = ?", wait.RunID).First(&run).Error; err != nil {
		// The run was pruned or deleted; there is nothing left to continue.
		return e.db().WithContext(ctx).Model(&models.AutomationWait{}).
			Where("id = ?", wait.ID).Update("status", models.WaitCancelled).Error
	}

	rule, err := e.Rules.Get(ctx, wait.OrganizationID, wait.RuleID)
	if err != nil {
		return e.cancelWait(ctx, wait, &run, models.CancelRuleDeleted)
	}
	// Switching a rule off stops everyone waiting in it. Carrying on would
	// mean "off" did not mean off for the people already inside.
	if !rule.Enabled {
		return e.cancelWait(ctx, wait, &run, models.CancelRuleOff)
	}
	if wait.ContactID != nil {
		var alive int64
		if err := e.db().WithContext(ctx).Model(&models.Contact{}).
			Where("id = ?", *wait.ContactID).Count(&alive).Error; err != nil {
			return err
		}
		if alive == 0 {
			return e.cancelWait(ctx, wait, &run, models.CancelContactGone)
		}
	}

	remaining, found := continuationAfter(RuleActions(rule), wait.StepID)
	if !found {
		return e.cancelWait(ctx, wait, &run, models.CancelStepRemoved)
	}

	var event crmevents.Event
	raw, err := json.Marshal(wait.Event)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}

	runner := &stepRunner{
		e: e, ctx: ctx, rule: rule,
		rc:      e.runContext(ctx, rule, event, false),
		results: finishWaitResult(runResults(&run), wait.StepID),
	}
	for _, list := range remaining {
		runner.run(list)
		if runner.halted() {
			break
		}
	}

	run.ActionResults = models.JSONB{"list": toAnySlice(runner.results)}
	if runner.paused != nil {
		run.Status = models.AutomationWaiting
		if err := e.db().WithContext(ctx).Model(&models.AutomationRun{}).
			Where("id = ?", run.ID).
			Updates(map[string]any{"status": run.Status, "action_results": run.ActionResults}).Error; err != nil {
			return err
		}
		return e.park(ctx, rule, &run, event, runner.paused)
	}

	run.Status = statusFor(runner.results)
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	if err := e.db().WithContext(ctx).Model(&models.AutomationRun{}).
		Where("id = ?", run.ID).
		Updates(map[string]any{
			"status":         run.Status,
			"action_results": run.ActionResults,
			"finished_at":    finished,
		}).Error; err != nil {
		return err
	}
	e.updateRuleCounters(ctx, rule, &run)
	return nil
}

// cancelWait ends a parked run that cannot carry on, and says why.
func (e *Engine) cancelWait(ctx context.Context, wait *models.AutomationWait, run *models.AutomationRun, reason string) error {
	finished := time.Now().UTC()
	if err := e.db().WithContext(ctx).Model(&models.AutomationRun{}).
		Where("id = ?", run.ID).
		Updates(map[string]any{
			"status":      models.AutomationCancelled,
			"skip_reason": reason,
			"finished_at": finished,
		}).Error; err != nil {
		return err
	}
	return e.db().WithContext(ctx).Model(&models.AutomationWait{}).
		Where("id = ?", wait.ID).Update("status", models.WaitCancelled).Error
}

// runResults reads a run's recorded step results.
func runResults(run *models.AutomationRun) []ActionResult {
	raw, err := json.Marshal(run.ActionResults["list"])
	if err != nil {
		return nil
	}
	var out []ActionResult
	_ = json.Unmarshal(raw, &out)
	return out
}

// finishWaitResult marks the wait a resumed run was parked at as done.
func finishWaitResult(results []ActionResult, stepID string) []ActionResult {
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].ID == stepID && results[i].Status == ResultWaiting {
			results[i].Status = "succeeded"
			break
		}
	}
	return results
}

// WaitingCounts reports how many contacts are parked at each wait step of a
// rule, so the builder can show "3 waiting here" on the card.
func (e *Engine) WaitingCounts(ctx context.Context, ruleID uuid.UUID) (map[string]int64, error) {
	type row struct {
		StepID string
		Count  int64
	}
	var rows []row
	if err := e.db().WithContext(ctx).Model(&models.AutomationWait{}).
		Select("step_id, count(*) AS count").
		Where("rule_id = ? AND status = ?", ruleID, models.WaitPending).
		Group("step_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.StepID] = r.Count
	}
	return out, nil
}
